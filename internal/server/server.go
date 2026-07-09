package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	catalogpkg "github.com/imprun/windforce-lite/internal/catalog"
	"github.com/imprun/windforce-lite/internal/contract"
	"github.com/imprun/windforce-lite/internal/state"
	"github.com/imprun/windforce-lite/internal/syncer"
)

type Catalog interface {
	GetDeployment(ctx context.Context, app string) (contract.Deployment, error)
}

type AdapterRoute struct {
	App    string
	Action string
	Env    []string
	Values map[string]string
}

type TriggerAdapter interface {
	Name() string
	MatchTrigger(path string) (AdapterRoute, bool)
	MatchSchema(path string) (AdapterRoute, bool)
	TriggerResponse(run state.Run, route AdapterRoute) (int, any)
}

type Config struct {
	Store              state.Store
	Catalog            Catalog
	Syncer             *syncer.Syncer
	EnableTrigger      bool
	EnableAPI          bool
	DisableCoreTrigger bool
	TriggerAdapters    []TriggerAdapter
	TriggerToken       string
	AdminToken         string
	Wait               time.Duration
}

type Handler struct {
	store              state.Store
	catalog            Catalog
	syncer             *syncer.Syncer
	enableTrigger      bool
	enableAPI          bool
	disableCoreTrigger bool
	triggerAdapters    []TriggerAdapter
	triggerToken       string
	adminToken         string
	wait               time.Duration
}

func New(config Config) http.Handler {
	return &Handler{
		store:              config.Store,
		catalog:            config.Catalog,
		syncer:             config.Syncer,
		enableTrigger:      config.EnableTrigger,
		enableAPI:          config.EnableAPI,
		disableCoreTrigger: config.DisableCoreTrigger,
		triggerAdapters:    append([]TriggerAdapter(nil), config.TriggerAdapters...),
		triggerToken:       config.TriggerToken,
		adminToken:         config.AdminToken,
		wait:               config.Wait,
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
	if h.enableTrigger && r.Method == http.MethodPost {
		if route, ok := h.matchTriggerRoute(r.URL.Path); ok {
			if !authorized(r, h.triggerToken) {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			h.handleTrigger(w, r, route)
			return
		}
	}
	if h.enableAPI && r.Method == http.MethodGet {
		if route, ok := h.matchSchemaRoute(r.URL.Path); ok {
			if !authorized(r, h.adminToken) {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			h.handleSchema(w, r, route)
			return
		}
	}
	if h.enableAPI {
		if !authorized(r, h.adminToken) {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if h.handleAPI(w, r) {
			return
		}
	}
	writeError(w, http.StatusNotFound, "not found")
}

func (h *Handler) handleTrigger(w http.ResponseWriter, r *http.Request, route triggerRoute) {
	if h.store == nil || h.catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "trigger is not configured")
		return
	}
	input, err := readJSONBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	deployment, err := h.catalog.GetDeployment(r.Context(), route.app)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if _, ok := deployment.Actions[route.action]; !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("action %q not found in app %q", route.action, route.app))
		return
	}

	runID := state.CleanID(r.Header.Get("TASKID"))
	if runID == "" {
		runID = state.CleanID(r.Header.Get("X-Request-ID"))
	}
	if runID == "" {
		runID = state.NewID("run")
	}
	run := state.NewRun(route.adapterName, runID, route.app, route.action, deployment, input)
	run.CorrelationID = runID
	run.Env = append([]string(nil), route.env...)
	job := state.NewActionJob(run, input)
	if err := h.store.CreateRunAndEnqueue(r.Context(), run, job); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, state.ErrConflict) {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}

	run = h.waitForRun(r.Context(), run.ID)
	status := http.StatusAccepted
	if run.State == state.RunSucceeded {
		status = http.StatusOK
	} else if run.State == state.RunFailed {
		status = http.StatusInternalServerError
	}
	if route.adapter != nil {
		status, response := route.adapter.TriggerResponse(run, route.adapterRoute)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, status, runResponse(run))
}

