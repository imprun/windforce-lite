package worker

import (
	"encoding/json"
	"strings"

	"github.com/imprun/windforce-core/internal/contract"
)

func actionDeclaredFailure(result contract.JobResult) (string, bool) {
	if len(result.Output) == 0 {
		return "", false
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(result.Output, &payload); err != nil || payload == nil {
		return "", false
	}
	if message, failed := windforceDeclaredFailure(payload["$windforce"]); failed {
		return message, true
	}
	if !jsonStringEqual(payload["RESULT"], "FAIL") {
		return "", false
	}
	for _, key := range []string{"ECODE", "EMSG", "ERRMSG"} {
		if value := jsonString(payload[key]); value != "" {
			return value, true
		}
	}
	return "action returned RESULT=FAIL", true
}

func windforceDeclaredFailure(raw json.RawMessage) (string, bool) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil || payload == nil {
		return "", false
	}
	if !jsonStringEqual(payload["type"], "action_failure") && !jsonStringEqual(payload["status"], "failed") {
		return "", false
	}
	for _, key := range []string{"code", "message", "name"} {
		if value := jsonString(payload[key]); value != "" {
			return value, true
		}
	}
	return "action declared failure", true
}

func jsonStringEqual(raw json.RawMessage, expected string) bool {
	return strings.EqualFold(jsonString(raw), expected)
}

func jsonString(raw json.RawMessage) string {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}
