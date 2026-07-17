package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/imprun/windforce-core/internal/bundle"
	"github.com/imprun/windforce-core/internal/contract"
	"github.com/imprun/windforce-core/internal/executionbundle"
	actionruntime "github.com/imprun/windforce-core/internal/runtime"
	"github.com/imprun/windforce-core/internal/state"
)

func TestDevelopmentPayloadLogsIncludeCompleteValues(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)

	logJobInput(true, "job-a", "app-a", "action-a", []byte(`{"account":"visible-local-value"}`))
	logJobExecution(true, "job-a", "app-a", "action-a", contract.JobResult{
		ExitCode: 7,
		Stdout:   "complete stdout",
		Stderr:   "complete stderr",
		Output:   json.RawMessage(`{"result":"complete output"}`),
	})

	logged := output.String()
	for _, expected := range []string{
		`{"account":"visible-local-value"}`,
		`complete stdout`,
		`complete stderr`,
		`{"result":"complete output"}`,
	} {
		if !strings.Contains(logged, expected) {
			t.Fatalf("payload log missing %q: %s", expected, logged)
		}
	}
}

func TestPayloadLogsAreDisabledByDefault(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previous)

	logJobInput(false, "job-a", "app-a", "action-a", []byte(`{"secret":"hidden"}`))
	logJobExecution(false, "job-a", "app-a", "action-a", contract.JobResult{Output: json.RawMessage(`{"secret":"hidden"}`)})
	if output.Len() != 0 {
		t.Fatalf("disabled payload logging wrote: %s", output.String())
	}
}

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

func TestProcessorAppliesInputConfigBeforeExecution(t *testing.T) {
	processor, stateStore, run := newProcessorTestHarness(t, "echo")
	if _, err := stateStore.SetInputConfig(context.Background(), state.InputConfig{
		WorkspaceID: "workspace-a", AppKey: "echo", ActionKey: "echo",
		Config: json.RawMessage(`{"region":"kr"}`),
	}, "operator"); err != nil {
		t.Fatal(err)
	}
	processed, err := processor.ProcessOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("ProcessOne = %v, %v", processed, err)
	}
	completed, err := stateStore.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Input map[string]json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(completed.Output, &output); err != nil {
		t.Fatal(err)
	}
	if string(output.Input["region"]) != `"kr"` || string(output.Input["message"]) != `"hello"` {
		t.Fatalf("worker input = %s", completed.Output)
	}
}

func TestProcessorOwnsRuntimeBindings(t *testing.T) {
	processor, stateStore, run := newProcessorTestHarness(t, "echo")
	processor.RuntimeBindings = RuntimeBindings{
		AuthSession: AuthSessionBinding{
			ServiceURL: "http://auth-session:8005",
			JWT:        "worker-token",
			Timeout:    12 * time.Second,
		},
	}
	if _, err := stateStore.SetInputConfig(context.Background(), state.InputConfig{
		WorkspaceID: "workspace-a", AppKey: "echo", ActionKey: "echo",
		Config: json.RawMessage(`{
			"_SCRAPING_RUNTIME":{"authSession":{"serviceUrl":"http://stale","jwt":"stale","timeoutMs":1}},
			"region":"kr"
		}`),
	}, "operator"); err != nil {
		t.Fatal(err)
	}
	processed, err := processor.ProcessOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("ProcessOne = %v, %v", processed, err)
	}
	completed, err := stateStore.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Input map[string]json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(completed.Output, &output); err != nil {
		t.Fatal(err)
	}
	if string(output.Input["region"]) != `"kr"` {
		t.Fatalf("business input setting missing: %s", completed.Output)
	}
	var runtimePayload struct {
		AuthSession struct {
			ServiceURL string `json:"serviceUrl"`
			JWT        string `json:"jwt"`
			TimeoutMs  int64  `json:"timeoutMs"`
		} `json:"authSession"`
	}
	if err := json.Unmarshal(output.Input["_SCRAPING_RUNTIME"], &runtimePayload); err != nil {
		t.Fatalf("runtime payload missing: %v: %s", err, completed.Output)
	}
	if runtimePayload.AuthSession.ServiceURL != "http://auth-session:8005" ||
		runtimePayload.AuthSession.JWT != "worker-token" ||
		runtimePayload.AuthSession.TimeoutMs != 12000 {
		t.Fatalf("runtime payload = %#v", runtimePayload.AuthSession)
	}
}

func TestProcessorRejectsLockedInputConfig(t *testing.T) {
	processor, stateStore, run := newProcessorTestHarness(t, "echo")
	if _, err := stateStore.SetInputConfig(context.Background(), state.InputConfig{
		WorkspaceID: "workspace-a", AppKey: "echo", ActionKey: "echo",
		Config: json.RawMessage(`{"message":"server"}`), LockedKeys: []string{"message"},
	}, "operator"); err != nil {
		t.Fatal(err)
	}
	processed, err := processor.ProcessOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("ProcessOne = %v, %v", processed, err)
	}
	completed, err := stateStore.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != state.RunFailed || completed.Result == nil || !strings.Contains(string(completed.Result.Output), "InputConfigLocked") {
		t.Fatalf("completed = %#v", completed)
	}
}

