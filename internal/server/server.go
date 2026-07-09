package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	catalogpkg "github.com/imprun/windforce-lite/internal/catalog"
	"github.com/imprun/windforce-lite/internal/contract"
	gitsourcepkg "github.com/imprun/windforce-lite/internal/gitsource"
	sourcepkg "github.com/imprun/windforce-lite/internal/source"
	"github.com/imprun/windforce-lite/internal/state"
	"github.com/imprun/windforce-lite/internal/syncer"
)

type Catalog interface {
	GetDeployment(ctx context.Context, app string) (contract.Deployment, error)
}

type GitSourceRegistry interface {
	Upsert(ctx context.Context, source gitsourcepkg.Source) error
	Get(ctx context.Context, workspace string, id string) (gitsourcepkg.Source, error)
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
	GitSources         GitSourceRegistry
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
	gitSources         GitSourceRegistry
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
		gitSources:         config.GitSources,
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
	if len(parts) == 7 && parts[0] == "api" && parts[1] == "w" && parts[3] == "apps" && parts[5] == "actions" && r.Method == http.MethodPatch {
		h.handleCanonicalPatchAction(w, r, parts[2], parts[4], parts[6])
		return true
	}
	if len(parts) == 6 && parts[0] == "api" && parts[1] == "w" && parts[3] == "apps" && parts[5] == "requeue" && r.Method == http.MethodPost {
		h.handleCanonicalRequeueApp(w, r, parts[2], parts[4])
		return true
	}
	if len(parts) == 5 && parts[0] == "api" && parts[1] == "w" && parts[3] == "deployments" && r.Method == http.MethodGet {
		h.handleCanonicalDeployment(w, r, parts[2], parts[4])
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
	if len(parts) == 0 || parts[0] != "v1" {
		return false
	}
	if len(parts) == 2 && parts[1] == "sync" && r.Method == http.MethodPost {
		h.handleSync(w, r)
		return true
	}
	if len(parts) == 2 && parts[1] == "git-sources" && r.Method == http.MethodPost {
		h.handleRegisterGitSource(w, r)
		return true
	}
	if len(parts) == 2 && parts[1] == "git-sources" && r.Method == http.MethodGet {
		h.handleGitSources(w, r)
		return true
	}
	if len(parts) == 3 && parts[1] == "git-sources" && r.Method == http.MethodGet {
		h.handleGitSource(w, r, parts[2])
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
		Subpath          string `json:"subpath"`
		Path             string `json:"path"`
		GitPath          string `json:"gitPath"`
		SourcePath       string `json:"sourcePath"`
		CloneRoot        string `json:"cloneRoot"`
		TokenEnv         string `json:"tokenEnv"`
	}
	if err := readOptionalJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	sourceDir := firstNonEmpty(request.SourceDir, request.Source)
	repoURL := firstNonEmpty(request.RepoURL, request.Repo)
	workspace := contract.NormalizeWorkspace(request.Workspace)
	gitSourceID := firstNonEmpty(request.GitSourceID, request.GitSourceIDSnake)
	subpath := firstNonEmpty(request.Subpath, request.Path, request.GitPath, request.SourcePath)
	branch := request.Branch
	if repoURL == "" && sourceDir == "" && gitSourceID != "" && h.gitSources != nil {
		registered, err := h.gitSources.Get(r.Context(), workspace, gitSourceID)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, gitsourcepkg.ErrGitSourceNotFound) {
				status = http.StatusNotFound
			}
			writeError(w, status, err.Error())
			return
		}
		repoURL = registered.RepoURL
		if branch == "" {
			branch = registered.Branch
		}
		if request.TokenEnv == "" {
			request.TokenEnv = registered.TokenEnv
		}
		if subpath == "" {
			subpath = registered.Subpath
		}
		gitSourceID = registered.ID
	}
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
		Workspace:   workspace,
		GitSourceID: gitSourceID,
		App:         request.App,
		RepoURL:     repoURL,
		Branch:      branch,
		Commit:      request.Commit,
		Subpath:     subpath,
		Token:       token,
		LocalDir:    sourceDir,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, deployment)
}

func (h *Handler) handleRegisterGitSource(w http.ResponseWriter, r *http.Request) {
	if h.gitSources == nil {
		writeError(w, http.StatusServiceUnavailable, "git source registry is not configured")
		return
	}
	var request struct {
		Workspace        string `json:"workspace"`
		ID               string `json:"id"`
		GitSourceID      string `json:"gitSourceId"`
		GitSourceIDSnake string `json:"git_source_id"`
		Repo             string `json:"repo"`
		RepoURL          string `json:"repoUrl"`
		Branch           string `json:"branch"`
		Subpath          string `json:"subpath"`
		Path             string `json:"path"`
		GitPath          string `json:"gitPath"`
		SourcePath       string `json:"sourcePath"`
		TokenEnv         string `json:"tokenEnv"`
	}
	if err := readOptionalJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	source := gitsourcepkg.Source{
		Workspace: request.Workspace,
		ID:        firstNonEmpty(request.ID, request.GitSourceID, request.GitSourceIDSnake),
		RepoURL:   firstNonEmpty(request.RepoURL, request.Repo),
		Branch:    request.Branch,
		Subpath:   firstNonEmpty(request.Subpath, request.Path, request.GitPath, request.SourcePath),
		TokenEnv:  request.TokenEnv,
	}
	if err := h.gitSources.Upsert(r.Context(), source); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	source.Workspace = contract.NormalizeWorkspace(source.Workspace)
	source.ID = contract.NormalizeGitSourceID(source.ID, "")
	if source.Branch == "" {
		source.Branch = "main"
	}
	source.Subpath, _ = contract.NormalizeSourcePath(source.Subpath)
	writeJSON(w, http.StatusOK, source)
}

