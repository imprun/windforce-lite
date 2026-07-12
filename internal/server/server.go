package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/imprun/windforce-lite/internal/contract"
	gitsourcepkg "github.com/imprun/windforce-lite/internal/gitsource"
	"github.com/imprun/windforce-lite/internal/state"
	"github.com/imprun/windforce-lite/internal/syncer"
	"github.com/imprun/windforce-lite/internal/token"
)

type Catalog interface {
	GetDeployment(ctx context.Context, app string) (contract.Deployment, error)
}

type GitSourceRegistry interface {
	Upsert(ctx context.Context, source gitsourcepkg.Source) error
	Get(ctx context.Context, workspace string, id string) (gitsourcepkg.Source, error)
}

const DefaultSecretKey = "dev-insecure-change-me-0000000000000000000000000000"

type Config struct {
	Store             state.Store
	Catalog           Catalog
	Syncer            *syncer.Syncer
	GitSources        GitSourceRegistry
	EnableAPI         bool
	AdminToken        string
	JobTokenSecret    string
	SecretKey         string
	SecretKeyPrevious string
	SampleRoot        string
	Wait              time.Duration
}

type Handler struct {
	store             state.Store
	catalog           Catalog
	syncer            *syncer.Syncer
	gitSources        GitSourceRegistry
	enableAPI         bool
	adminToken        string
	jobTokenSecret    string
	secretKey         string
	secretKeyPrevious string
	sampleRoot        string
	wait              time.Duration
	syncLocks         sync.Map
}

type jobPrincipal struct {
	Workspace string
	JobID     string
	Subject   string
}

type principalContextKey struct{}

func jobPrincipalFrom(ctx context.Context) *jobPrincipal {
	principal, _ := ctx.Value(principalContextKey{}).(*jobPrincipal)
	return principal
}

func requestActorSubject(r *http.Request) string {
	if principal := jobPrincipalFrom(r.Context()); principal != nil {
		if subject := strings.TrimSpace(principal.Subject); subject != "" {
			return subject
		}
	}
	return strings.TrimSpace(r.Header.Get("X-Windforce-Actor"))
}

