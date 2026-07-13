package server

import (
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxAPIClientNameRunes = 200
	maxAPIClientKeyRunes  = 512
)

type canonicalAPIClientRequest struct {
	Name      *string `json:"name"`
	ClientKey *string `json:"client_key"`
}

func (h *Handler) handleCanonicalAPIClients(w http.ResponseWriter, r *http.Request, workspaceID string) {
	clients, err := h.store.ListAPIClients(r.Context(), workspaceID)
	if err != nil {
		writeStateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, clients)
}

func (h *Handler) handleCanonicalAPIClient(w http.ResponseWriter, r *http.Request, workspaceID string, id string) {
	client, err := h.store.GetAPIClient(r.Context(), workspaceID, strings.TrimSpace(id))
	if err != nil {
		writeStateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, client)
}

func (h *Handler) handleCanonicalCreateAPIClient(w http.ResponseWriter, r *http.Request, workspaceID string) {
	var request canonicalAPIClientRequest
	if err := readRequiredJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if request.Name == nil || request.ClientKey == nil {
		writeError(w, http.StatusBadRequest, "name and client_key are required")
		return
	}
	name, clientKey, ok := normalizeAPIClientValues(w, *request.Name, *request.ClientKey)
	if !ok {
		return
	}
	client, err := h.store.CreateAPIClient(r.Context(), workspaceID, name, clientKey, apiClientActor(r))
	if err != nil {
		writeStateError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, client)
}

func (h *Handler) handleCanonicalUpdateAPIClient(w http.ResponseWriter, r *http.Request, workspaceID string, id string) {
	id = strings.TrimSpace(id)
	current, err := h.store.GetAPIClient(r.Context(), workspaceID, id)
	if err != nil {
		writeStateError(w, err)
		return
	}
	var request canonicalAPIClientRequest
	if err := readRequiredJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if request.Name == nil && request.ClientKey == nil {
		writeError(w, http.StatusBadRequest, "name or client_key is required")
		return
	}
	name := current.Name
	clientKey := current.ClientKey
	if request.Name != nil {
		name = *request.Name
	}
	if request.ClientKey != nil {
		clientKey = *request.ClientKey
	}
	name, clientKey, ok := normalizeAPIClientValues(w, name, clientKey)
	if !ok {
		return
	}
	client, err := h.store.UpdateAPIClient(r.Context(), workspaceID, id, name, clientKey, apiClientActor(r))
	if err != nil {
		writeStateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, client)
}

func (h *Handler) handleCanonicalDeleteAPIClient(w http.ResponseWriter, r *http.Request, workspaceID string, id string) {
	err := h.store.DeleteAPIClient(r.Context(), workspaceID, strings.TrimSpace(id), apiClientActor(r))
	if err != nil {
		writeStateError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleCanonicalAPIClientAudit(w http.ResponseWriter, r *http.Request, workspaceID string, id string) {
	records, err := h.store.ListAPIClientAudit(r.Context(), workspaceID, strings.TrimSpace(id))
	if err != nil {
		writeStateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, records)
}

func normalizeAPIClientValues(w http.ResponseWriter, name string, clientKey string) (string, string, bool) {
	name = strings.TrimSpace(name)
	clientKey = strings.TrimSpace(clientKey)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return "", "", false
	}
	if utf8.RuneCountInString(name) > maxAPIClientNameRunes {
		writeError(w, http.StatusBadRequest, "name is too long")
		return "", "", false
	}
	if clientKey == "" {
		writeError(w, http.StatusBadRequest, "client_key is required")
		return "", "", false
	}
	if utf8.RuneCountInString(clientKey) > maxAPIClientKeyRunes {
		writeError(w, http.StatusBadRequest, "client_key is too long")
		return "", "", false
	}
	if strings.IndexFunc(clientKey, unicode.IsSpace) >= 0 || strings.IndexFunc(clientKey, unicode.IsControl) >= 0 {
		writeError(w, http.StatusBadRequest, "client_key must not contain whitespace or control characters")
		return "", "", false
	}
	return name, clientKey, true
}

func apiClientActor(r *http.Request) string {
	actor := requestActorSubject(r)
	if actor == "" {
		return "system"
	}
	return actor
}