func (h *Handler) handleGitSources(w http.ResponseWriter, r *http.Request) {
	loader, ok := h.gitSources.(interface {
		Load(context.Context) (gitsourcepkg.Snapshot, error)
	})
	if !ok {
		writeError(w, http.StatusNotImplemented, "git source snapshot is not supported")
		return
	}
	snapshot, err := loader.Load(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (h *Handler) handleGitSource(w http.ResponseWriter, r *http.Request, id string) {
	if h.gitSources == nil {
		writeError(w, http.StatusServiceUnavailable, "git source registry is not configured")
		return
	}
	source, err := h.gitSources.Get(r.Context(), r.URL.Query().Get("workspace"), id)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, gitsourcepkg.ErrGitSourceNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, source)
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

func (h *Handler) handleCanonicalGitSources(w http.ResponseWriter, r *http.Request, workspaceID string) {
	snapshot, ok := h.loadGitSourceSnapshot(w, r)
	if !ok {
		return
	}
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	items := make([]canonicalGitSourceView, 0, len(snapshot.Sources))
	for _, source := range snapshot.Sources {
		if contract.NormalizeWorkspace(source.Workspace) != workspaceID {
			continue
		}
		items = append(items, newCanonicalGitSourceView(source))
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Name < items[j].Name
	})
	writeJSON(w, http.StatusOK, items)
}

func (h *Handler) handleCanonicalRegisterGitSource(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if h.gitSources == nil {
		writeError(w, http.StatusServiceUnavailable, "git source registry is not configured")
		return
	}
	var request struct {
		Name     string `json:"name"`
		RepoURL  string `json:"repo_url"`
		Branch   string `json:"branch"`
		Subpath  string `json:"subpath"`
		CredsRef string `json:"creds_ref"`

		NameCamel     string `json:"Name"`
		RepoURLCamel  string `json:"RepoURL"`
		BranchCamel   string `json:"Branch"`
		SubpathCamel  string `json:"Subpath"`
		CredsRefCamel string `json:"CredsRef"`

		ID        string `json:"id"`
		RepoURLV1 string `json:"repoUrl"`
		TokenEnv  string `json:"tokenEnv"`
	}
	if err := readOptionalJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "name and repo_url required")
		return
	}
	name := strings.TrimSpace(firstNonEmpty(request.Name, request.NameCamel, request.ID))
	repoURL := strings.TrimSpace(firstNonEmpty(request.RepoURL, request.RepoURLCamel, request.RepoURLV1))
	branch := strings.TrimSpace(firstNonEmpty(request.Branch, request.BranchCamel))
	subpath := strings.TrimSpace(firstNonEmpty(request.Subpath, request.SubpathCamel))
	credsRef := strings.TrimSpace(firstNonEmpty(request.CredsRef, request.CredsRefCamel, request.TokenEnv))
	if name == "" || repoURL == "" {
		writeError(w, http.StatusBadRequest, "name and repo_url required")
		return
	}
	source := gitsourcepkg.Source{
		Workspace: workspaceID,
		ID:        name,
		RepoURL:   repoURL,
		Branch:    branch,
		Subpath:   subpath,
		TokenEnv:  credsRef,
	}
	if err := h.gitSources.Upsert(r.Context(), source); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	source, err := h.gitSources.Get(r.Context(), workspaceID, name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, newCanonicalGitSourceView(source))
}

func (h *Handler) handleCanonicalProbeGitSource(w http.ResponseWriter, r *http.Request, workspaceID string) {
	var request struct {
		RepoURL     string `json:"repo_url"`
		RepoURLV1   string `json:"repoUrl"`
		Branch      string `json:"branch"`
		AccessToken string `json:"access_token"`
		CredsRef    string `json:"creds_ref"`
		CredsRefV1  string `json:"tokenEnv"`
	}
	if err := readOptionalJSON(r, &request); err != nil || strings.TrimSpace(firstNonEmpty(request.RepoURL, request.RepoURLV1)) == "" {
		writeError(w, http.StatusBadRequest, "repo_url required")
		return
	}
	token := strings.TrimSpace(request.AccessToken)
	if token == "" {
		if credsRef := strings.TrimSpace(firstNonEmpty(request.CredsRef, request.CredsRefV1)); credsRef != "" {
			token = os.Getenv(credsRef)
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()
	branches, err := sourcepkg.ListRemoteBranches(ctx, strings.TrimSpace(firstNonEmpty(request.RepoURL, request.RepoURLV1)), token)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"reachable": false,
			"error":     err.Error(),
			"branches":  []string{},
		})
		return
	}
	branch := strings.TrimSpace(request.Branch)
	if branch == "" {
		branch = "main"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"reachable":     true,
		"branch":        branch,
		"branch_exists": stringSliceContains(branches, branch),
		"branches":      branches,
	})
}

func (h *Handler) handleCanonicalPatchGitSource(w http.ResponseWriter, r *http.Request, workspaceID string, sourceID string) {
	patcher, ok := h.gitSources.(interface {
		Patch(context.Context, string, string, gitsourcepkg.Patch) (gitsourcepkg.Source, error)
	})
	if !ok {
		writeError(w, http.StatusNotImplemented, "git source patch is not supported")
		return
	}
	var request canonicalGitSourcePatchRequest
	if err := readOptionalJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	patch, ok := canonicalGitSourcePatchFromRequest(w, request)
	if !ok {
		return
	}
	source, err := patcher.Patch(r.Context(), workspaceID, sourceID, patch)
	if errors.Is(err, gitsourcepkg.ErrGitSourceConflict) {
		writeError(w, http.StatusConflict, "git source name already exists")
		return
	}
	if errors.Is(err, gitsourcepkg.ErrGitSourceNotFound) {
		writeError(w, http.StatusNotFound, "git source not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, newCanonicalGitSourceView(source))
}

func (h *Handler) handleCanonicalDeleteGitSource(w http.ResponseWriter, r *http.Request, workspaceID string, sourceID string) {
	deleter, ok := h.gitSources.(interface {
		Delete(context.Context, string, string) (bool, error)
	})
	if !ok {
		writeError(w, http.StatusNotImplemented, "git source delete is not supported")
		return
	}
	deleted, err := deleter.Delete(r.Context(), workspaceID, sourceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "git source not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleCanonicalGitSourceSync(w http.ResponseWriter, r *http.Request, workspaceID string, sourceID string) {
	if h.syncer == nil {
		writeError(w, http.StatusServiceUnavailable, "sync API is not configured")
		return
	}
	if h.gitSources == nil {
		writeError(w, http.StatusServiceUnavailable, "git source registry is not configured")
		return
	}
	var request struct {
		App            string `json:"app"`
		Commit         string `json:"commit"`
		CloneRoot      string `json:"cloneRoot"`
		CloneRootSnake string `json:"clone_root"`
	}
	if err := readOptionalJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	source, err := h.gitSources.Get(r.Context(), workspaceID, sourceID)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, gitsourcepkg.ErrGitSourceNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, "git source not found")
		return
	}
	token := ""
	if source.TokenEnv != "" {
		token = os.Getenv(source.TokenEnv)
	}
	s := *h.syncer
	if cloneRoot := firstNonEmpty(request.CloneRoot, request.CloneRootSnake); cloneRoot != "" {
		s.CloneRoot = cloneRoot
	}
	deployment, err := s.Sync(r.Context(), syncer.Source{
		Workspace:   workspaceID,
		GitSourceID: source.ID,
		App:         request.App,
		RepoURL:     source.RepoURL,
		Branch:      source.Branch,
		Commit:      request.Commit,
		Subpath:     source.Subpath,
		Token:       token,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, newCanonicalSyncResult(deployment))
}

func (h *Handler) handleCanonicalApps(w http.ResponseWriter, r *http.Request, workspaceID string) {
	snapshot, ok := h.loadCatalogSnapshot(w, r)
	if !ok {
		return
	}
	deployments := canonicalDeployments(snapshot, workspaceID)
	if r.URL.Query().Get("view") == "summary" {
		apps := make([]canonicalAppSummaryView, 0, len(deployments))
		for _, deployment := range deployments {
			apps = append(apps, newCanonicalAppSummaryView(deployment))
		}
		writeJSON(w, http.StatusOK, map[string]any{"apps": apps})
		return
	}
	apps := make([]string, 0, len(deployments))
	for _, deployment := range deployments {
		apps = append(apps, deployment.App)
	}
	writeJSON(w, http.StatusOK, apps)
}

func (h *Handler) handleCanonicalApp(w http.ResponseWriter, r *http.Request, workspaceID string, app string) {
	deployment, ok := h.getCanonicalDeployment(w, r, workspaceID, app, "app not found: "+app)
	if !ok {
		return
	}
	schemaReader := h.newCanonicalSchemaReader(r.Context(), deployment)
	defer schemaReader.Close()
	actions, err := h.newCanonicalActionViews(schemaReader, deployment)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"app":     newCanonicalAppView(deployment),
		"actions": actions,
	})
}