func New(config Config) http.Handler {
	secretKey := config.SecretKey
	if secretKey == "" {
		secretKey = DefaultSecretKey
	}
	return &Handler{
		store:             config.Store,
		catalog:           config.Catalog,
		syncer:            config.Syncer,
		gitSources:        config.GitSources,
		enableAPI:         config.EnableAPI,
		adminToken:        config.AdminToken,
		jobTokenSecret:    config.JobTokenSecret,
		secretKey:         secretKey,
		secretKeyPrevious: config.SecretKeyPrevious,
		sampleRoot:        config.SampleRoot,
		wait:              config.Wait,
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	if r.URL.Path == "/readyz" {
		writeJSON(w, http.StatusOK, map[string]bool{"ready": h.store != nil})
		return
	}
	if h.handleWebUI(w, r) {
		return
	}
	if h.enableAPI {
		authorizedRequest, status, message := h.authorizeAPIRequest(r)
		if status != 0 {
			writeError(w, status, message)
			return
		}
		if h.handleAPI(w, authorizedRequest) {
			return
		}
	}
	writeError(w, http.StatusNotFound, "not found")
}

func (h *Handler) handleAPI(w http.ResponseWriter, r *http.Request) bool {
	parts := splitPath(r.URL.Path)
	if len(parts) == 7 && parts[0] == "api" && parts[1] == "w" && parts[3] == "jobs" && parts[4] == "run" && r.Method == http.MethodPost {
		h.handleJobRun(w, r, parts[2], parts[5], parts[6], false)
		return true
	}
	if len(parts) == 8 && parts[0] == "api" && parts[1] == "w" && parts[3] == "jobs" && parts[4] == "run" && parts[7] == "wait" && r.Method == http.MethodPost {
		h.handleJobRun(w, r, parts[2], parts[5], parts[6], true)
		return true
	}
	if len(parts) == 7 && parts[0] == "api" && parts[1] == "w" && parts[3] == "jobs" && parts[4] == "webhook" && r.Method == http.MethodPost {
		h.handleJobWebhook(w, r, parts[2], parts[5], parts[6])
		return true
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "w" && parts[3] == "jobs" && r.Method == http.MethodGet {
		h.handleJobList(w, r, parts[2])
		return true
	}
	if len(parts) == 5 && parts[0] == "api" && parts[1] == "w" && parts[3] == "jobs" && parts[4] == "summary" && r.Method == http.MethodGet {
		h.handleJobSummary(w, r, parts[2])
		return true
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "w" && parts[3] == "state" && r.Method == http.MethodGet {
		h.handleGetState(w, r, parts[2])
		return true
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "w" && parts[3] == "state" && r.Method == http.MethodPost {
		h.handleSetState(w, r, parts[2])
		return true
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "w" && parts[3] == "variables" && r.Method == http.MethodGet {
		h.handleListVariables(w, r, parts[2])
		return true
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "w" && parts[3] == "variables" && r.Method == http.MethodPost {
		h.handleSetVariable(w, r, parts[2])
		return true
	}
	if len(parts) >= 7 && parts[0] == "api" && parts[1] == "w" && parts[3] == "variables" && parts[4] == "get" && parts[5] == "p" && r.Method == http.MethodGet {
		h.handleGetVariable(w, r, parts[2], joinPathParts(parts, 6))
		return true
	}
	if len(parts) >= 6 && parts[0] == "api" && parts[1] == "w" && parts[3] == "variables" && parts[4] == "p" && r.Method == http.MethodDelete {
		h.handleDeleteVariable(w, r, parts[2], joinPathParts(parts, 5))
		return true
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "w" && parts[3] == "resources" && r.Method == http.MethodPost {
		h.handleSetResource(w, r, parts[2])
		return true
	}
	if len(parts) >= 7 && parts[0] == "api" && parts[1] == "w" && parts[3] == "resources" && parts[4] == "get" && parts[5] == "p" && r.Method == http.MethodGet {
		h.handleGetResource(w, r, parts[2], joinPathParts(parts, 6))
		return true
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "w" && parts[3] == "openapi.json" && r.Method == http.MethodGet {
		h.handleCanonicalControlPlaneOpenAPI(w, r, parts[2])
		return true
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "w" && parts[3] == "git_sources" && r.Method == http.MethodGet {
		h.handleCanonicalGitSources(w, r, parts[2])
		return true
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "w" && parts[3] == "git_sources" && r.Method == http.MethodPost {
		h.handleCanonicalRegisterGitSource(w, r, parts[2])
		return true
	}
	if len(parts) == 5 && parts[0] == "api" && parts[1] == "w" && parts[3] == "git_sources" && parts[4] == "probe" && r.Method == http.MethodPost {
		h.handleCanonicalProbeGitSource(w, r, parts[2])
		return true
	}
	if len(parts) == 5 && parts[0] == "api" && parts[1] == "w" && parts[3] == "git_sources" && parts[4] == "sample" && r.Method == http.MethodPost {
		h.handleCanonicalSampleGitSource(w, r, parts[2])
		return true
	}
	if len(parts) == 5 && parts[0] == "api" && parts[1] == "w" && parts[3] == "git_sources" && r.Method == http.MethodPatch {
		h.handleCanonicalPatchGitSource(w, r, parts[2], parts[4])
		return true
	}
	if len(parts) == 5 && parts[0] == "api" && parts[1] == "w" && parts[3] == "git_sources" && r.Method == http.MethodDelete {
		h.handleCanonicalDeleteGitSource(w, r, parts[2], parts[4])
		return true
	}
	if len(parts) == 6 && parts[0] == "api" && parts[1] == "w" && parts[3] == "git_sources" && parts[5] == "sync" && r.Method == http.MethodPost {
		h.handleCanonicalGitSourceSync(w, r, parts[2], parts[4])
		return true
	}
	if len(parts) == 6 && parts[0] == "api" && parts[1] == "w" && parts[3] == "git_sources" && parts[5] == "deploy" && r.Method == http.MethodPost {
		h.handleCanonicalGitSourceDeploy(w, r, parts[2], parts[4])
		return true
	}
	if len(parts) == 6 && parts[0] == "api" && parts[1] == "w" && parts[3] == "git_sources" && parts[5] == "audit" && r.Method == http.MethodGet {
		h.handleCanonicalGitSourceAudit(w, r, parts[2], parts[4])
		return true
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "w" && parts[3] == "apps" && r.Method == http.MethodGet {
		h.handleCanonicalApps(w, r, parts[2])
		return true
	}
	if len(parts) == 6 && parts[0] == "api" && parts[1] == "w" && parts[3] == "apps" && parts[5] == "source" && r.Method == http.MethodGet {
		h.handleCanonicalAppSource(w, r, parts[2], parts[4])
		return true
	}
	if len(parts) == 6 && parts[0] == "api" && parts[1] == "w" && parts[3] == "apps" && parts[5] == "history" && r.Method == http.MethodGet {
		h.handleCanonicalAppHistory(w, r, parts[2], parts[4])
		return true
	}
	if len(parts) == 6 && parts[0] == "api" && parts[1] == "w" && parts[3] == "apps" && parts[5] == "openapi.json" && r.Method == http.MethodGet {
		h.handleCanonicalAppOpenAPI(w, r, parts[2], parts[4])
		return true
	}
	if len(parts) == 5 && parts[0] == "api" && parts[1] == "w" && parts[3] == "apps" && r.Method == http.MethodGet {
		h.handleCanonicalApp(w, r, parts[2], parts[4])
		return true
	}
	if len(parts) == 5 && parts[0] == "api" && parts[1] == "w" && parts[3] == "apps" && r.Method == http.MethodPatch {
		h.handleCanonicalPatchApp(w, r, parts[2], parts[4])
		return true
	}
	if len(parts) == 7 && parts[0] == "api" && parts[1] == "w" && parts[3] == "apps" && parts[5] == "actions" && r.Method == http.MethodGet {
		h.handleCanonicalAction(w, r, parts[2], parts[4], parts[6])
		return true
	}
	if len(parts) == 8 && parts[0] == "api" && parts[1] == "w" && parts[3] == "apps" && parts[5] == "actions" && parts[7] == "schema" && r.Method == http.MethodGet {
		h.handleCanonicalActionSchema(w, r, parts[2], parts[4], parts[6])
		return true
	}
	if len(parts) == 7 && parts[0] == "api" && parts[1] == "w" && parts[3] == "apps" && parts[5] == "actions" && r.Method == http.MethodPatch {
		h.handleCanonicalPatchAction(w, r, parts[2], parts[4], parts[6])
		return true
	}
	if len(parts) == 6 && parts[0] == "api" && parts[1] == "w" && parts[3] == "apps" && parts[5] == "requeue" && r.Method == http.MethodPost {
		h.handleCanonicalRequeueApp(w, r, parts[2], parts[4])
		return true
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "w" && parts[3] == "worker-tags" && r.Method == http.MethodGet {
		h.handleCanonicalWorkerTags(w, r, parts[2])
		return true
	}
	if len(parts) == 5 && parts[0] == "api" && parts[1] == "w" && parts[3] == "jobs" && r.Method == http.MethodGet {
		h.handleJobStatus(w, r, parts[2], parts[4])
		return true
	}
	if len(parts) == 6 && parts[0] == "api" && parts[1] == "w" && parts[3] == "jobs" && parts[5] == "result" && r.Method == http.MethodGet {
		h.handleJobResult(w, r, parts[2], parts[4])
		return true
	}
	if len(parts) == 6 && parts[0] == "api" && parts[1] == "w" && parts[3] == "jobs" && parts[5] == "cancel" && r.Method == http.MethodPost {
		h.handleJobCancel(w, r, parts[2], parts[4])
		return true
	}
	if len(parts) == 6 && parts[0] == "api" && parts[1] == "w" && parts[3] == "jobs" && parts[5] == "logs" && r.Method == http.MethodGet {
		h.handleJobLogs(w, r, parts[2], parts[4])
		return true
	}
	return false
}

func SplitPath(path string) []string {
	rawParts := strings.Split(strings.Trim(path, "/"), "/")
	parts := make([]string, 0, len(rawParts))
	for _, raw := range rawParts {
		if raw == "" {
			continue
		}
		value, err := url.PathUnescape(raw)
		if err != nil {
			value = raw
		}
		parts = append(parts, value)
	}
	return parts
}

func splitPath(path string) []string {
	return SplitPath(path)
}

func joinPathParts(parts []string, start int) string {
	if start >= len(parts) {
		return ""
	}
	return strings.Join(parts[start:], "/")
}

func readJSONBody(r *http.Request) (json.RawMessage, error) {
	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		data = []byte("{}")
	}
	if !json.Valid(data) {
		return nil, errors.New("request body is not valid JSON")
	}
	return json.RawMessage(append([]byte(nil), data...)), nil
}

const maxRunBodyBytes = 1 << 20

func readRunInput(w http.ResponseWriter, r *http.Request) (json.RawMessage, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRunBodyBytes)
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		} else {
			writeError(w, http.StatusBadRequest, "could not read request body")
		}
		return nil, false
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return json.RawMessage("{}"), true
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil || object == nil {
		writeError(w, http.StatusBadRequest, "request body must be a JSON object")
		return nil, false
	}
	if _, ok := object["__wf_enc"]; ok {
		writeError(w, http.StatusBadRequest, `"__wf_enc" is a reserved top-level input key`)
		return nil, false
	}
	return json.RawMessage(append([]byte(nil), body...)), true
}

func readWebhookRaw(w http.ResponseWriter, r *http.Request) (json.RawMessage, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRunBodyBytes)
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		} else {
			writeError(w, http.StatusBadRequest, "could not read request body")
		}
		return nil, false
	}
	raw, err := json.Marshal(string(body))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return nil, false
	}
	return json.RawMessage(raw), true
}

const (
	maxWebhookHeaderValueBytes = 1 << 10
	maxWebhookHeadersBytes     = 8 << 10
)

var webhookHeaderDenylist = map[string]bool{
	"Authorization":       true,
	"Cookie":              true,
	"Proxy-Authorization": true,
}

func captureWebhookHeaders(r *http.Request) json.RawMessage {
	names := make([]string, 0, len(r.Header))
	for name := range r.Header {
		if webhookHeaderDenylist[name] {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	headers := make(map[string]string, len(names))
	total := 0
	for _, name := range names {
		value := strings.Join(r.Header[name], ", ")
		if len(value) > maxWebhookHeaderValueBytes {
			value = value[:maxWebhookHeaderValueBytes]
		}
		if total+len(name)+len(value) > maxWebhookHeadersBytes {
			break
		}
		total += len(name) + len(value)
		headers[name] = value
	}
	if len(headers) == 0 {
		return nil
	}
	data, err := json.Marshal(headers)
	if err != nil {
		return nil
	}
	return json.RawMessage(data)
}

func readResumeInput(r *http.Request) (json.RawMessage, error) {
	body, err := readJSONBody(r)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Input json.RawMessage `json:"input"`
	}
	if json.Unmarshal(body, &envelope) == nil && len(envelope.Input) > 0 {
		if !json.Valid(envelope.Input) {
			return nil, errors.New("resume input is not valid JSON")
		}
		return envelope.Input, nil
	}
	return body, nil
}

func readOptionalJSON(r *http.Request, value any) error {
	body, err := readJSONBody(r)
	if err != nil {
		return err
	}
	if len(body) == 0 || string(body) == "{}" {
		return nil
	}
	return json.Unmarshal(body, value)
}

func readRequiredJSON(r *http.Request, value any) error {
	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return io.EOF
	}
	return json.Unmarshal(data, value)
}

func writeStateError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, state.ErrNotFound) {
		status = http.StatusNotFound
	} else if errors.Is(err, state.ErrInvalidState) {
		status = http.StatusConflict
	}
	writeError(w, status, err.Error())
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (h *Handler) authorizeAPIRequest(r *http.Request) (*http.Request, int, string) {
	bearerToken := bearer(r)
	if token.IsJobToken(bearerToken) {
		claims, ok := token.VerifyJobAny([]string{h.jobTokenSecret}, bearerToken)
		if !ok {
			return r, http.StatusUnauthorized, "unauthorized"
		}
		if !isJobSDKCallback(r) {
			return r, http.StatusForbidden, "job token may only call SDK callback endpoints"
		}
		if workspace := workspaceFromAPIPath(r.URL.Path); workspace != "" && workspace != claims.Workspace {
			return r, http.StatusForbidden, "job token workspace mismatch"
		}
		principal := &jobPrincipal{
			Workspace: claims.Workspace,
			JobID:     claims.JobID,
			Subject:   claims.Subject,
		}
		return r.WithContext(context.WithValue(r.Context(), principalContextKey{}, principal)), 0, ""
	}
	if !authorized(r, h.adminToken) {
		return r, http.StatusUnauthorized, "unauthorized"
	}
	return r, 0, ""
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	return ""
}

func isJobSDKCallback(r *http.Request) bool {
	path := r.URL.Path
	if !strings.HasPrefix(path, "/api/w/") {
		return false
	}
	if r.Method == http.MethodGet && strings.Contains(path, "/variables/get/p/") {
		return true
	}
	if r.Method == http.MethodGet && strings.Contains(path, "/resources/get/p/") {
		return true
	}
	if r.Method == http.MethodPost && strings.HasSuffix(path, "/flow/resume-urls") {
		return true
	}
	return (r.Method == http.MethodGet || r.Method == http.MethodPost) && strings.HasSuffix(path, "/state")
}

func workspaceFromAPIPath(path string) string {
	parts := splitPath(path)
	if len(parts) >= 3 && parts[0] == "api" && parts[1] == "w" {
		return parts[2]
	}
	return ""
}

func authorized(r *http.Request, adminToken string) bool {
	if adminToken == "" {
		return true
	}
	if r.Header.Get("Authorization") == "Bearer "+adminToken {
		return true
	}
	return false
}
