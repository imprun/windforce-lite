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
	"sync"
	"time"
	"unicode/utf8"

	catalogpkg "github.com/imprun/windforce-lite/internal/catalog"
	"github.com/imprun/windforce-lite/internal/contract"
	gitsourcepkg "github.com/imprun/windforce-lite/internal/gitsource"
	"github.com/imprun/windforce-lite/internal/sampleapp"
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
	TriggerResponse(run state.Run, route AdapterRoute) (int, any)
}

type Config struct {
	Store           state.Store
	Catalog         Catalog
	Syncer          *syncer.Syncer
	GitSources      GitSourceRegistry
	EnableTrigger   bool
	EnableAPI       bool
	TriggerAdapters []TriggerAdapter
	TriggerToken    string
	AdminToken      string
	SampleRoot      string
	Wait            time.Duration
}

type Handler struct {
	store           state.Store
	catalog         Catalog
	syncer          *syncer.Syncer
	gitSources      GitSourceRegistry
	enableTrigger   bool
	enableAPI       bool
	triggerAdapters []TriggerAdapter
	triggerToken    string
	adminToken      string
	sampleRoot      string
	wait            time.Duration
	syncLocks       sync.Map
}

func New(config Config) http.Handler {
	return &Handler{
		store:           config.Store,
		catalog:         config.Catalog,
		syncer:          config.Syncer,
		gitSources:      config.GitSources,
		enableTrigger:   config.EnableTrigger,
		enableAPI:       config.EnableAPI,
		triggerAdapters: append([]TriggerAdapter(nil), config.TriggerAdapters...),
		triggerToken:    config.TriggerToken,
		adminToken:      config.AdminToken,
		sampleRoot:      config.SampleRoot,
		wait:            config.Wait,
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
	if len(parts) == 8 && parts[0] == "api" && parts[1] == "w" && parts[3] == "apps" && parts[5] == "actions" && parts[7] == "schema" && r.Method == http.MethodGet {
		h.handleCanonicalActionSchema(w, r, parts[2], parts[4], parts[6])
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
	return false
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
	}
	if err := readOptionalJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "name and repo_url required")
		return
	}
	name := strings.TrimSpace(firstNonEmpty(request.Name, request.NameCamel))
	repoURL := strings.TrimSpace(firstNonEmpty(request.RepoURL, request.RepoURLCamel))
	branch := strings.TrimSpace(firstNonEmpty(request.Branch, request.BranchCamel))
	subpath := strings.TrimSpace(firstNonEmpty(request.Subpath, request.SubpathCamel))
	credsRef := strings.TrimSpace(firstNonEmpty(request.CredsRef, request.CredsRefCamel))
	if name == "" || repoURL == "" {
		writeError(w, http.StatusBadRequest, "name and repo_url required")
		return
	}
	source := gitsourcepkg.Source{
		Workspace: workspaceID,
		Name:      name,
		RepoURL:   repoURL,
		Branch:    branch,
		Subpath:   subpath,
		TokenEnv:  credsRef,
	}
	source, ok := h.createGitSource(w, r, source)
	if !ok {
		return
	}
	writeJSON(w, http.StatusCreated, newCanonicalGitSourceView(source))
}

func (h *Handler) createGitSource(w http.ResponseWriter, r *http.Request, source gitsourcepkg.Source) (gitsourcepkg.Source, bool) {
	if creator, ok := h.gitSources.(interface {
		Create(context.Context, gitsourcepkg.Source) (gitsourcepkg.Source, error)
	}); ok {
		created, err := creator.Create(r.Context(), source)
		if errors.Is(err, gitsourcepkg.ErrGitSourceConflict) {
			writeError(w, http.StatusConflict, "git source name already exists")
			return gitsourcepkg.Source{}, false
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return gitsourcepkg.Source{}, false
		}
		return created, true
	}

	if _, err := h.gitSources.Get(r.Context(), source.Workspace, source.ID); err == nil {
		writeError(w, http.StatusConflict, "git source name already exists")
		return gitsourcepkg.Source{}, false
	} else if !errors.Is(err, gitsourcepkg.ErrGitSourceNotFound) {
		writeError(w, http.StatusInternalServerError, err.Error())
		return gitsourcepkg.Source{}, false
	}
	if err := h.gitSources.Upsert(r.Context(), source); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return gitsourcepkg.Source{}, false
	}
	source, err := h.gitSources.Get(r.Context(), source.Workspace, source.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return gitsourcepkg.Source{}, false
	}
	return source, true
}

