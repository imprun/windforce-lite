package state

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/imprun/windforce-core/internal/contract"
	wfcrypto "github.com/imprun/windforce-core/internal/crypto"
)

var canonicalUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func TestNewIDUsesCanonicalUUIDsForRuntimeEntities(t *testing.T) {
	for _, prefix := range []string{"run", "job", "human"} {
		if got := NewID(prefix); !canonicalUUIDPattern.MatchString(got) {
			t.Fatalf("NewID(%q) = %q, want UUID", prefix, got)
		}
	}
	if got := NewID("worker"); !strings.HasPrefix(got, "worker_") {
		t.Fatalf("NewID(\"worker\") = %q, want worker_ prefix", got)
	}
}

func TestActionJobUsesCompactExecutionPin(t *testing.T) {
	maxConcurrent := int32(3)
	tagOverride := "browser"
	deploymentID := "release-a"
	deployment := contract.Deployment{
		Workspace:            "ws-a",
		GitSourceID:          "source-a",
		App:                  "echo",
		Version:              "1.2.3",
		Tag:                  "default",
		TagOverride:          &tagOverride,
		Entrypoint:           "main.py",
		Runtime:              "python",
		ScriptLang:           "python",
		TimeoutS:             90,
		MaxConcurrent:        &maxConcurrent,
		RequiredCapabilities: []string{"browser"},
		Commit:               "commit-a",
		DeploymentID:         &deploymentID,
		BundleDigest:         "sha256:bundle-a",
		BundleURI:            "execution-bundle://sha256/bundle-a",
		ObjectURI:            "bundle://ws-a/source-a/commit-a",
		Actions: map[string]contract.Action{
			"selected": {
				Action:           "selected",
				InputSchemaBody:  json.RawMessage(`{"type":"object","required":["message"]}`),
				OutputSchemaBody: json.RawMessage(`{"type":"object","required":["ok"]}`),
			},
			"unrelated": {Action: "unrelated"},
		},
	}

	run := NewRun("http", "run-a", "echo", "selected", deployment, json.RawMessage(`{"message":"hello"}`))
	if len(run.Deployment.Actions) != 1 || run.Deployment.Actions["selected"].Action != "selected" {
		t.Fatalf("run deployment actions = %#v", run.Deployment.Actions)
	}
	if len(deployment.Actions) != 2 {
		t.Fatalf("source deployment was mutated: %#v", deployment.Actions)
	}

	job := NewActionJob(run, nil)
	if job.Payload.Deployment != nil {
		t.Fatalf("new job retained legacy deployment: %#v", job.Payload.Deployment)
	}
	if len(job.Payload.ActionSpec.InputSchemaBody) != 0 || len(job.Payload.ActionSpec.OutputSchemaBody) != 0 {
		t.Fatalf("job action duplicated schema bodies: %#v", job.Payload.ActionSpec)
	}
	if !strings.Contains(string(job.Payload.InputSchema), `"message"`) || !strings.Contains(string(job.Payload.OutputSchema), `"ok"`) {
		t.Fatalf("job schemas = input:%s output:%s", job.Payload.InputSchema, job.Payload.OutputSchema)
	}
	encoded, err := json.Marshal(job.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"deployment":`) || strings.Contains(string(encoded), `"unrelated"`) {
		t.Fatalf("compact job payload contains full deployment: %s", encoded)
	}
	pinned := job.Payload.PinnedDeployment()
	if pinned.Commit != deployment.Commit || pinned.Entrypoint != deployment.Entrypoint || pinned.ScriptLang != deployment.ScriptLang ||
		pinned.MaxConcurrent == nil || *pinned.MaxConcurrent != maxConcurrent || pinned.DeploymentID == nil || *pinned.DeploymentID != deploymentID ||
		pinned.BundleDigest != deployment.BundleDigest || pinned.BundleURI != deployment.BundleURI || pinned.ObjectURI != deployment.ObjectURI ||
		len(pinned.Actions) != 1 || pinned.Actions["selected"].Action != "selected" {
		t.Fatalf("reconstructed deployment = %#v", pinned)
	}

	legacyJSON, err := json.Marshal(map[string]any{
		"app":        "echo",
		"action":     "selected",
		"deployment": deployment,
	})
	if err != nil {
		t.Fatal(err)
	}
	var legacy JobPayload
	if err := json.Unmarshal(legacyJSON, &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Deployment == nil || legacy.PinnedDeployment().Actions["unrelated"].Action != "unrelated" {
		t.Fatalf("legacy deployment was not restored: %#v", legacy.PinnedDeployment())
	}
}

func TestLocalStoreClaimCompleteAndResumeLifecycle(t *testing.T) {
	store := NewLocalStore(t.TempDir() + "/state.json")
	exerciseStoreLifecycle(t, store)
}

func TestLocalStoreEncryptsInputAtRest(t *testing.T) {
	store := NewLocalStore(t.TempDir() + "/state.json")
	store.ConfigureInputCrypto("test-secret-key", "")
	deployment := contract.Deployment{
		Workspace: "default",
		App:       "echo",
		Commit:    "commit-a",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo", Command: []string{"helper"}},
		},
	}
	run := NewRun("windforce", "run-encrypted", "echo", "echo", deployment, json.RawMessage(`{"message":"secret"}`))
	job := NewActionJob(run, nil)
	if err := store.CreateRunAndEnqueue(context.Background(), run, job); err != nil {
		t.Fatalf("CreateRunAndEnqueue returned error: %v", err)
	}

	snapshot, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	storedRun := snapshot.Runs[run.ID]
	storedJob := snapshot.Jobs[job.ID]
	if !wfcrypto.IsEnc(storedRun.Input) || !wfcrypto.IsEnc(storedJob.Payload.Input) {
		t.Fatalf("stored input is not encrypted: run=%s job=%s", storedRun.Input, storedJob.Payload.Input)
	}

	publicRun, err := store.GetRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if string(publicRun.Input) != `{"message":"secret"}` {
		t.Fatalf("public run input = %s", publicRun.Input)
	}
	claimed, _, err := store.ClaimJob(context.Background(), "worker-a", time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob returned error: %v", err)
	}
	if !wfcrypto.IsEnc(claimed.Payload.Input) {
		t.Fatalf("claimed input should remain encrypted until worker decrypt: %s", claimed.Payload.Input)
	}
	plain, err := store.DecryptInput(context.Background(), "default", claimed.Payload.Input)
	if err != nil {
		t.Fatalf("DecryptInput returned error: %v", err)
	}
	if string(plain) != `{"message":"secret"}` {
		t.Fatalf("plain input = %s", plain)
	}
}

func TestLocalStoreEncryptsResultAtRest(t *testing.T) {
	ctx := context.Background()
	store := NewLocalStore(t.TempDir() + "/state.json")
	store.ConfigureInputCrypto("test-secret-key", "")
	deployment := contract.Deployment{
		Workspace: "default",
		App:       "echo",
		Commit:    "commit-a",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo", Command: []string{"helper"}},
		},
	}
	run := NewRun("windforce", "run-result-encrypted", "echo", "echo", deployment, json.RawMessage(`{"message":"secret"}`))
	job := NewActionJob(run, nil)
	if err := store.CreateRunAndEnqueue(ctx, run, job); err != nil {
		t.Fatalf("CreateRunAndEnqueue returned error: %v", err)
	}
	if _, _, err := store.ClaimJob(ctx, "worker-skip", time.Minute); err != nil {
		t.Fatalf("ClaimJob returned error: %v", err)
	}
	lease := Lease{JobID: job.ID, WorkerID: "worker-skip", Attempt: 1}
	if err := store.CompleteJobSucceeded(ctx, lease, contract.JobResult{
		JobID:      job.ID,
		App:        "echo",
		Action:     "echo",
		Output:     json.RawMessage(`{"ok":true}`),
		ExitCode:   0,
		DurationMs: 12,
	}); err != nil {
		t.Fatalf("CompleteJobSucceeded returned error: %v", err)
	}

	snapshot, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	storedRun := snapshot.Runs[run.ID]
	if !wfcrypto.IsEnc(storedRun.Output) {
		t.Fatalf("stored run output is not encrypted: %s", storedRun.Output)
	}
	if storedRun.Result == nil || !wfcrypto.IsEnc(storedRun.Result.Output) {
		t.Fatalf("stored result output is not encrypted: %#v", storedRun.Result)
	}

	publicRun, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetRun returned error: %v", err)
	}
	if string(publicRun.Output) != `{"ok":true}` {
		t.Fatalf("public run output = %s", publicRun.Output)
	}
	if publicRun.Result == nil || string(publicRun.Result.Output) != `{"ok":true}` {
		t.Fatalf("public result output = %#v", publicRun.Result)
	}
	_, publicJobRun, found, err := store.GetJob(ctx, "default", job.ID)
	if err != nil {
		t.Fatalf("GetJob returned error: %v", err)
	}
	if !found || publicJobRun.Result == nil || string(publicJobRun.Result.Output) != `{"ok":true}` {
		t.Fatalf("public job run result = found:%v run:%#v", found, publicJobRun)
	}

	cancelRun := NewRun("windforce", "run-cancel-result-encrypted", "echo", "echo", deployment, json.RawMessage(`{"message":"cancel"}`))
	cancelJob := NewActionJob(cancelRun, nil)
	if err := store.CreateRunAndEnqueue(ctx, cancelRun, cancelJob); err != nil {
		t.Fatalf("CreateRunAndEnqueue cancel returned error: %v", err)
	}
	if _, err := store.CancelJob(ctx, "default", cancelJob.ID, "operator@example.test", "operator canceled"); err != nil {
		t.Fatalf("CancelJob returned error: %v", err)
	}
	snapshot, err = store.Load(ctx)
	if err != nil {
		t.Fatalf("Load after cancel returned error: %v", err)
	}
	storedCancelRun := snapshot.Runs[cancelRun.ID]
	if storedCancelRun.Result == nil || !wfcrypto.IsEnc(storedCancelRun.Result.Output) {
		t.Fatalf("stored canceled result output is not encrypted: %#v", storedCancelRun.Result)
	}
	_, publicCancelRun, found, err := store.GetJob(ctx, "default", cancelJob.ID)
	if err != nil {
		t.Fatalf("GetJob canceled returned error: %v", err)
	}
	if !found || publicCancelRun.Result == nil {
		t.Fatalf("public canceled result missing: found:%v run:%#v", found, publicCancelRun)
	}
	assertCanceledOutput(t, publicCancelRun.Result.Output, cancelBeforeExecutionMessage)
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

func TestPostgresStoreCreateRunAndEnqueueReturnsConflict(t *testing.T) {
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

	runID := NewID("run")
	deployment := contract.Deployment{
		Workspace: "postgres-conflict-test",
		App:       "echo",
		Commit:    "commit-a",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo"},
		},
	}
	run := NewRun("test", runID, "echo", "echo", deployment, json.RawMessage(`{}`))
	job := NewActionJob(run, json.RawMessage(`{}`))
	defer func() {
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM run_events WHERE run_id=$1`, runID)
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM jobs WHERE run_id=$1`, runID)
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM runs WHERE id=$1`, runID)
	}()

	if err := store.CreateRunAndEnqueue(context.Background(), run, job); err != nil {
		t.Fatalf("first CreateRunAndEnqueue returned error: %v", err)
	}
	duplicate := NewActionJob(run, json.RawMessage(`{}`))
	err = store.CreateRunAndEnqueue(context.Background(), run, duplicate)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate CreateRunAndEnqueue error = %v, want ErrConflict", err)
	}
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