func (h *Handler) handleCanonicalAppSource(w http.ResponseWriter, r *http.Request, workspaceID string, app string) {
	deployment, ok := h.getCanonicalDeployment(w, r, workspaceID, app, "app not found: "+app)
	if !ok {
		return
	}
	if h.syncer == nil || h.syncer.Store == nil {
		writeError(w, http.StatusInternalServerError, "source storage not configured")
		return
	}
	sourceDir, err := os.MkdirTemp("", "windforce-lite-app-source-")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer os.RemoveAll(sourceDir)
	if err := h.syncer.Store.FetchTo(r.Context(), sourceDir, deployment.SourceWorkspace(), deployment.SourceGitSourceID(), deployment.Commit); err != nil {
		writeError(w, http.StatusNotFound, "source commit is not materialized - re-sync the app")
		return
	}
	files, skipped, err := readCanonicalSourceFiles(sourceDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"app_key":       deployment.App,
		"git_source_id": nil,
		"commit_sha":    deployment.Commit,
		"files":         files,
		"skipped":       skipped,
	})
}

func (h *Handler) handleCanonicalAppHistory(w http.ResponseWriter, r *http.Request, workspaceID string, app string) {
	snapshot, ok := h.loadCatalogSnapshot(w, r)
	if !ok {
		return
	}
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	items := make([]canonicalAppHistoryItem, 0, len(snapshot.History))
	for _, item := range snapshot.History {
		if contract.NormalizeWorkspace(item.Workspace) != workspaceID || item.App != app {
			continue
		}
		items = append(items, newCanonicalAppHistoryItem(item))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	writeJSON(w, http.StatusOK, items)
}

func (h *Handler) handleCanonicalAction(w http.ResponseWriter, r *http.Request, workspaceID string, app string, actionKey string) {
	deployment, ok := h.getCanonicalDeployment(w, r, workspaceID, app, "app not found: "+app)
	if !ok {
		return
	}
	action, exists := deployment.Actions[actionKey]
	if !exists {
		writeError(w, http.StatusNotFound, "action not found: "+app+"/"+actionKey)
		return
	}
	schemaReader := h.newCanonicalSchemaReader(r.Context(), deployment)
	defer schemaReader.Close()
	view, err := h.newCanonicalActionView(schemaReader, deployment, actionKey, action)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *Handler) handleCanonicalPatchApp(w http.ResponseWriter, r *http.Request, workspaceID string, app string) {
	patcher, ok := h.catalog.(interface {
		SetAppTagOverride(context.Context, string, string, *string) (contract.Deployment, error)
	})
	if !ok {
		writeError(w, http.StatusNotImplemented, "app patch is not supported")
		return
	}
	tagOverride, ok := decodeCanonicalTagOverride(w, r)
	if !ok {
		return
	}
	deployment, err := patcher.SetAppTagOverride(r.Context(), workspaceID, app, tagOverride)
	if errors.Is(err, catalogpkg.ErrDeploymentNotFound) {
		writeError(w, http.StatusNotFound, "app not found: "+app)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, newCanonicalAppView(deployment))
}

func (h *Handler) handleCanonicalPatchAction(w http.ResponseWriter, r *http.Request, workspaceID string, app string, actionKey string) {
	patcher, ok := h.catalog.(interface {
		SetActionTagOverride(context.Context, string, string, string, *string) (contract.Action, error)
	})
	if !ok {
		writeError(w, http.StatusNotImplemented, "action patch is not supported")
		return
	}
	tagOverride, ok := decodeCanonicalTagOverride(w, r)
	if !ok {
		return
	}
	action, err := patcher.SetActionTagOverride(r.Context(), workspaceID, app, actionKey, tagOverride)
	if errors.Is(err, catalogpkg.ErrDeploymentNotFound) {
		writeError(w, http.StatusNotFound, "app not found: "+app)
		return
	}
	if errors.Is(err, catalogpkg.ErrActionNotFound) {
		writeError(w, http.StatusNotFound, "action not found: "+app+"/"+actionKey)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	deployment, ok := h.getCanonicalDeployment(w, r, workspaceID, app, "app not found: "+app)
	if !ok {
		return
	}
	schemaReader := h.newCanonicalSchemaReader(r.Context(), deployment)
	defer schemaReader.Close()
	view, err := h.newCanonicalActionView(schemaReader, deployment, actionKey, action)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *Handler) handleCanonicalRequeueApp(w http.ResponseWriter, r *http.Request, workspaceID string, app string) {
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "state store is not configured")
		return
	}
	deployment, ok := h.getCanonicalDeployment(w, r, workspaceID, app, "app not found: "+app)
	if !ok {
		return
	}
	var request struct {
		Action string `json:"action"`
	}
	body, err := readJSONBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &request); err != nil {
			writeError(w, http.StatusBadRequest, "request body must be a JSON object")
			return
		}
	}
	action := strings.TrimSpace(request.Action)
	var actionFilter *string
	if action != "" {
		if !validActionKey(action) {
			writeError(w, http.StatusBadRequest, "invalid action key")
			return
		}
		actionFilter = &action
	}
	actionTags := map[string]string{}
	for actionKey, actionSpec := range deployment.Actions {
		actionTags[actionKey] = effectiveRouteTag(deployment.Tag, deployment.TagOverride, actionSpec.Tag, actionSpec.TagOverride)
	}
	requeued, err := h.store.RequeueQueuedJobsForApp(r.Context(), state.RequeueAppSpec{
		WorkspaceID: workspaceID,
		AppKey:      app,
		ActionKey:   actionFilter,
		ActionTags:  actionTags,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"requeued": requeued})
}

