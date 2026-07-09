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

func exerciseStoreLifecycle(t *testing.T, store Store) {
	t.Helper()
	deployment := contract.Deployment{
		App:    "echo",
		Commit: "commit-a",
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
}