func TestJobSummarySkipsBreakdownsWithOnlyOldCompletedJobs(t *testing.T) {
	deployment := contract.Deployment{
		Workspace: "default",
		App:       "echo",
		Commit:    "commit-a",
		Tag:       "workers",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo", Command: []string{"helper"}},
		},
	}
	oldCompletedAt := time.Now().UTC().Add(-2 * time.Hour)
	run := NewRun("windforce", "old-run", "echo", "echo", deployment, json.RawMessage(`{}`))
	run.State = RunSucceeded
	run.CreatedAt = oldCompletedAt
	run.UpdatedAt = oldCompletedAt
	job := NewActionJob(run, nil)
	job.ID = "old-job"
	job.State = JobSucceeded
	job.CreatedAt = oldCompletedAt
	job.UpdatedAt = oldCompletedAt

	summary := summarizeJobs([]jobRunRecord{{Job: job, Run: run}}, "default", time.Hour)
	if summary.JobSummaryCounts != (JobSummaryCounts{}) {
		t.Fatalf("summary counts = %#v, want zero", summary.JobSummaryCounts)
	}
	if len(summary.ByTag) != 0 || len(summary.ByApp) != 0 {
		t.Fatalf("summary breakdowns = tags:%#v apps:%#v, want empty", summary.ByTag, summary.ByApp)
	}
}