func (h *Handler) handleCanonicalDeployment(w http.ResponseWriter, r *http.Request, workspaceID string, id string) {
	deployment, ok := h.getCanonicalDeployment(w, r, workspaceID, id, "deployment not found")
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, deployment)
}

func (h *Handler) handleCanonicalWorkerTags(w http.ResponseWriter, r *http.Request, workspaceID string) {
	snapshot, ok := h.loadCatalogSnapshot(w, r)
	if !ok {
		return
	}
	tags := map[string]struct{}{}
	for _, deployment := range canonicalDeployments(snapshot, workspaceID) {
		tags[defaultRouteTag()] = struct{}{}
		tags[effectiveRouteTag(deployment.Tag, deployment.TagOverride, nil, nil)] = struct{}{}
		for _, action := range deployment.Actions {
			tags[effectiveRouteTag(deployment.Tag, deployment.TagOverride, action.Tag, action.TagOverride)] = struct{}{}
		}
	}
	writeJSON(w, http.StatusOK, newCanonicalWorkerTagsView(tags))
}

func (h *Handler) loadGitSourceSnapshot(w http.ResponseWriter, r *http.Request) (gitsourcepkg.Snapshot, bool) {
	loader, ok := h.gitSources.(interface {
		Load(context.Context) (gitsourcepkg.Snapshot, error)
	})
	if !ok {
		writeError(w, http.StatusNotImplemented, "git source snapshot is not supported")
		return gitsourcepkg.Snapshot{}, false
	}
	snapshot, err := loader.Load(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return gitsourcepkg.Snapshot{}, false
	}
	return snapshot, true
}

func (h *Handler) loadCatalogSnapshot(w http.ResponseWriter, r *http.Request) (catalogpkg.Snapshot, bool) {
	loader, ok := h.catalog.(interface {
		Load(context.Context) (catalogpkg.Snapshot, error)
	})
	if !ok {
		writeError(w, http.StatusNotImplemented, "catalog snapshot is not supported")
		return catalogpkg.Snapshot{}, false
	}
	snapshot, err := loader.Load(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return catalogpkg.Snapshot{}, false
	}
	return snapshot, true
}

func (h *Handler) getCanonicalDeployment(w http.ResponseWriter, r *http.Request, workspaceID string, app string, notFoundMessage string) (contract.Deployment, bool) {
	if h.catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "catalog is not configured")
		return contract.Deployment{}, false
	}
	deployment, err := h.catalog.GetDeployment(r.Context(), app)
	if err != nil || contract.NormalizeWorkspace(deployment.SourceWorkspace()) != contract.NormalizeWorkspace(workspaceID) {
		writeError(w, http.StatusNotFound, notFoundMessage)
		return contract.Deployment{}, false
	}
	return deployment, true
}

type canonicalGitSourceView struct {
	ID               string     `json:"id"`
	WorkspaceID      string     `json:"workspace_id"`
	Name             string     `json:"name"`
	RepoURL          string     `json:"repo_url"`
	Branch           string     `json:"branch"`
	Subpath          string     `json:"subpath"`
	CredsRef         string     `json:"creds_ref"`
	Kind             string     `json:"kind"`
	LastSyncedCommit *string    `json:"last_synced_commit"`
	LastSyncedAt     *time.Time `json:"last_synced_at"`
}

func newCanonicalGitSourceView(source gitsourcepkg.Source) canonicalGitSourceView {
	return canonicalGitSourceView{
		ID:          source.ID,
		WorkspaceID: contract.NormalizeWorkspace(source.Workspace),
		Name:        source.ID,
		RepoURL:     source.RepoURL,
		Branch:      firstNonEmpty(source.Branch, "main"),
		Subpath:     source.Subpath,
		CredsRef:    source.TokenEnv,
		Kind:        "external",
	}
}

const probeTimeout = 15 * time.Second

type canonicalGitSourcePatchRequest struct {
	Name     *string `json:"name"`
	RepoURL  *string `json:"repo_url"`
	Branch   *string `json:"branch"`
	Subpath  *string `json:"subpath"`
	CredsRef *string `json:"creds_ref"`

	NameCamel     *string `json:"Name"`
	RepoURLCamel  *string `json:"RepoURL"`
	BranchCamel   *string `json:"Branch"`
	SubpathCamel  *string `json:"Subpath"`
	CredsRefCamel *string `json:"CredsRef"`

	ID        *string `json:"id"`
	RepoURLV1 *string `json:"repoUrl"`
	TokenEnv  *string `json:"tokenEnv"`
}

func canonicalGitSourcePatchFromRequest(w http.ResponseWriter, request canonicalGitSourcePatchRequest) (gitsourcepkg.Patch, bool) {
	var patch gitsourcepkg.Patch
	if value, ok := firstPresentString(request.Name, request.NameCamel, request.ID); ok {
		value = strings.TrimSpace(value)
		if value == "" {
			writeError(w, http.StatusBadRequest, "name cannot be empty")
			return patch, false
		}
		patch.ID = &value
	}
	if value, ok := firstPresentString(request.RepoURL, request.RepoURLCamel, request.RepoURLV1); ok {
		value = strings.TrimSpace(value)
		if value == "" {
			writeError(w, http.StatusBadRequest, "repo_url cannot be empty")
			return patch, false
		}
		patch.RepoURL = &value
	}
	if value, ok := firstPresentString(request.Branch, request.BranchCamel); ok {
		value = strings.TrimSpace(value)
		if value == "" {
			value = "main"
		}
		patch.Branch = &value
	}
	if value, ok := firstPresentString(request.Subpath, request.SubpathCamel); ok {
		value = strings.TrimSpace(value)
		patch.Subpath = &value
	}
	if value, ok := firstPresentString(request.CredsRef, request.CredsRefCamel, request.TokenEnv); ok {
		value = strings.TrimSpace(value)
		patch.TokenEnv = &value
	}
	return patch, true
}

func firstPresentString(values ...*string) (string, bool) {
	for _, value := range values {
		if value != nil {
			return *value, true
		}
	}
	return "", false
}

func stringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type canonicalSyncResult struct {
	Commit  string   `json:"commit"`
	App     string   `json:"app"`
	Actions []string `json:"actions"`
}

func newCanonicalSyncResult(deployment contract.Deployment) canonicalSyncResult {
	actions := make([]string, 0, len(deployment.Actions))
	for key := range deployment.Actions {
		actions = append(actions, deployment.App+"."+key)
	}
	sort.Strings(actions)
	return canonicalSyncResult{
		Commit:  deployment.Commit,
		App:     deployment.App,
		Actions: actions,
	}
}

