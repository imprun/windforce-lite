package server

import (
	"bytes"
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
	deployment, ok := h.getCanonicalDeployment(w, r, workspaceID, app, "app not found: "+app)
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
		inputSchema, err := schemaReader.Read(action.InputSchema)
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("action %s.%s input schema: %v", deployment.App, key, err))
			return
		}
		outputSchema, err := schemaReader.Read(action.OutputSchema)
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

func buildAppOpenAPI(baseURL string, workspaceID string, deployment contract.Deployment, actions []openAPIAction) map[string]any {
	sorted := append([]openAPIAction(nil), actions...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ActionKey < sorted[j].ActionKey })

	paths := map[string]any{}
	opSeg := newOpSegDeduper()
	for _, action := range sorted {
		inputSchema := schemaOrAny(action.InputSchema)
		outputSchema := schemaOrAny(action.OutputSchema)
		segment := opSeg(action.ActionKey)
		base := fmt.Sprintf("/api/w/%s/jobs/run/%s/%s", workspaceID, deployment.App, action.ActionKey)

		paths[base+"/wait"] = map[string]any{
			"post": map[string]any{
				"operationId": opID("run", segment, "sync"),
				"summary":     fmt.Sprintf("Run %s and wait for the result", action.ActionKey),
				"description": "Runs the action and blocks up to the wait timeout. 200 carries the finished result; 202 means it is still running — poll GET .../jobs/{id}/result with the returned job_id.",
				"requestBody": oapiJSONBody(inputSchema, true),
				"responses": withErrors(map[string]any{
					"200": oapiResponse("Finished run — status is \"completed\" or \"failed\"; result holds the action output, or the failure detail when failed.", map[string]any{
						"type": "object",
						"properties": map[string]any{
							"job_id": oapiStringSchema(),
							"status": oapiStatusSchema(),
							"result": outputSchema,
						},
					}),
					"202": oapiResponse("Still running (wait timed out) — poll GET .../jobs/{id}/result with job_id.", oapiPendingSchema()),
				}, "400", "401", "403", "404", "422"),
			},
		}

		paths[base] = map[string]any{
			"post": map[string]any{
				"operationId": opID("run", segment),
				"summary":     fmt.Sprintf("Run %s (async)", action.ActionKey),
				"description": "Enqueues the action and returns immediately with a job_id. Retrieve the result with GET .../jobs/{id}/result.",
				"requestBody": oapiJSONBody(inputSchema, true),
				"responses": withErrors(map[string]any{
					"201": oapiJobHandleResponse(),
				}, "400", "401", "403", "404", "422"),
			},
		}

		paths[fmt.Sprintf("/api/w/%s/jobs/webhook/%s/%s", workspaceID, deployment.App, action.ActionKey)] = map[string]any{
			"post": map[string]any{
				"operationId": opID("webhook", segment),
				"summary":     fmt.Sprintf("Invoke %s via webhook", action.ActionKey),
				"description": "External webhook intake (ADR-0028). The raw request body is delivered to the action verbatim as ctx.trigger.raw and request headers are pinned for signature verification — the action parses and authenticates the payload itself. Unlike the run endpoints the body is not the typed action input.",
				"requestBody": map[string]any{
					"required": false,
					"content":  map[string]any{"*/*": map[string]any{"schema": map[string]any{}}},
				},
				"responses": withErrors(map[string]any{
					"201": oapiJobHandleResponse(),
				}, "400", "401", "403", "404", "422"),
			},
		}
	}

	paths[fmt.Sprintf("/api/w/%s/jobs/{id}/result", workspaceID)] = map[string]any{
		"get": map[string]any{
			"operationId": "getJobResult",
			"summary":     "Poll a job's result",
			"parameters": []any{map[string]any{
				"name":        "id",
				"in":          "path",
				"required":    true,
				"schema":      oapiStringSchema(),
				"description": "job_id returned by an async run",
			}},
			"responses": map[string]any{
				"200": oapiResponse("Finished run — status \"completed\" or \"failed\"; result holds the output, or the failure detail when failed.", map[string]any{
					"type": "object",
					"properties": map[string]any{
						"status": oapiStatusSchema(),
						"result": map[string]any{},
					},
				}),
				"202": oapiResponse("Still running (or an unknown job_id).", oapiPendingSchema()),
				"401": map[string]any{"$ref": "#/components/responses/Unauthorized"},
				"403": map[string]any{"$ref": "#/components/responses/Forbidden"},
			},
		},
	}

	version := "current"
	if commit := strings.TrimSpace(deployment.Commit); commit != "" {
		if len(commit) > 12 {
			commit = commit[:12]
		}
		version = commit
	}

	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       deployment.App + " API",
			"version":     version,
			"description": "Auto-generated from windforce action input/output schemas. Actions are invoked over HTTP; the run API is asynchronous (enqueue + poll, ADR-0007). A run that FAILS is reported as status \"failed\" inside a 200 response (not an HTTP error) — HTTP 4xx covers only enqueue-time errors (auth, quota, bad request). Actions without a declared schema accept/return an unconstrained JSON body.",
		},
		"servers":  []any{map[string]any{"url": baseURL}},
		"security": []any{map[string]any{"bearerAuth": []any{}}},
		"components": map[string]any{
			"schemas": map[string]any{
				"Error": map[string]any{
					"type":        "object",
					"description": "windforce's uniform error envelope.",
					"properties":  map[string]any{"error": oapiStringSchema()},
					"required":    []any{"error"},
				},
			},
			"responses": openAPIErrorResponses(),
			"securitySchemes": map[string]any{
				"bearerAuth": map[string]any{
					"type":        "http",
					"scheme":      "bearer",
					"description": "windforce API token (Settings -> API tokens). Send as `Authorization: Bearer <token>`.",
				},
			},
		},
		"paths": paths,
	}
}