func (h *Handler) handleCanonicalProbeGitSource(w http.ResponseWriter, r *http.Request, workspaceID string) {
	var request struct {
		RepoURL     string `json:"repo_url"`
		Branch      string `json:"branch"`
		AccessToken string `json:"access_token"`
		CredsRef    string `json:"creds_ref"`
	}
	if err := readOptionalJSON(r, &request); err != nil || strings.TrimSpace(request.RepoURL) == "" {
		writeError(w, http.StatusBadRequest, "repo_url required")
		return
	}
	token := strings.TrimSpace(request.AccessToken)
	if token == "" {
		resolved, err := h.resolveGitSourceCreds(r.Context(), workspaceID, request.CredsRef)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		token = resolved
	}
	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()
	branches, err := sourcepkg.ListRemoteBranches(ctx, strings.TrimSpace(request.RepoURL), token)
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

func (h *Handler) handleCanonicalSampleGitSource(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if h.syncer == nil {
		writeError(w, http.StatusServiceUnavailable, "sync API is not configured")
		return
	}
	if h.gitSources == nil {
		writeError(w, http.StatusServiceUnavailable, "git source registry is not configured")
		return
	}
	var request struct {
		AppKey string `json:"app_key"`
	}
	if err := readOptionalJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	repo, err := sampleapp.EnsureRepository(r.Context(), h.sampleRoot, workspaceID, request.AppKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	source := gitsourcepkg.Source{
		Workspace: workspaceID,
		Name:      repo.SourceName,
		RepoURL:   repo.RepoURL,
		Branch:    repo.Branch,
		Kind:      "managed",
	}
	status := http.StatusCreated
	existing, err := h.gitSources.Get(r.Context(), workspaceID, repo.SourceName)
	if err == nil {
		source.CreatedAt = existing.CreatedAt
		source.LastSyncedCommit = existing.LastSyncedCommit
		source.LastSyncedAt = existing.LastSyncedAt
		if err := h.gitSources.Upsert(r.Context(), source); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		source, err = h.gitSources.Get(r.Context(), workspaceID, repo.SourceName)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		status = http.StatusOK
	} else if errors.Is(err, gitsourcepkg.ErrGitSourceNotFound) {
		created, ok := h.createGitSource(w, r, source)
		if !ok {
			return
		}
		source = created
	} else {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	deployment, ok := h.syncGitSource(w, r, workspaceID, source)
	if !ok {
		return
	}
	writeJSON(w, status, map[string]any{
		"source":      newCanonicalGitSourceView(source),
		"sync_result": newCanonicalSyncResult(deployment),
	})
}

func (h *Handler) handleCanonicalPatchGitSource(w http.ResponseWriter, r *http.Request, workspaceID string, sourceID string) {
	var ok bool
	sourceID, ok = requireCanonicalGitSourceRouteID(w, sourceID)
	if !ok {
		return
	}
	patcher, ok := h.gitSources.(interface {
		Patch(context.Context, string, string, gitsourcepkg.Patch) (gitsourcepkg.Source, error)
	})
	if !ok {
		writeError(w, http.StatusNotImplemented, "git source patch is not supported")
		return
	}
	var request canonicalGitSourcePatchRequest
	if err := readRequiredJSON(r, &request); err != nil {
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
	var ok bool
	sourceID, ok = requireCanonicalGitSourceRouteID(w, sourceID)
	if !ok {
		return
	}
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
	var ok bool
	sourceID, ok = requireCanonicalGitSourceRouteID(w, sourceID)
	if !ok {
		return
	}
	if h.syncer == nil {
		writeError(w, http.StatusServiceUnavailable, "sync API is not configured")
		return
	}
	if h.gitSources == nil {
		writeError(w, http.StatusServiceUnavailable, "git source registry is not configured")
		return
	}
	source, err := h.gitSources.Get(r.Context(), workspaceID, sourceID)
	if err != nil {
		if errors.Is(err, gitsourcepkg.ErrGitSourceNotFound) {
			writeError(w, http.StatusNotFound, "git source not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	deployment, ok := h.syncGitSource(w, r, workspaceID, source)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, newCanonicalSyncResult(deployment))
}

func (h *Handler) syncGitSource(w http.ResponseWriter, r *http.Request, workspaceID string, source gitsourcepkg.Source) (contract.Deployment, bool) {
	release, ok := h.acquireGitSourceOperation(workspaceID, source)
	if !ok {
		writeError(w, http.StatusConflict, "git source operation already in progress")
		return contract.Deployment{}, false
	}
	defer release()

	token, err := h.resolveGitSourceCreds(r.Context(), workspaceID, source.TokenEnv)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return contract.Deployment{}, false
	}
	s := *h.syncer
	deployment, err := s.Sync(r.Context(), syncer.Source{
		Workspace:   workspaceID,
		GitSourceID: source.ID,
		RepoURL:     source.RepoURL,
		Branch:      source.Branch,
		Subpath:     source.Subpath,
		Token:       token,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return contract.Deployment{}, false
	}
	if marker, ok := h.gitSources.(interface {
		MarkSynced(context.Context, string, string, string, time.Time) (gitsourcepkg.Source, error)
	}); ok {
		if _, err := marker.MarkSynced(r.Context(), workspaceID, source.ID, deployment.Commit, time.Now().UTC()); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return contract.Deployment{}, false
		}
	}
	return deployment, true
}

func (h *Handler) acquireGitSourceOperation(workspaceID string, source gitsourcepkg.Source) (func(), bool) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	sourceID := strings.TrimSpace(source.ID)
	if sourceID == "" {
		sourceID = strings.TrimSpace(source.Name)
	}
	key := workspaceID + "\x00" + sourceID
	value, _ := h.syncLocks.LoadOrStore(key, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	if !lock.TryLock() {
		return nil, false
	}
	return lock.Unlock, true
}

func (h *Handler) resolveGitSourceCreds(ctx context.Context, workspaceID string, credsRef string) (string, error) {
	credsRef = strings.TrimSpace(credsRef)
	if credsRef == "" || h.store == nil {
		return "", nil
	}
	variable, found, err := h.store.GetVariable(ctx, workspaceID, "", credsRef)
	if err != nil || !found {
		return "", err
	}
	return variable.Value, nil
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
	if !validAppKey(app) {
		writeError(w, http.StatusBadRequest, "invalid app key")
		return
	}
	deployment, ok := h.getCanonicalDeployment(w, r, workspaceID, app, "app not found")
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
	if !validAppKey(app) {
		writeError(w, http.StatusBadRequest, "invalid app key")
		return
	}
	deployment, ok := h.getCanonicalDeployment(w, r, workspaceID, app, "app not found")
	if !ok {
		return
	}
	if h.syncer == nil || h.syncer.Store == nil {
		writeError(w, http.StatusInternalServerError, "source storage not configured")
		return
	}
	exists, err := h.syncer.Store.Exists(r.Context(), deployment.SourceWorkspace(), deployment.SourceGitSourceID(), deployment.Commit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "source commit is not materialized \u2014 re-sync the app")
		return
	}
	sourceDir, err := os.MkdirTemp("", "windforce-lite-app-source-")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer os.RemoveAll(sourceDir)
	if err := h.syncer.Store.FetchTo(r.Context(), sourceDir, deployment.SourceWorkspace(), deployment.SourceGitSourceID(), deployment.Commit); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	files, skipped, err := readCanonicalSourceFiles(sourceDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"app_key":       deployment.App,
		"git_source_id": parseCanonicalGitSourceID(deployment.SourceGitSourceID()),
		"commit_sha":    deployment.Commit,
		"files":         files,
		"skipped":       skipped,
	})
}

func (h *Handler) handleCanonicalAppHistory(w http.ResponseWriter, r *http.Request, workspaceID string, app string) {
	if !validAppKey(app) {
		writeError(w, http.StatusBadRequest, "invalid app key")
		return
	}
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
	if !validAppKey(app) || !validActionKey(actionKey) {
		writeError(w, http.StatusBadRequest, "invalid app/action key")
		return
	}
	deployment, ok := h.getCanonicalDeployment(w, r, workspaceID, app, "action not found")
	if !ok {
		return
	}
	action, exists := deployment.Actions[actionKey]
	if !exists {
		writeError(w, http.StatusNotFound, "action not found")
		return
	}
	schemaReader := h.newCanonicalSchemaReader(r.Context(), deployment)
	defer schemaReader.Close()
	view, err := h.newCanonicalActionModel(schemaReader, deployment, actionKey, action)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *Handler) handleCanonicalActionSchema(w http.ResponseWriter, r *http.Request, workspaceID string, app string, actionKey string) {
	if !validAppKey(app) || !validActionKey(actionKey) {
		writeError(w, http.StatusBadRequest, "invalid app/action key")
		return
	}
	deployment, ok := h.getCanonicalDeployment(w, r, workspaceID, app, "action not found")
	if !ok {
		return
	}
	action, exists := deployment.Actions[actionKey]
	if !exists {
		writeError(w, http.StatusNotFound, "action not found")
		return
	}
	schemaReader := h.newCanonicalSchemaReader(r.Context(), deployment)
	defer schemaReader.Close()
	view, err := h.newCanonicalActionSchemaView(schemaReader, deployment, actionKey, action)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *Handler) handleCanonicalPatchApp(w http.ResponseWriter, r *http.Request, workspaceID string, app string) {
	if !validAppKey(app) {
		writeError(w, http.StatusBadRequest, "invalid app key")
		return
	}
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
		writeError(w, http.StatusNotFound, "app not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, newCanonicalAppModel(deployment))
}

func (h *Handler) handleCanonicalPatchAction(w http.ResponseWriter, r *http.Request, workspaceID string, app string, actionKey string) {
	if !validAppKey(app) || !validActionKey(actionKey) {
		writeError(w, http.StatusBadRequest, "invalid app/action key")
		return
	}
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
		writeError(w, http.StatusNotFound, "action not found")
		return
	}
	if errors.Is(err, catalogpkg.ErrActionNotFound) {
		writeError(w, http.StatusNotFound, "action not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	deployment, ok := h.getCanonicalDeployment(w, r, workspaceID, app, "app not found")
	if !ok {
		return
	}
	schemaReader := h.newCanonicalSchemaReader(r.Context(), deployment)
	defer schemaReader.Close()
	view, err := h.newCanonicalActionModel(schemaReader, deployment, actionKey, action)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (h *Handler) handleCanonicalRequeueApp(w http.ResponseWriter, r *http.Request, workspaceID string, app string) {
	if !validAppKey(app) {
		writeError(w, http.StatusBadRequest, "invalid app key")
		return
	}
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "state store is not configured")
		return
	}
	deployment, ok := h.getCanonicalDeployment(w, r, workspaceID, app, "app not found")
	if !ok {
		return
	}
	var request struct {
		Action string `json:"action"`
	}
	if err := readOptionalJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	action := request.Action
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
		actionTags[actionKey] = contract.EffectiveRouteTagForAction(deployment, actionSpec)
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
	// Full windforce returns an AppDeployment status row from the deploy control
	// plane. windforce-lite does not yet have that deploy state table; do not
	// expose the internal app Deployment contract through the canonical route.
	writeError(w, http.StatusNotFound, "deployment not found")
}

func (h *Handler) handleCanonicalWorkerTags(w http.ResponseWriter, r *http.Request, workspaceID string) {
	snapshot, ok := h.loadCatalogSnapshot(w, r)
	if !ok {
		return
	}
	tags := map[string]struct{}{}
	for _, deployment := range canonicalDeployments(snapshot, workspaceID) {
		tags[defaultRouteTag()] = struct{}{}
		tags[contract.EffectiveRouteTagForApp(deployment)] = struct{}{}
		for _, action := range deployment.Actions {
			tags[contract.EffectiveRouteTagForAction(deployment, action)] = struct{}{}
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
	deployment, err := h.lookupDeployment(r.Context(), workspaceID, app)
	if err != nil {
		writeError(w, http.StatusNotFound, notFoundMessage)
		return contract.Deployment{}, false
	}
	return deployment, true
}

func (h *Handler) lookupDeployment(ctx context.Context, workspaceID string, app string) (contract.Deployment, error) {
	if scoped, ok := h.catalog.(interface {
		GetDeploymentForWorkspace(context.Context, string, string) (contract.Deployment, error)
	}); ok {
		return scoped.GetDeploymentForWorkspace(ctx, workspaceID, app)
	}
	deployment, err := h.catalog.GetDeployment(ctx, app)
	if err != nil {
		return contract.Deployment{}, err
	}
	if contract.NormalizeWorkspace(deployment.SourceWorkspace()) != contract.NormalizeWorkspace(workspaceID) {
		return contract.Deployment{}, catalogpkg.ErrDeploymentNotFound
	}
	return deployment, nil
}

type canonicalGitSourceView struct {
	ID               int64      `json:"id"`
	WorkspaceID      string     `json:"workspace_id"`
	Name             string     `json:"name"`
	RepoURL          string     `json:"repo_url"`
	Branch           string     `json:"branch"`
	Subpath          string     `json:"subpath"`
	CredsRef         string     `json:"creds_ref"`
	Kind             string     `json:"kind"`
	LastSyncedCommit *string    `json:"last_synced_commit"`
	LastSyncedAt     *time.Time `json:"last_synced_at"`
	CreatedAt        time.Time  `json:"created_at"`
}

func newCanonicalGitSourceView(source gitsourcepkg.Source) canonicalGitSourceView {
	return canonicalGitSourceView{
		ID:               parseCanonicalGitSourceID(source.ID),
		WorkspaceID:      contract.NormalizeWorkspace(source.Workspace),
		Name:             source.Name,
		RepoURL:          source.RepoURL,
		Branch:           firstNonEmpty(source.Branch, "main"),
		Subpath:          source.Subpath,
		CredsRef:         source.TokenEnv,
		Kind:             firstNonEmpty(source.Kind, "external"),
		LastSyncedCommit: cloneStringPtr(source.LastSyncedCommit),
		LastSyncedAt:     cloneTimePtr(source.LastSyncedAt),
		CreatedAt:        timeValue(source.CreatedAt),
	}
}

func parseCanonicalGitSourceID(id string) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func requireCanonicalGitSourceRouteID(w http.ResponseWriter, id string) (string, bool) {
	id = strings.TrimSpace(id)
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		writeError(w, http.StatusBadRequest, "bad git source id")
		return "", false
	}
	return id, true
}

func canonicalGitSourceIDPtr(id string) *int64 {
	value := parseCanonicalGitSourceID(id)
	if value == 0 {
		return nil
	}
	return &value
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
}

func canonicalGitSourcePatchFromRequest(w http.ResponseWriter, request canonicalGitSourcePatchRequest) (gitsourcepkg.Patch, bool) {
	var patch gitsourcepkg.Patch
	if value, ok := firstPresentString(request.Name, request.NameCamel); ok {
		value = strings.TrimSpace(value)
		if value == "" {
			writeError(w, http.StatusBadRequest, "name cannot be empty")
			return patch, false
		}
		patch.Name = &value
	}
	if value, ok := firstPresentString(request.RepoURL, request.RepoURLCamel); ok {
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
	if value, ok := firstPresentString(request.CredsRef, request.CredsRefCamel); ok {
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
	Flows   []string `json:"flows,omitempty"`
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

type canonicalAppModel struct {
	ID                   string    `json:"id"`
	WorkspaceID          string    `json:"workspace_id"`
	AppKey               string    `json:"app_key"`
	GitSourceID          int64     `json:"git_source_id"`
	CommitSha            string    `json:"commit_sha"`
	Entrypoint           string    `json:"entrypoint"`
	Tag                  string    `json:"tag"`
	TagOverride          *string   `json:"tag_override,omitempty"`
	TimeoutS             int32     `json:"timeout_s"`
	ScriptLang           string    `json:"script_lang"`
	RequiredCapabilities []string  `json:"required_capabilities"`
	MaxConcurrent        *int32    `json:"max_concurrent,omitempty"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type canonicalAppView struct {
	canonicalAppModel
	EffectiveRouteTag string `json:"effective_route_tag"`
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
	DeploymentID *string   `json:"deployment_id,omitempty"`
	Message      *string   `json:"message,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type canonicalActionModel struct {
	ID                   string          `json:"id"`
	WorkspaceID          string          `json:"workspace_id"`
	AppKey               string          `json:"app_key"`
	ActionKey            string          `json:"action_key"`
	InputSchema          json.RawMessage `json:"input_schema"`
	OutputSchema         json.RawMessage `json:"output_schema"`
	Tag                  *string         `json:"tag,omitempty"`
	TagOverride          *string         `json:"tag_override,omitempty"`
	TimeoutS             *int32          `json:"timeout_s,omitempty"`
	RequiredCapabilities []string        `json:"required_capabilities,omitempty"`
	UpdatedAt            time.Time       `json:"updated_at"`
}

type canonicalActionSchemaView struct {
	AppKey       string          `json:"app_key"`
	ActionKey    string          `json:"action_key"`
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema"`
}

type canonicalActionView struct {
	canonicalActionModel
	EffectiveCapabilities []string `json:"effective_capabilities"`
	EffectiveRouteTag     string   `json:"effective_route_tag"`
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
		ID:         item.ID,
		CommitSha:  item.Commit,
		Entrypoint: item.Entrypoint,
		Source:     firstNonEmpty(item.Source, "external_sync"),
		Message:    item.Message,
		CreatedAt:  item.CreatedAt,
	}
}

func newCanonicalAppModel(deployment contract.Deployment) canonicalAppModel {
	return canonicalAppModel{
		ID:                   canonicalAppID(deployment),
		WorkspaceID:          contract.NormalizeWorkspace(deployment.SourceWorkspace()),
		AppKey:               deployment.App,
		GitSourceID:          parseCanonicalGitSourceID(deployment.SourceGitSourceID()),
		CommitSha:            deployment.Commit,
		Entrypoint:           canonicalDeploymentEntrypoint(deployment),
		Tag:                  firstNonEmpty(strings.TrimSpace(deployment.Tag), defaultRouteTag()),
		TagOverride:          cloneStringPtr(deployment.TagOverride),
		TimeoutS:             canonicalDeploymentTimeoutSeconds(deployment),
		ScriptLang:           canonicalDeploymentScriptLang(deployment),
		RequiredCapabilities: cloneStringSlice(deployment.RequiredCapabilities),
		MaxConcurrent:        cloneInt32Ptr(deployment.MaxConcurrent),
		UpdatedAt:            canonicalDeploymentUpdatedAt(deployment),
	}
}

func newCanonicalAppView(deployment contract.Deployment) canonicalAppView {
	return canonicalAppView{
		canonicalAppModel: newCanonicalAppModel(deployment),
		EffectiveRouteTag: contract.EffectiveRouteTagForApp(deployment),
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

func (h *Handler) newCanonicalActionModel(schemaReader *canonicalSchemaReader, deployment contract.Deployment, actionKey string, action contract.Action) (canonicalActionModel, error) {
	schemaView, err := h.newCanonicalActionSchemaView(schemaReader, deployment, actionKey, action)
	if err != nil {
		return canonicalActionModel{}, err
	}
	return canonicalActionModel{
		ID:                   canonicalAppID(deployment) + "/" + actionKey,
		WorkspaceID:          contract.NormalizeWorkspace(deployment.SourceWorkspace()),
		AppKey:               deployment.App,
		ActionKey:            actionKey,
		InputSchema:          schemaView.InputSchema,
		OutputSchema:         schemaView.OutputSchema,
		Tag:                  cloneStringPtr(action.Tag),
		TagOverride:          cloneStringPtr(action.TagOverride),
		TimeoutS:             cloneInt32Ptr(action.TimeoutS),
		RequiredCapabilities: cloneStringSlicePtr(action.Capabilities),
		UpdatedAt:            canonicalActionUpdatedAt(deployment, action),
	}, nil
}

func (h *Handler) newCanonicalActionSchemaView(schemaReader *canonicalSchemaReader, deployment contract.Deployment, actionKey string, action contract.Action) (canonicalActionSchemaView, error) {
	inputSchema, err := schemaReader.Read(action.InputSchema, action.InputSchemaBody)
	if err != nil {
		return canonicalActionSchemaView{}, fmt.Errorf("action %s.%s input schema: %w", deployment.App, actionKey, err)
	}
	outputSchema, err := schemaReader.Read(action.OutputSchema, action.OutputSchemaBody)
	if err != nil {
		return canonicalActionSchemaView{}, fmt.Errorf("action %s.%s output schema: %w", deployment.App, actionKey, err)
	}
	return canonicalActionSchemaView{
		AppKey:       deployment.App,
		ActionKey:    actionKey,
		InputSchema:  inputSchema,
		OutputSchema: outputSchema,
	}, nil
}

func (h *Handler) newCanonicalActionView(schemaReader *canonicalSchemaReader, deployment contract.Deployment, actionKey string, action contract.Action) (canonicalActionView, error) {
	model, err := h.newCanonicalActionModel(schemaReader, deployment, actionKey, action)
	if err != nil {
		return canonicalActionView{}, err
	}
	effectiveCapabilities := contract.EffectiveCapabilities(deployment.RequiredCapabilities, action.Capabilities)
	return canonicalActionView{
		canonicalActionModel:  model,
		EffectiveCapabilities: cloneStringSlice(effectiveCapabilities),
		EffectiveRouteTag:     contract.EffectiveRouteTagForAction(deployment, action),
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

func (r *canonicalSchemaReader) Read(schemaPath string, schemaBody json.RawMessage) (json.RawMessage, error) {
	if body, ok, err := materializedSchemaBody(schemaBody); ok || err != nil {
		return body, err
	}
	if schemaPath == "" {
		return emptyJSONSchema(), nil
	}
	if filepath.IsAbs(schemaPath) || strings.HasPrefix(schemaPath, "/") || strings.Contains(schemaPath, "..") {
		return nil, fmt.Errorf("schema path %q must be a relative path inside the app", schemaPath)
	}
	if r.store == nil {
		return nil, errors.New("source storage is not configured")
	}
	sourceDir, err := r.ensureSourceDir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(sourceDir, filepath.FromSlash(schemaPath)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("manifest references schema %q but the file is missing", schemaPath)
		}
		return nil, err
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("schema %q is not valid JSON", schemaPath)
	}
	return json.RawMessage(append([]byte(nil), data...)), nil
}

func materializedSchemaBody(schemaBody json.RawMessage) (json.RawMessage, bool, error) {
	trimmed := bytes.TrimSpace(schemaBody)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, false, nil
	}
	if !json.Valid(trimmed) {
		return nil, true, errors.New("materialized schema is not valid JSON")
	}
	return json.RawMessage(append([]byte(nil), trimmed...)), true, nil
}

func emptyJSONSchema() json.RawMessage {
	return json.RawMessage([]byte("{}"))
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
	return deployment.Entrypoint
}

func canonicalDeploymentScriptLang(deployment contract.Deployment) string {
	if deployment.ScriptLang == "" {
		return "typescript"
	}
	return deployment.ScriptLang
}

func canonicalDeploymentTimeoutSeconds(deployment contract.Deployment) int32 {
	if deployment.TimeoutS > 0 {
		return deployment.TimeoutS
	}
	return 0
}

func canonicalDeploymentUpdatedAt(deployment contract.Deployment) time.Time {
	if deployment.UpdatedAt != nil {
		return *deployment.UpdatedAt
	}
	return time.Time{}
}

func canonicalActionUpdatedAt(deployment contract.Deployment, action contract.Action) time.Time {
	if action.UpdatedAt != nil {
		return *action.UpdatedAt
	}
	return canonicalDeploymentUpdatedAt(deployment)
}

func defaultRouteTag() string {
	return contract.DefaultRouteTag
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
	return &value, true
}

func validRouteTag(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for index, item := range value {
		if item >= 'a' && item <= 'z' {
			continue
		}
		if item >= '0' && item <= '9' {
			continue
		}
		if index > 0 && (item == '_' || item == '-') {
			continue
		}
		return false
	}
	return true
}

func validAppKey(value string) bool {
	return contract.ValidAppKey(value)
}

func validActionKey(value string) bool {
	return contract.ValidActionKey(value)
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

func cloneStringSlice(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), values...)
}

func cloneStringSlicePtr(values *[]string) []string {
	if values == nil {
		return nil
	}
	return cloneStringSlice(*values)
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func timeValue(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

func cloneInt32Ptr(value *int32) *int32 {
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
	if !validAppKey(app) || !validActionKey(action) {
		writeError(w, http.StatusBadRequest, "invalid app/action key")
		return state.Job{}, false
	}
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	deployment, err := h.lookupDeployment(r.Context(), workspaceID, app)
	if err != nil {
		writeError(w, http.StatusNotFound, "app not found: "+app)
		return state.Job{}, false
	}
	actionSpec, ok := deployment.Actions[action]
	if !ok {
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
	schemaReader := h.newCanonicalSchemaReader(r.Context(), deployment)
	defer schemaReader.Close()
	inputSchema, err := schemaReader.Read(actionSpec.InputSchema, actionSpec.InputSchemaBody)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return state.Job{}, false
	}
	outputSchema, err := schemaReader.Read(actionSpec.OutputSchema, actionSpec.OutputSchemaBody)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return state.Job{}, false
	}
	job.Payload.InputSchema = inputSchema
	job.Payload.OutputSchema = outputSchema
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
	_ = readOptionalJSON(r, &request)
	result, err := h.store.CancelJob(r.Context(), workspaceID, jobID, requestActorSubject(r), request.Reason)
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
		writeError(w, http.StatusNotFound, "job not found")
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

func (h *Handler) handleGetState(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "state store is not configured")
		return
	}
	statePath := r.URL.Query().Get("path")
	if statePath == "" {
		writeError(w, http.StatusBadRequest, "path query required")
		return
	}
	value, _, err := h.store.GetState(r.Context(), workspaceID, statePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(rawOrNull(value))
}

func (h *Handler) handleSetState(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "state store is not configured")
		return
	}
	statePath := r.URL.Query().Get("path")
	if statePath == "" {
		writeError(w, http.StatusBadRequest, "path query required")
		return
	}
	defer r.Body.Close()
	body, _ := io.ReadAll(r.Body)
	if err := h.store.SetState(r.Context(), workspaceID, statePath, body); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": statePath})
}

func (h *Handler) handleListVariables(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "state store is not configured")
		return
	}
	variables, err := h.store.ListVariables(r.Context(), workspaceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range variables {
		if variables[i].IsSecret {
			variables[i].Value = ""
		}
	}
	writeJSON(w, http.StatusOK, variables)
}

func (h *Handler) handleSetVariable(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "state store is not configured")
		return
	}
	var request struct {
		Path        string `json:"path"`
		Value       string `json:"value"`
		Description string `json:"description"`
		IsSecret    bool   `json:"is_secret"`
		AppKey      string `json:"app_key"`
	}
	body, err := readJSONBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "path required")
		return
	}
	if err := json.Unmarshal(body, &request); err != nil || request.Path == "" {
		writeError(w, http.StatusBadRequest, "path required")
		return
	}
	if request.AppKey != "" && !validAppKey(request.AppKey) {
		writeError(w, http.StatusBadRequest, "invalid app key")
		return
	}
	if err := h.store.SetVariable(r.Context(), workspaceID, request.AppKey, request.Path, request.Value, request.IsSecret, request.Description); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": request.Path, "app_key": request.AppKey})
}

func (h *Handler) handleGetVariable(w http.ResponseWriter, r *http.Request, workspaceID string, variablePath string) {
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "state store is not configured")
		return
	}
	var (
		variable state.Variable
		found    bool
		err      error
	)
	if appKey, ok, lookupErr := h.jobVariableScope(r, workspaceID); lookupErr != nil {
		writeError(w, http.StatusInternalServerError, lookupErr.Error())
		return
	} else if ok {
		variable, found, err = h.store.GetVariable(r.Context(), workspaceID, appKey, variablePath)
	} else {
		variable, found, err = h.store.GetVariableExact(r.Context(), workspaceID, r.URL.Query().Get("app"), variablePath)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "variable not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": variable.Path, "value": variable.Value, "is_secret": variable.IsSecret})
}

