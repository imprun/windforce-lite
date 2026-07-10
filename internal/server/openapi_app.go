package server

import (
	"fmt"
	"sort"
	"strings"

	"github.com/imprun/windforce-lite/internal/contract"
)

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
			"responses": withErrors(map[string]any{
				"200": oapiResponse("Finished run — status \"completed\" or \"failed\"; result holds the output, or the failure detail when failed.", map[string]any{
					"type": "object",
					"properties": map[string]any{
						"status": oapiStatusSchema(),
						"result": map[string]any{},
					},
				}),
				"202": oapiResponse("Still running (or an unknown job_id).", oapiPendingSchema()),
			}, "401", "403"),
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
			"responses": appOpenAPIErrorResponses(),
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