func (h *Handler) handleAPI(w http.ResponseWriter, r *http.Request) bool {
	parts := splitPath(r.URL.Path)
	if len(parts) == 0 || parts[0] != "v1" {
		return false
	}
	if len(parts) == 2 && parts[1] == "sync" && r.Method == http.MethodPost {
		h.handleSync(w, r)
		return true
	}
	if len(parts) == 2 && parts[1] == "catalog" && r.Method == http.MethodGet {
		h.handleCatalog(w, r)
		return true
	}
	if len(parts) == 3 && parts[1] == "deployments" && r.Method == http.MethodGet {
		deployment, err := h.catalog.GetDeployment(r.Context(), parts[2])
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return true
		}
		writeJSON(w, http.StatusOK, deployment)
		return true
	}
	if len(parts) == 3 && parts[1] == "runs" && r.Method == http.MethodGet {
		run, err := h.store.GetRun(r.Context(), parts[2])
		if err != nil {
			writeStateError(w, err)
			return true
		}
		writeJSON(w, http.StatusOK, runResponse(run))
		return true
	}
	if len(parts) == 4 && parts[1] == "runs" && parts[3] == "resume" && r.Method == http.MethodPost {
		input, err := readResumeInput(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return true
		}
		run, _, err := h.store.ResumeRun(r.Context(), parts[2], input)
		if err != nil {
			writeStateError(w, err)
			return true
		}
		writeJSON(w, http.StatusAccepted, runResponse(run))
		return true
	}
	if len(parts) == 4 && parts[1] == "runs" && parts[3] == "cancel" && r.Method == http.MethodPost {
		var request struct {
			Reason string `json:"reason"`
		}
		if err := readOptionalJSON(r, &request); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return true
		}
		run, err := h.store.CancelRun(r.Context(), parts[2], request.Reason)
		if err != nil {
			writeStateError(w, err)
			return true
		}
		writeJSON(w, http.StatusOK, runResponse(run))
		return true
	}
	if len(parts) == 4 && parts[1] == "runs" && parts[3] == "retry" && r.Method == http.MethodPost {
		run, job, err := h.store.RetryRun(r.Context(), parts[2])
		if err != nil {
			writeStateError(w, err)
			return true
		}
		response := runResponse(run)
		response["jobId"] = job.ID
		writeJSON(w, http.StatusAccepted, response)
		return true
	}
	if len(parts) == 3 && parts[1] == "human-tasks" && r.Method == http.MethodGet {
		task, err := h.store.GetHumanTask(r.Context(), parts[2])
		if err != nil {
			writeStateError(w, err)
			return true
		}
		writeJSON(w, http.StatusOK, task)
		return true
	}
	if len(parts) == 4 && parts[1] == "human-tasks" && parts[3] == "resume" && r.Method == http.MethodPost {
		input, err := readResumeInput(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return true
		}
		run, _, err := h.store.ResumeHumanTask(r.Context(), parts[2], input)
		if err != nil {
			writeStateError(w, err)
			return true
		}
		writeJSON(w, http.StatusAccepted, runResponse(run))
		return true
	}
	return false
}

func (h *Handler) handleSync(w http.ResponseWriter, r *http.Request) {
	if h.syncer == nil {
		writeError(w, http.StatusServiceUnavailable, "sync API is not configured")
		return
	}
	var request struct {
		Workspace        string `json:"workspace"`
		GitSourceID      string `json:"gitSourceId"`
		GitSourceIDSnake string `json:"git_source_id"`
		App              string `json:"app"`
		Source           string `json:"source"`
		SourceDir        string `json:"sourceDir"`
		Repo             string `json:"repo"`
		RepoURL          string `json:"repoUrl"`
		Branch           string `json:"branch"`
		Commit           string `json:"commit"`
		CloneRoot        string `json:"cloneRoot"`
		TokenEnv         string `json:"tokenEnv"`
	}
	if err := readOptionalJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sourceDir := firstNonEmpty(request.SourceDir, request.Source)
	repoURL := firstNonEmpty(request.RepoURL, request.Repo)
	branch := request.Branch
	if branch == "" {
		branch = "main"
	}
	token := ""
	if request.TokenEnv != "" {
		token = os.Getenv(request.TokenEnv)
	}
	s := *h.syncer
	if request.CloneRoot != "" {
		s.CloneRoot = request.CloneRoot
	}
	deployment, err := s.Sync(r.Context(), syncer.Source{
		Workspace:   request.Workspace,
		GitSourceID: firstNonEmpty(request.GitSourceID, request.GitSourceIDSnake),
		App:         request.App,
		RepoURL:     repoURL,
		Branch:      branch,
		Commit:      request.Commit,
		Token:       token,
		LocalDir:    sourceDir,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, deployment)
}

func (h *Handler) handleCatalog(w http.ResponseWriter, r *http.Request) {
	loader, ok := h.catalog.(interface {
		Load(context.Context) (catalogpkg.Snapshot, error)
	})
	if !ok {
		writeError(w, http.StatusNotImplemented, "catalog snapshot is not supported")
		return
	}
	snapshot, err := loader.Load(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (h *Handler) handleSchema(w http.ResponseWriter, r *http.Request, route triggerRoute) {
	if h.catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog is not configured")
		return
	}
	deployment, err := h.catalog.GetDeployment(r.Context(), route.app)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	action, ok := deployment.Actions[route.action]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("action %q not found in app %q", route.action, route.app))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"app":          route.app,
		"action":       route.action,
		"inputSchema":  action.InputSchema,
		"outputSchema": action.OutputSchema,
		"metadata":     action,
	})
}