func (h *Handler) jobVariableScope(r *http.Request, workspaceID string) (string, bool, error) {
	jobID := strings.TrimSpace(r.Header.Get("X-Windforce-Job-ID"))
	if jobID == "" {
		return "", false, nil
	}
	job, _, found, err := h.store.GetJob(r.Context(), workspaceID, jobID)
	if err != nil {
		return "", true, err
	}
	if !found {
		return "", true, nil
	}
	return job.Payload.App, true, nil
}

func (h *Handler) handleDeleteVariable(w http.ResponseWriter, r *http.Request, workspaceID string, variablePath string) {
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "state store is not configured")
		return
	}
	if err := h.store.DeleteVariable(r.Context(), workspaceID, r.URL.Query().Get("app"), variablePath); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleSetResource(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "state store is not configured")
		return
	}
	var request struct {
		Path         string          `json:"path"`
		Value        json.RawMessage `json:"value"`
		ResourceType string          `json:"resource_type"`
		Description  string          `json:"description"`
	}
	body, err := readJSONBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "path required")
		return
	}
	if err := json.Unmarshal(body, &request); err != nil || request.Path == "" {
		writeError(w, http.StatusBadRequest, "path required")
		return
	}
	if err := h.store.SetResource(r.Context(), workspaceID, request.Path, request.Value, request.ResourceType, request.Description); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": request.Path})
}

