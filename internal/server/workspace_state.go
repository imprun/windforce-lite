package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/imprun/windforce-lite/internal/crypto"
	"github.com/imprun/windforce-lite/internal/state"
)

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
	value := request.Value
	if request.IsSecret {
		encrypted, err := h.encryptSecretVariable(r.Context(), workspaceID, request.Value)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		value = encrypted
	}
	if err := h.store.SetVariable(r.Context(), workspaceID, request.AppKey, request.Path, value, request.IsSecret, request.Description); err != nil {
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
	value := variable.Value
	if variable.IsSecret {
		decrypted, err := h.decryptSecretVariable(r.Context(), workspaceID, variable.Value)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "decrypt: "+err.Error())
			return
		}
		value = decrypted
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": variable.Path, "value": value, "is_secret": variable.IsSecret})
}

type workspaceKeyProvider interface {
	GetWorkspaceKeyVersioned(ctx context.Context, workspaceID string) (string, int32, error)
}

func (h *Handler) encryptSecretVariable(ctx context.Context, workspaceID string, value string) (string, error) {
	key, err := h.workspaceDEK(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	return crypto.Encrypt(key, value)
}

func (h *Handler) decryptSecretVariable(ctx context.Context, workspaceID string, value string) (string, error) {
	key, err := h.workspaceDEK(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	return crypto.Decrypt(key, value)
}

// workspaceDEK mirrors canonical Windforce secret resolution: use a stored
// workspace DEK when the store exposes one, otherwise fall back to the legacy
// SECRET_KEY-derived per-workspace key.
func (h *Handler) workspaceDEK(ctx context.Context, workspaceID string) (string, error) {
	if keyStore, ok := h.store.(workspaceKeyProvider); ok {
		key, version, err := keyStore.GetWorkspaceKeyVersioned(ctx, workspaceID)
		if err != nil {
			return "", err
		}
		if key != "" {
			return crypto.ResolveDEK(key, version, h.keks())
		}
	}
	return crypto.DeriveWorkspaceKey(h.secretKey, workspaceID), nil
}

func (h *Handler) keks() []string {
	keks := []string{crypto.DeriveKEK(h.secretKey)}
	if h.secretKeyPrevious != "" {
		keks = append(keks, crypto.DeriveKEK(h.secretKeyPrevious))
	}
	return keks
}

func (h *Handler) jobVariableScope(r *http.Request, workspaceID string) (string, bool, error) {
	principal := jobPrincipalFrom(r.Context())
	if principal == nil || principal.JobID == "" {
		return "", false, nil
	}
	jobID := principal.JobID
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
