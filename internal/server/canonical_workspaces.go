package server

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"github.com/imprun/windforce-core/internal/contract"
	"github.com/imprun/windforce-core/internal/state"
)

type workspaceView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	HasToken  bool   `json:"has_token"`
	CreatedBy string `json:"created_by"`
	UpdatedBy string `json:"updated_by"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func workspaceResponse(workspace state.Workspace) workspaceView {
	return workspaceView{
		ID: workspace.ID, Name: workspace.Name, Status: workspace.Status, HasToken: workspace.TokenHash != "",
		CreatedBy: workspace.CreatedBy, UpdatedBy: workspace.UpdatedBy,
		CreatedAt: workspace.CreatedAt.UTC().Format(timeLayout), UpdatedAt: workspace.UpdatedAt.UTC().Format(timeLayout),
	}
}

const timeLayout = "2006-01-02T15:04:05.000000000Z07:00"

func (h *Handler) handleWorkspaceAPI(w http.ResponseWriter, r *http.Request, parts []string) bool {
	if len(parts) < 2 || parts[0] != "api" || parts[1] != "workspaces" {
		return false
	}
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "state store is not configured")
		return true
	}
	if len(parts) == 2 && r.Method == http.MethodGet {
		items, err := h.store.ListWorkspaces(r.Context())
		if err != nil {
			writeStateError(w, err)
			return true
		}
		views := make([]workspaceView, 0, len(items))
		for _, item := range items {
			views = append(views, workspaceResponse(item))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": views})
		return true
	}
	if len(parts) == 2 && r.Method == http.MethodPost {
		var request struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if err := readRequiredJSON(r, &request); err != nil {
			writeError(w, http.StatusBadRequest, "valid workspace JSON is required")
			return true
		}
		request.ID = strings.TrimSpace(request.ID)
		request.Name = strings.TrimSpace(request.Name)
		if !contract.ValidWorkspaceID(request.ID) {
			writeError(w, http.StatusBadRequest, "workspace id must start with a lowercase letter and contain only lowercase letters, digits, or hyphens (2-48 characters)")
			return true
		}
		if request.Name == "" || len(request.Name) > 100 {
			writeError(w, http.StatusBadRequest, "workspace name is required and must be at most 100 characters")
			return true
		}
		token, err := newWorkspaceToken()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not generate workspace token")
			return true
		}
		workspace, err := h.store.CreateWorkspace(r.Context(), request.ID, request.Name, state.HashWorkspaceToken(token), requestActorOrSystem(r))
		if err != nil {
			writeStateError(w, err)
			return true
		}
		writeJSON(w, http.StatusCreated, map[string]any{"workspace": workspaceResponse(workspace), "api_token": token})
		return true
	}
	if len(parts) < 3 {
		return false
	}
	workspaceID := parts[2]
	if len(parts) == 3 && r.Method == http.MethodGet {
		workspace, err := h.store.GetWorkspace(r.Context(), workspaceID)
		if err != nil {
			writeStateError(w, err)
			return true
		}
		writeJSON(w, http.StatusOK, workspaceResponse(workspace))
		return true
	}
	if len(parts) == 3 && r.Method == http.MethodPatch {
		var request struct {
			Name string `json:"name"`
		}
		if err := readRequiredJSON(r, &request); err != nil {
			writeError(w, http.StatusBadRequest, "valid workspace JSON is required")
			return true
		}
		request.Name = strings.TrimSpace(request.Name)
		if request.Name == "" || len(request.Name) > 100 {
			writeError(w, http.StatusBadRequest, "workspace name is required and must be at most 100 characters")
			return true
		}
		workspace, err := h.store.UpdateWorkspace(r.Context(), workspaceID, request.Name, requestActorOrSystem(r))
		if err != nil {
			writeStateError(w, err)
			return true
		}
		writeJSON(w, http.StatusOK, workspaceResponse(workspace))
		return true
	}
	if len(parts) == 4 && parts[3] == "archive" && r.Method == http.MethodPost {
		workspace, err := h.store.ArchiveWorkspace(r.Context(), workspaceID, requestActorOrSystem(r))
		if err != nil {
			writeStateError(w, err)
			return true
		}
		writeJSON(w, http.StatusOK, workspaceResponse(workspace))
		return true
	}
	if len(parts) == 4 && parts[3] == "token" && r.Method == http.MethodPost {
		token, err := newWorkspaceToken()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not generate workspace token")
			return true
		}
		workspace, err := h.store.RotateWorkspaceToken(r.Context(), workspaceID, state.HashWorkspaceToken(token), requestActorOrSystem(r))
		if err != nil {
			writeStateError(w, err)
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"workspace": workspaceResponse(workspace), "api_token": token})
		return true
	}
	if len(parts) == 4 && parts[3] == "audit" && r.Method == http.MethodGet {
		if _, err := h.store.GetWorkspace(r.Context(), workspaceID); err != nil {
			writeStateError(w, err)
			return true
		}
		items, err := h.store.ListWorkspaceAudit(r.Context(), workspaceID)
		if err != nil {
			writeStateError(w, err)
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
		return true
	}
	return false
}

func newWorkspaceToken() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return contract.WorkspaceTokenPrefix + base64.RawURLEncoding.EncodeToString(data), nil
}

func requestActorOrSystem(r *http.Request) string {
	if actor := requestActorSubject(r); actor != "" {
		return actor
	}
	return "system"
}

func workspaceNotFound(err error) bool {
	return errors.Is(err, state.ErrNotFound)
}
