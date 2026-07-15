package state

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/imprun/windforce-lite/internal/contract"
	wfcrypto "github.com/imprun/windforce-lite/internal/crypto"
)

// TestStoreAtRestEncryptionSymmetry pins the at-rest encryption contract across
// BOTH backends (Local file snapshot and Postgres). It exercises every input and
// result write path — CreateRunAndEnqueue, CompleteJobWaitingHuman,
// CompleteJobSucceeded, CancelJob, and ResumeHumanTask — and asserts that the
// bytes physically persisted are ciphertext (crypto.IsEnc), while the public
// read APIs return plaintext. This is the lite analogue of the windforce G-2
// guard: any enqueue/complete path that persists plaintext in one backend but
// not the other is a divergence bug and will turn this test RED.
func TestStoreAtRestEncryptionSymmetry(t *testing.T) {
	t.Run("local", func(t *testing.T) {
		store := NewLocalStore(t.TempDir() + "/state.json")
		store.ConfigureInputCrypto("test-secret-key", "")
		assertAtRestEncryption(t, store, func(runID, jobID string) rawStored {
			return localRawStored(t, store, runID, jobID)
		}, func(taskID string) json.RawMessage {
			return localRawResumeInput(t, store, taskID)
		})
	})

	t.Run("postgres", func(t *testing.T) {
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
		store.ConfigureInputCrypto("test-secret-key", "")
		assertAtRestEncryption(t, store, func(runID, jobID string) rawStored {
			return postgresRawStored(t, store, runID, jobID)
		}, func(taskID string) json.RawMessage {
			return postgresRawResumeInput(t, store, taskID)
		})
	})
}

// rawStored is the ciphertext-at-rest captured for a run+job as physically
// persisted by a backend (never routed through the decrypting read APIs).
type rawStored struct {
	RunInput     json.RawMessage
	RunOutput    json.RawMessage
	RunResultOut json.RawMessage
	JobInput     json.RawMessage
}