func schemaOrAny(schema json.RawMessage) any {
	trimmed := bytes.TrimSpace(schema)
	if len(trimmed) == 0 || string(trimmed) == "null" || string(trimmed) == "{}" {
		return map[string]any{}
	}
	return json.RawMessage(append([]byte(nil), trimmed...))
}

func oapiJSONBody(schema any, required bool) map[string]any {
	return map[string]any{
		"required": required,
		"content":  map[string]any{"application/json": map[string]any{"schema": schema}},
	}
}

func oapiResponse(description string, schema any) map[string]any {
	return map[string]any{
		"description": description,
		"content":     map[string]any{"application/json": map[string]any{"schema": schema}},
	}
}

func oapiStringSchema() map[string]any {
	return map[string]any{"type": "string"}
}

func oapiStatusSchema() map[string]any {
	return map[string]any{
		"type":        "string",
		"enum":        []any{"completed", "failed", "canceled"},
		"description": "Terminal run status. A failed action surfaces as \"failed\" within a 200 response, not an HTTP error — inspect result for the failure detail.",
	}
}

var errCodeToComponent = map[string]string{
	"400": "BadRequest",
	"401": "Unauthorized",
	"403": "Forbidden",
	"404": "NotFound",
	"422": "QuotaExceeded",
}

func withErrors(responses map[string]any, codes ...string) map[string]any {
	for _, code := range codes {
		responses[code] = map[string]any{"$ref": "#/components/responses/" + errCodeToComponent[code]}
	}
	return responses
}

func openAPIErrorResponses() map[string]any {
	body := func(description string) map[string]any {
		return map[string]any{
			"description": description,
			"content": map[string]any{
				"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/Error"}},
			},
		}
	}
	return map[string]any{
		"BadRequest":    body("Malformed body, invalid app/action key, or a reserved input key."),
		"Unauthorized":  body("Missing or invalid API token."),
		"Forbidden":     body("Not a member of the workspace, or the workspace is suspended/offboarded."),
		"NotFound":      body("App or action not found."),
		"QuotaExceeded": body("Workspace concurrency or daily-run quota reached."),
	}
}

func oapiJobHandleResponse() map[string]any {
	return oapiResponse("Job enqueued", map[string]any{
		"type":       "object",
		"properties": map[string]any{"job_id": oapiStringSchema()},
		"required":   []any{"job_id"},
	})
}

func oapiPendingSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"job_id": oapiStringSchema(), "status": oapiStringSchema()},
	}
}

func sanitizeIdent(value string) string {
	var builder strings.Builder
	for _, item := range value {
		switch {
		case item >= 'a' && item <= 'z', item >= 'A' && item <= 'Z', item >= '0' && item <= '9':
			builder.WriteRune(item)
		default:
			builder.WriteByte('_')
		}
	}
	return builder.String()
}

func opID(parts ...string) string {
	segments := make([]string, len(parts))
	for index, part := range parts {
		segments[index] = sanitizeIdent(part)
	}
	return strings.Join(segments, "_")
}

func newOpSegDeduper() func(string) string {
	used := map[string]bool{}
	return func(key string) string {
		base := sanitizeIdent(key)
		segment := base
		for index := 2; used[segment]; index++ {
			segment = fmt.Sprintf("%s_%d", base, index)
		}
		used[segment] = true
		return segment
	}
}
