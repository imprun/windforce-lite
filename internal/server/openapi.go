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

func buildControlPlaneOpenAPI(baseURL string, workspaceID string) map[string]any {
	paths := map[string]any{
		"/api/w/{workspace}/openapi.json": map[string]any{
			"get": map[string]any{
				"operationId": "getControlPlaneOpenAPI",
				"summary":     "Get the control-plane OpenAPI document",
				"parameters":  []any{oapiWorkspaceParam(workspaceID)},
				"responses": map[string]any{
					"200": oapiResponse("Control-plane OpenAPI document.", map[string]any{"type": "object", "additionalProperties": true}),
				},
			},
		},
		"/api/w/{workspace}/git_sources": map[string]any{
			"get": map[string]any{
				"operationId": "listGitSources",
				"summary":     "List git sources",
				"parameters":  []any{oapiWorkspaceParam(workspaceID)},
				"responses": withErrors(map[string]any{
					"200": oapiResponse("Registered git sources.", map[string]any{
						"type":  "array",
						"items": oapiSchemaRef("GitSource"),
					}),
				}, "401", "403"),
			},
			"post": map[string]any{
				"operationId": "registerGitSource",
				"summary":     "Register a git source",
				"parameters":  []any{oapiWorkspaceParam(workspaceID)},
				"requestBody": oapiJSONBody(oapiSchemaRef("RegisterGitSourceRequest"), true),
				"responses": withErrors(map[string]any{
					"201": oapiResponse("Registered git source.", oapiSchemaRef("GitSource")),
				}, "400", "401", "403"),
			},
		},
		"/api/w/{workspace}/git_sources/probe": map[string]any{
			"post": map[string]any{
				"operationId": "probeGitSource",
				"summary":     "Probe a git source without registering it",
				"parameters":  []any{oapiWorkspaceParam(workspaceID)},
				"requestBody": oapiJSONBody(oapiSchemaRef("ProbeGitSourceRequest"), true),
				"responses": withErrors(map[string]any{
					"200": oapiResponse("Probe result.", oapiSchemaRef("GitSourceProbeResult")),
				}, "400", "401", "403"),
			},
		},
		"/api/w/{workspace}/git_sources/sample": map[string]any{
			"post": map[string]any{
				"operationId": "createSampleGitSource",
				"summary":     "Create and sync a managed sample git source",
				"parameters":  []any{oapiWorkspaceParam(workspaceID)},
				"requestBody": oapiJSONBody(oapiSchemaRef("SampleGitSourceRequest"), false),
				"responses": withErrors(map[string]any{
					"200": oapiResponse("Existing sample source synced.", oapiSchemaRef("SampleSyncResponse")),
					"201": oapiResponse("Sample source created and synced.", oapiSchemaRef("SampleSyncResponse")),
				}, "400", "401", "403"),
			},
		},
		"/api/w/{workspace}/git_sources/{gitSourceId}": map[string]any{
			"patch": map[string]any{
				"operationId": "patchGitSource",
				"summary":     "Patch a git source",
				"parameters":  []any{oapiWorkspaceParam(workspaceID), oapiPathParam("gitSourceId", "Numeric git source id returned by register/list.")},
				"requestBody": oapiJSONBody(oapiSchemaRef("PatchGitSourceRequest"), true),
				"responses": withErrors(map[string]any{
					"200": oapiResponse("Updated git source.", oapiSchemaRef("GitSource")),
				}, "400", "401", "403", "404"),
			},
			"delete": map[string]any{
				"operationId": "deleteGitSource",
				"summary":     "Delete a git source",
				"parameters":  []any{oapiWorkspaceParam(workspaceID), oapiPathParam("gitSourceId", "Numeric git source id returned by register/list.")},
				"responses": withErrors(map[string]any{
					"204": map[string]any{"description": "Deleted."},
				}, "400", "401", "403", "404"),
			},
		},
		"/api/w/{workspace}/git_sources/{gitSourceId}/sync": map[string]any{
			"post": map[string]any{
				"operationId": "syncGitSource",
				"summary":     "Sync a registered git source",
				"parameters":  []any{oapiWorkspaceParam(workspaceID), oapiPathParam("gitSourceId", "Numeric git source id returned by register/list.")},
				"responses": withErrors(map[string]any{
					"200": oapiResponse("Sync result and discovered actions.", oapiSchemaRef("GitSourceSyncResult")),
				}, "400", "401", "403", "404"),
			},
		},
		"/api/w/{workspace}/apps": map[string]any{
			"get": map[string]any{
				"operationId": "listApps",
				"summary":     "List apps",
				"description": "The bare response is an array of app keys. Use view=summary for catalog rows.",
				"parameters": []any{
					oapiWorkspaceParam(workspaceID),
					oapiQueryParam("view", "Set to summary to return app summary rows.", map[string]any{"type": "string", "enum": []any{"summary"}}, false),
				},
				"responses": withErrors(map[string]any{
					"200": oapiResponse("App keys or summary rows.", map[string]any{
						"oneOf": []any{
							map[string]any{"type": "array", "items": oapiStringSchema()},
							oapiSchemaRef("AppsSummaryResponse"),
						},
					}),
				}, "401", "403"),
			},
		},
		"/api/w/{workspace}/apps/{app}": map[string]any{
			"get": map[string]any{
				"operationId": "getApp",
				"summary":     "Get app detail and action contracts",
				"description": "Returns app metadata and actions. Each action includes materialized input_schema and output_schema JSON Schema documents for adapters and UI forms.",
				"parameters":  []any{oapiWorkspaceParam(workspaceID), oapiPathParam("app", "App key.")},
				"responses": withErrors(map[string]any{
					"200": oapiResponse("App detail including action schemas.", oapiSchemaRef("AppDetailResponse")),
				}, "400", "401", "403", "404"),
			},
			"patch": map[string]any{
				"operationId": "patchApp",
				"summary":     "Set or clear the app route tag override",
				"parameters":  []any{oapiWorkspaceParam(workspaceID), oapiPathParam("app", "App key.")},
				"requestBody": oapiJSONBody(oapiSchemaRef("TagOverrideRequest"), true),
				"responses": withErrors(map[string]any{
					"200": oapiResponse("Updated app.", oapiSchemaRef("App")),
				}, "400", "401", "403", "404"),
			},
		},
		"/api/w/{workspace}/apps/{app}/source": map[string]any{
			"get": map[string]any{
				"operationId": "getAppSource",
				"summary":     "Get materialized app source files",
				"parameters":  []any{oapiWorkspaceParam(workspaceID), oapiPathParam("app", "App key.")},
				"responses": withErrors(map[string]any{
					"200": oapiResponse("Materialized source files.", oapiSchemaRef("AppSourceResponse")),
				}, "400", "401", "403", "404"),
			},
		},
		"/api/w/{workspace}/apps/{app}/history": map[string]any{
			"get": map[string]any{
				"operationId": "getAppHistory",
				"summary":     "Get app deployment history",
				"parameters":  []any{oapiWorkspaceParam(workspaceID), oapiPathParam("app", "App key.")},
				"responses": withErrors(map[string]any{
					"200": oapiResponse("Deployment history.", map[string]any{"type": "array", "items": oapiSchemaRef("AppHistoryItem")}),
				}, "400", "401", "403", "404"),
			},
		},
		"/api/w/{workspace}/apps/{app}/openapi.json": map[string]any{
			"get": map[string]any{
				"operationId": "getAppInvocationOpenAPI",
				"summary":     "Get app invocation OpenAPI",
				"description": "Returns an app-specific OpenAPI generated from the materialized action input/output schemas.",
				"parameters":  []any{oapiWorkspaceParam(workspaceID), oapiPathParam("app", "App key.")},
				"responses": withErrors(map[string]any{
					"200": oapiResponse("App invocation OpenAPI.", map[string]any{"type": "object", "additionalProperties": true}),
				}, "400", "401", "403", "404"),
			},
		},
		"/api/w/{workspace}/apps/{app}/actions/{action}": map[string]any{
			"get": map[string]any{
				"operationId": "getAction",
				"summary":     "Get action detail and schemas",
				"description": "This is the schema discovery endpoint for protocol adapters. input_schema and output_schema are the materialized JSON Schema documents from windforce.json/source.",
				"parameters":  []any{oapiWorkspaceParam(workspaceID), oapiPathParam("app", "App key."), oapiPathParam("action", "Action key.")},
				"responses": withErrors(map[string]any{
					"200": oapiResponse("Action detail including materialized schemas.", oapiSchemaRef("Action")),
				}, "400", "401", "403", "404"),
			},
			"patch": map[string]any{
				"operationId": "patchAction",
				"summary":     "Set or clear the action route tag override",
				"parameters":  []any{oapiWorkspaceParam(workspaceID), oapiPathParam("app", "App key."), oapiPathParam("action", "Action key.")},
				"requestBody": oapiJSONBody(oapiSchemaRef("TagOverrideRequest"), true),
				"responses": withErrors(map[string]any{
					"200": oapiResponse("Updated action.", oapiSchemaRef("Action")),
				}, "400", "401", "403", "404"),
			},
		},
		"/api/w/{workspace}/apps/{app}/requeue": map[string]any{
			"post": map[string]any{
				"operationId": "requeueApp",
				"summary":     "Requeue queued jobs for an app",
				"parameters":  []any{oapiWorkspaceParam(workspaceID), oapiPathParam("app", "App key.")},
				"requestBody": oapiJSONBody(map[string]any{"type": "object", "additionalProperties": false}, false),
				"responses": withErrors(map[string]any{
					"200": oapiResponse("Requeue count.", oapiSchemaRef("RequeueResponse")),
				}, "400", "401", "403", "404"),
			},
		},
		"/api/w/{workspace}/deployments/{deploymentId}": map[string]any{
			"get": map[string]any{
				"operationId": "getDeployment",
				"summary":     "Get deployment status",
				"parameters":  []any{oapiWorkspaceParam(workspaceID), oapiPathParam("deploymentId", "Deployment id.")},
				"responses": withErrors(map[string]any{
					"200": oapiResponse("Deployment status.", oapiSchemaRef("Deployment")),
				}, "401", "403", "404"),
			},
		},
		"/api/w/{workspace}/worker-tags": map[string]any{
			"get": map[string]any{
				"operationId": "listWorkerTags",
				"summary":     "List worker tag liveness",
				"parameters":  []any{oapiWorkspaceParam(workspaceID)},
				"responses": withErrors(map[string]any{
					"200": oapiResponse("Worker tag liveness.", oapiSchemaRef("WorkerTagsResponse")),
				}, "401", "403"),
			},
		},
	}

	return map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       "Windforce Lite Control Plane API",
			"version":     "current",
			"description": "Workspace control-plane API for registering git sources, syncing windforce.json apps, inspecting app/action metadata, and discovering materialized action input/output schemas.",
		},
		"servers":  []any{map[string]any{"url": baseURL}},
		"security": []any{map[string]any{"bearerAuth": []any{}}},
		"components": map[string]any{
			"schemas":         controlPlaneSchemas(),
			"responses":       openAPIErrorResponses(),
			"securitySchemes": openAPISecuritySchemes(),
		},
		"paths": paths,
	}
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