func (h *Handler) handleGetResource(w http.ResponseWriter, r *http.Request, workspaceID string, resourcePath string) {
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "state store is not configured")
		return
	}
	resource, found, err := h.store.GetResource(r.Context(), workspaceID, resourcePath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(rawOrNull(resource.Value))
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

func (h *Handler) matchTriggerRoute(path string) (triggerRoute, bool) {
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
	ID             string          `json:"id"`
	WorkspaceID    string          `json:"workspace_id"`
	State          string          `json:"state"`
	Status         *string         `json:"status,omitempty"`
	Worker         *string         `json:"worker,omitempty"`
	AppKey         *string         `json:"app_key,omitempty"`
	ActionKey      *string         `json:"action_key,omitempty"`
	TriggerKind    *string         `json:"trigger_kind,omitempty"`
	Kind           *string         `json:"kind,omitempty"`
	GitSourceID    *int64          `json:"git_source_id,omitempty"`
	CommitSha      *string         `json:"commit_sha,omitempty"`
	Entrypoint     *string         `json:"entrypoint,omitempty"`
	InputSchema    json.RawMessage `json:"input_schema,omitempty"`
	OutputSchema   json.RawMessage `json:"output_schema,omitempty"`
	Tag            string          `json:"tag,omitempty"`
	TimeoutS       int32           `json:"timeout_s,omitempty"`
	CreatedBy      string          `json:"created_by,omitempty"`
	PermissionedAs string          `json:"permissioned_as,omitempty"`
	Input          json.RawMessage `json:"input,omitempty"`
	CreatedAt      *time.Time      `json:"created_at,omitempty"`
	StartedAt      *time.Time      `json:"started_at,omitempty"`
	CompletedAt    *time.Time      `json:"completed_at,omitempty"`
	DurationMs     int64           `json:"duration_ms,omitempty"`
	CanceledBy     *string         `json:"canceled_by,omitempty"`
	CanceledReason *string         `json:"canceled_reason,omitempty"`
	FlowRunID      *string         `json:"flow_run_id,omitempty"`
	FlowKey        *string         `json:"flow_key,omitempty"`
	FlowStepKey    *string         `json:"flow_step_key,omitempty"`
}

func newJobStatus(workspaceID string, job state.Job, run state.Run) jobStatusResponse {
	stateValue := "queued"
	var statusValue *string
	var worker *string
	startedAt := job.StartedAt
	var completedAt *time.Time
	switch job.State {
	case state.JobRunning:
		stateValue = "running"
		worker = stringPtr(job.LeaseOwner)
		if startedAt == nil {
			startedAt = &job.UpdatedAt
		}
	case state.JobSucceeded, state.JobFailed:
		stateValue = "completed"
		status := jobDetailStatus(job, run)
		statusValue = &status
		completedAt = &run.UpdatedAt
	}
	app := job.Payload.App
	action := job.Payload.Action
	kind := job.Kind
	commit := job.Payload.Commit
	tag := strings.TrimSpace(job.Payload.Tag)
	if tag == "" {
		tag = contract.EffectiveRouteTagForAction(job.Payload.Deployment, job.Payload.ActionSpec)
	}
	response := jobStatusResponse{
		ID:             job.ID,
		WorkspaceID:    contract.NormalizeWorkspace(workspaceID),
		State:          stateValue,
		Status:         statusValue,
		Worker:         worker,
		AppKey:         stringPtr(app),
		ActionKey:      stringPtr(action),
		TriggerKind:    stringPtr(jobStatusTriggerKind(job, run)),
		Kind:           stringPtr(kind),
		GitSourceID:    canonicalGitSourceIDPtr(job.Payload.GitSourceID),
		CommitSha:      stringPtr(commit),
		Entrypoint:     stringPtr(jobStatusEntrypoint(job)),
		InputSchema:    cloneRaw(job.Payload.InputSchema),
		OutputSchema:   cloneRaw(job.Payload.OutputSchema),
		Tag:            tag,
		TimeoutS:       timeoutSeconds(job.Payload.ActionSpec.TimeoutMs),
		CreatedBy:      firstNonEmpty(strings.TrimSpace(job.Payload.CreatedBy), strings.TrimSpace(run.CreatedBy)),
		PermissionedAs: firstNonEmpty(strings.TrimSpace(job.Payload.PermissionedAs), strings.TrimSpace(run.PermissionedAs), strings.TrimSpace(job.Payload.CreatedBy), strings.TrimSpace(run.CreatedBy)),
		Input:          cloneRaw(job.Payload.Input),
		CreatedAt:      &job.CreatedAt,
		StartedAt:      startedAt,
		CompletedAt:    completedAt,
		CanceledBy:     firstPresentStringPtr(job.CanceledBy, jobStatusCanceledBy(run)),
		CanceledReason: firstPresentStringPtr(job.CanceledReason, jobStatusCanceledReason(run)),
		FlowRunID:      stringPtr(job.Payload.FlowRunID),
		FlowKey:        stringPtr(job.Payload.FlowKey),
		FlowStepKey:    stringPtr(job.Payload.FlowStepKey),
	}
	if run.Result != nil {
		response.DurationMs = run.Result.DurationMs
	}
	return response
}

func jobStatusEntrypoint(job state.Job) string {
	if entrypoint := strings.TrimSpace(job.Payload.Deployment.Entrypoint); entrypoint != "" {
		return entrypoint
	}
	return strings.TrimSpace(job.Payload.ActionSpec.Entrypoint)
}

func jobStatusTriggerKind(job state.Job, run state.Run) string {
	if job.Payload.TriggerKind != "" {
		return job.Payload.TriggerKind
	}
	return run.Adapter
}

func jobStatusCanceledReason(run state.Run) *string {
	if run.State != state.RunCanceled || len(run.Error) == 0 {
		return nil
	}
	var payload struct {
		Message        string  `json:"message"`
		CanceledReason *string `json:"canceledReason"`
	}
	if json.Unmarshal(run.Error, &payload) == nil {
		if payload.CanceledReason != nil {
			return payload.CanceledReason
		}
		if strings.TrimSpace(payload.Message) != "" {
			return stringPtr(payload.Message)
		}
	}
	return nil
}

func jobStatusCanceledBy(run state.Run) *string {
	if run.State != state.RunCanceled || len(run.Error) == 0 {
		return nil
	}
	var payload struct {
		CanceledBy string `json:"canceledBy"`
	}
	if json.Unmarshal(run.Error, &payload) == nil {
		return stringPtr(strings.TrimSpace(payload.CanceledBy))
	}
	return nil
}

func timeoutSeconds(timeoutMs int64) int32 {
	if timeoutMs <= 0 {
		return 0
	}
	return int32((timeoutMs + 999) / 1000)
}

func jobResult(job state.Job, run state.Run) (string, json.RawMessage, bool) {
	if job.State == state.JobQueued || job.State == state.JobRunning {
		return "", nil, false
	}
	status := terminalJobStatus(job, run)
	switch status {
	case "success":
		return status, rawOrNull(run.Output), true
	case "canceled":
		message := runErrorMessage(run)
		if message == "" {
			message = "job canceled"
		}
		return status, mustRaw(map[string]string{"name": "Canceled", "message": message}), true
	default:
		if run.Result != nil && len(run.Result.Output) > 0 {
			return "failure", rawOrNull(run.Result.Output), true
		}
		message := runErrorMessage(run)
		if message == "" {
			message = "job failed"
		}
		return "failure", mustRaw(map[string]string{"name": "Error", "message": message}), true
	}
}

func terminalJobStatus(job state.Job, run state.Run) string {
	if run.State == state.RunCanceled {
		return "canceled"
	}
	if job.State == state.JobSucceeded || run.State == state.RunSucceeded || run.State == state.RunWaitingHuman {
		return "success"
	}
	return "failure"
}

func jobDetailStatus(job state.Job, run state.Run) string {
	return terminalJobStatus(job, run)
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
	_ = json.NewEncoder(w).Encode(value)
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
	case "queued", "running", "success", "failure", "completed", "canceled", "all":
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

func requestActorSubject(r *http.Request) string {
	for _, name := range []string{"X-Windforce-Actor", "X-Windforce-User"} {
		if value := strings.TrimSpace(r.Header.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

func firstPresentStringPtr(values ...*string) *string {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
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
