package runtime

import (
	"context"
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
	if err := os.WriteFile(filepath.Join(sourceDir, "action.go"), []byte(`package main

import (
	"os"
)

func main() {
	input, err := os.ReadFile(os.Getenv("WINDFORCE_INPUT_JSON"))
	if err != nil {
		os.Exit(2)
	}
	output := []byte(`+"`"+`{"app":"echo","action":"echo","input":`+"`"+` + string(input) + `+"`"+`}`+"`"+`)
	if err := os.WriteFile(os.Getenv("WINDFORCE_OUTPUT_JSON"), output, 0o644); err != nil {
		os.Exit(2)
	}
}
`), 0o644); err != nil {
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
		Action:    "echo",
		InputPath: inputPath,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d", result.ExitCode)
	}
	if string(result.Output) != `{"app":"echo","action":"echo","input":{"message":"hello"}}` {
		t.Fatalf("output = %s", result.Output)
	}
}