func oapiIntegerSchema() map[string]any {
	return map[string]any{"type": "integer"}
}

func oapiBooleanSchema() map[string]any {
	return map[string]any{"type": "boolean"}
}

func oapiDateTimeSchema() map[string]any {
	return map[string]any{"type": "string", "format": "date-time"}
}

func oapiSchemaRef(name string) map[string]any {
	return map[string]any{"$ref": "#/components/schemas/" + name}
}

func oapiPathParam(name string, description string) map[string]any {
	return map[string]any{
		"name":        name,
		"in":          "path",
		"required":    true,
		"description": description,
		"schema":      oapiStringSchema(),
	}
}

func oapiWorkspaceParam(example string) map[string]any {
	param := oapiPathParam("workspace", "Workspace id.")
	if example != "" {
		param["example"] = example
	}
	return param
}

func oapiQueryParam(name string, description string, schema map[string]any, required bool) map[string]any {
	return map[string]any{
		"name":        name,
		"in":          "query",
		"required":    required,
		"description": description,
		"schema":      schema,
	}
}

func openAPISecuritySchemes() map[string]any {
	return map[string]any{
		"bearerAuth": map[string]any{
			"type":        "http",
			"scheme":      "bearer",
			"description": "windforce API token (Settings -> API tokens). Send as `Authorization: Bearer <token>`.",
		},
	}
}

