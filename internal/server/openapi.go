package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/imprun/windforce-lite/internal/contract"
)

type openAPIAction struct {
	ActionKey    string
	InputSchema  json.RawMessage
	OutputSchema json.RawMessage
}

func (h *Handler) handleCanonicalAppOpenAPI(w http.ResponseWriter, r *http.Request, workspaceID string, app string) {
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

	keys := make([]string, 0, len(deployment.Actions))
	for key := range deployment.Actions {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	actions := make([]openAPIAction, 0, len(keys))
	for _, key := range keys {
		action := deployment.Actions[key]
		inputSchema, err := schemaReader.Read(action.InputSchema, action.InputSchemaBody)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("action %s.%s input schema: %v", deployment.App, key, err))
			return
		}
		outputSchema, err := schemaReader.Read(action.OutputSchema, action.OutputSchemaBody)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("action %s.%s output schema: %v", deployment.App, key, err))
			return
		}
		actions = append(actions, openAPIAction{
			ActionKey:    key,
			InputSchema:  inputSchema,
			OutputSchema: outputSchema,
		})
	}

	writeJSON(w, http.StatusOK, buildAppOpenAPI(requestBaseURL(r), contract.NormalizeWorkspace(workspaceID), deployment, actions))
}

func (h *Handler) handleCanonicalControlPlaneOpenAPI(w http.ResponseWriter, r *http.Request, workspaceID string) {
	writeJSON(w, http.StatusOK, buildControlPlaneOpenAPI(requestBaseURL(r), contract.NormalizeWorkspace(workspaceID)))
}

func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if value := r.Header.Get("X-Forwarded-Proto"); value != "" {
		scheme = strings.TrimSpace(strings.SplitN(value, ",", 2)[0])
	}
	host := r.Host
	if value := r.Header.Get("X-Forwarded-Host"); value != "" {
		host = strings.TrimSpace(strings.SplitN(value, ",", 2)[0])
	}
	return scheme + "://" + host
}