type canonicalAppView struct {
	ID                   string   `json:"id"`
	WorkspaceID          string   `json:"workspace_id"`
	AppKey               string   `json:"app_key"`
	GitSourceID          *int64   `json:"git_source_id"`
	CommitSha            string   `json:"commit_sha"`
	Entrypoint           string   `json:"entrypoint"`
	Tag                  string   `json:"tag"`
	TagOverride          *string  `json:"tag_override,omitempty"`
	TimeoutS             int32    `json:"timeout_s"`
	ScriptLang           string   `json:"script_lang"`
	RequiredCapabilities []string `json:"required_capabilities"`
	EffectiveRouteTag    string   `json:"effective_route_tag"`
}

type canonicalAppSummaryView struct {
	canonicalAppView
	ActionsCount   int64 `json:"actions_count"`
	SchedulesCount int64 `json:"schedules_count"`
	FlowsCount     int64 `json:"flows_count"`
}

type canonicalAppHistoryItem struct {
	ID           string    `json:"id"`
	CommitSha    string    `json:"commit_sha"`
	Entrypoint   string    `json:"entrypoint"`
	Source       string    `json:"source"`
	GitSourceKey string    `json:"git_source_key,omitempty"`
	DeploymentID *string   `json:"deployment_id,omitempty"`
	Message      *string   `json:"message,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	Status       string    `json:"status,omitempty"`
}

type canonicalActionView struct {
	ID                    string          `json:"id"`
	WorkspaceID           string          `json:"workspace_id"`
	AppKey                string          `json:"app_key"`
	ActionKey             string          `json:"action_key"`
	InputSchema           json.RawMessage `json:"input_schema,omitempty"`
	OutputSchema          json.RawMessage `json:"output_schema,omitempty"`
	Tag                   *string         `json:"tag,omitempty"`
	TagOverride           *string         `json:"tag_override,omitempty"`
	TimeoutS              *int32          `json:"timeout_s,omitempty"`
	RequiredCapabilities  []string        `json:"required_capabilities,omitempty"`
	EffectiveCapabilities []string        `json:"effective_capabilities"`
	EffectiveRouteTag     string          `json:"effective_route_tag"`
}

type canonicalWorkerTagsView struct {
	Tags         []canonicalTagLiveness `json:"tags"`
	DedicatedTag *string                `json:"dedicated_tag"`
}

type canonicalTagLiveness struct {
	Tag          string   `json:"tag"`
	LiveWorkers  int64    `json:"live_workers"`
	Capabilities []string `json:"capabilities"`
	Workers      []any    `json:"workers"`
}

func canonicalDeployments(snapshot catalogpkg.Snapshot, workspaceID string) []contract.Deployment {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	deployments := make([]contract.Deployment, 0, len(snapshot.Deployments))
	for _, deployment := range snapshot.Deployments {
		if contract.NormalizeWorkspace(deployment.SourceWorkspace()) != workspaceID {
			continue
		}
		deployments = append(deployments, deployment)
	}
	sort.Slice(deployments, func(i, j int) bool {
		return deployments[i].App < deployments[j].App
	})
	return deployments
}

func newCanonicalAppSummaryView(deployment contract.Deployment) canonicalAppSummaryView {
	return canonicalAppSummaryView{
		canonicalAppView: newCanonicalAppView(deployment),
		ActionsCount:     int64(len(deployment.Actions)),
	}
}

func newCanonicalAppHistoryItem(item catalogpkg.DeploymentHistory) canonicalAppHistoryItem {
	return canonicalAppHistoryItem{
		ID:           item.ID,
		CommitSha:    item.Commit,
		Entrypoint:   item.Entrypoint,
		Source:       firstNonEmpty(item.Source, "external_sync"),
		GitSourceKey: item.GitSourceID,
		CreatedAt:    item.CreatedAt,
		Status:       item.Status,
	}
}

func newCanonicalAppView(deployment contract.Deployment) canonicalAppView {
	return canonicalAppView{
		ID:                   canonicalAppID(deployment),
		WorkspaceID:          contract.NormalizeWorkspace(deployment.SourceWorkspace()),
		AppKey:               deployment.App,
		GitSourceID:          nil,
		CommitSha:            deployment.Commit,
		Entrypoint:           canonicalDeploymentEntrypoint(deployment),
		Tag:                  effectiveRouteTag(deployment.Tag, nil, nil, nil),
		TagOverride:          cloneStringPtr(deployment.TagOverride),
		ScriptLang:           canonicalDeploymentScriptLang(deployment),
		RequiredCapabilities: []string{},
		EffectiveRouteTag:    effectiveRouteTag(deployment.Tag, deployment.TagOverride, nil, nil),
	}
}

func (h *Handler) newCanonicalActionViews(schemaReader *canonicalSchemaReader, deployment contract.Deployment) ([]canonicalActionView, error) {
	keys := make([]string, 0, len(deployment.Actions))
	for key := range deployment.Actions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	actions := make([]canonicalActionView, 0, len(keys))
	for _, key := range keys {
		action, err := h.newCanonicalActionView(schemaReader, deployment, key, deployment.Actions[key])
		if err != nil {
			return nil, err
		}
		actions = append(actions, action)
	}
	return actions, nil
}

func (h *Handler) newCanonicalActionView(schemaReader *canonicalSchemaReader, deployment contract.Deployment, actionKey string, action contract.Action) (canonicalActionView, error) {
	inputSchema, err := schemaReader.Read(action.InputSchema)
	if err != nil {
		return canonicalActionView{}, fmt.Errorf("action %s.%s input schema: %w", deployment.App, actionKey, err)
	}
	outputSchema, err := schemaReader.Read(action.OutputSchema)
	if err != nil {
		return canonicalActionView{}, fmt.Errorf("action %s.%s output schema: %w", deployment.App, actionKey, err)
	}
	return canonicalActionView{
		ID:                    canonicalAppID(deployment) + "/" + actionKey,
		WorkspaceID:           contract.NormalizeWorkspace(deployment.SourceWorkspace()),
		AppKey:                deployment.App,
		ActionKey:             actionKey,
		InputSchema:           inputSchema,
		OutputSchema:          outputSchema,
		Tag:                   cloneStringPtr(action.Tag),
		TagOverride:           cloneStringPtr(action.TagOverride),
		TimeoutS:              canonicalTimeoutSeconds(action.TimeoutMs),
		RequiredCapabilities:  []string{},
		EffectiveCapabilities: []string{},
		EffectiveRouteTag:     effectiveRouteTag(deployment.Tag, deployment.TagOverride, action.Tag, action.TagOverride),
	}, nil
}

type canonicalSchemaReader struct {
	ctx   context.Context
	store interface {
		FetchTo(context.Context, string, string, string, string) error
	}
	deployment contract.Deployment
	sourceDir  string
	err        error
}

func (h *Handler) newCanonicalSchemaReader(ctx context.Context, deployment contract.Deployment) *canonicalSchemaReader {
	reader := &canonicalSchemaReader{ctx: ctx, deployment: deployment}
	if h.syncer != nil && h.syncer.Store != nil {
		reader.store = h.syncer.Store
	}
	return reader
}

func (r *canonicalSchemaReader) Close() {
	if r.sourceDir != "" {
		_ = os.RemoveAll(r.sourceDir)
	}
}

func (r *canonicalSchemaReader) Read(schemaPath string) (json.RawMessage, error) {
	schemaPath = strings.TrimSpace(schemaPath)
	if schemaPath == "" {
		return nil, nil
	}
	if r.store == nil {
		return nil, nil
	}
	sourceDir, err := r.ensureSourceDir()
	if err != nil {
		return nil, err
	}
	normalized, err := contract.NormalizeSourcePath(schemaPath)
	if err != nil {
		return nil, err
	}
	if normalized == "" {
		return nil, nil
	}
	data, err := os.ReadFile(filepath.Join(sourceDir, filepath.FromSlash(normalized)))
	if err != nil {
		return nil, err
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("%q is not valid JSON", schemaPath)
	}
	return json.RawMessage(append([]byte(nil), data...)), nil
}

func (r *canonicalSchemaReader) ensureSourceDir() (string, error) {
	if r.err != nil {
		return "", r.err
	}
	if r.sourceDir != "" {
		return r.sourceDir, nil
	}
	if r.store == nil {
		return "", nil
	}
	sourceDir, err := os.MkdirTemp("", "windforce-lite-schema-")
	if err != nil {
		r.err = err
		return "", err
	}
	if err := r.store.FetchTo(r.ctx, sourceDir, r.deployment.SourceWorkspace(), r.deployment.SourceGitSourceID(), r.deployment.Commit); err != nil {
		_ = os.RemoveAll(sourceDir)
		r.err = err
		return "", err
	}
	r.sourceDir = sourceDir
	return sourceDir, nil
}

func canonicalAppID(deployment contract.Deployment) string {
	return contract.NormalizeWorkspace(deployment.SourceWorkspace()) + "/" + deployment.App
}

func canonicalDeploymentEntrypoint(deployment contract.Deployment) string {
	keys := make([]string, 0, len(deployment.Actions))
	for key := range deployment.Actions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if deployment.Actions[key].Entrypoint != "" {
			return deployment.Actions[key].Entrypoint
		}
	}
	return ""
}

func canonicalDeploymentScriptLang(deployment contract.Deployment) string {
	keys := make([]string, 0, len(deployment.Actions))
	for key := range deployment.Actions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if deployment.Actions[key].Runtime != "" {
			return deployment.Actions[key].Runtime
		}
	}
	return ""
}

func canonicalTimeoutSeconds(timeoutMs int64) *int32 {
	if timeoutMs <= 0 {
		return nil
	}
	value := int32((timeoutMs + 999) / 1000)
	return &value
}

func defaultRouteTag() string {
	return contract.DefaultRouteTag
}

func effectiveRouteTag(appTag string, appTagOverride *string, actionTag *string, actionTagOverride *string) string {
	return contract.EffectiveRouteTag(appTag, appTagOverride, actionTag, actionTagOverride)
}

func decodeCanonicalTagOverride(w http.ResponseWriter, r *http.Request) (*string, bool) {
	var request struct {
		TagOverride json.RawMessage `json:"tag_override"`
	}
	if err := readOptionalJSON(r, &request); err != nil || request.TagOverride == nil {
		writeError(w, http.StatusBadRequest, "tag_override required (string to set, null to clear)")
		return nil, false
	}
	if string(bytes.TrimSpace(request.TagOverride)) == "null" {
		return nil, true
	}
	var value string
	if err := json.Unmarshal(request.TagOverride, &value); err != nil || !validRouteTag(value) {
		writeError(w, http.StatusBadRequest, "tag_override must be a valid tag (lowercase alphanumeric, _ or -, max 64) or null")
		return nil, false
	}
	value = strings.TrimSpace(value)
	return &value, true
}

func validRouteTag(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return false
	}
	for _, item := range value {
		if item >= 'a' && item <= 'z' {
			continue
		}
		if item >= '0' && item <= '9' {
			continue
		}
		if item == '_' || item == '-' {
			continue
		}
		return false
	}
	return true
}

func validActionKey(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 || !utf8.ValidString(value) {
		return false
	}
	if value == "." || value == ".." || strings.ContainsAny(value, `/\`) {
		return false
	}
	return true
}

func cloneRawMessage(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), value...)
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func newCanonicalWorkerTagsView(tags map[string]struct{}) canonicalWorkerTagsView {
	if tags == nil {
		tags = map[string]struct{}{}
	}
	if len(tags) == 0 {
		tags[defaultRouteTag()] = struct{}{}
	}
	keys := make([]string, 0, len(tags))
	for tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		keys = append(keys, tag)
	}
	sort.Strings(keys)
	items := make([]canonicalTagLiveness, 0, len(keys))
	for _, tag := range keys {
		items = append(items, canonicalTagLiveness{
			Tag:          tag,
			Capabilities: []string{},
			Workers:      []any{},
		})
	}
	return canonicalWorkerTagsView{
		Tags: items,
	}
}

const (
	sourceFileCapBytes  = 512 * 1024
	sourceTotalCapBytes = 8 * 1024 * 1024
)

func readCanonicalSourceFiles(root string) (map[string]string, []string, error) {
	files := map[string]string{}
	skipped := []string{}
	total := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > sourceFileCapBytes || total+int(info.Size()) > sourceTotalCapBytes {
			skipped = append(skipped, rel)
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !utf8.Valid(content) {
			skipped = append(skipped, rel)
			return nil
		}
		files[rel] = string(content)
		total += len(content)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(skipped)
	return files, skipped, nil
}

func (h *Handler) handleJobList(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "state store is not configured")
		return
	}
	query, limit, ok := parseJobListQuery(w, r, workspaceID)
	if !ok {
		return
	}
	query.Limit = limit + 1
	items, err := h.store.ListJobs(r.Context(), query)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	pagination := map[string]any{
		"limit":    limit,
		"count":    len(items),
		"has_more": hasMore,
	}
	if hasMore {
		last := items[len(items)-1]
		pagination["next_cursor"] = encodeJobCursor(last.CreatedAt, last.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "pagination": pagination})
}

func (h *Handler) handleJobSummary(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "state store is not configured")
		return
	}
	recent := 24 * time.Hour
	if raw := strings.TrimSpace(r.URL.Query().Get("recent_seconds")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 || value > 7*24*60*60 {
			writeError(w, http.StatusBadRequest, "recent_seconds must be between 1 and 604800")
			return
		}
		recent = time.Duration(value) * time.Second
	}
	summary, err := h.store.JobSummary(r.Context(), workspaceID, recent)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (h *Handler) handleJobRun(w http.ResponseWriter, r *http.Request, workspaceID string, app string, action string, wait bool) {
	timeout := time.Duration(0)
	if wait {
		var ok bool
		timeout, ok = parseRunWaitTimeout(w, r)
		if !ok {
			return
		}
	}
	job, ok := h.enqueueJobRun(w, r, workspaceID, app, action)
	if !ok {
		return
	}
	if !wait {
		writeJSON(w, http.StatusCreated, map[string]string{"job_id": job.ID})
		return
	}
	h.waitForJobResult(w, r, workspaceID, job.ID, timeout)
}

func (h *Handler) handleJobWebhook(w http.ResponseWriter, r *http.Request, workspaceID string, app string, action string) {
	input, ok := readWebhookRaw(w, r)
	if !ok {
		return
	}
	triggerHeaders := captureWebhookHeaders(r)
	job, ok := h.enqueueJob(w, r, workspaceID, app, action, "webhook", input, triggerHeaders)
	if !ok {
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"job_id": job.ID})
}

func (h *Handler) enqueueJobRun(w http.ResponseWriter, r *http.Request, workspaceID string, app string, action string) (state.Job, bool) {
	input, ok := readRunInput(w, r)
	if !ok {
		return state.Job{}, false
	}
	return h.enqueueJob(w, r, workspaceID, app, action, "api", input, nil)
}

func (h *Handler) enqueueJob(w http.ResponseWriter, r *http.Request, workspaceID string, app string, action string, triggerKind string, input json.RawMessage, triggerHeaders json.RawMessage) (state.Job, bool) {
	if h.store == nil || h.catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "job API is not configured")
		return state.Job{}, false
	}
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	deployment, err := h.catalog.GetDeployment(r.Context(), app)
	if err != nil || contract.NormalizeWorkspace(deployment.SourceWorkspace()) != workspaceID {
		writeError(w, http.StatusNotFound, "app not found: "+app)
		return state.Job{}, false
	}
	if _, ok := deployment.Actions[action]; !ok {
		writeError(w, http.StatusNotFound, "action not found: "+app+"/"+action)
		return state.Job{}, false
	}
	run := state.NewRun("windforce", "", app, action, deployment, input)
	if correlationID := state.CleanID(r.Header.Get("X-Request-ID")); correlationID != "" {
		run.CorrelationID = correlationID
	}
	job := state.NewActionJob(run, input)
	job.Payload.TriggerKind = triggerKind
	job.Payload.TriggerHeaders = cloneRawMessage(triggerHeaders)
	if err := h.store.CreateRunAndEnqueue(r.Context(), run, job); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, state.ErrConflict) {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return state.Job{}, false
	}
	return job, true
}

func (h *Handler) handleJobStatus(w http.ResponseWriter, r *http.Request, workspaceID string, jobID string) {
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "state store is not configured")
		return
	}
	job, run, found, err := h.store.GetJob(r.Context(), workspaceID, jobID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	writeJSON(w, http.StatusOK, newJobStatus(workspaceID, job, run))
}

func (h *Handler) handleJobResult(w http.ResponseWriter, r *http.Request, workspaceID string, jobID string) {
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "state store is not configured")
		return
	}
	job, run, found, err := h.store.GetJob(r.Context(), workspaceID, jobID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	status, result, done := jobResult(job, run)
	if !done {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "pending"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": status, "result": result})
}

func (h *Handler) handleJobCancel(w http.ResponseWriter, r *http.Request, workspaceID string, jobID string) {
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "state store is not configured")
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	if err := readOptionalJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := h.store.CancelJob(r.Context(), workspaceID, jobID, request.Reason)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !result.Found {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) waitForJobResult(w http.ResponseWriter, r *http.Request, workspaceID string, jobID string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for {
		job, run, found, err := h.store.GetJob(r.Context(), workspaceID, jobID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, "job not found")
			return
		}
		status, result, done := jobResult(job, run)
		if done {
			writeJSON(w, http.StatusOK, map[string]any{"job_id": jobID, "status": status, "result": result})
			return
		}
		if !time.Now().Before(deadline) {
			writeJSON(w, http.StatusAccepted, map[string]string{"job_id": jobID, "status": "pending"})
			return
		}
		sleep := 50 * time.Millisecond
		if remaining := time.Until(deadline); remaining < sleep {
			sleep = remaining
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(sleep):
		}
	}
}

func (h *Handler) handleJobLogs(w http.ResponseWriter, r *http.Request, workspaceID string, jobID string) {
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "state store is not configured")
		return
	}
	logs, exists, err := h.store.GetLogs(r.Context(), workspaceID, jobID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	tailBytes, err := parseTailBytes(r.URL.Query().Get("tail_bytes"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	data := []byte(logs)
	if tailBytes >= 0 && len(data) > tailBytes {
		data = data[len(data)-tailBytes:]
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
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
	schemaReader := h.newCanonicalSchemaReader(r.Context(), deployment)
	defer schemaReader.Close()
	view, err := h.newCanonicalActionView(schemaReader, deployment, route.action, action)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"app":              route.app,
		"action":           route.action,
		"inputSchema":      view.InputSchema,
		"outputSchema":     view.OutputSchema,
		"inputSchemaPath":  action.InputSchema,
		"outputSchemaPath": action.OutputSchema,
		"metadata":         action,
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

type jobStatusResponse struct {
	ID          string          `json:"id"`
	WorkspaceID string          `json:"workspace_id"`
	State       string          `json:"state"`
	Status      *string         `json:"status,omitempty"`
	Worker      *string         `json:"worker,omitempty"`
	AppKey      *string         `json:"app_key,omitempty"`
	ActionKey   *string         `json:"action_key,omitempty"`
	Kind        *string         `json:"kind,omitempty"`
	CommitSha   *string         `json:"commit_sha,omitempty"`
	Tag         string          `json:"tag,omitempty"`
	Input       json.RawMessage `json:"input,omitempty"`
	CreatedAt   *time.Time      `json:"created_at,omitempty"`
	StartedAt   *time.Time      `json:"started_at,omitempty"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
	DurationMs  int64           `json:"duration_ms,omitempty"`
}