func controlPlaneSchemas() map[string]any {
	jsonSchema := map[string]any{
		"type":                 "object",
		"description":          "Materialized JSON Schema document. An empty object means unconstrained JSON.",
		"additionalProperties": true,
	}
	stringArray := map[string]any{"type": "array", "items": oapiStringSchema()}
	nullableString := map[string]any{"type": []any{"string", "null"}}
	nullableInteger := map[string]any{"type": []any{"integer", "null"}}
	nullableDateTime := map[string]any{"type": []any{"string", "null"}, "format": "date-time"}

	return map[string]any{
		"Error": map[string]any{
			"type":        "object",
			"description": "windforce's uniform error envelope.",
			"properties":  map[string]any{"error": oapiStringSchema()},
			"required":    []any{"error"},
		},
		"GitSource": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":                 oapiIntegerSchema(),
				"name":               oapiStringSchema(),
				"workspace_id":       oapiStringSchema(),
				"repo_url":           oapiStringSchema(),
				"branch":             oapiStringSchema(),
				"subpath":            oapiStringSchema(),
				"creds_ref":          oapiStringSchema(),
				"kind":               oapiStringSchema(),
				"last_synced_commit": nullableString,
				"last_synced_at":     nullableDateTime,
				"created_at":         oapiDateTimeSchema(),
			},
			"required": []any{"id", "name", "workspace_id", "repo_url", "branch", "subpath", "creds_ref", "kind", "last_synced_commit", "last_synced_at", "created_at"},
		},
		"RegisterGitSourceRequest": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":      oapiStringSchema(),
				"repo_url":  oapiStringSchema(),
				"branch":    oapiStringSchema(),
				"subpath":   oapiStringSchema(),
				"creds_ref": oapiStringSchema(),
			},
			"required": []any{"name", "repo_url"},
		},
		"ProbeGitSourceRequest": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"repo_url":     oapiStringSchema(),
				"branch":       oapiStringSchema(),
				"access_token": oapiStringSchema(),
				"creds_ref":    oapiStringSchema(),
			},
			"required": []any{"repo_url"},
		},
		"PatchGitSourceRequest": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":      oapiStringSchema(),
				"repo_url":  oapiStringSchema(),
				"branch":    oapiStringSchema(),
				"subpath":   oapiStringSchema(),
				"creds_ref": nullableString,
			},
		},
		"SampleGitSourceRequest": map[string]any{
			"type":       "object",
			"properties": map[string]any{"app_key": oapiStringSchema()},
		},
		"GitSourceProbeResult": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"reachable":     oapiBooleanSchema(),
				"branch":        oapiStringSchema(),
				"branch_exists": oapiBooleanSchema(),
				"branches":      stringArray,
				"error":         oapiStringSchema(),
			},
			"required": []any{"reachable", "branches"},
		},
		"GitSourceSyncResult": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"commit":  oapiStringSchema(),
				"app":     oapiStringSchema(),
				"actions": stringArray,
			},
			"required": []any{"commit", "app", "actions"},
		},
		"SampleSyncResponse": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"source":      oapiSchemaRef("GitSource"),
				"sync_result": oapiSchemaRef("GitSourceSyncResult"),
			},
			"required": []any{"source", "sync_result"},
		},
		"App": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":                    oapiStringSchema(),
				"workspace_id":          oapiStringSchema(),
				"app_key":               oapiStringSchema(),
				"git_source_id":         oapiIntegerSchema(),
				"commit_sha":            oapiStringSchema(),
				"entrypoint":            oapiStringSchema(),
				"tag":                   oapiStringSchema(),
				"tag_override":          nullableString,
				"timeout_s":             oapiIntegerSchema(),
				"script_lang":           oapiStringSchema(),
				"required_capabilities": stringArray,
				"max_concurrent":        nullableInteger,
				"updated_at":            oapiDateTimeSchema(),
				"effective_route_tag":   oapiStringSchema(),
			},
			"required": []any{"id", "workspace_id", "app_key", "git_source_id", "commit_sha", "entrypoint", "timeout_s", "updated_at"},
		},
		"AppSummary": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":                    oapiStringSchema(),
				"workspace_id":          oapiStringSchema(),
				"app_key":               oapiStringSchema(),
				"git_source_id":         oapiIntegerSchema(),
				"commit_sha":            oapiStringSchema(),
				"entrypoint":            oapiStringSchema(),
				"tag":                   oapiStringSchema(),
				"tag_override":          nullableString,
				"timeout_s":             oapiIntegerSchema(),
				"script_lang":           oapiStringSchema(),
				"required_capabilities": stringArray,
				"max_concurrent":        nullableInteger,
				"updated_at":            oapiDateTimeSchema(),
				"effective_route_tag":   oapiStringSchema(),
				"actions_count":         oapiIntegerSchema(),
				"schedules_count":       oapiIntegerSchema(),
				"flows_count":           oapiIntegerSchema(),
			},
		},
		"AppsSummaryResponse": map[string]any{
			"type":       "object",
			"properties": map[string]any{"apps": map[string]any{"type": "array", "items": oapiSchemaRef("AppSummary")}},
			"required":   []any{"apps"},
		},
		"Action": map[string]any{
			"type":        "object",
			"description": "Canonical action detail. input_schema and output_schema expose the materialized action contract.",
			"properties": map[string]any{
				"id":                     oapiStringSchema(),
				"workspace_id":           oapiStringSchema(),
				"app_key":                oapiStringSchema(),
				"action_key":             oapiStringSchema(),
				"input_schema":           jsonSchema,
				"output_schema":          jsonSchema,
				"tag":                    nullableString,
				"tag_override":           nullableString,
				"timeout_s":              nullableInteger,
				"required_capabilities":  stringArray,
				"updated_at":             oapiDateTimeSchema(),
				"effective_capabilities": stringArray,
				"effective_route_tag":    oapiStringSchema(),
			},
			"required": []any{"id", "workspace_id", "app_key", "action_key", "input_schema", "output_schema", "updated_at"},
		},
		"AppDetailResponse": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"app":     oapiSchemaRef("App"),
				"actions": map[string]any{"type": "array", "items": oapiSchemaRef("Action")},
			},
			"required": []any{"app", "actions"},
		},
		"TagOverrideRequest": map[string]any{
			"type":       "object",
			"properties": map[string]any{"tag_override": nullableString},
			"required":   []any{"tag_override"},
		},
		"AppSourceResponse": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"app_key":       oapiStringSchema(),
				"git_source_id": oapiIntegerSchema(),
				"commit_sha":    oapiStringSchema(),
				"files":         map[string]any{"type": "object", "additionalProperties": oapiStringSchema()},
				"skipped":       stringArray,
			},
		},
		"AppHistoryItem": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":            oapiStringSchema(),
				"commit_sha":    oapiStringSchema(),
				"entrypoint":    oapiStringSchema(),
				"source":        oapiStringSchema(),
				"deployment_id": nullableString,
				"message":       nullableString,
				"created_at":    oapiDateTimeSchema(),
			},
		},
		"RequeueResponse": map[string]any{
			"type":       "object",
			"properties": map[string]any{"requeued": oapiIntegerSchema()},
		},
		"Deployment": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":         oapiStringSchema(),
				"app_key":    oapiStringSchema(),
				"commit_sha": oapiStringSchema(),
				"created_at": oapiDateTimeSchema(),
			},
			"additionalProperties": true,
		},
		"WorkerTagsResponse": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"tags": map[string]any{"type": "array", "items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"tag":          oapiStringSchema(),
						"live_workers": oapiIntegerSchema(),
						"capabilities": stringArray,
						"workers":      map[string]any{"type": "array", "items": map[string]any{}},
					},
				}},
				"dedicated_tag": nullableString,
			},
		},
	}
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
