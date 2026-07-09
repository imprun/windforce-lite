package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/imprun/windforce-lite/internal/bundle"
	"github.com/imprun/windforce-lite/internal/contract"
	actionruntime "github.com/imprun/windforce-lite/internal/runtime"
	"github.com/imprun/windforce-lite/internal/state"
)

func TestProcessorCompletesQueuedRun(t *testing.T) {
	processor, stateStore, run := newProcessorTestHarness(t, "echo")

	processed, err := processor.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("ProcessOne returned error: %v", err)
	}
	if !processed {
		t.Fatalf("ProcessOne processed no job")
	}

	completed, err := stateStore.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if completed.State != state.RunSucceeded {
		t.Fatalf("run state = %s, want %s", completed.State, state.RunSucceeded)
	}
	if completed.Result == nil || completed.Result.JobID == "" {
		t.Fatalf("completed result missing job id: %#v", completed.Result)
	}
	if completed.Result.Stdout != "" || completed.Result.Stderr != "" {
		t.Fatalf("completed result should not expose logs: %#v", completed.Result)
	}
	logs, exists, err := stateStore.GetLogs(context.Background(), "workspace-a", completed.Result.JobID)
	if err != nil {
		t.Fatalf("GetLogs returned error: %v", err)
	}
	if !exists || !strings.Contains(logs, "worker stdout") || !strings.Contains(logs, "worker stderr") {
		t.Fatalf("logs = %q, exists = %v", logs, exists)
	}
	var output struct {
		OK          bool   `json:"ok"`
		WorkerGroup string `json:"worker_group"`
		ProxyURL    string `json:"proxy_url"`
		Input       struct {
			Message string `json:"message"`
		} `json:"input"`
	}
	if err := json.Unmarshal(completed.Output, &output); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	if !output.OK || output.Input.Message != "hello" || output.WorkerGroup != "test" ||
		output.ProxyURL != "http://job-"+completed.Result.JobID+"@proxy:18080" {
		t.Fatalf("output = %s", completed.Output)
	}
}

func TestProcessorStoresFailedActionOutputAndLogsSeparately(t *testing.T) {
	processor, stateStore, run := newProcessorTestHarness(t, "fail")

	processed, err := processor.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("ProcessOne returned error: %v", err)
	}
	if !processed {
		t.Fatalf("ProcessOne processed no job")
	}

	completed, err := stateStore.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if completed.State != state.RunFailed {
		t.Fatalf("run state = %s, want %s", completed.State, state.RunFailed)
	}
	if completed.Result == nil {
		t.Fatalf("completed result is nil")
	}
	if completed.Result.ExitCode != 7 {
		t.Fatalf("exit code = %d, want 7", completed.Result.ExitCode)
	}
	if completed.Result.Stdout != "" || completed.Result.Stderr != "" {
		t.Fatalf("failed result should not expose logs: %#v", completed.Result)
	}
	var output struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(completed.Result.Output, &output); err != nil {
		t.Fatalf("failed output is not JSON: %v", err)
	}
	if output.Name != "TargetError" || output.Message != "target rejected" {
		t.Fatalf("failed output = %s", completed.Result.Output)
	}
	logs, exists, err := stateStore.GetLogs(context.Background(), "workspace-a", completed.Result.JobID)
	if err != nil {
		t.Fatalf("GetLogs returned error: %v", err)
	}
	if !exists || !strings.Contains(logs, "failure stdout") || !strings.Contains(logs, "failure stderr") {
		t.Fatalf("logs = %q, exists = %v", logs, exists)
	}
}