func newJobStatus(workspaceID string, job state.Job, run state.Run) jobStatusResponse {
	stateValue := "queued"
	var statusValue *string
	var worker *string
	var startedAt *time.Time
	var completedAt *time.Time
	switch job.State {
	case state.JobRunning:
		stateValue = "running"
		worker = stringPtr(job.LeaseOwner)
		startedAt = &job.UpdatedAt
	case state.JobSucceeded, state.JobFailed:
		stateValue = "completed"
		status := terminalJobStatus(job, run)
		statusValue = &status
		completedAt = &run.UpdatedAt
	}
	app := job.Payload.App
	action := job.Payload.Action
	kind := job.Kind
	commit := job.Payload.Commit
	tag := strings.TrimSpace(job.Payload.Tag)
	if tag == "" {
		tag = contract.EffectiveRouteTag(job.Payload.Deployment.Tag, job.Payload.Deployment.TagOverride, job.Payload.ActionSpec.Tag, job.Payload.ActionSpec.TagOverride)
	}
	response := jobStatusResponse{
		ID:          job.ID,
		WorkspaceID: contract.NormalizeWorkspace(workspaceID),
		State:       stateValue,
		Status:      statusValue,
		Worker:      worker,
		AppKey:      stringPtr(app),
		ActionKey:   stringPtr(action),
		Kind:        stringPtr(kind),
		CommitSha:   stringPtr(commit),
		Tag:         tag,
		Input:       cloneRaw(job.Payload.Input),
		CreatedAt:   &job.CreatedAt,
		StartedAt:   startedAt,
		CompletedAt: completedAt,
	}
	if run.Result != nil {
		response.DurationMs = run.Result.DurationMs
	}
	return response
}