func assertAtRestEncryption(
	t *testing.T,
	store Store,
	rawFor func(runID, jobID string) rawStored,
	rawResumeInput func(taskID string) json.RawMessage,
) {
	t.Helper()
	ctx := context.Background()
	deployment := contract.Deployment{
		Workspace: "default",
		App:       "echo",
		Commit:    "commit-a",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo", Command: []string{"helper"}},
		},
	}

	// --- Path 1: CreateRunAndEnqueue encrypts run.Input and job.Payload.Input.
	run := NewRun("windforce", NewID("run"), "echo", "echo", deployment, json.RawMessage(`{"secret":"input-1"}`))
	job := NewActionJob(run, nil)
	if err := store.CreateRunAndEnqueue(ctx, run, job); err != nil {
		t.Fatalf("CreateRunAndEnqueue returned error: %v", err)
	}
	raw := rawFor(run.ID, job.ID)
	if !wfcrypto.IsEnc(raw.RunInput) {
		t.Fatalf("run.Input persisted in cleartext: %s", raw.RunInput)
	}
	if !wfcrypto.IsEnc(raw.JobInput) {
		t.Fatalf("job.Payload.Input persisted in cleartext: %s", raw.JobInput)
	}
	if got := readRunInput(t, store, run.ID); got != `{"secret":"input-1"}` {
		t.Fatalf("GetRun input roundtrip = %s", got)
	}

	// --- Path 2: CompleteJobWaitingHuman encrypts the stored result output.
	_, lease, err := store.ClaimJob(ctx, "worker-a", time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob returned error: %v", err)
	}
	humanOutput := json.RawMessage(`{"$windforce":{"type":"human_task"},"secret":"result-1"}`)
	taskID := NewID("human")
	if err := store.CompleteJobWaitingHuman(ctx, lease, contract.JobResult{
		JobID: job.ID, App: "echo", Action: "echo", ExitCode: 0, Output: humanOutput,
	}, HumanTask{ID: taskID, RunID: run.ID, Title: "Approve"}); err != nil {
		t.Fatalf("CompleteJobWaitingHuman returned error: %v", err)
	}
	raw = rawFor(run.ID, job.ID)
	if !wfcrypto.IsEnc(raw.RunResultOut) {
		t.Fatalf("waiting-human result output persisted in cleartext: %s", raw.RunResultOut)
	}

	// --- Path 3: ResumeHumanTask encrypts the resume job's input, run.Input stays enc.
	resumedRun, resumeJob, err := store.ResumeHumanTask(ctx, taskID, json.RawMessage(`{"secret":"resume-1"}`))
	if err != nil {
		t.Fatalf("ResumeHumanTask returned error: %v", err)
	}
	if resumedRun.State != RunResuming {
		t.Fatalf("resumed run state = %s", resumedRun.State)
	}
	raw = rawFor(run.ID, resumeJob.ID)
	if !wfcrypto.IsEnc(raw.JobInput) {
		t.Fatalf("resume job input persisted in cleartext: %s", raw.JobInput)
	}
	if !wfcrypto.IsEnc(raw.RunInput) {
		t.Fatalf("run.Input no longer encrypted after resume: %s", raw.RunInput)
	}

	// Observation lock: the human-provided resume input is ALSO persisted
	// verbatim in human_tasks.resume_input, in CLEARTEXT, in both backends.
	// The same bytes are encrypted when merged into the job payload above, so
	// this is a genuine (symmetric) at-rest gap for the resume-input copy. This
	// assertion documents current behaviour; flip it if the field is encrypted.
	rawResume := rawResumeInput(taskID)
	if wfcrypto.IsEnc(rawResume) {
		t.Fatalf("resume_input unexpectedly encrypted (behaviour changed): %s", rawResume)
	}

	// Drain the still-queued resume job so the success path below claims the
	// job it intends to (ClaimJob returns the oldest queued job, not by ID).
	drained, drainLease, err := store.ClaimJob(ctx, "worker-drain", time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob (drain resume job) returned error: %v", err)
	}
	if drained.ID != resumeJob.ID {
		t.Fatalf("expected to drain resume job %q, got %q", resumeJob.ID, drained.ID)
	}
	if err := store.CompleteJobSucceeded(ctx, drainLease, contract.JobResult{
		JobID: drained.ID, App: "echo", Action: "echo", ExitCode: 0, Output: json.RawMessage(`{"drained":true}`),
	}); err != nil {
		t.Fatalf("CompleteJobSucceeded (drain) returned error: %v", err)
	}

	// --- Path 4: CompleteJobSucceeded encrypts run.Output and result output.
	run2 := NewRun("windforce", NewID("run"), "echo", "echo", deployment, json.RawMessage(`{"secret":"input-2"}`))
	job2 := NewActionJob(run2, nil)
	if err := store.CreateRunAndEnqueue(ctx, run2, job2); err != nil {
		t.Fatalf("CreateRunAndEnqueue run2 returned error: %v", err)
	}
	claimed2, lease2, err := store.ClaimJob(ctx, "worker-b", time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob run2 returned error: %v", err)
	}
	if claimed2.ID != job2.ID {
		t.Fatalf("expected to claim job2 %q, got %q", job2.ID, claimed2.ID)
	}
	if err := store.CompleteJobSucceeded(ctx, lease2, contract.JobResult{
		JobID: job2.ID, App: "echo", Action: "echo", ExitCode: 0, Output: json.RawMessage(`{"secret":"result-2"}`),
	}); err != nil {
		t.Fatalf("CompleteJobSucceeded returned error: %v", err)
	}
	raw = rawFor(run2.ID, job2.ID)
	if !wfcrypto.IsEnc(raw.RunOutput) {
		t.Fatalf("success run.Output persisted in cleartext: %s", raw.RunOutput)
	}
	if !wfcrypto.IsEnc(raw.RunResultOut) {
		t.Fatalf("success result output persisted in cleartext: %s", raw.RunResultOut)
	}

	// --- Path 5: CancelJob (queued -> completed) encrypts the canceled result output.
	run3 := NewRun("windforce", NewID("run"), "echo", "echo", deployment, json.RawMessage(`{"secret":"input-3"}`))
	job3 := NewActionJob(run3, nil)
	if err := store.CreateRunAndEnqueue(ctx, run3, job3); err != nil {
		t.Fatalf("CreateRunAndEnqueue run3 returned error: %v", err)
	}
	if _, err := store.CancelJob(ctx, "default", job3.ID, "operator@example.test", "operator canceled"); err != nil {
		t.Fatalf("CancelJob returned error: %v", err)
	}
	raw = rawFor(run3.ID, job3.ID)
	if !wfcrypto.IsEnc(raw.RunResultOut) {
		t.Fatalf("canceled result output persisted in cleartext: %s", raw.RunResultOut)
	}
}

func readRunInput(t *testing.T, store Store, runID string) string {
	t.Helper()
	run, err := store.GetRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	return string(run.Input)
}

func localRawStored(t *testing.T, store *LocalStore, runID, jobID string) rawStored {
	t.Helper()
	snapshot, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	run := snapshot.Runs[runID]
	job := snapshot.Jobs[jobID]
	var resultOut json.RawMessage
	if run.Result != nil {
		resultOut = run.Result.Output
	}
	return rawStored{
		RunInput:     run.Input,
		RunOutput:    run.Output,
		RunResultOut: resultOut,
		JobInput:     job.Payload.Input,
	}
}

func localRawResumeInput(t *testing.T, store *LocalStore, taskID string) json.RawMessage {
	t.Helper()
	snapshot, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	return snapshot.HumanTasks[taskID].ResumeInput
}

