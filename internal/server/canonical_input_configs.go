package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/imprun/windforce-lite/internal/contract"
	"github.com/imprun/windforce-lite/internal/state"
)

type canonicalInputConfigRequest struct {
	ActionKey  string                     `json:"action_key"`
	ClientID   string                     `json:"client_id,omitempty"`
	Config     map[string]json.RawMessage `json:"config"`
	LockedKeys []string                   `json:"locked_keys"`
}

func (h *Handler) handleCanonicalAppInputConfigs(w http.ResponseWriter, r *http.Request, workspaceID string, appKey string) {
	if _, ok := h.getCanonicalDeployment(w, r, workspaceID, appKey, "app not found"); !ok {
		return
	}
	configs, err := h.store.ListInputConfigsForApp(r.Context(), workspaceID, appKey)
	if err != nil {
		writeStateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, configs)
}

func (h *Handler) handleCanonicalClientInputConfigs(w http.ResponseWriter, r *http.Request, workspaceID string, clientID string) {
	if _, err := h.store.GetClient(r.Context(), workspaceID, clientID); err != nil {
		writeStateError(w, err)
		return
	}
	configs, err := h.store.ListInputConfigsForClient(r.Context(), workspaceID, clientID)
	if err != nil {
		writeStateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, configs)
}

func (h *Handler) handleCanonicalSetInputConfig(w http.ResponseWriter, r *http.Request, workspaceID string, appKey string) {
	deployment, ok := h.getCanonicalDeployment(w, r, workspaceID, appKey, "app not found")
	if !ok {
		return
	}
	var request canonicalInputConfigRequest
	if err := readRequiredJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "request body must be a JSON object")
		return
	}
	request.ActionKey = strings.TrimSpace(request.ActionKey)
	request.ClientID = strings.TrimSpace(request.ClientID)
	if request.ActionKey != "" {
		if !contract.ValidActionKey(request.ActionKey) {
			writeError(w, http.StatusBadRequest, "invalid action key")
			return
		}
		if _, exists := deployment.Actions[request.ActionKey]; !exists {
			writeError(w, http.StatusNotFound, "action not found")
			return
		}
	}
	if request.ClientID != "" {
		if _, err := h.store.GetClient(r.Context(), workspaceID, request.ClientID); err != nil {
			writeStateError(w, err)
			return
		}
	}
	if request.Config == nil {
		request.Config = map[string]json.RawMessage{}
	}
	configJSON, err := json.Marshal(request.Config)
	if err != nil {
		writeError(w, http.StatusBadRequest, "config must be a JSON object")
		return
	}
	config, err := h.store.SetInputConfig(r.Context(), state.InputConfig{
		WorkspaceID: contract.NormalizeWorkspace(workspaceID),
		AppKey:      appKey,
		ActionKey:   request.ActionKey,
		ClientID:    request.ClientID,
		Config:      configJSON,
		LockedKeys:  request.LockedKeys,
	}, requestActorSubject(r))
	if err != nil {
		if errors.Is(err, state.ErrInvalidInputConfig) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeStateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, config)
}

func (h *Handler) handleCanonicalDeleteInputConfig(w http.ResponseWriter, r *http.Request, workspaceID string, appKey string) {
	if _, ok := h.getCanonicalDeployment(w, r, workspaceID, appKey, "app not found"); !ok {
		return
	}
	actionKey := strings.TrimSpace(r.URL.Query().Get("action_key"))
	clientID := strings.TrimSpace(r.URL.Query().Get("client_id"))
	if actionKey != "" && !contract.ValidActionKey(actionKey) {
		writeError(w, http.StatusBadRequest, "invalid action key")
		return
	}
	if err := h.store.DeleteInputConfig(r.Context(), workspaceID, appKey, actionKey, clientID, requestActorSubject(r)); err != nil {
		writeStateError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleCanonicalAppInputConfigAudit(w http.ResponseWriter, r *http.Request, workspaceID string, appKey string) {
	records, err := h.store.ListInputConfigAudit(r.Context(), workspaceID, appKey, "")
	if err != nil {
		writeStateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, records)
}

func (h *Handler) handleCanonicalClientInputConfigAudit(w http.ResponseWriter, r *http.Request, workspaceID string, clientID string) {
	if _, err := h.store.GetClient(r.Context(), workspaceID, clientID); err != nil {
		writeStateError(w, err)
		return
	}
	records, err := h.store.ListInputConfigAudit(r.Context(), workspaceID, "", clientID)
	if err != nil {
		writeStateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, records)
}