func jobResult(job state.Job, run state.Run) (string, json.RawMessage, bool) {
	if job.State == state.JobQueued || job.State == state.JobRunning {
		return "", nil, false
	}
	status := terminalJobStatus(job, run)
	switch status {
	case "completed":
		return status, rawOrNull(run.Output), true
	case "canceled":
		message := runErrorMessage(run)
		if message == "" {
			message = "job canceled"
		}
		return status, mustRaw(map[string]string{"name": "Canceled", "message": message}), true
	default:
		message := runErrorMessage(run)
		if message == "" {
			message = "job failed"
		}
		return "failed", mustRaw(map[string]string{"name": "Error", "message": message}), true
	}
}

func terminalJobStatus(job state.Job, run state.Run) string {
	if run.State == state.RunCanceled {
		return "canceled"
	}
	if job.State == state.JobSucceeded || run.State == state.RunSucceeded || run.State == state.RunWaitingHuman {
		return "completed"
	}
	return "failed"
}

func runErrorMessage(run state.Run) string {
	if run.Result != nil && run.Result.Error != "" {
		return run.Result.Error
	}
	if len(run.Error) == 0 {
		return ""
	}
	var envelope struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(run.Error, &envelope) == nil {
		return envelope.Message
	}
	return string(run.Error)
}