func TestLocalStoreHeartbeatExtendsLease(t *testing.T) {
	store := NewLocalStore(t.TempDir() + "/state.json")
	exerciseStoreHeartbeatExtendsLease(t, store)
}

func TestPostgresStoreHeartbeatExtendsLease(t *testing.T) {
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
	exerciseStoreHeartbeatExtendsLease(t, store)
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

	claimed, _, err := store.ClaimJobForWorker(context.Background(), "worker-blue", []string{"blue"}, nil, time.Minute)
	if err != nil {
		t.Fatalf("ClaimJobForTags returned error: %v", err)
	}
	if claimed.Payload.Action != "blue" || claimed.Payload.Tag != "blue" {
		t.Fatalf("claimed job = %#v", claimed.Payload)
	}
	if _, _, err := store.ClaimJobForWorker(context.Background(), "worker-green", []string{"green"}, nil, time.Minute); err != ErrNoQueuedJob {
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
	job.Payload.FlowRunID = "flow-run-a"
	job.Payload.FlowStepID = "flow-step-a"
	if job.Payload.CreatedBy != "runner@example.test" || job.Payload.PermissionedAs != "delegate@example.test" {
		t.Fatalf("job actor = %q/%q", job.Payload.CreatedBy, job.Payload.PermissionedAs)
	}
	item := newJobListItem("ws-a", job, run)
	if item.CreatedBy != "runner@example.test" || item.PermissionedAs != "delegate@example.test" {
		t.Fatalf("list actor = %q/%q", item.CreatedBy, item.PermissionedAs)
	}
	if item.Entrypoint != "main.ts" {
		t.Fatalf("list entrypoint = %q, want main.ts", item.Entrypoint)
	}
	if item.FlowRunID == nil || *item.FlowRunID != "flow-run-a" || item.FlowStepID == nil || *item.FlowStepID != "flow-step-a" {
		t.Fatalf("list flow linkage = %v/%v", item.FlowRunID, item.FlowStepID)
	}
}

func TestJobListFailureSnippetMatchesCanonicalResult(t *testing.T) {
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
	run.State = RunFailed
	run.Result = &contract.JobResult{
		Output: json.RawMessage(`{"name":"TargetError","message":"target rejected"}`),
		Error:  "fallback should not win",
	}
	job := NewActionJob(run, nil)
	job.State = JobFailed

	item := newJobListItem("ws-a", job, run)
	if item.ErrorSnippet == nil || *item.ErrorSnippet != "TargetError: target rejected" {
		t.Fatalf("error snippet = %v, want TargetError: target rejected", item.ErrorSnippet)
	}
}

func TestJobListFailureSnippetCapsWithCanonicalEllipsis(t *testing.T) {
	run := Run{
		State: RunFailed,
		Result: &contract.JobResult{
			Error: strings.Repeat("x", errorSnippetMax+5),
		},
	}
	snippet := failureSnippet("failure", run)
	if snippet == nil {
		t.Fatal("error snippet is nil")
	}
	if got := len([]rune(*snippet)); got != errorSnippetMax+1 {
		t.Fatalf("snippet rune length = %d, want %d", got, errorSnippetMax+1)
	}
	if !strings.HasSuffix(*snippet, "\u2026") {
		t.Fatalf("snippet = %q, want canonical ellipsis suffix", *snippet)
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

func exerciseStoreHeartbeatExtendsLease(t *testing.T, store Store) {
	t.Helper()
	ttl := 100 * time.Millisecond
	deployment := contract.Deployment{
		Workspace: "default",
		App:       "echo",
		Commit:    "commit-a",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo", Command: []string{"helper"}},
		},
	}
	run := NewRun("windforce", "run-heartbeat", "echo", "echo", deployment, json.RawMessage(`{}`))
	job := NewActionJob(run, nil)
	if err := store.CreateRunAndEnqueue(context.Background(), run, job); err != nil {
		t.Fatalf("CreateRunAndEnqueue returned error: %v", err)
	}
	claimed, lease, err := store.ClaimJob(context.Background(), "worker-a", ttl)
	if err != nil {
		t.Fatalf("ClaimJob returned error: %v", err)
	}
	if claimed.ID != job.ID {
		t.Fatalf("claimed job = %s, want %s", claimed.ID, job.ID)
	}
	time.Sleep(70 * time.Millisecond)
	heartbeat, err := store.HeartbeatJob(context.Background(), lease, ttl)
	if err != nil {
		t.Fatalf("HeartbeatJob returned error: %v", err)
	}
	if !heartbeat.StillOwned {
		t.Fatalf("HeartbeatJob StillOwned = false")
	}
	time.Sleep(50 * time.Millisecond)
	_, _, err = store.ClaimJob(context.Background(), "worker-b", ttl)
	if !errors.Is(err, ErrNoQueuedJob) {
		t.Fatalf("ClaimJob after heartbeat error = %v, want ErrNoQueuedJob", err)
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
	variable, found, err = store.GetVariableExact(ctx, "ws-a", "echo", "config/token")
	if err != nil {
		t.Fatalf("GetVariableExact scoped returned error: %v", err)
	}
	if !found || variable.Value != "scoped" || variable.AppKey != "echo" {
		t.Fatalf("exact scoped variable found=%v variable=%#v", found, variable)
	}
	variable, found, err = store.GetVariable(ctx, "ws-a", "other", "config/token")
	if err != nil {
		t.Fatalf("GetVariable shared fallback returned error: %v", err)
	}
	if !found || variable.Value != "shared" || variable.AppKey != "" {
		t.Fatalf("shared variable found=%v variable=%#v", found, variable)
	}
	if variable, found, err = store.GetVariableExact(ctx, "ws-a", "other", "config/token"); err != nil {
		t.Fatalf("GetVariableExact foreign scope returned error: %v", err)
	}
	if found {
		t.Fatalf("exact foreign scope found=%v variable=%#v, want miss", found, variable)
	}
	variable, found, err = store.GetVariableExact(ctx, "ws-a", "", "config/token")
	if err != nil {
		t.Fatalf("GetVariableExact shared returned error: %v", err)
	}
	if !found || variable.Value != "shared" || variable.AppKey != "" {
		t.Fatalf("exact shared variable found=%v variable=%#v", found, variable)
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
	if err := store.SetResource(ctx, "ws-a", "browser/default", nil, "", "default settings"); err != nil {
		t.Fatalf("SetResource nil value returned error: %v", err)
	}
	resource, found, err = store.GetResource(ctx, "ws-a", "browser/default")
	if err != nil {
		t.Fatalf("GetResource nil value returned error: %v", err)
	}
	if !found || string(resource.Value) != "{}" {
		t.Fatalf("nil resource found=%v value=%s", found, resource.Value)
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

	claimed, firstLease, err := store.ClaimJobForWorker(ctx, "worker-first", []string{"default"}, nil, time.Minute)
	if err != nil {
		t.Fatalf("first ClaimJobForTags returned error: %v", err)
	}
	if claimed.ID != firstEcho.ID {
		t.Fatalf("first claimed job = %q, want %q", claimed.ID, firstEcho.ID)
	}

	claimed, _, err = store.ClaimJobForWorker(ctx, "worker-other", []string{"default"}, nil, time.Minute)
	if err != nil {
		t.Fatalf("second ClaimJobForTags returned error: %v", err)
	}
	if claimed.ID != other.ID {
		t.Fatalf("second claimed job = %q, want other app job %q", claimed.ID, other.ID)
	}

	if _, _, err := store.ClaimJobForWorker(ctx, "worker-blocked", []string{"default"}, nil, time.Minute); err != ErrNoQueuedJob {
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

	claimed, _, err = store.ClaimJobForWorker(ctx, "worker-next", []string{"default"}, nil, time.Minute)
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
	completedItems, err := store.ListJobs(context.Background(), JobListQuery{
		WorkspaceID: "default",
		Status:      "completed",
		AppKey:      "echo",
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("ListJobs completed returned error: %v", err)
	}
	if len(completedItems) != 1 || completedItems[0].Worker == nil || *completedItems[0].Worker != "worker-a" {
		t.Fatalf("completed items = %#v", completedItems)
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
	cancelResult, err := store.CancelJob(context.Background(), "default", retryJob.ID, "operator@example.test", "operator canceled")
	if err != nil {
		t.Fatalf("CancelJob returned error: %v", err)
	}
	if !cancelResult.Found || !cancelResult.CompletedNow || cancelResult.SoftCanceled || cancelResult.AlreadyCompleted {
		t.Fatalf("cancel result = %#v", cancelResult)
	}
	_, canceledRetryRun, found, err := store.GetJob(context.Background(), "default", retryJob.ID)
	if err != nil {
		t.Fatalf("GetJob canceled retry returned error: %v", err)
	}
	if !found || canceledRetryRun.Result == nil {
		t.Fatalf("canceled retry result missing: found:%v run:%#v", found, canceledRetryRun)
	}
	assertCanceledOutput(t, canceledRetryRun.Result.Output, cancelBeforeExecutionMessage)
	canceledAgain, err := store.CancelJob(context.Background(), "default", retryJob.ID, "operator@example.test", "")
	if err != nil {
		t.Fatalf("CancelJob second call returned error: %v", err)
	}
	if !canceledAgain.Found || !canceledAgain.AlreadyCompleted {
		t.Fatalf("second cancel result = %#v", canceledAgain)
	}

	softRun := NewRun("windforce", "run-soft-cancel", "echo", "echo", deployment, json.RawMessage(`{"message":"soft"}`))
	softJob := NewActionJob(softRun, nil)
	if err := store.CreateRunAndEnqueue(context.Background(), softRun, softJob); err != nil {
		t.Fatalf("CreateRunAndEnqueue soft cancel returned error: %v", err)
	}
	_, softLease, err := store.ClaimJob(context.Background(), "worker-soft", time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob soft cancel returned error: %v", err)
	}
	softCancel, err := store.CancelJob(context.Background(), "default", softJob.ID, "operator@example.test", "stop running")
	if err != nil {
		t.Fatalf("CancelJob soft cancel returned error: %v", err)
	}
	if !softCancel.Found || !softCancel.SoftCanceled || softCancel.CompletedNow || softCancel.AlreadyCompleted {
		t.Fatalf("soft cancel result = %#v", softCancel)
	}
	runningJob, _, found, err := store.GetJob(context.Background(), "default", softJob.ID)
	if err != nil {
		t.Fatalf("GetJob soft-canceled running returned error: %v", err)
	}
	if !found || runningJob.State != JobRunning {
		t.Fatalf("soft-canceled job state = found:%v job:%#v", found, runningJob)
	}
	if runningJob.CanceledBy == nil || *runningJob.CanceledBy != "operator@example.test" {
		t.Fatalf("soft-canceled job canceled_by = %v", runningJob.CanceledBy)
	}
	if runningJob.CanceledReason == nil || *runningJob.CanceledReason != "stop running" {
		t.Fatalf("soft-canceled job canceled_reason = %v", runningJob.CanceledReason)
	}
	if err := store.CompleteJobSucceeded(context.Background(), softLease, contract.JobResult{JobID: softJob.ID, App: "echo", Action: "echo", ExitCode: 0, Output: json.RawMessage(`{"ok":true}`)}); err != nil {
		t.Fatalf("CompleteJobSucceeded soft-canceled job returned error: %v", err)
	}
	completedSoftJob, completedSoftRun, found, err := store.GetJob(context.Background(), "default", softJob.ID)
	if err != nil {
		t.Fatalf("GetJob completed soft-canceled returned error: %v", err)
	}
	if !found || completedSoftJob.State != JobFailed || completedSoftRun.State != RunCanceled {
		t.Fatalf("completed soft-canceled state = found:%v job:%s run:%s", found, completedSoftJob.State, completedSoftRun.State)
	}
	if completedSoftJob.CanceledBy == nil || *completedSoftJob.CanceledBy != "operator@example.test" {
		t.Fatalf("completed soft-canceled canceled_by = %v", completedSoftJob.CanceledBy)
	}
	if completedSoftRun.Result == nil {
		t.Fatalf("completed soft-canceled result is nil")
	}
	assertCanceledOutput(t, completedSoftRun.Result.Output, cancelDuringExecutionMessage)

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
			if item.CanceledBy == nil || *item.CanceledBy != "operator@example.test" {
				t.Fatalf("canceled_by = %v, want operator@example.test", item.CanceledBy)
			}
			if item.CanceledReason == nil || *item.CanceledReason != "operator canceled" {
				t.Fatalf("canceled_reason = %v, want operator canceled", item.CanceledReason)
			}
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

func assertCanceledOutput(t *testing.T, output json.RawMessage, message string) {
	t.Helper()
	var body struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(output, &body); err != nil {
		t.Fatalf("canceled output is not JSON: %v", err)
	}
	if body.Name != "Canceled" || body.Message != message {
		t.Fatalf("canceled output = %s, want Canceled/%q", output, message)
	}
}

func TestLocalStorePruneSettledJobs(t *testing.T) {
	store := NewLocalStore(t.TempDir() + "/state.json")
	exercisePruneSettledJobs(t, store)
}

func TestPostgresStorePruneSettledJobs(t *testing.T) {
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
	exercisePruneSettledJobs(t, store)
}

func exercisePruneSettledJobs(t *testing.T, store Store) {
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

	settledRun := NewRun("windforce", "run-settled-"+NewID("run"), "echo", "echo", deployment, json.RawMessage(`{}`))
	settledJob := NewActionJob(settledRun, nil)
	if err := store.CreateRunAndEnqueue(ctx, settledRun, settledJob); err != nil {
		t.Fatalf("CreateRunAndEnqueue(settled) returned error: %v", err)
	}
	claimed, lease, err := store.ClaimJob(ctx, "worker-a", time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob returned error: %v", err)
	}
	if claimed.ID != settledJob.ID {
		t.Fatalf("claimed job = %s, want %s", claimed.ID, settledJob.ID)
	}
	if err := store.AppendLogs(ctx, settledJob.ID, "default", "settled log line\n"); err != nil {
		t.Fatalf("AppendLogs returned error: %v", err)
	}
	if err := store.CompleteJobSucceeded(ctx, lease, contract.JobResult{App: "echo", Action: "echo", Output: json.RawMessage(`{"ok":true}`)}); err != nil {
		t.Fatalf("CompleteJobSucceeded returned error: %v", err)
	}

	queuedRun := NewRun("windforce", "run-queued-"+NewID("run"), "echo", "echo", deployment, json.RawMessage(`{}`))
	queuedJob := NewActionJob(queuedRun, nil)
	if err := store.CreateRunAndEnqueue(ctx, queuedRun, queuedJob); err != nil {
		t.Fatalf("CreateRunAndEnqueue(queued) returned error: %v", err)
	}

	canceledRun := NewRun("windforce", "run-canceled-"+NewID("run"), "echo", "echo", deployment, json.RawMessage(`{}`))
	canceledJob := NewActionJob(canceledRun, nil)
	if err := store.CreateRunAndEnqueue(ctx, canceledRun, canceledJob); err != nil {
		t.Fatalf("CreateRunAndEnqueue(canceled) returned error: %v", err)
	}
	if _, err := store.CancelJob(ctx, "default", canceledJob.ID, "tester", "cleanup"); err != nil {
		t.Fatalf("CancelJob returned error: %v", err)
	}

	// Success TTL elapsed, failure TTL disabled: only the succeeded run goes.
	pruned, err := store.PruneSettledJobs(ctx, time.Now().UTC().Add(time.Hour), time.Time{})
	if err != nil {
		t.Fatalf("PruneSettledJobs returned error: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("pruned = %d, want 1", pruned)
	}
	if _, _, found, err := store.GetJob(ctx, "default", canceledJob.ID); err != nil {
		t.Fatalf("GetJob(canceled) returned error: %v", err)
	} else if !found {
		t.Fatalf("canceled job was pruned by the success TTL")
	}

	// Failure TTL elapsed: the canceled run goes too.
	prunedFailures, err := store.PruneSettledJobs(ctx, time.Time{}, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("PruneSettledJobs(failures) returned error: %v", err)
	}
	if prunedFailures != 1 {
		t.Fatalf("pruned failures = %d, want 1", prunedFailures)
	}

	if _, _, found, err := store.GetJob(ctx, "default", settledJob.ID); err != nil {
		t.Fatalf("GetJob(settled) returned error: %v", err)
	} else if found {
		t.Fatalf("settled job still exists after prune")
	}
	if _, found, err := store.GetLogs(ctx, "default", settledJob.ID); err != nil {
		t.Fatalf("GetLogs returned error: %v", err)
	} else if found {
		t.Fatalf("settled job logs still exist after prune")
	}
	if _, _, found, err := store.GetJob(ctx, "default", queuedJob.ID); err != nil {
		t.Fatalf("GetJob(queued) returned error: %v", err)
	} else if !found {
		t.Fatalf("queued job was pruned")
	}
	summary, err := store.JobSummary(ctx, "default", 24*time.Hour)
	if err != nil {
		t.Fatalf("JobSummary returned error: %v", err)
	}
	if summary.QueuedCount != 1 {
		t.Fatalf("queued count after prune = %d, want 1", summary.QueuedCount)
	}

	again, err := store.PruneSettledJobs(ctx, time.Now().UTC().Add(time.Hour), time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("second PruneSettledJobs returned error: %v", err)
	}
	if again != 0 {
		t.Fatalf("second prune = %d, want 0", again)
	}

	// A queued run with no progress past the stuck cutoff expires into the
	// failure family and becomes prunable; the fresh queued run above must
	// have been expired by the same call, so recreate one that stays live.
	expired, err := store.ExpireStuckJobs(ctx, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("ExpireStuckJobs returned error: %v", err)
	}
	if expired != 1 {
		t.Fatalf("expired = %d, want 1", expired)
	}
	if job, run, found, err := store.GetJob(ctx, "default", queuedJob.ID); err != nil || !found {
		t.Fatalf("GetJob(expired) = found %v, err %v", found, err)
	} else {
		if run.State != RunExpired {
			t.Fatalf("stuck run state = %s, want %s", run.State, RunExpired)
		}
		if job.State != JobFailed {
			t.Fatalf("stuck job state = %s, want %s", job.State, JobFailed)
		}
	}
	prunedExpired, err := store.PruneSettledJobs(ctx, time.Time{}, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("PruneSettledJobs(expired) returned error: %v", err)
	}
	if prunedExpired != 1 {
		t.Fatalf("pruned expired = %d, want 1", prunedExpired)
	}
}

func TestClaimJobForWorkerLabelSubset(t *testing.T) {
	store := NewLocalStore(t.TempDir() + "/state.json")
	plainDeployment := contract.Deployment{
		Workspace: "ws-a",
		App:       "plain",
		Commit:    "commit-a",
		Actions:   map[string]contract.Action{"run": {Action: "run", Command: []string{"helper"}}},
	}
	browserDeployment := contract.Deployment{
		Workspace:      "ws-a",
		App:            "browser-app",
		Commit:         "commit-b",
		RequiredLabels: []string{"browser", "kr"},
		Actions:        map[string]contract.Action{"run": {Action: "run", Command: []string{"helper"}}},
	}
	for name, deployment := range map[string]contract.Deployment{
		"plain": plainDeployment, "browser": browserDeployment,
	} {
		run := NewRun("windforce", "run-"+name, deployment.App, "run", deployment, json.RawMessage(`{}`))
		job := NewActionJob(run, nil)
		if err := store.CreateRunAndEnqueue(context.Background(), run, job); err != nil {
			t.Fatalf("enqueue %s: %v", name, err)
		}
	}

	// A worker with no labels claims only unconstrained jobs (ADR 0009 —
	// the deliberate flip of the old claim-everything default).
	claimed, _, err := store.ClaimJobForWorker(context.Background(), "worker-bare", nil, nil, time.Minute)
	if err != nil {
		t.Fatalf("bare claim: %v", err)
	}
	if claimed.Payload.App != "plain" {
		t.Fatalf("bare worker claimed %q, want plain", claimed.Payload.App)
	}
	if _, _, err := store.ClaimJobForWorker(context.Background(), "worker-bare", nil, nil, time.Minute); err != ErrNoQueuedJob {
		t.Fatalf("bare worker must not claim labeled jobs: %v", err)
	}

	// A partial label set is not enough; the superset claims it.
	if _, _, err := store.ClaimJobForWorker(context.Background(), "worker-partial", nil, []string{"browser"}, time.Minute); err != ErrNoQueuedJob {
		t.Fatalf("partial labels must not claim: %v", err)
	}
	claimed, _, err = store.ClaimJobForWorker(context.Background(), "worker-full", nil, []string{"browser", "kr", "extra"}, time.Minute)
	if err != nil {
		t.Fatalf("superset claim: %v", err)
	}
	if claimed.Payload.App != "browser-app" {
		t.Fatalf("superset worker claimed %q", claimed.Payload.App)
	}
}

func TestClaimHonorsLegacyCapabilityJobs(t *testing.T) {
	store := NewLocalStore(t.TempDir() + "/state.json")
	legacy := contract.Deployment{
		Workspace:            "ws-a",
		App:                  "legacy",
		Commit:               "commit-a",
		RequiredCapabilities: []string{"browser"},
		Actions:              map[string]contract.Action{"run": {Action: "run", Command: []string{"helper"}}},
	}
	run := NewRun("windforce", "run-legacy", legacy.App, "run", legacy, json.RawMessage(`{}`))
	job := NewActionJob(run, nil)
	// Simulate a pre-labels job: capabilities pinned, labels absent.
	job.Payload.RequiredLabels = nil
	if err := store.CreateRunAndEnqueue(context.Background(), run, job); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ClaimJobForWorker(context.Background(), "worker-bare", nil, nil, time.Minute); err != ErrNoQueuedJob {
		t.Fatalf("bare worker must not claim legacy capability job: %v", err)
	}
	claimed, _, err := store.ClaimJobForWorker(context.Background(), "worker-browser", nil, []string{"browser"}, time.Minute)
	if err != nil {
		t.Fatalf("browser-label claim of legacy job: %v", err)
	}
	if claimed.Payload.App != "legacy" {
		t.Fatalf("claimed %q", claimed.Payload.App)
	}
}

func TestWorkerRegistryLifecycle(t *testing.T) {
	store := NewLocalStore(t.TempDir() + "/state.json")
	ctx := context.Background()
	if err := store.RegisterWorker(ctx, WorkerRecord{ID: "w-1", Group: "default", Labels: []string{"browser"}, Tags: []string{"default"}}); err != nil {
		t.Fatal(err)
	}
	workers, err := store.ListWorkers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(workers) != 1 || workers[0].ID != "w-1" || workers[0].Slots != 1 ||
		!workers[0].Live(time.Now()) {
		t.Fatalf("workers = %#v", workers)
	}
	if err := store.HeartbeatWorker(ctx, "w-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.HeartbeatWorker(ctx, "ghost"); err == nil {
		t.Fatal("heartbeat for unknown worker must fail")
	}
	if err := store.DeregisterWorker(ctx, "w-1"); err != nil {
		t.Fatal(err)
	}
	workers, err = store.ListWorkers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(workers) != 0 {
		t.Fatalf("workers after deregister = %#v", workers)
	}
}
