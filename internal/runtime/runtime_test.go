package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/imprun/windforce-lite/internal/bundle"
	"github.com/imprun/windforce-lite/internal/contract"
)

func TestRunnerFetchesBundleAndRunsAction(t *testing.T) {
	tempDir := t.TempDir()
	sourceDir := filepath.Join(tempDir, "source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "windforce.json"), []byte(`{"app":"echo","actions":{"echo":{"command":["helper"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	actionSource := `package main

import (
	"fmt"
	"os"
)

func main() {
	input, err := os.ReadFile(os.Getenv("WINDFORCE_INPUT_JSON"))
	if err != nil {
		os.Exit(2)
	}
	headers := []byte("{}")
	if path := os.Getenv("WINDFORCE_TRIGGER_HEADERS_JSON"); path != "" {
		headers, err = os.ReadFile(path)
		if err != nil {
			os.Exit(2)
		}
	}
	output := fmt.Sprintf("{\"app\":\"echo\",\"action\":\"echo\",\"input\":%s,\"headers\":%s}", string(input), string(headers))
	if err := os.WriteFile(os.Getenv("WINDFORCE_OUTPUT_JSON"), []byte(output), 0o644); err != nil {
		os.Exit(2)
	}
}
`
	if err := os.WriteFile(filepath.Join(sourceDir, "action.go"), []byte(actionSource), 0o644); err != nil {
		t.Fatal(err)
	}
	inputPath := filepath.Join(tempDir, "input.json")
	if err := os.WriteFile(inputPath, []byte(`{"message":"hello"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := bundle.NewLocalStore(filepath.Join(tempDir, "store"))
	if err := store.Materialize(context.Background(), "workspace-a", "source-a", "commit-a", sourceDir); err != nil {
		t.Fatal(err)
	}

	runner := Runner{Store: store, CacheRoot: filepath.Join(tempDir, "cache")}
	result, err := runner.Run(context.Background(), RunRequest{
		Deployment: contract.Deployment{
			Workspace:   "workspace-a",
			GitSourceID: "source-a",
			App:         "echo",
			Commit:      "commit-a",
			Actions: map[string]contract.Action{
				"echo": {
					Action:  "echo",
					Command: []string{"go", "run", "./action.go"},
				},
			},
		},
		Action:         "echo",
		InputPath:      inputPath,
		TriggerHeaders: json.RawMessage(`{"X-Hub-Signature-256":"sha256=abc"}`),
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d", result.ExitCode)
	}
	var output struct {
		App     string            `json:"app"`
		Action  string            `json:"action"`
		Input   map[string]string `json:"input"`
		Headers map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	if output.App != "echo" || output.Action != "echo" || output.Input["message"] != "hello" ||
		output.Headers["X-Hub-Signature-256"] != "sha256=abc" {
		t.Fatalf("output = %#v", output)
	}
}

func TestRunnerRunsActionThroughCommandAdapter(t *testing.T) {
	tempDir := t.TempDir()
	sourceDir := filepath.Join(tempDir, "source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "windforce.json"), []byte(`{"app":"echo","actions":{"echo":{"command":["legacy","script"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	store := bundle.NewLocalStore(filepath.Join(tempDir, "store"))
	if err := store.Materialize(context.Background(), "workspace-a", "source-a", "commit-a", sourceDir); err != nil {
		t.Fatal(err)
	}

	runner := Runner{Store: store, CacheRoot: filepath.Join(tempDir, "cache")}
	result, err := runner.Run(context.Background(), RunRequest{
		Deployment: contract.Deployment{
			Workspace:   "workspace-a",
			GitSourceID: "source-a",
			App:         "echo",
			Commit:      "commit-a",
			Actions: map[string]contract.Action{
				"echo": {
					Action:  "echo",
					Command: []string{"legacy", "script"},
					Adapter: &contract.ActionAdapter{
						Type:    contract.ActionAdapterCommand,
						Command: []string{os.Args[0], "-test.run=TestRuntimeActionAdapterHelperProcess", "--"},
						Env:     []string{"WINDFORCE_RUNTIME_ADAPTER_HELPER=1"},
						Options: map[string]json.RawMessage{"mode": json.RawMessage(`"compat"`)},
					},
				},
			},
		},
		Action:         "echo",
		Input:          json.RawMessage(`{"message":"hello"}`),
		TriggerHeaders: json.RawMessage(`{"X-Hub-Signature-256":"sha256=abc"}`),
		Env:            []string{"SCRIPT_ENV=1"},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d", result.ExitCode)
	}
	var output struct {
		Adapter string            `json:"adapter"`
		Command []string          `json:"command"`
		Env     []string          `json:"env"`
		Headers map[string]string `json:"headers"`
		Input   map[string]string `json:"input"`
		Option  string            `json:"option"`
		Version string            `json:"version"`
	}
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	if output.Adapter != "command" || output.Version != actionAdapterProtocolVersion || output.Option != "compat" {
		t.Fatalf("output = %#v", output)
	}
	if len(output.Command) != 2 || output.Command[0] != "legacy" || output.Command[1] != "script" {
		t.Fatalf("command = %#v", output.Command)
	}
	if !containsEnv(output.Env, "SCRIPT_ENV=1") || !containsEnvPrefix(output.Env, "WINDFORCE_TRIGGER_HEADERS_JSON=") {
		t.Fatalf("env = %#v", output.Env)
	}
	if output.Input["message"] != "hello" {
		t.Fatalf("input = %#v", output.Input)
	}
	if output.Headers["X-Hub-Signature-256"] != "sha256=abc" {
		t.Fatalf("headers = %#v", output.Headers)
	}
}

func TestRuntimeActionAdapterHelperProcess(t *testing.T) {
	if os.Getenv("WINDFORCE_RUNTIME_ADAPTER_HELPER") != "1" {
		return
	}
	var request struct {
		Version    string                     `json:"version"`
		Command    []string                   `json:"command"`
		InputPath  string                     `json:"inputPath"`
		OutputPath string                     `json:"outputPath"`
		Env        []string                   `json:"env"`
		Headers    map[string]string          `json:"triggerHeaders"`
		Options    map[string]json.RawMessage `json:"options"`
	}
	requestBytes, err := os.ReadFile(os.Getenv("WINDFORCE_ADAPTER_REQUEST_JSON"))
	if err != nil {
		os.Exit(2)
	}
	if err := json.Unmarshal(requestBytes, &request); err != nil {
		os.Exit(2)
	}
	inputBytes, err := os.ReadFile(request.InputPath)
	if err != nil {
		os.Exit(3)
	}
	var input map[string]string
	if err := json.Unmarshal(inputBytes, &input); err != nil {
		os.Exit(3)
	}
	var option string
	if err := json.Unmarshal(request.Options["mode"], &option); err != nil {
		os.Exit(4)
	}
	outputBytes, err := json.Marshal(map[string]any{
		"adapter": "command",
		"command": request.Command,
		"env":     request.Env,
		"headers": request.Headers,
		"input":   input,
		"option":  option,
		"version": request.Version,
	})
	if err != nil {
		os.Exit(5)
	}
	if err := os.WriteFile(request.OutputPath, outputBytes, 0o644); err != nil {
		os.Exit(5)
	}
	resultBytes, err := json.Marshal(map[string]any{"exitCode": 0, "stdout": "adapter ok", "durationMs": 1})
	if err != nil {
		os.Exit(6)
	}
	if err := os.WriteFile(os.Getenv("WINDFORCE_ADAPTER_RESULT_JSON"), resultBytes, 0o644); err != nil {
		os.Exit(6)
	}
	os.Exit(0)
}

func containsEnv(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsEnvPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if len(value) >= len(prefix) && value[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}
