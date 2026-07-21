package server

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/imprun/windforce-core/internal/contract"
	"github.com/imprun/windforce-core/internal/state"
)

const maxClientNameRunes = 200

type canonicalClientRequest struct {
	Name *string `json:"name"`
}

type clientView struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	HasToken    bool   `json:"has_token"`
	CreatedBy   string `json:"created_by"`
	UpdatedBy   string `json:"updated_by"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func clientResponse(client state.Client) clientView {
	return clientView{
		ID: client.ID, WorkspaceID: client.WorkspaceID, Name: client.Name, HasToken: client.TokenHash != "",
		CreatedBy: client.CreatedBy, UpdatedBy: client.UpdatedBy,
		CreatedAt: client.CreatedAt.UTC().Format(timeLayout), UpdatedAt: client.UpdatedAt.UTC().Format(timeLayout),
	}
}

func (h *Handler) handleCanonicalClients(w http.ResponseWriter, r *http.Request, workspaceID string) {
	clients, err := h.store.ListClients(r.Context(), workspaceID)
	if err != nil {
		writeStateError(w, err)
		return
	}
	views := make([]clientView, 0, len(clients))
	for _, client := range clients {
		views = append(views, clientResponse(client))
	}
	writeJSON(w, http.StatusOK, views)
}

func (h *Handler) handleCanonicalClient(w http.ResponseWriter, r *http.Request, workspaceID string, id string) {
	client, err := h.store.GetClient(r.Context(), workspaceID, strings.TrimSpace(id))
	if err != nil {
		writeStateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, clientResponse(client))
}

func (h *Handler) handleCanonicalCreateClient(w http.ResponseWriter, r *http.Request, workspaceID string) {
	var request canonicalClientRequest
	if err := readRequiredJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if request.Name == nil {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	name, ok := normalizeClientName(w, *request.Name)
	if !ok {
		return
	}
	value, err := newClientToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not generate client token")
		return
	}
	client, err := h.store.CreateClient(r.Context(), workspaceID, name, state.HashClientToken(value), clientActor(r))
	if err != nil {
		writeStateError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"client": clientResponse(client), "api_token": value})
}

func (h *Handler) handleCanonicalUpdateClient(w http.ResponseWriter, r *http.Request, workspaceID string, id string) {
	id = strings.TrimSpace(id)
	if _, err := h.store.GetClient(r.Context(), workspaceID, id); err != nil {
		writeStateError(w, err)
		return
	}
	var request canonicalClientRequest
	if err := readRequiredJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if request.Name == nil {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	name, ok := normalizeClientName(w, *request.Name)
	if !ok {
		return
	}
	client, err := h.store.UpdateClient(r.Context(), workspaceID, id, name, clientActor(r))
	if err != nil {
		writeStateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, clientResponse(client))
}

func (h *Handler) handleCanonicalRotateClientToken(w http.ResponseWriter, r *http.Request, workspaceID string, id string) {
	value, err := newClientToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not generate client token")
		return
	}
	client, err := h.store.RotateClientToken(r.Context(), workspaceID, strings.TrimSpace(id), state.HashClientToken(value), clientActor(r))
	if err != nil {
		writeStateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"client": clientResponse(client), "api_token": value})
}

func (h *Handler) handleCanonicalRevokeClientToken(w http.ResponseWriter, r *http.Request, workspaceID string, id string) {
	client, err := h.store.RevokeClientToken(r.Context(), workspaceID, strings.TrimSpace(id), clientActor(r))
	if err != nil {
		writeStateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, clientResponse(client))
}

func (h *Handler) handleCanonicalDeleteClient(w http.ResponseWriter, r *http.Request, workspaceID string, id string) {
	err := h.store.DeleteClient(r.Context(), workspaceID, strings.TrimSpace(id), clientActor(r))
	if err != nil {
		writeStateError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleCanonicalClientAudit(w http.ResponseWriter, r *http.Request, workspaceID string, id string) {
	records, err := h.store.ListClientAudit(r.Context(), workspaceID, strings.TrimSpace(id))
	if err != nil {
		writeStateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, records)
}

func normalizeClientName(w http.ResponseWriter, name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return "", false
	}
	if utf8.RuneCountInString(name) > maxClientNameRunes {
		writeError(w, http.StatusBadRequest, "name is too long")
		return "", false
	}
	return name, true
}

func newClientToken() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return contract.ClientTokenPrefix + base64.RawURLEncoding.EncodeToString(data), nil
}

func clientActor(r *http.Request) string {
	actor := requestActorSubject(r)
	if actor == "" {
		return "system"
	}
	return actor
}