func rawOrNull(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return json.RawMessage("null")
	}
	return cloneRaw(value)
}

func mustRaw(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage("null")
	}
	return data
}

func cloneRaw(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), value...)
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
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

const maxTailBytes = 1048576
const (
	defaultRunWaitTimeout = 30 * time.Second
	maxRunWaitTimeout     = 30 * time.Second
)

func parseTailBytes(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return -1, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, errors.New("tail_bytes must be a non-negative integer")
	}
	if value > maxTailBytes {
		return 0, errors.New("tail_bytes exceeds server limit")
	}
	return int(value), nil
}

func parseRunWaitTimeout(w http.ResponseWriter, r *http.Request) (time.Duration, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("timeout_ms"))
	if raw == "" {
		return defaultRunWaitTimeout, true
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		writeError(w, http.StatusBadRequest, "timeout_ms must be a non-negative integer")
		return 0, false
	}
	timeout := time.Duration(value) * time.Millisecond
	if timeout > maxRunWaitTimeout {
		timeout = maxRunWaitTimeout
	}
	return timeout, true
}

const (
	defaultJobListLimit = 50
	maxJobListLimit     = 500
)

func parseJobListQuery(w http.ResponseWriter, r *http.Request, workspaceID string) (state.JobListQuery, int, bool) {
	query := r.URL.Query()
	status := strings.TrimSpace(query.Get("status"))
	if status == "" {
		status = "all"
	}
	if !validJobStatusFilter(status) {
		writeError(w, http.StatusBadRequest, "invalid status filter")
		return state.JobListQuery{}, 0, false
	}
	order := strings.TrimSpace(query.Get("order"))
	if order != "" && order != "created_at_desc" {
		writeError(w, http.StatusBadRequest, "unsupported order")
		return state.JobListQuery{}, 0, false
	}
	limit := defaultJobListLimit
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 || value > maxJobListLimit {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 500")
			return state.JobListQuery{}, 0, false
		}
		limit = value
	}
	var cursorCreatedAt *time.Time
	cursorID := ""
	if raw := strings.TrimSpace(query.Get("cursor")); raw != "" {
		createdAt, id, err := decodeJobCursor(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid cursor")
			return state.JobListQuery{}, 0, false
		}
		cursorCreatedAt = &createdAt
		cursorID = id
	}
	since, ok := parseOptionalTime(w, query.Get("since"), "since")
	if !ok {
		return state.JobListQuery{}, 0, false
	}
	until, ok := parseOptionalTime(w, query.Get("until"), "until")
	if !ok {
		return state.JobListQuery{}, 0, false
	}
	return state.JobListQuery{
		WorkspaceID:     contract.NormalizeWorkspace(workspaceID),
		Status:          status,
		AppKey:          strings.TrimSpace(query.Get("app")),
		ActionKey:       strings.TrimSpace(query.Get("action")),
		TriggerKind:     strings.TrimSpace(query.Get("trigger_kind")),
		Limit:           limit,
		CursorCreatedAt: cursorCreatedAt,
		CursorID:        cursorID,
		Since:           since,
		Until:           until,
	}, limit, true
}

func validJobStatusFilter(status string) bool {
	switch status {
	case "queued", "running", "success", "failure", "completed", "failed", "canceled", "all":
		return true
	default:
		return false
	}
}

func parseOptionalTime(w http.ResponseWriter, raw string, name string) (*time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, true
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, name+" must be RFC3339")
		return nil, false
	}
	return &value, true
}

func encodeJobCursor(createdAt time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(createdAt.UTC().Format(time.RFC3339Nano) + "|" + id))
}

func decodeJobCursor(raw string) (time.Time, string, error) {
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return time.Time{}, "", err
	}
	createdRaw, id, ok := strings.Cut(string(data), "|")
	if !ok {
		return time.Time{}, "", fmt.Errorf("malformed cursor")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdRaw)
	if err != nil {
		return time.Time{}, "", err
	}
	if id == "" {
		return time.Time{}, "", fmt.Errorf("malformed cursor")
	}
	return createdAt, id, nil
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
