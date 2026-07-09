package manifest

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseFillsActionName(t *testing.T) {
	app, err := Parse([]byte(`{
		"app": "echo",
		"entrypoint": "main.ts",
		"scriptLang": "typescript",
		"actions": {
			"run": {}
		}
	}`))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if app.Actions["run"].Action != "run" {
		t.Fatalf("action name = %q", app.Actions["run"].Action)
	}
}

func TestLoadMissingManifestUsesCanonicalMessage(t *testing.T) {
	_, err := Load(t.TempDir())
	if err == nil || err.Error() != "no windforce.json manifest at source root (subpath)" {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadWrapsParseErrorWithManifestName(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, FileName), []byte(`{not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), "parse windforce.json:") {
		t.Fatalf("Load error = %v, want parse windforce.json prefix", err)
	}
}

func TestParseAppliesCanonicalAppDefaults(t *testing.T) {
	app, err := Parse([]byte(`{
		"app": "echo",
		"entrypoint": "main.ts",
		"scriptLang": "typescript",
		"timeout": 120,
		"maxConcurrent": 2,
		"actions": {
			"run": {},
			"fast": {"timeout": 45}
		}
	}`))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if app.MaxConcurrent == nil || *app.MaxConcurrent != 2 {
		t.Fatalf("maxConcurrent = %v, want 2", app.MaxConcurrent)
	}
	run := app.Actions["run"]
	if run.Entrypoint != "main.ts" || run.Runtime != "typescript" || run.TimeoutMs != 120000 {
		t.Fatalf("run defaults = %#v", run)
	}
	fast := app.Actions["fast"]
	if fast.Entrypoint != "main.ts" || fast.Runtime != "typescript" || fast.TimeoutMs != 45000 {
		t.Fatalf("fast overrides = %#v", fast)
	}
}

func TestParseAppliesCanonicalDefaultTimeout(t *testing.T) {
	app, err := Parse([]byte(`{
		"app": "echo",
		"entrypoint": "main.ts",
		"actions": {
			"run": {}
		}
	}`))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if app.TimeoutS != 300 {
		t.Fatalf("app timeout = %d, want 300", app.TimeoutS)
	}
	if app.Tag != "default" {
		t.Fatalf("app tag = %q, want default", app.Tag)
	}
	run := app.Actions["run"]
	if run.TimeoutMs != 300000 {
		t.Fatalf("run timeout ms = %d, want 300000", run.TimeoutMs)
	}
}

func TestParseRejectsUnsupportedLiteScriptLang(t *testing.T) {
	for _, test := range []struct {
		name       string
		scriptLang string
		want       string
	}{
		{name: "go", scriptLang: "go", want: `app echo scriptLang "go" is not supported by windforce-lite`},
		{name: "whitespace", scriptLang: " typescript ", want: `app echo scriptLang " typescript " is not supported by windforce-lite`},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse([]byte(`{
				"app": "echo",
				"entrypoint": "main.go",
				"scriptLang": "` + test.scriptLang + `",
				"actions": {
					"run": {}
				}
			}`))
			if err == nil || err.Error() != test.want {
				t.Fatalf("Parse error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestParseDefaultTagDoesNotConflictWithActionCapabilities(t *testing.T) {
	app, err := Parse([]byte(`{
		"app": "echo",
		"entrypoint": "main.ts",
		"actions": {
			"run": {"capabilities": ["browser"]}
		}
	}`))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if app.Tag != "default" {
		t.Fatalf("app tag = %q, want default", app.Tag)
	}
	caps := app.Actions["run"].Capabilities
	if caps == nil || len(*caps) != 1 || (*caps)[0] != "browser" {
		t.Fatalf("action capabilities = %#v", caps)
	}
}

func TestParsePreservesCapabilities(t *testing.T) {
	app, err := Parse([]byte(`{
		"app": "echo",
		"entrypoint": "main.ts",
		"capabilities": ["browser", "browser"],
		"actions": {
			"run": {},
			"plain": {"capabilities": []}
		}
	}`))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if !reflect.DeepEqual(app.Capabilities, []string{"browser"}) {
		t.Fatalf("app capabilities = %#v", app.Capabilities)
	}
	if app.Actions["run"].Capabilities != nil {
		t.Fatalf("run capabilities = %#v, want nil inheritance", app.Actions["run"].Capabilities)
	}
	plain := app.Actions["plain"].Capabilities
	if plain == nil || len(*plain) != 0 {
		t.Fatalf("plain capabilities = %#v, want explicit empty override", plain)
	}
}

func TestParseRejectsCapabilityTagConflicts(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "app tag",
			body: `{"app":"echo","entrypoint":"main.ts","tag":"default","capabilities":["browser"],"actions":{"run":{}}}`,
			want: "declares both tag and capabilities",
		},
		{
			name: "action tag",
			body: `{"app":"echo","entrypoint":"main.ts","capabilities":["browser"],"actions":{"run":{"tag":"fast"}}}`,
			want: "declares both tag and capabilities",
		},
		{
			name: "app whitespace tag",
			body: `{"app":"echo","entrypoint":"main.ts","tag":" ","capabilities":["browser"],"actions":{"run":{}}}`,
			want: "declares both tag and capabilities",
		},
		{
			name: "action whitespace tag",
			body: `{"app":"echo","entrypoint":"main.ts","capabilities":["browser"],"actions":{"run":{"tag":" "}}}`,
			want: "declares both tag and capabilities",
		},
		{
			name: "unsupported",
			body: `{"app":"echo","entrypoint":"main.ts","capabilities":["gpu"],"actions":{"run":{}}}`,
			want: "unsupported capability",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse([]byte(test.body))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Parse error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestParseRejectsInvalidMaxConcurrent(t *testing.T) {
	_, err := Parse([]byte(`{
		"app": "echo",
		"entrypoint": "main.ts",
		"maxConcurrent": 0,
		"actions": {"run": {}}
	}`))
	if err == nil || !strings.Contains(err.Error(), "maxConcurrent must be positive") {
		t.Fatalf("Parse error = %v, want maxConcurrent validation", err)
	}
}

func TestParseIgnoresActionNameFieldAndUsesMapKey(t *testing.T) {
	app, err := Parse([]byte(`{
		"app": "echo",
		"entrypoint": "main.ts",
		"actions": {
			"run": { "action": "other" }
		}
	}`))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if app.Actions["run"].Action != "run" {
		t.Fatalf("action name = %q, want map key", app.Actions["run"].Action)
	}
}

func TestParseRejectsInvalidCanonicalKeys(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "app",
			body: `{"app":"Echo","entrypoint":"main.ts","actions":{"run":{}}}`,
			want: "invalid app key",
		},
		{
			name: "action",
			body: `{"app":"echo","entrypoint":"main.ts","actions":{"bad-action":{}}}`,
			want: "invalid action key",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse([]byte(test.body))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Parse error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestParseRejectsMissingEntrypoint(t *testing.T) {
	_, err := Parse([]byte(`{"app":"echo","actions":{"run":{}}}`))
	if err == nil || !strings.Contains(err.Error(), "has no entrypoint") {
		t.Fatalf("Parse error = %v, want missing entrypoint validation", err)
	}
}

func TestParseRejectsUnsupportedFlows(t *testing.T) {
	_, err := Parse([]byte(`{
		"app": "echo",
		"entrypoint": "main.ts",
		"actions": {
			"run": {}
		},
		"flows": {
			"main": {
				"steps": [
					{"key": "run", "action": "run"}
				]
			}
		}
	}`))
	if err == nil || !strings.Contains(err.Error(), "does not support flows") {
		t.Fatalf("Parse error = %v, want unsupported flows validation", err)
	}
}

func TestParseIgnoresNonCanonicalManifestFields(t *testing.T) {
	tagOverride := "operator-owned"
	app, err := Parse([]byte(`{
		"app": "echo",
		"entrypoint": "main.ts",
		"runtime": "legacy",
		"scriptLang": "typescript",
		"timeout": 120,
		"actions": {
			"run": {
				"action": "other",
				"entrypoint": "run.ts",
				"runtime": "go",
				"timeoutMs": 30000,
				"tagOverride": "operator-owned",
				"command": ["go", "run", "./main.go"],
				"adapter": {"type": "command", "command": ["adapter"]},
				"inputSchemaBody": {"type": "string"},
				"outputSchemaBody": {"type": "string"},
				"updatedAt": "2025-01-01T00:00:00Z"
			}
		}
	}`))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if app.Runtime != "" {
		t.Fatalf("app runtime = %q, want ignored", app.Runtime)
	}
	run := app.Actions["run"]
	if run.Action != "run" || run.Entrypoint != "main.ts" || run.Runtime != "typescript" || run.TimeoutMs != 120000 {
		t.Fatalf("run canonical fields = %#v", run)
	}
	if run.TagOverride != nil || len(run.Command) != 0 || run.Adapter != nil ||
		len(run.InputSchemaBody) != 0 || len(run.OutputSchemaBody) != 0 || run.UpdatedAt != nil {
		t.Fatalf("run non-canonical fields leaked = %#v; tagOverride input was %q", run, tagOverride)
	}
}

func TestParseRejectsEscapingEntrypoint(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "app entrypoint",
			body: `{"app":"echo","entrypoint":"../main.ts","actions":{"run":{}}}`,
			want: `app echo entrypoint "../main.ts" must be a relative path inside the app`,
		},
		{
			name: "absolute app entrypoint",
			body: `{"app":"echo","entrypoint":"/main.ts","actions":{"run":{}}}`,
			want: `app echo entrypoint "/main.ts" must be a relative path inside the app`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse([]byte(test.body))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Parse error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestParsePreservesSchemaPathsForMaterialization(t *testing.T) {
	app, err := Parse([]byte(`{
		"app": "echo",
		"entrypoint": "main.ts",
		"actions": {
			"run": {
				"inputSchema": "../input.json",
				"outputSchema": "/output.json"
			}
		}
	}`))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	run := app.Actions["run"]
	if run.InputSchema != "../input.json" || run.OutputSchema != "/output.json" {
		t.Fatalf("schema paths = input:%q output:%q", run.InputSchema, run.OutputSchema)
	}
}
