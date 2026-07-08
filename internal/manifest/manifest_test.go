package manifest

import "testing"

func TestParseFillsActionName(t *testing.T) {
	app, err := Parse([]byte(`{
		"app": "echo",
		"actions": {
			"run": {
				"runtime": "go",
				"command": ["go", "run", "./action.go"]
			}
		}
	}`))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if app.Actions["run"].Action != "run" {
		t.Fatalf("action name = %q", app.Actions["run"].Action)
	}
}

func TestParseRejectsMismatchedActionName(t *testing.T) {
	_, err := Parse([]byte(`{
		"app": "echo",
		"actions": {
			"run": { "action": "other" }
		}
	}`))
	if err == nil {
		t.Fatalf("expected error")
	}
}
