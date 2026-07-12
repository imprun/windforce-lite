package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"sort"
	"strings"

	catalogpkg "github.com/imprun/windforce-lite/internal/catalog"
	"github.com/imprun/windforce-lite/internal/contract"
	gitsourcepkg "github.com/imprun/windforce-lite/internal/gitsource"
	"github.com/imprun/windforce-lite/internal/state"
)

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
	h.recordAudit(r, workspaceID, deployment.SourceGitSourceID(), app, "route_tag_override", tagOverrideDetail("app", tagOverride))
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
	h.recordAudit(r, workspaceID, deployment.SourceGitSourceID(), app, "route_tag_override", tagOverrideDetail("action "+actionKey, tagOverride))
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

func (h *Handler) handleCanonicalWorkerTags(w http.ResponseWriter, r *http.Request, workspaceID string) {
	snapshot, ok := h.loadCatalogSnapshot(w, r)
	if !ok {
		return
	}
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	tags := map[string]struct{}{}
	for _, deployment := range canonicalDeployments(snapshot, workspaceID) {
		for _, action := range deployment.Actions {
			tags[contract.EffectiveRouteTagForAction(deployment, action)] = struct{}{}
		}
	}
	if h.store != nil {
		items, err := h.store.ListJobs(r.Context(), state.JobListQuery{WorkspaceID: workspaceID, Status: "queued"})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, item := range items {
			tag := strings.TrimSpace(item.Tag)
			if tag != "" {
				tags[tag] = struct{}{}
			}
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
