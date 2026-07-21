package server

import (
	"net/http"
	"strings"
)

func (h *Handler) handleExecutionOpenAPI(w http.ResponseWriter, r *http.Request) {
	serverURL := strings.TrimSuffix(requestBaseURL(r), "/")
	writeJSON(w, http.StatusOK, map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       "Windforce Execution API",
			"version":     "v1",
			"description": "Protocol adapters create and observe runs through this API. Run admission pins the active release before enqueueing a worker job.",
		},
		"servers": []map[string]string{{"url": serverURL}},
		"paths": map[string]any{
			"/execution/v1/workspaces/{workspace}/runs": map[string]any{
				"post": map[string]any{
					"summary": "Create a run", "operationId": "createRun",
					"requestBody": oapiJSONBody(map[string]any{
						"type": "object",
						"properties": map[string]any{
							"app":          oapiStringSchema(),
							"action":       oapiStringSchema(),
							"input":        map[string]any{"type": "object", "additionalProperties": true},
							"client_id":    map[string]any{"type": "string", "description": "Client Registry identity asserted by a trusted trigger adapter."},
							"adapter":      oapiStringSchema(),
							"trigger_kind": oapiStringSchema(),
						},
						"required": []any{"app", "action", "input"},
					}, true),
					"responses": executionOpenAPIResponses("Run admitted"),
				},
			},
			"/execution/v1/workspaces/{workspace}/runs/{run_id}": map[string]any{
				"get": map[string]any{"summary": "Get run status", "operationId": "getRun", "responses": executionOpenAPIResponses("Run status")},
			},
			"/execution/v1/workspaces/{workspace}/runs/{run_id}/result": map[string]any{
				"get": map[string]any{"summary": "Get run result", "operationId": "getRunResult", "responses": executionOpenAPIResponses("Run result")},
			},
			"/execution/v1/workspaces/{workspace}/runs/{run_id}/cancel": map[string]any{
				"post": map[string]any{"summary": "Cancel a run", "operationId": "cancelRun", "responses": executionOpenAPIResponses("Run canceled")},
			},
			"/execution/v1/workspaces/{workspace}/apps/{app}": map[string]any{
				"get": map[string]any{"summary": "Read the active app contract", "operationId": "describeApp", "responses": executionOpenAPIResponses("Active app contract")},
			},
		},
	})
}

func executionOpenAPIResponses(description string) map[string]any {
	return map[string]any{
		"200": map[string]any{"description": description},
		"201": map[string]any{"description": description},
		"400": map[string]any{"description": "Invalid request"},
		"401": map[string]any{"description": "Unauthorized"},
		"404": map[string]any{"description": "Resource not found"},
		"409": map[string]any{"description": "Admission conflict"},
		"503": map[string]any{"description": "Execution service unavailable"},
	}
}
