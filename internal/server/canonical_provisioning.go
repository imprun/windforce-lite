package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"

	catalogpkg "github.com/imprun/windforce-core/internal/catalog"
	"github.com/imprun/windforce-core/internal/contract"
	"github.com/imprun/windforce-core/internal/provisioning"
)

type canonicalProvisioningImportRequest struct {
	Resources []provisioning.Document `json:"resources"`
	DryRun    bool                    `json:"dry_run"`
}

func (h *Handler) handleCanonicalProvisioningImport(w http.ResponseWriter, r *http.Request, workspaceID string) {
	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "request body must contain resources")
		return
	}
	var request canonicalProvisioningImportRequest
	if json.Valid(data) {
		if err := json.Unmarshal(data, &request); err != nil || request.Resources == nil {
			docs, decodeErr := decodeProvisioningDocuments(data, ".json")
			if decodeErr != nil {
				writeError(w, http.StatusBadRequest, decodeErr.Error())
				return
			}
			request.Resources = docs
		}
	} else {
		docs, decodeErr := decodeProvisioningDocuments(data, ".yaml")
		if decodeErr != nil {
			writeError(w, http.StatusBadRequest, decodeErr.Error())
			return
		}
		request.Resources = docs
	}
	if len(request.Resources) == 0 {
		writeError(w, http.StatusBadRequest, "resources are required")
		return
	}
	if r.URL.Query().Get("dry_run") == "true" {
		request.DryRun = true
	}
	service := h.provisioningService()
	result, err := service.Apply(r.Context(), request.Resources, provisioning.Options{
		Workspace: workspaceID,
		Actor:     firstNonEmpty(strings.TrimSpace(requestActorSubject(r)), "provisioning"),
		DryRun:    request.DryRun,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) handleCanonicalProvisioningExport(w http.ResponseWriter, r *http.Request, workspaceID string) {
	includeValues := r.URL.Query().Get("include_values") == "true"
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	service := h.provisioningService()
	service.AppKeys = h.provisioningAppKeys(r.Context(), workspaceID)
	docs, err := service.Export(r.Context(), workspaceID, includeValues)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if format == "yaml" || format == "yml" {
		data, err := provisioning.EncodeYAML(docs)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"resources": docs})
}

func (h *Handler) provisioningAppKeys(ctx context.Context, workspaceID string) []string {
	if h.catalog == nil {
		return nil
	}
	var (
		snapshot catalogpkg.Snapshot
		err      error
		ok       bool
	)
	if loader, matched := h.catalog.(interface {
		LoadCatalog(context.Context) (catalogpkg.Snapshot, error)
	}); matched {
		snapshot, err = loader.LoadCatalog(ctx)
		ok = true
	} else if loader, matched := h.catalog.(interface {
		Load(context.Context) (catalogpkg.Snapshot, error)
	}); matched {
		snapshot, err = loader.Load(ctx)
		ok = true
	}
	if !ok || err != nil {
		return nil
	}
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	seen := map[string]struct{}{}
	for _, deployment := range snapshot.Deployments {
		if contract.NormalizeWorkspace(deployment.SourceWorkspace()) == workspaceID && deployment.App != "" {
			seen[deployment.App] = struct{}{}
		}
	}
	apps := make([]string, 0, len(seen))
	for app := range seen {
		apps = append(apps, app)
	}
	sort.Strings(apps)
	return apps
}

func (h *Handler) provisioningService() provisioning.Service {
	var gitSources provisioning.GitSourceRegistry
	if registry, ok := h.gitSources.(provisioning.GitSourceRegistry); ok {
		gitSources = registry
	}
	return provisioning.Service{
		Store:      h.store,
		GitSources: gitSources,
		Encrypt: func(ctx context.Context, workspaceID string, value string) (string, error) {
			return h.encryptSecretVariable(ctx, workspaceID, value)
		},
	}
}

func decodeProvisioningDocuments(data []byte, ext string) ([]provisioning.Document, error) {
	if ext == ".json" || json.Valid(data) {
		return provisioning.Decode(data, ".json")
	}
	return provisioning.Decode(data, ext)
}