func TestProcessorAppliesLogSizeCap(t *testing.T) {
	processor, stateStore, run := newProcessorTestHarness(t, "echo")
	processor.LogCapBytes = 5

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
	if completed.Result == nil {
		t.Fatalf("completed result missing")
	}
	logs, exists, err := stateStore.GetLogs(context.Background(), "workspace-a", completed.Result.JobID)
	if err != nil {
		t.Fatalf("GetLogs returned error: %v", err)
	}
	if !exists || !strings.Contains(logs, "[log truncated: job exceeded log size cap]") {
		t.Fatalf("logs = %q, exists = %v", logs, exists)
	}
	if strings.Contains(logs, "worker stdout") && strings.Contains(logs, "worker stderr") {
		t.Fatalf("logs were not capped: %q", logs)
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

func TestProcessorPromotesActionFailurePayload(t *testing.T) {
	processor, stateStore, run := newProcessorTestHarness(t, "declared-fail")

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
	if completed.Result.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", completed.Result.ExitCode)
	}
	if completed.Result.Error != "ERR_MLCOM_MSG10000" {
		t.Fatalf("error = %q, want ECODE", completed.Result.Error)
	}
	if !strings.Contains(string(completed.Result.Output), `"RESULT":"FAIL"`) {
		t.Fatalf("action output was not preserved: %s", completed.Result.Output)
	}
}

func TestProcessorStoresExecutionBundleFetchErrorResult(t *testing.T) {
	processor, stateStore, run := newProcessorTestHarness(t, "echo")
	processor.Runner.ArtifactStore = executionbundle.NewLocalStore(filepath.Join(t.TempDir(), "empty-artifact-store"))

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
	var output struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(completed.Result.Output, &output); err != nil {
		t.Fatalf("prepare output is not JSON: %v", err)
	}
	if output.Name != "BundleFetchError" || !strings.Contains(output.Message, "execution bundle") {
		t.Fatalf("execution bundle output = %s", completed.Result.Output)
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

func TestProcessorHeartbeatCancelsRunningAction(t *testing.T) {
	processor, stateStore, run := newProcessorTestHarness(t, "sleep")
	processor.LeaseTTL = 200 * time.Millisecond
	processor.HeartbeatInterval = 20 * time.Millisecond

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		processed, err := processor.ProcessOne(context.Background())
		if err != nil {
			done <- err
			return
		}
		if !processed {
			done <- fmt.Errorf("ProcessOne processed no job")
			return
		}
		done <- nil
	}()

	jobID := waitForRunningJob(t, stateStore, run.ID)
	cancelResult, err := stateStore.CancelJob(context.Background(), "workspace-a", jobID, "operator@example.test", "stop")
	if err != nil {
		t.Fatalf("CancelJob returned error: %v", err)
	}
	if !cancelResult.SoftCanceled {
		t.Fatalf("CancelJob result = %#v, want soft cancel", cancelResult)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ProcessOne returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("ProcessOne did not stop after cancel")
	}
	if elapsed := time.Since(start); elapsed >= 4*time.Second {
		t.Fatalf("ProcessOne waited for the helper sleep instead of canceling: %s", elapsed)
	}
	completed, err := stateStore.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if completed.State != state.RunCanceled {
		t.Fatalf("run state = %s, want %s", completed.State, state.RunCanceled)
	}
	if completed.Result == nil || completed.Result.Error != "job canceled" {
		t.Fatalf("completed result = %#v", completed.Result)
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
	if err := os.WriteFile(filepath.Join(sourceDir, "main.ts"), []byte("export async function main(input: unknown) { return input; }\n"), 0o644); err != nil {
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
		Entrypoint:  "main.ts",
		ScriptLang:  "typescript",
		Actions: map[string]contract.Action{
			"echo": {
				Action:  "echo",
				Command: []string{os.Args[0], "-test.run=TestWorkerHelperProcess", "--", helperMode},
			},
		},
	}
	executionBundleStore := executionbundle.NewLocalStore(filepath.Join(tempDir, "artifacts"))
	runner := actionruntime.Runner{
		Store:         bundleStore,
		ArtifactStore: executionBundleStore,
		CacheRoot:     filepath.Join(tempDir, "cache"),
	}
	deployment, err := runner.BuildExecutionBundle(context.Background(), deployment)
	if err != nil {
		t.Fatalf("BuildExecutionBundle returned error: %v", err)
	}
	runner.Store = nil
	run := state.NewRun("windforce", "run-"+helperMode, "echo", "echo", deployment, json.RawMessage(`{"message":"hello"}`))
	job := state.NewActionJob(run, nil)
	stateStore := state.NewLocalStore(filepath.Join(tempDir, "state.json"))
	stateStore.ConfigureInputCrypto("test-secret-key", "")
	if err := stateStore.CreateRunAndEnqueue(context.Background(), run, job); err != nil {
		t.Fatal(err)
	}
	return Processor{
		Store:           stateStore,
		Runner:          runner,
		WorkerID:        "worker-a",
		Group:           "test",
		EgressProxyAddr: "proxy:18080",
		LeaseTTL:        time.Minute,
	}, stateStore, run
}

func waitForRunningJob(t *testing.T, stateStore *state.LocalStore, runID string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := stateStore.Load(context.Background())
		if err != nil {
			t.Fatalf("Load returned error: %v", err)
		}
		for _, job := range snapshot.Jobs {
			if job.RunID == runID && job.State == state.JobRunning {
				return job.ID
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job for run %s did not reach running state", runID)
	return ""
}

func TestWorkerHelperProcess(t *testing.T) {
	mode := ""
	for index, arg := range os.Args {
		if mode == "" && arg == "--" && index+1 < len(os.Args) {
			mode = os.Args[index+1]
		}
	}
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
	case "declared-fail":
		output := []byte(`{"RESULT":"FAIL","ECODE":"ERR_MLCOM_MSG10000","EMSG":"schema validation failed"}`)
		if err := os.WriteFile(os.Getenv("WF_RESULT_JSON"), output, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	case "sleep":
		time.Sleep(5 * time.Second)
		if err := os.WriteFile(os.Getenv("WF_RESULT_JSON"), []byte(`{"ok":true}`), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	default:
		os.Exit(2)
	}
	os.Exit(0)
}
