package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

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

func oapiTextResponse(description string) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			"text/plain": map[string]any{"schema": map[string]any{"type": "string"}},
		},
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

func cloneSchemaProperties(properties map[string]any) map[string]any {
	clone := make(map[string]any, len(properties)+2)
	for key, value := range properties {
		clone[key] = value
	}
	return clone
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
	"409": "Conflict",
	"413": "RequestEntityTooLarge",
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
		"BadRequest":            body("Malformed body, invalid app/action key, or a reserved input key."),
		"Unauthorized":          body("Missing or invalid API token."),
		"Forbidden":             body("Not a member of the workspace, or the workspace is suspended/offboarded."),
		"NotFound":              body("App or action not found."),
		"Conflict":              body("A conflicting operation or incompatible route state prevented the request."),
		"RequestEntityTooLarge": body("Request body exceeds the server limit."),
		"QuotaExceeded":         body("Workspace concurrency or daily-run quota reached."),
	}
}

func appOpenAPIErrorResponses() map[string]any {
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
