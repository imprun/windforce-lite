package server

import (
	"net/http"

	"github.com/imprun/windforce-core/internal/contract"
)

type systemInfoResponse struct {
	Service       string                 `json:"service"`
	Workspace     string                 `json:"workspace"`
	Ready         bool                   `json:"ready"`
	Planes        map[string]bool        `json:"planes"`
	Backends      map[string]bool        `json:"backends"`
	Auth          map[string]bool        `json:"auth"`
	RuntimeConfig map[string]interface{} `json:"runtime_config"`
}

func (h *Handler) handleSystemInfo(w http.ResponseWriter, _ *http.Request, workspaceID string) {
	waitMilliseconds := int64(0)
	if h.wait > 0 {
		waitMilliseconds = h.wait.Milliseconds()
	}
	writeJSON(w, http.StatusOK, systemInfoResponse{
		Service:   "windforce-lite",
		Workspace: contract.NormalizeWorkspace(workspaceID),
		Ready:     h.store != nil,
		Planes: map[string]bool{
			"control_api":   true,
			"execution_api": true,
			"public_api":    true,
			"worker_api":    true,
			"web_ui":        true,
			"metrics":       h.metricsHandler != nil,
		},
		Backends: map[string]bool{
			"state_store":       h.store != nil,
			"catalog":           h.catalog != nil,
			"syncer":            h.syncer != nil,
			"execution_bundles": h.executionBundles != nil,
			"git_sources":       h.gitSources != nil,
			"artifact_store":    h.artifactStore != nil,
		},
		Auth: map[string]bool{
			"admin_token_configured":  h.adminToken != "",
			"worker_token_configured": h.workerToken != "",
			"job_token_configured":    h.jobTokenSecret != "",
			"secret_key_configured":   h.secretKey != "" && h.secretKey != DefaultSecretKey,
			"previous_secret_key":     h.secretKeyPrevious != "",
		},
		RuntimeConfig: map[string]interface{}{
			"wait_ms":            waitMilliseconds,
			"sample_root":        h.sampleRoot != "",
			"managed_workspaces": h.managedWorkspaces,
		},
	})
}
