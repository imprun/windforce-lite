package server

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
				"summary":     "Validate and register a git source",
				"parameters":  []any{oapiWorkspaceParam(workspaceID)},
				"requestBody": oapiJSONBody(oapiSchemaRef("RegisterGitSourceRequest"), true),
				"responses": withErrors(map[string]any{
					"201": oapiResponse("Registered git source.", oapiSchemaRef("GitSource")),
				}, "400", "401", "403", "422"),
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
				}, "400", "401", "403", "404", "422"),
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
		"/api/w/{workspace}/git_sources/{gitSourceId}/deploy": map[string]any{
			"post": map[string]any{
				"operationId": "deployGitSource",
				"summary":     "Deploy the current commit of a registered git source",
				"parameters":  []any{oapiWorkspaceParam(workspaceID), oapiPathParam("gitSourceId", "Numeric git source id returned by register/list.")},
				"responses": withErrors(map[string]any{
					"200": oapiResponse("Deployment result and discovered actions.", oapiSchemaRef("GitSourceSyncResult")),
				}, "400", "401", "403", "404", "422"),
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
				"description": "Returns app metadata and actions. Each action includes Windforce catalog input_schema and output_schema fields as base64-encoded materialized JSON Schema bytes. This is the bulk schema discovery API.",
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
				"description": "Returns canonical action metadata. input_schema and output_schema use Windforce catalog encoding: base64-encoded materialized JSON Schema bytes from windforce.json/source. Use the sibling /schema endpoint when a control-plane client needs raw JSON Schema documents.",
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
		"/api/w/{workspace}/apps/{app}/actions/{action}/schema": map[string]any{
			"get": map[string]any{
				"operationId": "getActionSchema",
				"summary":     "Get action JSON Schemas",
				"description": "Schema discovery endpoint for protocol adapters and UI forms. Returns the materialized input_schema and output_schema as raw JSON Schema documents pinned by sync, while GET /actions/{action} keeps Windforce's canonical base64 catalog encoding.",
				"parameters":  []any{oapiWorkspaceParam(workspaceID), oapiPathParam("app", "App key."), oapiPathParam("action", "Action key.")},
				"responses": withErrors(map[string]any{
					"200": oapiResponse("Action input/output JSON Schemas.", oapiSchemaRef("ActionSchema")),
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
		"/api/w/{workspace}/state": map[string]any{
			"get": map[string]any{
				"operationId": "getState",
				"summary":     "Get a ctx.state value",
				"parameters": []any{
					oapiWorkspaceParam(workspaceID),
					oapiQueryParam("path", "State path.", oapiStringSchema(), true),
				},
				"responses": withErrors(map[string]any{
					"200": oapiResponse("Stored JSON value or null.", oapiSchemaRef("JSONValue")),
				}, "400", "401", "403"),
			},
			"post": map[string]any{
				"operationId": "setState",
				"summary":     "Set a ctx.state value",
				"parameters": []any{
					oapiWorkspaceParam(workspaceID),
					oapiQueryParam("path", "State path.", oapiStringSchema(), true),
				},
				"requestBody": oapiJSONBody(oapiSchemaRef("JSONValue"), true),
				"responses": withErrors(map[string]any{
					"200": oapiResponse("Stored path.", oapiSchemaRef("PathResponse")),
				}, "400", "401", "403"),
			},
		},
		"/api/w/{workspace}/variables": map[string]any{
			"get": map[string]any{
				"operationId": "listVariables",
				"summary":     "List workspace variables",
				"parameters":  []any{oapiWorkspaceParam(workspaceID)},
				"responses": withErrors(map[string]any{
					"200": oapiResponse("Variables. Secret values are redacted in list responses.", map[string]any{"type": "array", "items": oapiSchemaRef("Variable")}),
				}, "401", "403"),
			},
			"post": map[string]any{
				"operationId": "setVariable",
				"summary":     "Set a workspace or app-scoped variable",
				"parameters":  []any{oapiWorkspaceParam(workspaceID)},
				"requestBody": oapiJSONBody(oapiSchemaRef("SetVariableRequest"), true),
				"responses": withErrors(map[string]any{
					"200": oapiResponse("Stored variable key.", oapiSchemaRef("VariableSetResponse")),
				}, "400", "401", "403"),
			},
		},
		"/api/w/{workspace}/variables/get/p/{path}": map[string]any{
			"get": map[string]any{
				"operationId": "getVariable",
				"summary":     "Get a variable by path",
				"description": "The {path} segment represents the remaining path after /variables/get/p/ and may contain slashes.",
				"parameters": []any{
					oapiWorkspaceParam(workspaceID),
					oapiPathParam("path", "Variable path."),
					oapiQueryParam("app", "Optional exact app key scope for console lookup.", oapiStringSchema(), false),
				},
				"responses": withErrors(map[string]any{
					"200": oapiResponse("Variable value.", oapiSchemaRef("VariableValueResponse")),
				}, "401", "403", "404"),
			},
		},
		"/api/w/{workspace}/variables/p/{path}": map[string]any{
			"delete": map[string]any{
				"operationId": "deleteVariable",
				"summary":     "Delete a variable by path",
				"description": "The {path} segment represents the remaining path after /variables/p/ and may contain slashes.",
				"parameters": []any{
					oapiWorkspaceParam(workspaceID),
					oapiPathParam("path", "Variable path."),
					oapiQueryParam("app", "Optional app key for app-scoped deletion.", oapiStringSchema(), false),
				},
				"responses": withErrors(map[string]any{
					"204": map[string]any{"description": "Deleted."},
				}, "401", "403"),
			},
		},
		"/api/w/{workspace}/resources": map[string]any{
			"post": map[string]any{
				"operationId": "setResource",
				"summary":     "Set a JSON resource",
				"parameters":  []any{oapiWorkspaceParam(workspaceID)},
				"requestBody": oapiJSONBody(oapiSchemaRef("SetResourceRequest"), true),
				"responses": withErrors(map[string]any{
					"200": oapiResponse("Stored resource path.", oapiSchemaRef("PathResponse")),
				}, "400", "401", "403"),
			},
		},
		"/api/w/{workspace}/resources/get/p/{path}": map[string]any{
			"get": map[string]any{
				"operationId": "getResource",
				"summary":     "Get a JSON resource by path",
				"description": "The {path} segment represents the remaining path after /resources/get/p/ and may contain slashes.",
				"parameters":  []any{oapiWorkspaceParam(workspaceID), oapiPathParam("path", "Resource path.")},
				"responses": withErrors(map[string]any{
					"200": oapiResponse("Stored JSON value or null.", oapiSchemaRef("JSONValue")),
				}, "401", "403", "404"),
			},
		},
		"/api/w/{workspace}/jobs/run/{app}/{action}": map[string]any{
			"post": map[string]any{
				"operationId": "runJob",
				"summary":     "Enqueue an action job",
				"parameters":  []any{oapiWorkspaceParam(workspaceID), oapiPathParam("app", "App key."), oapiPathParam("action", "Action key.")},
				"requestBody": oapiJSONBody(oapiSchemaRef("JobInput"), true),
				"responses": withErrors(map[string]any{
					"201": oapiResponse("Job enqueued.", oapiSchemaRef("JobHandleResponse")),
				}, "400", "401", "403", "404", "409", "413"),
			},
		},
		"/api/w/{workspace}/jobs/run/{app}/{action}/wait": map[string]any{
			"post": map[string]any{
				"operationId": "runJobAndWait",
				"summary":     "Enqueue an action job and wait for completion",
				"parameters": []any{
					oapiWorkspaceParam(workspaceID),
					oapiPathParam("app", "App key."),
					oapiPathParam("action", "Action key."),
					oapiQueryParam("timeout_ms", "Wait timeout in milliseconds. The server caps this at its maximum wait timeout.", oapiIntegerSchema(), false),
				},
				"requestBody": oapiJSONBody(oapiSchemaRef("JobInput"), true),
				"responses": withErrors(map[string]any{
					"200": oapiResponse("Finished job result.", oapiSchemaRef("JobWaitResultResponse")),
					"202": oapiResponse("Job is still pending.", oapiSchemaRef("JobPendingResponse")),
				}, "400", "401", "403", "404", "409", "413"),
			},
		},
		"/api/w/{workspace}/jobs/webhook/{app}/{action}": map[string]any{
			"post": map[string]any{
				"operationId": "webhookJob",
				"summary":     "Enqueue an action job from a raw webhook payload",
				"description": "The raw request body is delivered to the action as trigger raw payload, with denylisted and size-capped request headers pinned on the job.",
				"parameters":  []any{oapiWorkspaceParam(workspaceID), oapiPathParam("app", "App key."), oapiPathParam("action", "Action key.")},
				"requestBody": map[string]any{
					"required": false,
					"content":  map[string]any{"*/*": map[string]any{"schema": map[string]any{}}},
				},
				"responses": withErrors(map[string]any{
					"201": oapiResponse("Job enqueued.", oapiSchemaRef("JobHandleResponse")),
				}, "400", "401", "403", "404", "409", "413"),
			},
		},
		"/api/w/{workspace}/jobs": map[string]any{
			"get": map[string]any{
				"operationId": "listJobs",
				"summary":     "List jobs",
				"parameters": []any{
					oapiWorkspaceParam(workspaceID),
					oapiQueryParam("status", "Filter by queued, running, success, failure, canceled, completed, or all.", oapiStringSchema(), false),
					oapiQueryParam("limit", "Page size from 1 to 500.", oapiIntegerSchema(), false),
					oapiQueryParam("cursor", "Opaque cursor returned by the previous page.", oapiStringSchema(), false),
					oapiQueryParam("app", "Optional app key filter.", oapiStringSchema(), false),
					oapiQueryParam("action", "Optional action key filter.", oapiStringSchema(), false),
					oapiQueryParam("trigger_kind", "Optional trigger kind filter.", oapiStringSchema(), false),
					oapiQueryParam("since", "RFC3339 lower bound for created_at.", oapiStringSchema(), false),
					oapiQueryParam("until", "RFC3339 upper bound for created_at.", oapiStringSchema(), false),
				},
				"responses": withErrors(map[string]any{
					"200": oapiResponse("Job page.", oapiSchemaRef("JobListResponse")),
				}, "400", "401", "403"),
			},
		},
		"/api/w/{workspace}/jobs/summary": map[string]any{
			"get": map[string]any{
				"operationId": "getJobSummary",
				"summary":     "Get job queue summary",
				"parameters": []any{
					oapiWorkspaceParam(workspaceID),
					oapiQueryParam("recent_seconds", "Recent completion window from 1 to 604800 seconds.", oapiIntegerSchema(), false),
				},
				"responses": withErrors(map[string]any{
					"200": oapiResponse("Queue summary.", oapiSchemaRef("JobSummary")),
				}, "400", "401", "403"),
			},
		},
		"/api/w/{workspace}/jobs/{jobId}": map[string]any{
			"get": map[string]any{
				"operationId": "getJob",
				"summary":     "Get job status",
				"parameters":  []any{oapiWorkspaceParam(workspaceID), oapiPathParam("jobId", "Job id.")},
				"responses": withErrors(map[string]any{
					"200": oapiResponse("Job status.", oapiSchemaRef("JobStatus")),
				}, "401", "403", "404"),
			},
		},
		"/api/w/{workspace}/jobs/{jobId}/result": map[string]any{
			"get": map[string]any{
				"operationId": "getJobResult",
				"summary":     "Get or poll a job result",
				"parameters":  []any{oapiWorkspaceParam(workspaceID), oapiPathParam("jobId", "Job id.")},
				"responses": withErrors(map[string]any{
					"200": oapiResponse("Finished job result.", oapiSchemaRef("JobResultResponse")),
					"202": oapiResponse("Job is still pending.", oapiSchemaRef("JobPendingResponse")),
				}, "401", "403", "404"),
			},
		},
		"/api/w/{workspace}/jobs/{jobId}/logs": map[string]any{
			"get": map[string]any{
				"operationId": "getJobLogs",
				"summary":     "Get job logs",
				"parameters": []any{
					oapiWorkspaceParam(workspaceID),
					oapiPathParam("jobId", "Job id."),
					oapiQueryParam("tail_bytes", "Optional non-negative byte count; capped by the server.", oapiIntegerSchema(), false),
				},
				"responses": withErrors(map[string]any{
					"200": oapiTextResponse("Plaintext job logs."),
				}, "400", "401", "403", "404"),
			},
		},
		"/api/w/{workspace}/jobs/{jobId}/cancel": map[string]any{
			"post": map[string]any{
				"operationId": "cancelJob",
				"summary":     "Cancel a job",
				"parameters":  []any{oapiWorkspaceParam(workspaceID), oapiPathParam("jobId", "Job id.")},
				"requestBody": oapiJSONBody(oapiSchemaRef("CancelJobRequest"), false),
				"responses": withErrors(map[string]any{
					"200": oapiResponse("Cancel result.", oapiSchemaRef("CancelResult")),
				}, "400", "401", "403", "404"),
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

func controlPlaneSchemas() map[string]any {
	jsonSchema := oapiSchemaRef("JSONSchema")
	catalogSchema := oapiSchemaRef("Base64JSONSchema")
	stringArray := map[string]any{"type": "array", "items": oapiStringSchema()}
	nullableString := map[string]any{"type": []any{"string", "null"}}
	nullableInteger := map[string]any{"type": []any{"integer", "null"}}
	nullableDateTime := map[string]any{"type": []any{"string", "null"}, "format": "date-time"}
	appProperties := map[string]any{
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
	}
	appViewProperties := cloneSchemaProperties(appProperties)
	appViewProperties["effective_route_tag"] = oapiStringSchema()
	actionProperties := map[string]any{
		"id":                    oapiStringSchema(),
		"workspace_id":          oapiStringSchema(),
		"app_key":               oapiStringSchema(),
		"action_key":            oapiStringSchema(),
		"input_schema":          catalogSchema,
		"output_schema":         catalogSchema,
		"tag":                   nullableString,
		"tag_override":          nullableString,
		"timeout_s":             nullableInteger,
		"required_capabilities": stringArray,
		"updated_at":            oapiDateTimeSchema(),
	}
	appActionProperties := cloneSchemaProperties(actionProperties)
	appActionProperties["effective_capabilities"] = stringArray
	appActionProperties["effective_route_tag"] = oapiStringSchema()

	return map[string]any{
		"JSONSchema": map[string]any{
			"type":                 "object",
			"description":          "Materialized action input/output JSON Schema document. An empty object means unconstrained JSON.",
			"additionalProperties": true,
		},
		"Base64JSONSchema": map[string]any{
			"type":        "string",
			"format":      "byte",
			"description": "Base64-encoded materialized JSON Schema bytes, matching canonical Windforce catalog action JSON encoding.",
		},
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
				"name":         oapiStringSchema(),
				"repo_url":     oapiStringSchema(),
				"branch":       oapiStringSchema(),
				"subpath":      oapiStringSchema(),
				"creds_ref":    oapiStringSchema(),
				"auth_method":  oapiStringEnumSchema("none", "pat", "basic"),
				"access_token": oapiStringSchema(),
				"username":     oapiStringSchema(),
				"password":     oapiStringSchema(),
			},
			"required": []any{"name", "repo_url"},
		},
		"ProbeGitSourceRequest": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"repo_url":     oapiStringSchema(),
				"branch":       oapiStringSchema(),
				"auth_method":  oapiStringEnumSchema("none", "pat", "basic"),
				"access_token": oapiStringSchema(),
				"username":     oapiStringSchema(),
				"password":     oapiStringSchema(),
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
				"flows":   stringArray,
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
			"type":       "object",
			"properties": appProperties,
			"required":   []any{"id", "workspace_id", "app_key", "git_source_id", "commit_sha", "entrypoint", "timeout_s", "updated_at"},
		},
		"AppView": map[string]any{
			"type":        "object",
			"description": "App detail view returned by GET /apps/{app}, including server-computed routing fields.",
			"properties":  appViewProperties,
			"required":    []any{"id", "workspace_id", "app_key", "git_source_id", "commit_sha", "entrypoint", "timeout_s", "updated_at", "effective_route_tag"},
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
			"description": "Canonical action detail. input_schema and output_schema use the canonical catalog encoding: base64-encoded materialized JSON Schema bytes.",
			"properties":  actionProperties,
			"required":    []any{"id", "workspace_id", "app_key", "action_key", "input_schema", "output_schema", "updated_at"},
		},
		"ActionSchema": map[string]any{
			"type":        "object",
			"description": "Raw materialized JSON Schema documents for one action.",
			"properties": map[string]any{
				"workspace_id":  oapiStringSchema(),
				"app_key":       oapiStringSchema(),
				"action_key":    oapiStringSchema(),
				"input_schema":  jsonSchema,
				"output_schema": jsonSchema,
			},
			"required": []any{"workspace_id", "app_key", "action_key", "input_schema", "output_schema"},
		},
		"AppAction": map[string]any{
			"type":        "object",
			"description": "Action view returned inside app detail, including server-computed routing fields.",
			"properties":  appActionProperties,
			"required": []any{
				"id", "workspace_id", "app_key", "action_key", "input_schema", "output_schema", "updated_at",
				"effective_capabilities", "effective_route_tag",
			},
		},
		"AppDetailResponse": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"app":     oapiSchemaRef("AppView"),
				"actions": map[string]any{"type": "array", "items": oapiSchemaRef("AppAction")},
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
		"JSONValue": map[string]any{
			"description": "Any JSON value.",
		},
		"PathResponse": map[string]any{
			"type":       "object",
			"properties": map[string]any{"path": oapiStringSchema()},
			"required":   []any{"path"},
		},
		"Variable": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"app_key":     oapiStringSchema(),
				"path":        oapiStringSchema(),
				"value":       oapiStringSchema(),
				"is_secret":   oapiBooleanSchema(),
				"description": oapiStringSchema(),
			},
			"required": []any{"app_key", "path", "value", "is_secret", "description"},
		},
		"SetVariableRequest": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":        oapiStringSchema(),
				"value":       oapiStringSchema(),
				"description": oapiStringSchema(),
				"is_secret":   oapiBooleanSchema(),
				"app_key":     oapiStringSchema(),
			},
			"required": []any{"path"},
		},
		"VariableSetResponse": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    oapiStringSchema(),
				"app_key": oapiStringSchema(),
			},
			"required": []any{"path", "app_key"},
		},
		"VariableValueResponse": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":      oapiStringSchema(),
				"value":     oapiStringSchema(),
				"is_secret": oapiBooleanSchema(),
			},
			"required": []any{"path", "value", "is_secret"},
		},
		"SetResourceRequest": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":          oapiStringSchema(),
				"value":         oapiSchemaRef("JSONValue"),
				"resource_type": oapiStringSchema(),
				"description":   oapiStringSchema(),
			},
			"required": []any{"path"},
		},
		"JobInput": map[string]any{
			"type":                 "object",
			"description":          "Action input JSON object. The top-level __wf_enc key is reserved.",
			"additionalProperties": true,
		},
		"JobHandleResponse": map[string]any{
			"type":       "object",
			"properties": map[string]any{"job_id": oapiStringSchema()},
			"required":   []any{"job_id"},
		},
		"JobPendingResponse": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"job_id": oapiStringSchema(),
				"status": oapiStringSchema(),
			},
			"required": []any{"status"},
		},
		"JobWaitResultResponse": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"job_id": oapiStringSchema(),
				"status": oapiStringSchema(),
				"result": oapiSchemaRef("JSONValue"),
			},
			"required": []any{"job_id", "status", "result"},
		},
		"JobResultResponse": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status": oapiStringSchema(),
				"result": oapiSchemaRef("JSONValue"),
			},
			"required": []any{"status", "result"},
		},
		"JobStatus": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":              oapiStringSchema(),
				"workspace_id":    oapiStringSchema(),
				"state":           oapiStringSchema(),
				"status":          nullableString,
				"worker":          nullableString,
				"app_key":         nullableString,
				"action_key":      nullableString,
				"trigger_kind":    nullableString,
				"kind":            nullableString,
				"git_source_id":   nullableInteger,
				"commit_sha":      nullableString,
				"entrypoint":      nullableString,
				"input_schema":    jsonSchema,
				"output_schema":   jsonSchema,
				"input":           oapiSchemaRef("JSONValue"),
				"tag":             oapiStringSchema(),
				"timeout_s":       oapiIntegerSchema(),
				"created_by":      oapiStringSchema(),
				"permissioned_as": oapiStringSchema(),
				"created_at":      nullableDateTime,
				"started_at":      nullableDateTime,
				"completed_at":    nullableDateTime,
				"duration_ms":     oapiIntegerSchema(),
				"canceled_by":     nullableString,
				"canceled_reason": nullableString,
				"flow_run_id":     nullableString,
				"flow_key":        nullableString,
				"flow_step_key":   nullableString,
			},
			"required": []any{"id", "workspace_id", "state"},
		},
		"JobListItem": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":              oapiStringSchema(),
				"workspace_id":    oapiStringSchema(),
				"app_key":         oapiStringSchema(),
				"action_key":      oapiStringSchema(),
				"trigger_kind":    oapiStringSchema(),
				"status":          oapiStringSchema(),
				"queued":          oapiBooleanSchema(),
				"running":         oapiBooleanSchema(),
				"completed":       oapiBooleanSchema(),
				"created_at":      oapiDateTimeSchema(),
				"started_at":      nullableDateTime,
				"completed_at":    nullableDateTime,
				"duration_ms":     oapiIntegerSchema(),
				"worker":          nullableString,
				"git_source_id":   nullableInteger,
				"commit_sha":      nullableString,
				"entrypoint":      oapiStringSchema(),
				"tag":             oapiStringSchema(),
				"created_by":      oapiStringSchema(),
				"permissioned_as": oapiStringSchema(),
				"canceled_by":     nullableString,
				"canceled_reason": nullableString,
				"flow_run_id":     nullableString,
				"flow_step_id":    nullableString,
				"error_snippet":   nullableString,
			},
			"required": []any{"id", "workspace_id", "app_key", "action_key", "trigger_kind", "status", "queued", "running", "completed", "created_at"},
		},
		"JobListResponse": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"items": map[string]any{"type": "array", "items": oapiSchemaRef("JobListItem")},
				"pagination": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"limit":       oapiIntegerSchema(),
						"count":       oapiIntegerSchema(),
						"has_more":    oapiBooleanSchema(),
						"next_cursor": oapiStringSchema(),
					},
					"required": []any{"limit", "count", "has_more"},
				},
			},
			"required": []any{"items", "pagination"},
		},
		"JobSummaryCounts": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"queued_count":           oapiIntegerSchema(),
				"running_count":          oapiIntegerSchema(),
				"completed_count_recent": oapiIntegerSchema(),
				"failed_count_recent":    oapiIntegerSchema(),
				"canceled_count_recent":  oapiIntegerSchema(),
			},
		},
		"JobSummary": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"queued_count":           oapiIntegerSchema(),
				"running_count":          oapiIntegerSchema(),
				"completed_count_recent": oapiIntegerSchema(),
				"failed_count_recent":    oapiIntegerSchema(),
				"canceled_count_recent":  oapiIntegerSchema(),
				"oldest_queued_at":       nullableDateTime,
				"by_tag": map[string]any{"type": "array", "items": map[string]any{
					"allOf": []any{
						oapiSchemaRef("JobSummaryCounts"),
						map[string]any{"type": "object", "properties": map[string]any{"tag": oapiStringSchema()}},
					},
				}},
				"by_app": map[string]any{"type": "array", "items": map[string]any{
					"allOf": []any{
						oapiSchemaRef("JobSummaryCounts"),
						map[string]any{"type": "object", "properties": map[string]any{"app_key": oapiStringSchema()}},
					},
				}},
			},
		},
		"CancelJobRequest": map[string]any{
			"type":       "object",
			"properties": map[string]any{"reason": oapiStringSchema()},
		},
		"CancelResult": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"found":             oapiBooleanSchema(),
				"completed_now":     oapiBooleanSchema(),
				"soft_canceled":     oapiBooleanSchema(),
				"already_completed": oapiBooleanSchema(),
			},
			"required": []any{"found", "completed_now", "soft_canceled", "already_completed"},
		},
	}
}