func postgresRawStored(t *testing.T, store *PostgresStore, runID, jobID string) rawStored {
	t.Helper()
	ctx := context.Background()
	var runInput, runOutput, runResult json.RawMessage
	if err := store.pool.QueryRow(ctx, `SELECT input, output, result FROM runs WHERE id=$1`, runID).
		Scan(&runInput, &runOutput, &runResult); err != nil {
		t.Fatalf("select run raw returned error: %v", err)
	}
	var resultOut json.RawMessage
	if len(runResult) > 0 {
		var jr contract.JobResult
		if err := json.Unmarshal(runResult, &jr); err != nil {
			t.Fatalf("unmarshal stored result: %v", err)
		}
		resultOut = jr.Output
	}
	var payload json.RawMessage
	if jobID != "" {
		if err := store.pool.QueryRow(ctx, `SELECT payload FROM jobs WHERE id=$1`, jobID).Scan(&payload); err != nil {
			t.Fatalf("select job raw returned error: %v", err)
		}
	}
	var jobPayload JobPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &jobPayload); err != nil {
			t.Fatalf("unmarshal stored payload: %v", err)
		}
	}
	return rawStored{
		RunInput:     runInput,
		RunOutput:    runOutput,
		RunResultOut: resultOut,
		JobInput:     jobPayload.Input,
	}
}

func postgresRawResumeInput(t *testing.T, store *PostgresStore, taskID string) json.RawMessage {
	t.Helper()
	var resumeInput json.RawMessage
	if err := store.pool.QueryRow(context.Background(), `SELECT resume_input FROM human_tasks WHERE id=$1`, taskID).
		Scan(&resumeInput); err != nil {
		t.Fatalf("select resume_input returned error: %v", err)
	}
	return resumeInput
}

// exerciseStoreExpiredLeaseReclaim characterizes dead-worker recovery: a claimed
// job whose lease expires WITHOUT a heartbeat must become re-claimable by another
// worker, and the original worker must lose ownership. This is the complement of
// exerciseStoreHeartbeatExtendsLease (which only proves a live heartbeat keeps
// ownership) and closes the top structural coverage gap — the reclaim path was
// untested on BOTH backends. Runs against Local and Postgres for parity.
func exerciseStoreExpiredLeaseReclaim(t *testing.T, store Store) {
	t.Helper()
	ttl := 60 * time.Millisecond
	deployment := contract.Deployment{
		Workspace: "default",
		App:       "echo",
		Commit:    "commit-a",
		Actions:   map[string]contract.Action{"echo": {Action: "echo", Command: []string{"helper"}}},
	}
	run := NewRun("windforce", "run-reclaim", "echo", "echo", deployment, json.RawMessage(`{}`))
	job := NewActionJob(run, nil)
	if err := store.CreateRunAndEnqueue(context.Background(), run, job); err != nil {
		t.Fatalf("CreateRunAndEnqueue: %v", err)
	}

	// worker-a claims, then dies (no heartbeat).
	claimedA, leaseA, err := store.ClaimJob(context.Background(), "worker-a", ttl)
	if err != nil {
		t.Fatalf("worker-a ClaimJob: %v", err)
	}
	if claimedA.ID != job.ID {
		t.Fatalf("worker-a claimed %s, want %s", claimedA.ID, job.ID)
	}

	// Let the lease expire.
	time.Sleep(ttl + 40*time.Millisecond)

	// worker-b must be able to re-claim the SAME job.
	claimedB, _, err := store.ClaimJob(context.Background(), "worker-b", ttl)
	if err != nil {
		t.Fatalf("worker-b ClaimJob after lease expiry: %v (dead-worker job was not reclaimed)", err)
	}
	if claimedB.ID != job.ID {
		t.Fatalf("worker-b claimed %s, want the reclaimed job %s", claimedB.ID, job.ID)
	}

	// worker-a has lost ownership: its heartbeat must report StillOwned=false so it
	// stops executing (and its later Complete is rejected).
	hb, err := store.HeartbeatJob(context.Background(), leaseA, ttl)
	if err == nil && hb.StillOwned {
		t.Fatalf("worker-a still owns the job after it was reclaimed by worker-b")
	}
}

func TestLocalStoreExpiredLeaseIsReclaimed(t *testing.T) {
	store := NewLocalStore(t.TempDir() + "/state.json")
	exerciseStoreExpiredLeaseReclaim(t, store)
}

func TestPostgresStoreExpiredLeaseIsReclaimed(t *testing.T) {
	dsn := os.Getenv("WINDFORCE_LITE_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("WINDFORCE_LITE_POSTGRES_TEST_DSN is not set")
	}
	store, err := OpenPostgresStore(context.Background(), dsn)
	if err != nil {
		t.Fatalf("OpenPostgresStore: %v", err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := store.pool.Exec(context.Background(), `TRUNCATE job_logs, run_events, human_tasks, jobs, runs RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("TRUNCATE: %v", err)
	}
	exerciseStoreExpiredLeaseReclaim(t, store)
}