func (h *Handler) waitForRun(ctx context.Context, runID string) state.Run {
	run, err := h.store.GetRun(ctx, runID)
	if err != nil || h.wait <= 0 || state.IsSettledForTrigger(run) {
		return run
	}
	waitCtx, cancel := context.WithTimeout(ctx, h.wait)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-waitCtx.Done():
			run, _ = h.store.GetRun(context.Background(), runID)
			return run
		case <-ticker.C:
			current, err := h.store.GetRun(waitCtx, runID)
			if err == nil {
				run = current
				if state.IsSettledForTrigger(run) {
					return run
				}
			}
		}
	}
}

type triggerRoute struct {
	adapter      TriggerAdapter
	adapterName  string
	adapterRoute AdapterRoute
	app          string
	action       string
	env          []string
}

func parseTriggerRoute(path string) (triggerRoute, bool) {
	parts := splitPath(path)
	if len(parts) == 5 && parts[0] == "v1" && parts[1] == "apps" && parts[3] == "actions" {
		return triggerRoute{adapterName: "windforce", app: parts[2], action: parts[4]}, true
	}
	return triggerRoute{}, false
}

func (h *Handler) matchTriggerRoute(path string) (triggerRoute, bool) {
	if !h.disableCoreTrigger {
		if route, ok := parseTriggerRoute(path); ok {
			return route, true
		}
	}
	for _, adapter := range h.triggerAdapters {
		if adapter == nil {
			continue
		}
		adapterRoute, ok := adapter.MatchTrigger(path)
		if !ok {
			continue
		}
		return triggerRoute{
			adapter:      adapter,
			adapterName:  adapter.Name(),
			adapterRoute: adapterRoute,
			app:          adapterRoute.App,
			action:       adapterRoute.Action,
			env:          append([]string(nil), adapterRoute.Env...),
		}, true
	}
	return triggerRoute{}, false
}

func parseSchemaRoute(path string) (triggerRoute, bool) {
	parts := splitPath(path)
	if len(parts) == 6 && parts[0] == "v1" && parts[1] == "apps" && parts[3] == "actions" && parts[5] == "schema" {
		return triggerRoute{adapterName: "windforce", app: parts[2], action: parts[4]}, true
	}
	return triggerRoute{}, false
}

func (h *Handler) matchSchemaRoute(path string) (triggerRoute, bool) {
	if route, ok := parseSchemaRoute(path); ok {
		return route, true
	}
	for _, adapter := range h.triggerAdapters {
		if adapter == nil {
			continue
		}
		adapterRoute, ok := adapter.MatchSchema(path)
		if !ok {
			continue
		}
		return triggerRoute{
			adapter:      adapter,
			adapterName:  adapter.Name(),
			adapterRoute: adapterRoute,
			app:          adapterRoute.App,
			action:       adapterRoute.Action,
			env:          append([]string(nil), adapterRoute.Env...),
		}, true
	}
	return triggerRoute{}, false
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

func runResponse(run state.Run) map[string]any {
	response := map[string]any{
		"runId":  run.ID,
		"state":  run.State,
		"app":    run.App,
		"action": run.Action,
	}
	if run.TaskID != "" {
		response["humanTaskId"] = run.TaskID
	}
	if run.CorrelationID != "" {
		response["correlationId"] = run.CorrelationID
	}
	if len(run.Output) > 0 {
		response["output"] = json.RawMessage(run.Output)
	}
	if run.Result != nil {
		response["result"] = run.Result
	}
	if len(run.Error) > 0 {
		response["error"] = json.RawMessage(run.Error)
	}
	return response
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
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func authorized(r *http.Request, token string) bool {
	if token == "" {
		return true
	}
	if r.Header.Get("Authorization") == "Bearer "+token {
		return true
	}
	if r.Header.Get("X-Windforce-Token") == token {
		return true
	}
	return false
}
