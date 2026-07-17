package runtime

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/imprun/windforce-core/internal/bundle"
	"github.com/imprun/windforce-core/internal/contract"
	"github.com/imprun/windforce-core/internal/executionbundle"
	"github.com/imprun/windforce-core/internal/token"
)

func TestRunnerFetchesBundleAndRunsAction(t *testing.T) {
	requirePythonRuntime(t)
	tempDir := t.TempDir()
	sourceDir := filepath.Join(tempDir, "source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "windforce.json"), []byte(`{"app":"echo","entrypoint":"main.py","scriptLang":"python","actions":{"echo":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	actionSource := `def main(ctx):
    ctx.logger.info("canonical stdout", ctx.app, ctx.action)
    return {
        "app": ctx.app,
        "action": ctx.action,
        "input": ctx.input,
        "headers": ctx.trigger.headers,
        "job": {
            "id": ctx.job.id,
            "workspace": ctx.job.workspace,
            "tag": ctx.job.tag,
        },
    }
`
	if err := os.WriteFile(filepath.Join(sourceDir, "main.py"), []byte(actionSource), 0o644); err != nil {
		t.Fatal(err)
	}
	inputPath := filepath.Join(tempDir, "input.json")
	if err := os.WriteFile(inputPath, []byte(`{"message":"hello"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(tempDir, "output.json")

	store := bundle.NewLocalStore(filepath.Join(tempDir, "store"))
	if err := store.Materialize(context.Background(), "workspace-a", "source-a", "commit-a", sourceDir); err != nil {
		t.Fatal(err)
	}

	runner := Runner{
		Store:         store,
		ArtifactStore: executionbundle.NewLocalStore(filepath.Join(tempDir, "store")),
		CacheRoot:     filepath.Join(tempDir, "cache"),
	}
	deployment := buildExecutionBundleForTest(t, &runner, contract.Deployment{
		Workspace:   "workspace-a",
		GitSourceID: "source-a",
		App:         "echo",
		Commit:      "commit-a",
		Entrypoint:  "main.py",
		ScriptLang:  "python",
		ObjectURI:   "bundle://workspace-a/source-a/commit-a",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo"},
		},
	})
	if deployment.ObjectURI != "bundle://workspace-a/source-a/commit-a" || deployment.BundleURI == "" {
		t.Fatalf("source URI = %q, execution bundle URI = %q", deployment.ObjectURI, deployment.BundleURI)
	}
	result, err := runner.Run(context.Background(), RunRequest{
		Deployment:     deployment,
		JobID:          "job-a",
		WorkspaceID:    "workspace-a",
		Action:         "echo",
		InputPath:      inputPath,
		OutputPath:     outputPath,
		TriggerKind:    "webhook",
		TriggerHeaders: json.RawMessage(`{"X-Hub-Signature-256":"sha256=abc"}`),
		Tag:            "default",
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
		Job     map[string]string `json:"job"`
	}
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	if output.App != "echo" || output.Action != "echo" || output.Input["message"] != "hello" ||
		output.Headers["X-Hub-Signature-256"] != "sha256=abc" || output.Job["id"] != "job-a" ||
		output.Job["workspace"] != "workspace-a" || output.Job["tag"] != "default" {
		t.Fatalf("output = %#v", output)
	}
	if result.Stdout == "" {
		t.Fatalf("stdout was not captured")
	}
	outputFile, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("output file was not written: %v", err)
	}
	var fileOutput struct {
		App    string            `json:"app"`
		Input  map[string]string `json:"input"`
		Header map[string]string `json:"headers"`
	}
	if err := json.Unmarshal(outputFile, &fileOutput); err != nil {
		t.Fatalf("output file is not JSON: %v", err)
	}
	if fileOutput.App != "echo" || fileOutput.Input["message"] != "hello" ||
		fileOutput.Header["X-Hub-Signature-256"] != "sha256=abc" {
		t.Fatalf("output file = %s", outputFile)
	}

	if err := os.WriteFile(inputPath, []byte(`{`), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err = runner.Run(context.Background(), RunRequest{
		Deployment:  deployment,
		JobID:       "job-b",
		WorkspaceID: "workspace-a",
		Action:      "echo",
		InputPath:   inputPath,
		TriggerKind: "api",
		Tag:         "default",
	})
	if err != nil {
		t.Fatalf("Run with invalid input returned error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("invalid input exit code = %d, output=%s, stderr=%s", result.ExitCode, result.Output, result.Stderr)
	}
	var fallbackOutput struct {
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(result.Output, &fallbackOutput); err != nil {
		t.Fatalf("fallback output is not JSON: %v", err)
	}
	if len(fallbackOutput.Input) != 0 {
		t.Fatalf("fallback input = %#v, want empty object", fallbackOutput.Input)
	}
}

func TestBuildExecutionBundleSupportsRelativeCacheRoot(t *testing.T) {
	requirePythonRuntime(t)
	testRoot, err := os.MkdirTemp(".", ".relative-execution-bundle-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testRoot)

	sourceDir := filepath.Join(testRoot, "source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "main.py"), []byte("def main(ctx): return ctx.input\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := bundle.NewLocalStore(filepath.Join(testRoot, "source-store"))
	if err := store.Materialize(context.Background(), "workspace-a", "source-a", "commit-a", sourceDir); err != nil {
		t.Fatal(err)
	}
	runner := Runner{
		Store:         store,
		ArtifactStore: executionbundle.NewLocalStore(filepath.Join(testRoot, "artifact-store")),
		CacheRoot:     filepath.Join(testRoot, "cache"),
	}
	deployment, err := runner.BuildExecutionBundle(context.Background(), contract.Deployment{
		Workspace:   "workspace-a",
		GitSourceID: "source-a",
		App:         "echo",
		Commit:      "commit-a",
		Entrypoint:  "main.py",
		ScriptLang:  "python",
		ObjectURI:   "bundle://workspace-a/source-a/commit-a",
		Actions:     map[string]contract.Action{"echo": {Action: "echo"}},
	})
	if err != nil {
		t.Fatalf("BuildExecutionBundle returned error: %v", err)
	}
	if deployment.BundleDigest == "" || deployment.BundleURI == "" || deployment.ObjectURI != "bundle://workspace-a/source-a/commit-a" {
		t.Fatalf("deployment bundle fields = %#v", deployment)
	}
}

func TestRunnerInjectsPythonSDK(t *testing.T) {
	requirePythonRuntime(t)
	tempDir := t.TempDir()
	sourceDir := filepath.Join(tempDir, "source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "windforce.json"), []byte(`{"app":"echo","entrypoint":"main.py","scriptLang":"python","actions":{"echo":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	actionSource := `from windforce_client import create_app


async def echo(ctx):
    return {
        "via": "windforce_client",
        "app": ctx.app,
        "action": ctx.action,
        "input": ctx.input,
        "job": {
            "id": ctx.job.id,
            "workspace": ctx.job.workspace,
        },
    }


main = create_app(actions={"echo": echo})
`
	if err := os.WriteFile(filepath.Join(sourceDir, "main.py"), []byte(actionSource), 0o644); err != nil {
		t.Fatal(err)
	}

	store := bundle.NewLocalStore(filepath.Join(tempDir, "store"))
	if err := store.Materialize(context.Background(), "workspace-a", "source-a", "commit-py", sourceDir); err != nil {
		t.Fatal(err)
	}

	cacheRoot := filepath.Join(tempDir, "cache")
	runner := Runner{
		Store:         store,
		ArtifactStore: executionbundle.NewLocalStore(filepath.Join(tempDir, "store")),
		CacheRoot:     cacheRoot,
	}
	deployment := contract.Deployment{
		Workspace:   "workspace-a",
		GitSourceID: "source-a",
		App:         "echo",
		Commit:      "commit-py",
		Entrypoint:  "main.py",
		ScriptLang:  "python",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo"},
		},
	}
	deployment = buildExecutionBundleForTest(t, &runner, deployment)
	preparedDir := filepath.Join(cacheRoot, "src", "workspace-a", "source-a", "commit-py")
	if err := os.RemoveAll(preparedDir); err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), RunRequest{
		Deployment:  deployment,
		JobID:       "job-py",
		WorkspaceID: "workspace-a",
		Action:      "echo",
		Input:       json.RawMessage(`{"message":"hello"}`),
		Tag:         "default",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if _, err := os.Stat(preparedDir); !os.IsNotExist(err) {
		t.Fatalf("Run touched the source preparation cache: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d, stdout=%s, error=%s", result.ExitCode, result.Stdout, result.Error)
	}
	var output struct {
		Via    string            `json:"via"`
		App    string            `json:"app"`
		Action string            `json:"action"`
		Input  map[string]string `json:"input"`
		Job    map[string]string `json:"job"`
	}
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	if output.Via != "windforce_client" || output.App != "echo" || output.Action != "echo" ||
		output.Input["message"] != "hello" || output.Job["id"] != "job-py" ||
		output.Job["workspace"] != "workspace-a" {
		t.Fatalf("output = %#v", output)
	}
	injected := filepath.Join(cacheRoot, "execution-bundles", safePath(deployment.BundleDigest), ".windforce", "site-packages", "windforce_client", "__init__.py")
	if _, err := os.Stat(injected); err != nil {
		t.Fatalf("injected SDK missing: %v", err)
	}
}

func TestRunnerInjectsTypeScriptSDK(t *testing.T) {
	requireBunRuntime(t)
	tempDir := t.TempDir()
	sourceDir := filepath.Join(tempDir, "source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "windforce.json"), []byte(`{"app":"echo","entrypoint":"main.ts","scriptLang":"typescript","actions":{"echo":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	actionSource := `import { createApp } from "windforce-client"

export const main = createApp({
  actions: {
    echo: async (ctx) => ({
      app: ctx.app,
      action: ctx.action,
      input: ctx.input,
      job: ctx.job,
    }),
  },
})
`
	if err := os.WriteFile(filepath.Join(sourceDir, "main.ts"), []byte(actionSource), 0o644); err != nil {
		t.Fatal(err)
	}

	store := bundle.NewLocalStore(filepath.Join(tempDir, "store"))
	if err := store.Materialize(context.Background(), "workspace-a", "source-a", "commit-ts", sourceDir); err != nil {
		t.Fatal(err)
	}

	cacheRoot := filepath.Join(tempDir, "cache")
	runner := Runner{
		Store:         store,
		ArtifactStore: executionbundle.NewLocalStore(filepath.Join(tempDir, "store")),
		CacheRoot:     cacheRoot,
	}
	deployment := buildExecutionBundleForTest(t, &runner, contract.Deployment{
		Workspace:   "workspace-a",
		GitSourceID: "source-a",
		App:         "echo",
		Commit:      "commit-ts",
		Entrypoint:  "main.ts",
		ScriptLang:  "typescript",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo"},
		},
	})
	result, err := runner.Run(context.Background(), RunRequest{
		Deployment:  deployment,
		JobID:       "job-ts",
		WorkspaceID: "workspace-a",
		Action:      "echo",
		Input:       json.RawMessage(`{"message":"hello"}`),
		Tag:         "default",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d, stdout=%s, error=%s", result.ExitCode, result.Stdout, result.Error)
	}
	var output struct {
		App    string            `json:"app"`
		Action string            `json:"action"`
		Input  map[string]string `json:"input"`
		Job    map[string]string `json:"job"`
	}
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	if output.App != "echo" || output.Action != "echo" || output.Input["message"] != "hello" ||
		output.Job["id"] != "job-ts" || output.Job["workspace"] != "workspace-a" {
		t.Fatalf("output = %#v", output)
	}
	injected := filepath.Join(cacheRoot, "execution-bundles", safePath(deployment.BundleDigest), "node_modules", "windforce-client", "package.json")
	if _, err := os.Stat(injected); err != nil {
		t.Fatalf("injected SDK missing: %v", err)
	}
}

func TestPrepareSourceDefaultsUnknownScriptLangToTypeScriptSDK(t *testing.T) {
	sourceDir := t.TempDir()
	runner := Runner{}
	if err := runner.prepareSource(context.Background(), sourceDir, "ruby", "main.rb"); err != nil {
		t.Fatalf("prepareSource returned error: %v", err)
	}
	injected := filepath.Join(sourceDir, "node_modules", "windforce-client", "package.json")
	if _, err := os.Stat(injected); err != nil {
		t.Fatalf("injected SDK missing: %v", err)
	}
}

func TestRunnerBuildsAndRunsGoApp(t *testing.T) {
	requireGoRuntime(t)
	tempDir := t.TempDir()
	sourceDir := filepath.Join(tempDir, "source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "windforce.json"), []byte(`{"app":"echo","entrypoint":"main.go","scriptLang":"go","actions":{"echo":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "go.mod"), []byte("module example.com/echo\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	actionSource := `package main

import wf "windforce-client"

var Main = wf.CreateApp(wf.App{Actions: wf.Actions{
	"echo": func(ctx *wf.Context) (any, error) {
		return map[string]any{
			"app": ctx.App,
			"action": ctx.Action,
			"input": ctx.Input,
			"job": map[string]string{
				"id": ctx.Job.ID,
				"workspace": ctx.Job.Workspace,
				"tag": ctx.Job.Tag,
			},
		}, nil
	},
}})
`
	if err := os.WriteFile(filepath.Join(sourceDir, "main.go"), []byte(actionSource), 0o644); err != nil {
		t.Fatal(err)
	}

	store := bundle.NewLocalStore(filepath.Join(tempDir, "store"))
	if err := store.Materialize(context.Background(), "workspace-a", "source-a", "commit-go", sourceDir); err != nil {
		t.Fatal(err)
	}

	cacheRoot := filepath.Join(tempDir, "cache")
	runner := Runner{
		Store:         store,
		ArtifactStore: executionbundle.NewLocalStore(filepath.Join(tempDir, "store")),
		CacheRoot:     cacheRoot,
	}
	deployment := buildExecutionBundleForTest(t, &runner, contract.Deployment{
		Workspace:   "workspace-a",
		GitSourceID: "source-a",
		App:         "echo",
		Commit:      "commit-go",
		Entrypoint:  "main.go",
		ScriptLang:  "go",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo"},
		},
	})
	result, err := runner.Run(context.Background(), RunRequest{
		Deployment:  deployment,
		JobID:       "job-go",
		WorkspaceID: "workspace-a",
		Action:      "echo",
		Input:       json.RawMessage(`{"message":"hello"}`),
		Tag:         "default",
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d, stdout=%s, error=%s", result.ExitCode, result.Stdout, result.Error)
	}
	var output struct {
		App    string            `json:"app"`
		Action string            `json:"action"`
		Input  map[string]any    `json:"input"`
		Job    map[string]string `json:"job"`
	}
	if err := json.Unmarshal(result.Output, &output); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	if output.App != "echo" || output.Action != "echo" || output.Input["message"] != "hello" ||
		output.Job["id"] != "job-go" || output.Job["workspace"] != "workspace-a" ||
		output.Job["tag"] != "default" {
		t.Fatalf("output = %#v", output)
	}
	binaryPath := filepath.Join(cacheRoot, "execution-bundles", safePath(deployment.BundleDigest), filepath.FromSlash(goBinaryRel()))
	if _, err := os.Stat(binaryPath); err != nil {
		t.Fatalf("go binary missing: %v", err)
	}
}

func TestRunnerReusesReadyPreparedSource(t *testing.T) {
	tempDir := t.TempDir()
	sourceDir := filepath.Join(tempDir, "source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "windforce.json"), []byte(`{"app":"echo","entrypoint":"main.ts","scriptLang":"typescript","actions":{"echo":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "main.ts"), []byte(`export async function main(ctx) { return ctx.input }`), 0o644); err != nil {
		t.Fatal(err)
	}
	store := bundle.NewLocalStore(filepath.Join(tempDir, "store"))
	if err := store.Materialize(context.Background(), "workspace-a", "source-a", "commit-ready", sourceDir); err != nil {
		t.Fatal(err)
	}

	cacheRoot := filepath.Join(tempDir, "cache")
	runner := Runner{
		Store:         store,
		ArtifactStore: executionbundle.NewLocalStore(filepath.Join(tempDir, "store")),
		CacheRoot:     cacheRoot,
	}
	req := RunRequest{
		Deployment: contract.Deployment{
			Workspace:   "workspace-a",
			GitSourceID: "source-a",
			App:         "echo",
			Commit:      "commit-ready",
			Entrypoint:  "main.ts",
			ScriptLang:  "typescript",
			Actions: map[string]contract.Action{
				"echo": {
					Action:  "echo",
					Command: []string{"legacy", "script"},
					Adapter: &contract.ActionAdapter{
						Type:    contract.ActionAdapterCommand,
						Command: []string{os.Args[0], "-test.run=TestRuntimeActionAdapterHelperProcess", "--"},
						Env:     []string{"WINDFORCE_RUNTIME_ADAPTER_HELPER=1"},
						Options: map[string]json.RawMessage{"mode": json.RawMessage(`"cache"`)},
					},
				},
			},
		},
		Action: "echo",
		Input:  json.RawMessage(`{"message":"hello"}`),
	}
	req.Deployment = buildExecutionBundleForTest(t, &runner, req.Deployment)
	if _, err := runner.Run(context.Background(), req); err != nil {
		t.Fatalf("first Run returned error: %v", err)
	}
	preparedDir := filepath.Join(cacheRoot, "execution-bundles", safePath(req.Deployment.BundleDigest))
	if _, err := os.Stat(filepath.Join(preparedDir, executionBundleReadyFile)); err != nil {
		t.Fatalf("execution bundle marker missing: %v", err)
	}
	sentinel := filepath.Join(preparedDir, "prepare-sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(context.Background(), req); err != nil {
		t.Fatalf("second Run returned error: %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("prepared source was fetched again despite ready marker: %v", err)
	}
}

func TestRunnerRepreparesSourceWhenReadyMarkerChanges(t *testing.T) {
	tempDir := t.TempDir()
	sourceDir := filepath.Join(tempDir, "source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "windforce.json"), []byte(`{"app":"echo","entrypoint":"main.ts","scriptLang":"typescript","actions":{"echo":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "main.ts"), []byte(`export async function main(ctx) { return ctx.input }`), 0o644); err != nil {
		t.Fatal(err)
	}
	store := bundle.NewLocalStore(filepath.Join(tempDir, "store"))
	if err := store.Materialize(context.Background(), "workspace-a", "source-a", "commit-stale", sourceDir); err != nil {
		t.Fatal(err)
	}

	cacheRoot := filepath.Join(tempDir, "cache")
	runner := Runner{Store: store, CacheRoot: cacheRoot}
	preparedDir, err := runner.ensureSource(context.Background(), "workspace-a", "source-a", "commit-stale", "typescript", "main.ts")
	if err != nil {
		t.Fatal(err)
	}
	readyPath := filepath.Join(preparedDir, sourceReadyFile)
	if err := os.WriteFile(readyPath, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(preparedDir, "stale-runtime-artifact")
	if err := os.WriteFile(sentinel, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := runner.ensureSource(context.Background(), "workspace-a", "source-a", "commit-stale", "typescript", "main.ts"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("stale prepared source artifact stat error = %v, want not exist", err)
	}
}

func TestSourceReadyValueIncludesPythonABI(t *testing.T) {
	requirePythonRuntime(t)
	value, err := sourceReadyValue(context.Background(), "python", defaultPythonPath(), "bun", "go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(value, `"version":"prepare-v3"`) ||
		!strings.Contains(value, `"language":"python"`) ||
		!strings.Contains(value, `"runtime":"cpython-`) {
		t.Fatalf("sourceReadyValue() = %q, want CPython ABI tag", value)
	}
}

func TestRunnerDoesNotMarkFailedPrepareReady(t *testing.T) {
	tempDir := t.TempDir()
	sourceDir := filepath.Join(tempDir, "source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "windforce.json"), []byte(`{"app":"echo","entrypoint":"main.py","scriptLang":"python","actions":{"echo":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "main.py"), []byte(`def main(ctx): return ctx.input`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "requirements.txt"), []byte("definitely-missing-package-for-windforce-core-test==0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := bundle.NewLocalStore(filepath.Join(tempDir, "store"))
	if err := store.Materialize(context.Background(), "workspace-a", "source-a", "commit-fail", sourceDir); err != nil {
		t.Fatal(err)
	}

	cacheRoot := filepath.Join(tempDir, "cache")
	runner := Runner{
		Store:         store,
		ArtifactStore: executionbundle.NewLocalStore(filepath.Join(tempDir, "store")),
		CacheRoot:     cacheRoot,
		PythonPath:    filepath.Join(tempDir, "missing-python"),
	}
	_, err := runner.BuildExecutionBundle(context.Background(), contract.Deployment{
		Workspace:   "workspace-a",
		GitSourceID: "source-a",
		App:         "echo",
		Commit:      "commit-fail",
		Entrypoint:  "main.py",
		ScriptLang:  "python",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo"},
		},
	})
	if err == nil {
		t.Fatalf("BuildExecutionBundle unexpectedly succeeded")
	}
	readyPath := filepath.Join(cacheRoot, "src", "workspace-a", "source-a", "commit-fail", sourceReadyFile)
	if _, statErr := os.Stat(readyPath); !os.IsNotExist(statErr) {
		t.Fatalf("ready marker after failed prepare stat err = %v, want not exist", statErr)
	}
}

func TestPreparePythonPyprojectInstallsProject(t *testing.T) {
	tempDir := t.TempDir()
	sourceDir := filepath.Join(tempDir, "source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "pyproject.toml"), []byte(`[project]
name = "windforce-core-pyproject-test"
version = "0.0.0"
dependencies = []
`), 0o644); err != nil {
		t.Fatal(err)
	}

	runner := Runner{PythonPath: filepath.Join(tempDir, "missing-python")}
	err := runner.prepareSource(context.Background(), sourceDir, "python", "main.py")
	if err == nil || !strings.Contains(err.Error(), "pip install project") {
		t.Fatalf("prepareSource error = %v, want project install attempt", err)
	}
}

func TestAppendPreparedSourceEnvIncludesPythonSourceRoot(t *testing.T) {
	sourceDir := filepath.Join("cache", "src", "workspace", "source", "commit")
	env := appendPreparedSourceEnv([]string{"PATH=/bin"}, sourceDir, "python")
	if !containsEnv(env, "WF_PY_VENDOR="+filepath.Join(sourceDir, pyVendorDir)) {
		t.Fatalf("prepared env missing WF_PY_VENDOR: %#v", env)
	}
	if !containsEnv(env, "WF_PY_SOURCE_ROOT="+sourceDir) {
		t.Fatalf("prepared env missing WF_PY_SOURCE_ROOT: %#v", env)
	}
}

func TestRunnerJobEnvIncludesSDKCallbackEndpoint(t *testing.T) {
	runner := Runner{
		BaseURL:        "http://127.0.0.1:18080",
		JobTokenSecret: "token-secret",
	}
	env := runner.jobEnv(RunRequest{
		JobID:       "job-a",
		WorkspaceID: "ws-a",
		Deployment: contract.Deployment{
			App:        "echo",
			Entrypoint: "main.py",
			Actions: map[string]contract.Action{
				"run": {Action: "run"},
			},
		},
		Action:          "run",
		TriggerKind:     "api",
		CreatedBy:       "runner@example.test",
		PermissionedAs:  "delegate@example.test",
		WorkerGroup:     "test",
		EgressProxyAddr: "proxy:18080",
	}, contract.Action{Action: "run"})

	for _, want := range []string{
		"WF_BASE_URL=http://127.0.0.1:18080",
		"WF_STATE_PATH=echo/run",
		"WF_RUNNABLE_PATH=",
		"WF_EMAIL=runner@example.test",
		"WF_USERNAME=runner@example.test",
		"WF_PERMISSIONED_AS=delegate@example.test",
		"WF_WORKER_GROUP=test",
		"WF_PROXY_URL=http://job-job-a@proxy:18080",
		"HTTP_PROXY=http://job-job-a@proxy:18080",
		"HTTPS_PROXY=http://job-job-a@proxy:18080",
	} {
		if !containsEnv(env, want) {
			t.Fatalf("env missing %q in %#v", want, env)
		}
	}
	tokenValue := envValue(env, "WF_TOKEN")
	claims, ok := token.VerifyJob("token-secret", tokenValue)
	if !ok {
		t.Fatalf("WF_TOKEN is not a valid job token: %q", tokenValue)
	}
	if claims.Workspace != "ws-a" || claims.JobID != "job-a" || claims.Subject != "delegate@example.test" {
		t.Fatalf("job token claims = %#v", claims)
	}
	if got := envValue(env, "WF_RUNNABLE_PATH"); got != "" {
		t.Fatalf("WF_RUNNABLE_PATH = %q, want empty for catalog action jobs", got)
	}

	emptyRunner := Runner{}
	emptyCallbackEnv := emptyRunner.jobEnv(RunRequest{
		JobID:       "job-b",
		WorkspaceID: "ws-a",
		Deployment: contract.Deployment{
			App:        "echo",
			Entrypoint: "main.py",
		},
		Action: "run",
	}, contract.Action{Action: "run"})
	for _, want := range []string{
		"WF_BASE_URL=",
		"WF_TOKEN=",
	} {
		if !containsEnv(emptyCallbackEnv, want) {
			t.Fatalf("empty callback env missing %q in %#v", want, emptyCallbackEnv)
		}
	}
}

func TestRunnerRunsActionThroughCommandAdapter(t *testing.T) {
	tempDir := t.TempDir()
	sourceDir := filepath.Join(tempDir, "source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "windforce.json"), []byte(`{"app":"echo","entrypoint":"main.ts","actions":{"echo":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	store := bundle.NewLocalStore(filepath.Join(tempDir, "store"))
	if err := store.Materialize(context.Background(), "workspace-a", "source-a", "commit-a", sourceDir); err != nil {
		t.Fatal(err)
	}

	runner := Runner{
		Store:         store,
		ArtifactStore: executionbundle.NewLocalStore(filepath.Join(tempDir, "store")),
		CacheRoot:     filepath.Join(tempDir, "cache"),
	}
	deployment := buildExecutionBundleForTest(t, &runner, contract.Deployment{
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
	})
	result, err := runner.Run(context.Background(), RunRequest{
		Deployment:     deployment,
		Action:         "echo",
		Input:          json.RawMessage(`{"message":"hello"}`),
		TriggerHeaders: json.RawMessage(`{"X-Hub-Signature-256":"sha256=abc"}`),
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
	if containsEnv(output.Env, "SCRIPT_ENV=1") || !containsEnv(output.Env, `WF_TRIGGER_HEADERS={"X-Hub-Signature-256":"sha256=abc"}`) {
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
	requestBytes, err := os.ReadFile(os.Getenv("WF_ADAPTER_REQUEST_JSON"))
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
	if err := os.WriteFile(os.Getenv("WF_ADAPTER_RESULT_JSON"), resultBytes, 0o644); err != nil {
		os.Exit(6)
	}
	os.Exit(0)
}

func buildExecutionBundleForTest(t *testing.T, runner *Runner, deployment contract.Deployment) contract.Deployment {
	t.Helper()
	prepared, err := runner.BuildExecutionBundle(context.Background(), deployment)
	if err != nil {
		t.Fatalf("BuildExecutionBundle returned error: %v", err)
	}
	if prepared.BundleDigest == "" || prepared.BundleURI == "" {
		t.Fatalf("execution bundle identity is empty: %#v", prepared)
	}
	runner.Store = nil
	return prepared
}

func requirePythonRuntime(t *testing.T) {
	t.Helper()
	python := "python3"
	if goruntime.GOOS == "windows" {
		python = "python"
	}
	if _, err := exec.LookPath(python); err != nil {
		t.Skipf("%s not found in PATH", python)
	}
}

func requireBunRuntime(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bun"); err != nil {
		t.Skipf("bun not found in PATH")
	}
}

func requireGoRuntime(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go not found in PATH")
	}
}

func containsEnv(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func envValue(values []string, key string) string {
	prefix := key + "="
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimPrefix(value, prefix)
		}
	}
	return ""
}

func containsEnvPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if len(value) >= len(prefix) && value[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}