func TestProcessorCreatesHumanTask(t *testing.T) {
	processor, stateStore, run := newProcessorTestHarness(t, "human")

	processed, err := processor.ProcessOne(context.Background())
	if err != nil {
		t.Fatalf("ProcessOne returned error: %v", err)
	}
	if !processed {
		t.Fatalf("ProcessOne processed no job")
	}

	waiting, err := stateStore.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if waiting.State != state.RunWaitingHuman {
		t.Fatalf("run state = %s, want %s", waiting.State, state.RunWaitingHuman)
	}
	task, err := stateStore.GetHumanTask(context.Background(), waiting.TaskID)
	if err != nil {
		t.Fatalf("GetHumanTask returned error: %v", err)
	}
	if task.Title != "Approve" {
		t.Fatalf("task title = %q", task.Title)
	}

	_, resumeJob, err := stateStore.ResumeHumanTask(context.Background(), task.ID, json.RawMessage(`{"approved":true}`))
	if err != nil {
		t.Fatalf("ResumeHumanTask returned error: %v", err)
	}
	if !strings.Contains(string(resumeJob.Payload.Input), `"$resume"`) {
		t.Fatalf("resume job input = %s", resumeJob.Payload.Input)
	}
}

func newProcessorTestHarness(t *testing.T, helperMode string) (Processor, *state.LocalStore, state.Run) {
	t.Helper()
	tempDir := t.TempDir()
	sourceDir := filepath.Join(tempDir, "source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "windforce.json"), []byte(`{"app":"echo","entrypoint":"main.ts","actions":{"echo":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	bundleStore := bundle.NewLocalStore(filepath.Join(tempDir, "store"))
	if err := bundleStore.Materialize(context.Background(), "workspace-a", "source-a", "commit-a", sourceDir); err != nil {
		t.Fatal(err)
	}
	deployment := contract.Deployment{
		Workspace:   "workspace-a",
		GitSourceID: "source-a",
		App:         "echo",
		Commit:      "commit-a",
		Actions: map[string]contract.Action{
			"echo": {
				Action:  "echo",
				Command: []string{os.Args[0], "-test.run=TestWorkerHelperProcess", "--"},
			},
		},
	}
	run := state.NewRun("windforce", "run-"+helperMode, "echo", "echo", deployment, json.RawMessage(`{"message":"hello"}`))
	run.Env = []string{"WINDFORCE_LITE_WORKER_HELPER=" + helperMode}
	job := state.NewActionJob(run, nil)
	stateStore := state.NewLocalStore(filepath.Join(tempDir, "state.json"))
	if err := stateStore.CreateRunAndEnqueue(context.Background(), run, job); err != nil {
		t.Fatal(err)
	}
	return Processor{
		Store: stateStore,
		Runner: actionruntime.Runner{
			Store:     bundleStore,
			CacheRoot: filepath.Join(tempDir, "cache"),
		},
		WorkerID:        "worker-a",
		Group:           "test",
		EgressProxyAddr: "proxy:18080",
		LeaseTTL:        time.Minute,
	}, stateStore, run
}

func TestWorkerHelperProcess(t *testing.T) {
	mode := os.Getenv("WINDFORCE_LITE_WORKER_HELPER")
	if mode == "" {
		return
	}

	switch mode {
	case "echo":
		input, err := os.ReadFile(os.Getenv("WF_INPUT_JSON"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		output := []byte(`{"ok":true,"worker_group":` + strconv.Quote(os.Getenv("WF_WORKER_GROUP")) + `,"proxy_url":` + strconv.Quote(os.Getenv("WF_PROXY_URL")) + `,"input":` + string(input) + `}`)
		if err := os.WriteFile(os.Getenv("WF_RESULT_JSON"), output, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Println("worker stdout")
		fmt.Fprintln(os.Stderr, "worker stderr")
	case "human":
		output := []byte(`{"$windforce":{"type":"human_task","title":"Approve","fields":[{"name":"approved","type":"boolean","required":true}]}}`)
		if err := os.WriteFile(os.Getenv("WF_RESULT_JSON"), output, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	case "fail":
		output := []byte(`{"name":"TargetError","message":"target rejected"}`)
		if err := os.WriteFile(os.Getenv("WF_RESULT_JSON"), output, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Println("failure stdout")
		fmt.Fprintln(os.Stderr, "failure stderr")
		os.Exit(7)
	default:
		os.Exit(2)
	}
	os.Exit(0)
}
