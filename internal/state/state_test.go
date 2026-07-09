package state

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/imprun/windforce-lite/internal/contract"
)

func TestLocalStoreClaimCompleteAndResumeLifecycle(t *testing.T) {
	store := NewLocalStore(t.TempDir() + "/state.json")
	exerciseStoreLifecycle(t, store)
}

func TestPostgresStoreClaimCompleteAndResumeLifecycle(t *testing.T) {
	dsn := os.Getenv("WINDFORCE_LITE_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("WINDFORCE_LITE_POSTGRES_TEST_DSN is not set")
	}
	store, err := OpenPostgresStore(context.Background(), dsn)
	if err != nil {
		t.Fatalf("OpenPostgresStore returned error: %v", err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	if _, err := store.pool.Exec(context.Background(), `TRUNCATE job_logs, run_events, human_tasks, jobs, runs RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("TRUNCATE returned error: %v", err)
	}
	exerciseStoreLifecycle(t, store)
}

func TestLocalStoreJobState(t *testing.T) {
	store := NewLocalStore(t.TempDir() + "/state.json")
	exerciseStoreJobState(t, store)
}

func TestPostgresStoreJobState(t *testing.T) {
	dsn := os.Getenv("WINDFORCE_LITE_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("WINDFORCE_LITE_POSTGRES_TEST_DSN is not set")
	}
	store, err := OpenPostgresStore(context.Background(), dsn)
	if err != nil {
		t.Fatalf("OpenPostgresStore returned error: %v", err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	if _, err := store.pool.Exec(context.Background(), `TRUNCATE job_state, job_logs, run_events, human_tasks, jobs, runs RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("TRUNCATE returned error: %v", err)
	}
	exerciseStoreJobState(t, store)
}

func TestLocalStoreVariablesAndResources(t *testing.T) {
	store := NewLocalStore(t.TempDir() + "/state.json")
	exerciseStoreVariablesAndResources(t, store)
}

func TestPostgresStoreVariablesAndResources(t *testing.T) {
	dsn := os.Getenv("WINDFORCE_LITE_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("WINDFORCE_LITE_POSTGRES_TEST_DSN is not set")
	}
	store, err := OpenPostgresStore(context.Background(), dsn)
	if err != nil {
		t.Fatalf("OpenPostgresStore returned error: %v", err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	if _, err := store.pool.Exec(context.Background(), `TRUNCATE resource, variable, job_state, job_logs, run_events, human_tasks, jobs, runs RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("TRUNCATE returned error: %v", err)
	}
	exerciseStoreVariablesAndResources(t, store)
}

func TestLocalStoreClaimJobEnforcesMaxConcurrent(t *testing.T) {
	store := NewLocalStore(t.TempDir() + "/state.json")
	exerciseStoreMaxConcurrent(t, store)
}

func TestPostgresStoreClaimJobEnforcesMaxConcurrent(t *testing.T) {
	dsn := os.Getenv("WINDFORCE_LITE_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("WINDFORCE_LITE_POSTGRES_TEST_DSN is not set")
	}
	store, err := OpenPostgresStore(context.Background(), dsn)
	if err != nil {
		t.Fatalf("OpenPostgresStore returned error: %v", err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate returned error: %v", err)
	}
	if _, err := store.pool.Exec(context.Background(), `TRUNCATE job_logs, run_events, human_tasks, jobs, runs RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("TRUNCATE returned error: %v", err)
	}
	exerciseStoreMaxConcurrent(t, store)
}

func TestLocalStoreClaimJobForTags(t *testing.T) {
	store := NewLocalStore(t.TempDir() + "/state.json")
	redTag := "red"
	blueTag := "blue"
	deployment := contract.Deployment{
		Workspace: "ws-a",
		App:       "echo",
		Commit:    "commit-a",
		Actions: map[string]contract.Action{
			"red":  {Action: "red", Tag: &redTag, Command: []string{"helper"}},
			"blue": {Action: "blue", Tag: &blueTag, Command: []string{"helper"}},
		},
	}
	for _, action := range []string{"red", "blue"} {
		run := NewRun("windforce", "run-"+action, "echo", action, deployment, json.RawMessage(`{}`))
		job := NewActionJob(run, nil)
		if err := store.CreateRunAndEnqueue(context.Background(), run, job); err != nil {
			t.Fatalf("CreateRunAndEnqueue(%s) returned error: %v", action, err)
		}
	}

	claimed, _, err := store.ClaimJobForTags(context.Background(), "worker-blue", []string{"blue"}, time.Minute)
	if err != nil {
		t.Fatalf("ClaimJobForTags returned error: %v", err)
	}
	if claimed.Payload.Action != "blue" || claimed.Payload.Tag != "blue" {
		t.Fatalf("claimed job = %#v", claimed.Payload)
	}
	if _, _, err := store.ClaimJobForTags(context.Background(), "worker-green", []string{"green"}, time.Minute); err != ErrNoQueuedJob {
		t.Fatalf("green claim error = %v, want %v", err, ErrNoQueuedJob)
	}
}

func TestActionJobPreservesActorAudit(t *testing.T) {
	deployment := contract.Deployment{
		Workspace:   "ws-a",
		GitSourceID: "1",
		App:         "echo",
		Commit:      "commit-a",
		Entrypoint:  "main.ts",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo"},
		},
	}
	run := NewRun("windforce", "run-a", "echo", "echo", deployment, json.RawMessage(`{}`))
	run.CreatedBy = "runner@example.test"
	run.PermissionedAs = "delegate@example.test"

	job := NewActionJob(run, nil)
	if job.Payload.CreatedBy != "runner@example.test" || job.Payload.PermissionedAs != "delegate@example.test" {
		t.Fatalf("job actor = %q/%q", job.Payload.CreatedBy, job.Payload.PermissionedAs)
	}
	item := newJobListItem("ws-a", job, run)
	if item.CreatedBy != "runner@example.test" || item.PermissionedAs != "delegate@example.test" {
		t.Fatalf("list actor = %q/%q", item.CreatedBy, item.PermissionedAs)
	}
}

func TestActionJobDefaultsActorAudit(t *testing.T) {
	deployment := contract.Deployment{
		Workspace:   "ws-a",
		GitSourceID: "1",
		App:         "echo",
		Commit:      "commit-a",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo"},
		},
	}
	run := NewRun("windforce", "run-a", "echo", "echo", deployment, json.RawMessage(`{}`))
	job := NewActionJob(run, nil)
	if run.CreatedBy != "system" || run.PermissionedAs != "system" {
		t.Fatalf("run actor = %q/%q", run.CreatedBy, run.PermissionedAs)
	}
	if job.Payload.CreatedBy != "system" || job.Payload.PermissionedAs != "system" {
		t.Fatalf("job actor = %q/%q", job.Payload.CreatedBy, job.Payload.PermissionedAs)
	}
}

func exerciseStoreVariablesAndResources(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	if err := store.SetVariable(ctx, "ws-a", "", "config/token", "shared", true, "shared token"); err != nil {
		t.Fatalf("SetVariable shared returned error: %v", err)
	}
	if err := store.SetVariable(ctx, "ws-a", "echo", "config/token", "scoped", false, "scoped token"); err != nil {
		t.Fatalf("SetVariable scoped returned error: %v", err)
	}
	variable, found, err := store.GetVariable(ctx, "ws-a", "echo", "config/token")
	if err != nil {
		t.Fatalf("GetVariable scoped returned error: %v", err)
	}
	if !found || variable.Value != "scoped" || variable.AppKey != "echo" {
		t.Fatalf("scoped variable found=%v variable=%#v", found, variable)
	}
	variable, found, err = store.GetVariable(ctx, "ws-a", "other", "config/token")
	if err != nil {
		t.Fatalf("GetVariable shared fallback returned error: %v", err)
	}
	if !found || variable.Value != "shared" || variable.AppKey != "" {
		t.Fatalf("shared variable found=%v variable=%#v", found, variable)
	}
	variables, err := store.ListVariables(ctx, "ws-a")
	if err != nil {
		t.Fatalf("ListVariables returned error: %v", err)
	}
	if len(variables) != 2 {
		t.Fatalf("variables = %#v", variables)
	}
	if err := store.DeleteVariable(ctx, "ws-a", "echo", "config/token"); err != nil {
		t.Fatalf("DeleteVariable returned error: %v", err)
	}
	variable, found, err = store.GetVariable(ctx, "ws-a", "echo", "config/token")
	if err != nil {
		t.Fatalf("GetVariable after delete returned error: %v", err)
	}
	if !found || variable.Value != "shared" {
		t.Fatalf("post-delete variable found=%v variable=%#v", found, variable)
	}

	if err := store.SetResource(ctx, "ws-a", "browser/profile", json.RawMessage(`{"headless":true}`), "json", "browser settings"); err != nil {
		t.Fatalf("SetResource returned error: %v", err)
	}
	resource, found, err := store.GetResource(ctx, "ws-a", "browser/profile")
	if err != nil {
		t.Fatalf("GetResource returned error: %v", err)
	}
	var got map[string]bool
	if err := json.Unmarshal(resource.Value, &got); err != nil {
		t.Fatalf("resource value is not JSON object: %v", err)
	}
	if !found || !got["headless"] || resource.ResourceType != "json" {
		t.Fatalf("resource found=%v resource=%#v", found, resource)
	}
}

func exerciseStoreJobState(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	value, found, err := store.GetState(ctx, "ws-a", "flow/count")
	if err != nil {
		t.Fatalf("GetState missing returned error: %v", err)
	}
	if found || string(value) != "null" {
		t.Fatalf("missing state found=%v value=%s, want false null", found, value)
	}
	if err := store.SetState(ctx, "ws-a", "flow/count", json.RawMessage(`{"count":1}`)); err != nil {
		t.Fatalf("SetState returned error: %v", err)
	}
	value, found, err = store.GetState(ctx, "ws-a", "flow/count")
	if err != nil {
		t.Fatalf("GetState returned error: %v", err)
	}
	var got map[string]int
	if err := json.Unmarshal(value, &got); err != nil {
		t.Fatalf("state value is not JSON object: %v", err)
	}
	if !found || got["count"] != 1 {
		t.Fatalf("state found=%v value=%s", found, value)
	}
	value, found, err = store.GetState(ctx, "ws-b", "flow/count")
	if err != nil {
		t.Fatalf("GetState other workspace returned error: %v", err)
	}
	if found || string(value) != "null" {
		t.Fatalf("other workspace state found=%v value=%s, want false null", found, value)
	}
}

func exerciseStoreMaxConcurrent(t *testing.T, store Store) {
	t.Helper()
	ctx := context.Background()
	limit := int32(1)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	echoDeployment := maxConcurrentDeployment("echo", &limit)
	otherDeployment := maxConcurrentDeployment("other", &limit)
	firstEcho := enqueueMaxConcurrentJob(t, store, echoDeployment, "run-echo-1", base)
	secondEcho := enqueueMaxConcurrentJob(t, store, echoDeployment, "run-echo-2", base.Add(time.Second))
	other := enqueueMaxConcurrentJob(t, store, otherDeployment, "run-other-1", base.Add(2*time.Second))

	claimed, firstLease, err := store.ClaimJobForTags(ctx, "worker-first", []string{"default"}, time.Minute)
	if err != nil {
		t.Fatalf("first ClaimJobForTags returned error: %v", err)
	}
	if claimed.ID != firstEcho.ID {
		t.Fatalf("first claimed job = %q, want %q", claimed.ID, firstEcho.ID)
	}

	claimed, _, err = store.ClaimJobForTags(ctx, "worker-other", []string{"default"}, time.Minute)
	if err != nil {
		t.Fatalf("second ClaimJobForTags returned error: %v", err)
	}
	if claimed.ID != other.ID {
		t.Fatalf("second claimed job = %q, want other app job %q", claimed.ID, other.ID)
	}

	if _, _, err := store.ClaimJobForTags(ctx, "worker-blocked", []string{"default"}, time.Minute); err != ErrNoQueuedJob {
		t.Fatalf("blocked claim error = %v, want %v", err, ErrNoQueuedJob)
	}

	if err := store.CompleteJobSucceeded(ctx, firstLease, contract.JobResult{
		JobID:  firstEcho.ID,
		App:    "echo",
		Action: "echo",
		Output: json.RawMessage(`{"ok":true}`),
	}); err != nil {
		t.Fatalf("CompleteJobSucceeded returned error: %v", err)
	}

	claimed, _, err = store.ClaimJobForTags(ctx, "worker-next", []string{"default"}, time.Minute)
	if err != nil {
		t.Fatalf("third ClaimJobForTags returned error: %v", err)
	}
	if claimed.ID != secondEcho.ID {
		t.Fatalf("third claimed job = %q, want unblocked echo job %q", claimed.ID, secondEcho.ID)
	}
}

func maxConcurrentDeployment(app string, limit *int32) contract.Deployment {
	gitSourceID := "1"
	if app != "echo" {
		gitSourceID = "2"
	}
	return contract.Deployment{
		Workspace:     "ws-a",
		GitSourceID:   gitSourceID,
		App:           app,
		Commit:        "commit-a",
		MaxConcurrent: limit,
		Actions: map[string]contract.Action{
			"echo": {Action: "echo", Command: []string{"helper"}},
		},
	}
}

func enqueueMaxConcurrentJob(t *testing.T, store Store, deployment contract.Deployment, runID string, createdAt time.Time) Job {
	t.Helper()
	run := NewRun("windforce", runID, deployment.App, "echo", deployment, json.RawMessage(`{}`))
	job := NewActionJob(run, nil)
	job.ID = runID + "-job"
	job.CreatedAt = createdAt
	job.UpdatedAt = createdAt
	if err := store.CreateRunAndEnqueue(context.Background(), run, job); err != nil {
		t.Fatalf("CreateRunAndEnqueue(%s) returned error: %v", runID, err)
	}
	return job
}

func exerciseStoreLifecycle(t *testing.T, store Store) {
	t.Helper()
	deployment := contract.Deployment{
		GitSourceID: "1",
		App:         "echo",
		Commit:      "commit-a",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo", Command: []string{"helper"}},
		},
	}
	run := NewRun("windforce", "run-a", "echo", "echo", deployment, json.RawMessage(`{"message":"hello"}`))
	run.CorrelationID = "task-a"
	job := NewActionJob(run, nil)
	if job.Payload.CorrelationID != "task-a" {
		t.Fatalf("job correlation id = %q, want task-a", job.Payload.CorrelationID)
	}
	if err := store.CreateRunAndEnqueue(context.Background(), run, job); err != nil {
		t.Fatalf("CreateRunAndEnqueue returned error: %v", err)
	}
	storedJob, storedRun, found, err := store.GetJob(context.Background(), "default", job.ID)
	if err != nil {
		t.Fatalf("GetJob returned error: %v", err)
	}
	if !found || storedJob.ID != job.ID || storedRun.ID != run.ID {
		t.Fatalf("GetJob found=%v job=%q run=%q", found, storedJob.ID, storedRun.ID)
	}

	claimed, lease, err := store.ClaimJob(context.Background(), "worker-a", time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob returned error: %v", err)
	}
	if claimed.ID != job.ID {
		t.Fatalf("claimed job = %q, want %q", claimed.ID, job.ID)
	}
	if err := store.AppendLogs(context.Background(), job.ID, "default", "first\n"); err != nil {
		t.Fatalf("AppendLogs returned error: %v", err)
	}
	if err := store.AppendLogs(context.Background(), job.ID, "default", "second\n"); err != nil {
		t.Fatalf("AppendLogs returned error: %v", err)
	}
	logs, exists, err := store.GetLogs(context.Background(), "default", job.ID)
	if err != nil {
		t.Fatalf("GetLogs returned error: %v", err)
	}
	if !exists || logs != "first\nsecond\n" {
		t.Fatalf("logs = %q, exists = %v", logs, exists)
	}
	if err := store.CompleteJobWaitingHuman(context.Background(), lease, contract.JobResult{
		JobID:    job.ID,
		App:      "echo",
		Action:   "echo",
		ExitCode: 0,
		Output:   json.RawMessage(`{"$windforce":{"type":"human_task"}}`),
	}, HumanTask{
		ID:    "human-a",
		RunID: run.ID,
		Title: "Approve",
	}); err != nil {
		t.Fatalf("CompleteJobWaitingHuman returned error: %v", err)
	}
	waiting, err := store.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if waiting.State != RunWaitingHuman || waiting.TaskID != "human-a" {
		t.Fatalf("waiting run = %#v", waiting)
	}
	if waiting.CorrelationID != "task-a" {
		t.Fatalf("waiting correlation id = %q, want task-a", waiting.CorrelationID)
	}

	resumed, resumeJob, err := store.ResumeHumanTask(context.Background(), "human-a", json.RawMessage(`{"approved":true}`))
	if err != nil {
		t.Fatalf("ResumeHumanTask returned error: %v", err)
	}
	if resumed.State != RunResuming {
		t.Fatalf("resumed state = %s, want %s", resumed.State, RunResuming)
	}
	if resumed.CorrelationID != "task-a" || resumeJob.Payload.CorrelationID != "task-a" {
		t.Fatalf("resumed correlation id = %q, job = %q, want task-a", resumed.CorrelationID, resumeJob.Payload.CorrelationID)
	}
	input := string(resumeJob.Payload.Input)
	if !strings.Contains(input, `"$resume"`) || !strings.Contains(input, `"approved":true`) {
		t.Fatalf("resume job input = %s", input)
	}

	canceled, err := store.CancelRun(context.Background(), run.ID, "operator canceled")
	if err != nil {
		t.Fatalf("CancelRun returned error: %v", err)
	}
	if canceled.State != RunCanceled {
		t.Fatalf("canceled state = %s, want %s", canceled.State, RunCanceled)
	}
	retried, retryJob, err := store.RetryRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("RetryRun returned error: %v", err)
	}
	if retried.State != RunQueued {
		t.Fatalf("retried state = %s, want %s", retried.State, RunQueued)
	}
	if retried.CorrelationID != "task-a" || retryJob.Payload.CorrelationID != "task-a" {
		t.Fatalf("retried correlation id = %q, job = %q, want task-a", retried.CorrelationID, retryJob.Payload.CorrelationID)
	}
	var retryInput struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(retryJob.Payload.Input, &retryInput); err != nil {
		t.Fatalf("retry job input is not JSON: %v", err)
	}
	if retryInput.Message != "hello" {
		t.Fatalf("retry job input = %s", retryJob.Payload.Input)
	}
	cancelResult, err := store.CancelJob(context.Background(), "default", retryJob.ID, "operator canceled")
	if err != nil {
		t.Fatalf("CancelJob returned error: %v", err)
	}
	if !cancelResult.Found || !cancelResult.CompletedNow || cancelResult.SoftCanceled || cancelResult.AlreadyCompleted {
		t.Fatalf("cancel result = %#v", cancelResult)
	}
	canceledAgain, err := store.CancelJob(context.Background(), "default", retryJob.ID, "")
	if err != nil {
		t.Fatalf("CancelJob second call returned error: %v", err)
	}
	if !canceledAgain.Found || !canceledAgain.AlreadyCompleted {
		t.Fatalf("second cancel result = %#v", canceledAgain)
	}
	items, err := store.ListJobs(context.Background(), JobListQuery{
		WorkspaceID: "default",
		Status:      "canceled",
		AppKey:      "echo",
		ActionKey:   "echo",
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("ListJobs returned error: %v", err)
	}
	foundCanceledRetry := false
	for _, item := range items {
		if item.ID == retryJob.ID && item.Completed && item.Status == "canceled" {
			foundCanceledRetry = true
			break
		}
	}
	if !foundCanceledRetry {
		t.Fatalf("canceled list items = %#v", items)
	}
	summary, err := store.JobSummary(context.Background(), "default", time.Hour)
	if err != nil {
		t.Fatalf("JobSummary returned error: %v", err)
	}
	if summary.CanceledCountRecent < 1 || len(summary.ByApp) == 0 || summary.ByApp[0].AppKey != "echo" {
		t.Fatalf("summary = %#v", summary)
	}
}
