package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/imprun/windforce-lite/internal/contract"
)

const runColumns = `
	id, adapter, app, action, state, deployment, input, output, result, error,
	task_id, correlation_id, env, created_at, updated_at, expires_at
`

const jobColumns = `
	id, run_id, state, kind, payload, priority, attempt, lease_owner,
	lease_expires_at, created_at, updated_at
`

const humanTaskColumns = `
	id, run_id, state, title, description, schema, resume_input,
	created_at, completed_at, expires_at
`

type PostgresStore struct {
	pool *pgxpool.Pool
}

func OpenPostgresStore(ctx context.Context, databaseURL string) (*PostgresStore, error) {
	if databaseURL == "" {
		return nil, errors.New("database URL is required")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &PostgresStore{pool: pool}, nil
}

func (s *PostgresStore) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

func (s *PostgresStore) Migrate(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS runs (
    id TEXT PRIMARY KEY,
    adapter TEXT NOT NULL,
    app TEXT NOT NULL,
    action TEXT NOT NULL,
    state TEXT NOT NULL,
    deployment JSONB NOT NULL,
    input JSONB NOT NULL,
    output JSONB,
    result JSONB,
    error JSONB,
    task_id TEXT,
    correlation_id TEXT,
    env JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS jobs (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES runs(id),
    state TEXT NOT NULL,
    kind TEXT NOT NULL,
    payload JSONB NOT NULL,
    priority INTEGER NOT NULL DEFAULT 100,
    attempt INTEGER NOT NULL DEFAULT 0,
    lease_owner TEXT,
    lease_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS human_tasks (
    id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES runs(id),
    state TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT,
    schema JSONB,
    resume_input JSONB,
    token_hash TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS run_events (
    id BIGSERIAL PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES runs(id),
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS job_logs (
    job_id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL DEFAULT 'default',
    logs TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE runs ADD COLUMN IF NOT EXISTS result JSONB;
ALTER TABLE runs ADD COLUMN IF NOT EXISTS correlation_id TEXT;
ALTER TABLE runs ADD COLUMN IF NOT EXISTS env JSONB;
ALTER TABLE job_logs ADD COLUMN IF NOT EXISTS workspace_id TEXT NOT NULL DEFAULT 'default';

CREATE INDEX IF NOT EXISTS jobs_claim_idx
    ON jobs (priority, created_at)
    WHERE state = 'queued';

CREATE INDEX IF NOT EXISTS jobs_lease_idx
    ON jobs (lease_expires_at)
    WHERE state = 'running';

CREATE INDEX IF NOT EXISTS human_tasks_pending_idx
    ON human_tasks (created_at)
    WHERE state = 'pending';

CREATE INDEX IF NOT EXISTS runs_correlation_id_idx
    ON runs (correlation_id)
    WHERE correlation_id IS NOT NULL;
`)
	return err
}

func (s *PostgresStore) AppendLogs(ctx context.Context, jobID string, workspaceID string, chunk string) error {
	if chunk == "" {
		return nil
	}
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	_, err := s.pool.Exec(ctx, `
INSERT INTO job_logs (job_id, workspace_id, logs)
VALUES ($1, $2, $3)
ON CONFLICT (job_id) DO UPDATE SET logs = job_logs.logs || EXCLUDED.logs
`, jobID, workspaceID, chunk)
	return err
}

func (s *PostgresStore) GetLogs(ctx context.Context, workspaceID string, jobID string) (string, bool, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	var logs string
	err := s.pool.QueryRow(ctx, `
SELECT logs FROM job_logs
WHERE job_id=$1 AND workspace_id=$2
`, jobID, workspaceID).Scan(&logs)
	if err == nil {
		return logs, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, err
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM jobs
    WHERE id=$1
      AND COALESCE(NULLIF(payload->>'workspace', ''), NULLIF(payload->'deployment'->>'workspace', ''), 'default')=$2
)
`, jobID, workspaceID).Scan(&exists); err != nil {
		return "", false, err
	}
	return "", exists, nil
}

func (s *PostgresStore) GetJob(ctx context.Context, workspaceID string, jobID string) (Job, Run, bool, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	job, err := scanJob(s.pool.QueryRow(ctx, `
SELECT `+jobColumns+`
FROM jobs
WHERE id=$1
  AND COALESCE(NULLIF(payload->>'workspace', ''), NULLIF(payload->'deployment'->>'workspace', ''), 'default')=$2
`, jobID, workspaceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, Run{}, false, nil
	}
	if err != nil {
		return Job{}, Run{}, false, err
	}
	run, err := scanRun(s.pool.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id=$1`, job.RunID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, Run{}, false, fmt.Errorf("%w: run %q", ErrNotFound, job.RunID)
	}
	if err != nil {
		return Job{}, Run{}, false, err
	}
	return job, run, true, nil
}

func (s *PostgresStore) ListJobs(ctx context.Context, query JobListQuery) ([]JobListItem, error) {
	workspaceID := contract.NormalizeWorkspace(query.WorkspaceID)
	rows, err := s.pool.Query(ctx, `
SELECT id
FROM jobs
WHERE COALESCE(NULLIF(payload->>'workspace', ''), NULLIF(payload->'deployment'->>'workspace', ''), 'default')=$1
`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []jobRunRecord{}
	for rows.Next() {
		var jobID string
		if err := rows.Scan(&jobID); err != nil {
			return nil, err
		}
		job, run, found, err := s.GetJob(ctx, workspaceID, jobID)
		if err != nil {
			return nil, err
		}
		if found {
			records = append(records, jobRunRecord{Job: job, Run: run})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	query.WorkspaceID = workspaceID
	return listJobsFromRecords(records, query), nil
}

func (s *PostgresStore) JobSummary(ctx context.Context, workspaceID string, recent time.Duration) (JobSummary, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	rows, err := s.pool.Query(ctx, `
SELECT id
FROM jobs
WHERE COALESCE(NULLIF(payload->>'workspace', ''), NULLIF(payload->'deployment'->>'workspace', ''), 'default')=$1
`, workspaceID)
	if err != nil {
		return JobSummary{}, err
	}
	defer rows.Close()
	records := []jobRunRecord{}
	for rows.Next() {
		var jobID string
		if err := rows.Scan(&jobID); err != nil {
			return JobSummary{}, err
		}
		job, run, found, err := s.GetJob(ctx, workspaceID, jobID)
		if err != nil {
			return JobSummary{}, err
		}
		if found {
			records = append(records, jobRunRecord{Job: job, Run: run})
		}
	}
	if err := rows.Err(); err != nil {
		return JobSummary{}, err
	}
	return summarizeJobs(records, workspaceID, recent), nil
}

func (s *PostgresStore) CreateRunAndEnqueue(ctx context.Context, run Run, job Job) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		now := time.Now().UTC()
		run.CreatedAt = nonZeroTime(run.CreatedAt, now)
		run.UpdatedAt = now
		job.CreatedAt = nonZeroTime(job.CreatedAt, now)
		job.UpdatedAt = now
		if job.Payload.CorrelationID == "" {
			job.Payload.CorrelationID = run.CorrelationID
		}

		if _, err := tx.Exec(ctx, `
INSERT INTO runs (
	id, adapter, app, action, state, deployment, input, output, result, error,
	task_id, correlation_id, env, created_at, updated_at, expires_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
`, run.ID, run.Adapter, run.App, run.Action, string(run.State), mustRaw(run.Deployment), requiredRaw(run.Input),
			nullableRaw(run.Output), nullableResult(run.Result), nullableRaw(run.Error), nullableString(run.TaskID),
			nullableString(run.CorrelationID), nullableStrings(run.Env), run.CreatedAt, run.UpdatedAt, run.ExpiresAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO jobs (
	id, run_id, state, kind, payload, priority, attempt, lease_owner,
	lease_expires_at, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
`, job.ID, job.RunID, string(job.State), job.Kind, mustRaw(job.Payload), job.Priority, job.Attempt,
			nullableString(job.LeaseOwner), job.LeaseExpiresAt, job.CreatedAt, job.UpdatedAt); err != nil {
			return err
		}
		runCreated := eventPayload(run.CorrelationID, map[string]any{"app": run.App, "action": run.Action})
		if err := insertEvent(ctx, tx, run.ID, "run_created", runCreated); err != nil {
			return err
		}
		jobEnqueued := eventPayload(run.CorrelationID, map[string]any{"jobId": job.ID})
		return insertEvent(ctx, tx, run.ID, "job_enqueued", jobEnqueued)
	})
}

func (s *PostgresStore) GetRun(ctx context.Context, runID string) (Run, error) {
	run, err := scanRun(s.pool.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id=$1`, runID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, fmt.Errorf("%w: run %q", ErrNotFound, runID)
	}
	return run, err
}

func (s *PostgresStore) GetHumanTask(ctx context.Context, taskID string) (HumanTask, error) {
	task, err := scanHumanTask(s.pool.QueryRow(ctx, `SELECT `+humanTaskColumns+` FROM human_tasks WHERE id=$1`, taskID))
	if errors.Is(err, pgx.ErrNoRows) {
		return HumanTask{}, fmt.Errorf("%w: human task %q", ErrNotFound, taskID)
	}
	return task, err
}

func (s *PostgresStore) ClaimJob(ctx context.Context, workerID string, leaseTTL time.Duration) (Job, Lease, error) {
	if workerID == "" {
		workerID = NewID("worker")
	}
	if leaseTTL <= 0 {
		leaseTTL = defaultLeaseTime
	}
	var claimed Job
	var lease Lease
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		now := time.Now().UTC()
		if _, err := tx.Exec(ctx, `
UPDATE jobs
SET state='queued', lease_owner=NULL, lease_expires_at=NULL, updated_at=$1
WHERE state='running' AND lease_expires_at < $1
`, now); err != nil {
			return err
		}
		expiresAt := now.Add(leaseTTL)
		job, err := scanJob(tx.QueryRow(ctx, `
WITH claimed AS (
    SELECT id
    FROM jobs
    WHERE state = 'queued'
    ORDER BY priority ASC, created_at ASC
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE jobs
SET state='running',
    lease_owner=$1,
    lease_expires_at=$2,
    attempt=attempt + 1,
    updated_at=$3
WHERE id IN (SELECT id FROM claimed)
RETURNING `+jobColumns, workerID, expiresAt, now))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoQueuedJob
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
UPDATE runs
SET state=$1, updated_at=$2
WHERE id=$3 AND state IN ($4, $5)
`, string(RunRunning), now, job.RunID, string(RunQueued), string(RunResuming)); err != nil {
			return err
		}
		if err := insertEvent(ctx, tx, job.RunID, "job_claimed", eventPayload(job.Payload.CorrelationID, map[string]any{"jobId": job.ID, "workerId": workerID, "attempt": job.Attempt})); err != nil {
			return err
		}
		claimed = job
		lease = Lease{JobID: job.ID, WorkerID: workerID, ExpiresAt: expiresAt, Attempt: job.Attempt, AcquiredAt: now}
		return nil
	})
	if err != nil {
		return Job{}, Lease{}, err
	}
	return claimed, lease, nil
}

func (s *PostgresStore) CompleteJobSucceeded(ctx context.Context, lease Lease, result contract.JobResult) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		job, run, err := leasedJobAndRunPostgres(ctx, tx, lease)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		if err := updateJobComplete(ctx, tx, job.ID, JobSucceeded, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
UPDATE runs
SET state=$1, output=$2, result=$3, error=NULL, task_id=NULL, updated_at=$4
WHERE id=$5
`, string(RunSucceeded), nullableRaw(result.Output), mustRaw(result), now, run.ID); err != nil {
			return err
		}
		return insertEvent(ctx, tx, run.ID, "run_succeeded", eventPayload(run.CorrelationID, map[string]any{"jobId": job.ID}))
	})
}

func (s *PostgresStore) CompleteJobFailed(ctx context.Context, lease Lease, result contract.JobResult) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		job, run, err := leasedJobAndRunPostgres(ctx, tx, lease)
		if err != nil {
			return err
		}
		if result.Error == "" && result.ExitCode != 0 {
			result.Error = fmt.Sprintf("action exited with code %d", result.ExitCode)
		}
		now := time.Now().UTC()
		if err := updateJobComplete(ctx, tx, job.ID, JobFailed, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
UPDATE runs
SET state=$1, result=$2, error=$3, updated_at=$4
WHERE id=$5
`, string(RunFailed), mustRaw(result), mustRaw(map[string]any{"message": result.Error, "exitCode": result.ExitCode}), now, run.ID); err != nil {
			return err
		}
		return insertEvent(ctx, tx, run.ID, "run_failed", eventPayload(run.CorrelationID, map[string]any{"jobId": job.ID, "error": result.Error, "exitCode": result.ExitCode}))
	})
}

func (s *PostgresStore) CompleteJobWaitingHuman(ctx context.Context, lease Lease, result contract.JobResult, task HumanTask) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		job, run, err := leasedJobAndRunPostgres(ctx, tx, lease)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		if task.ID == "" {
			task.ID = NewID("human")
		}
		if task.RunID == "" {
			task.RunID = run.ID
		}
		if task.State == "" {
			task.State = HumanTaskPending
		}
		task.CreatedAt = nonZeroTime(task.CreatedAt, now)
		if err := updateJobComplete(ctx, tx, job.ID, JobSucceeded, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO human_tasks (
	id, run_id, state, title, description, schema, resume_input,
	created_at, completed_at, expires_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
`, task.ID, task.RunID, string(task.State), task.Title, nullableString(task.Description), nullableRaw(task.Schema),
			nullableRaw(task.ResumeInput), task.CreatedAt, task.CompletedAt, task.ExpiresAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
UPDATE runs
SET state=$1, result=$2, error=NULL, task_id=$3, updated_at=$4
WHERE id=$5
`, string(RunWaitingHuman), mustRaw(result), task.ID, now, run.ID); err != nil {
			return err
		}
		return insertEvent(ctx, tx, run.ID, "human_task_created", eventPayload(run.CorrelationID, map[string]any{"jobId": job.ID, "taskId": task.ID}))
	})
}

func (s *PostgresStore) ResumeHumanTask(ctx context.Context, taskID string, resumeInput json.RawMessage) (Run, Job, error) {
	var resumedRun Run
	var enqueuedJob Job
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		task, err := scanHumanTask(tx.QueryRow(ctx, `SELECT `+humanTaskColumns+` FROM human_tasks WHERE id=$1 FOR UPDATE`, taskID))
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: human task %q", ErrNotFound, taskID)
		}
		if err != nil {
			return err
		}
		if task.State != HumanTaskPending {
			return fmt.Errorf("%w: human task %q is %s", ErrInvalidState, taskID, task.State)
		}
		run, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id=$1 FOR UPDATE`, task.RunID))
		if err != nil {
			return err
		}
		if run.State != RunWaitingHuman {
			return fmt.Errorf("%w: run %q is %s", ErrInvalidState, run.ID, run.State)
		}
		if len(resumeInput) == 0 {
			resumeInput = json.RawMessage("{}")
		}
		if !json.Valid(resumeInput) {
			return errors.New("resume input is not valid JSON")
		}

		now := time.Now().UTC()
		task.State = HumanTaskCompleted
		task.ResumeInput = cloneRaw(resumeInput)
		task.CompletedAt = &now
		run.State = RunResuming
		run.TaskID = ""
		run.UpdatedAt = now
		job := NewActionJob(run, mergeResumeInput(run.Input, task.ID, resumeInput))
		job.CreatedAt = now
		job.UpdatedAt = now

		if _, err := tx.Exec(ctx, `
UPDATE human_tasks
SET state=$1, resume_input=$2, completed_at=$3
WHERE id=$4
`, string(task.State), nullableRaw(task.ResumeInput), task.CompletedAt, task.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
UPDATE runs
SET state=$1, task_id=NULL, updated_at=$2
WHERE id=$3
`, string(run.State), run.UpdatedAt, run.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO jobs (
	id, run_id, state, kind, payload, priority, attempt, lease_owner,
	lease_expires_at, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
`, job.ID, job.RunID, string(job.State), job.Kind, mustRaw(job.Payload), job.Priority, job.Attempt,
			nullableString(job.LeaseOwner), job.LeaseExpiresAt, job.CreatedAt, job.UpdatedAt); err != nil {
			return err
		}
		if err := insertEvent(ctx, tx, run.ID, "human_task_resumed", eventPayload(run.CorrelationID, map[string]any{"taskId": task.ID, "jobId": job.ID})); err != nil {
			return err
		}
		resumedRun = run
		enqueuedJob = job
		return nil
	})
	return resumedRun, enqueuedJob, err
}

func (s *PostgresStore) ResumeRun(ctx context.Context, runID string, resumeInput json.RawMessage) (Run, Job, error) {
	run, err := s.GetRun(ctx, runID)
	if err != nil {
		return Run{}, Job{}, err
	}
	if run.TaskID == "" {
		return Run{}, Job{}, fmt.Errorf("%w: run %q has no pending human task", ErrInvalidState, runID)
	}
	return s.ResumeHumanTask(ctx, run.TaskID, resumeInput)
}

func (s *PostgresStore) CancelJob(ctx context.Context, workspaceID string, jobID string, reason string) (CancelResult, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	var result CancelResult
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		job, err := scanJob(tx.QueryRow(ctx, `
SELECT `+jobColumns+`
FROM jobs
WHERE id=$1
  AND COALESCE(NULLIF(payload->>'workspace', ''), NULLIF(payload->'deployment'->>'workspace', ''), 'default')=$2
FOR UPDATE
`, jobID, workspaceID))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		run, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id=$1 FOR UPDATE`, job.RunID))
		if err != nil {
			return err
		}
		result.Found = true
		if IsTerminal(run) || job.State == JobSucceeded || job.State == JobFailed {
			result.AlreadyCompleted = true
			return nil
		}
		if job.State == JobRunning {
			result.SoftCanceled = true
		} else {
			result.CompletedNow = true
		}
		message := reason
		if message == "" {
			message = "job canceled"
		}
		now := time.Now().UTC()
		job.State = JobFailed
		job.LeaseOwner = ""
		job.LeaseExpiresAt = nil
		job.UpdatedAt = now
		run.State = RunCanceled
		run.Result = &contract.JobResult{
			JobID:    job.ID,
			App:      run.App,
			Action:   run.Action,
			ExitCode: -1,
			Error:    message,
		}
		run.Error = mustRaw(map[string]string{"message": message})
		run.UpdatedAt = now
		if _, err := tx.Exec(ctx, `
UPDATE jobs
SET state=$1, lease_owner=NULL, lease_expires_at=NULL, updated_at=$2
WHERE id=$3
`, string(job.State), job.UpdatedAt, job.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
UPDATE runs
SET state=$1, result=$2, error=$3, updated_at=$4
WHERE id=$5
`, string(run.State), mustRaw(run.Result), run.Error, run.UpdatedAt, run.ID); err != nil {
			return err
		}
		return insertEvent(ctx, tx, run.ID, "run_canceled", eventPayload(run.CorrelationID, map[string]any{"jobId": job.ID, "reason": reason}))
	})
	return result, err
}

func (s *PostgresStore) CancelRun(ctx context.Context, runID string, reason string) (Run, error) {
	var canceled Run
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		run, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id=$1 FOR UPDATE`, runID))
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: run %q", ErrNotFound, runID)
		}
		if err != nil {
			return err
		}
		if IsTerminal(run) {
			return fmt.Errorf("%w: run %q is %s", ErrInvalidState, runID, run.State)
		}
		now := time.Now().UTC()
		run.State = RunCanceled
		run.Error = mustRaw(map[string]string{"message": reason})
		run.UpdatedAt = now
		if _, err := tx.Exec(ctx, `
UPDATE runs
SET state=$1, error=$2, updated_at=$3
WHERE id=$4
`, string(run.State), run.Error, run.UpdatedAt, run.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
UPDATE jobs
SET state=$1, lease_owner=NULL, lease_expires_at=NULL, updated_at=$2
WHERE run_id=$3 AND state IN ($4, $5)
`, string(JobFailed), now, run.ID, string(JobQueued), string(JobRunning)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
UPDATE human_tasks
SET state=$1
WHERE run_id=$2 AND state=$3
`, string(HumanTaskExpired), run.ID, string(HumanTaskPending)); err != nil {
			return err
		}
		if err := insertEvent(ctx, tx, run.ID, "run_canceled", eventPayload(run.CorrelationID, map[string]any{"reason": reason})); err != nil {
			return err
		}
		canceled = run
		return nil
	})
	return canceled, err
}

func (s *PostgresStore) RetryRun(ctx context.Context, runID string) (Run, Job, error) {
	var retried Run
	var enqueued Job
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		run, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id=$1 FOR UPDATE`, runID))
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: run %q", ErrNotFound, runID)
		}
		if err != nil {
			return err
		}
		switch run.State {
		case RunFailed, RunCanceled, RunExpired:
		default:
			return fmt.Errorf("%w: run %q is %s", ErrInvalidState, runID, run.State)
		}
		now := time.Now().UTC()
		run.State = RunQueued
		run.Output = nil
		run.Result = nil
		run.Error = nil
		run.TaskID = ""
		run.UpdatedAt = now
		job := NewActionJob(run, run.Input)
		job.CreatedAt = now
		job.UpdatedAt = now
		if _, err := tx.Exec(ctx, `
UPDATE runs
SET state=$1, output=NULL, result=NULL, error=NULL, task_id=NULL, updated_at=$2
WHERE id=$3
`, string(run.State), run.UpdatedAt, run.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO jobs (
	id, run_id, state, kind, payload, priority, attempt, lease_owner,
	lease_expires_at, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
`, job.ID, job.RunID, string(job.State), job.Kind, mustRaw(job.Payload), job.Priority, job.Attempt,
			nullableString(job.LeaseOwner), job.LeaseExpiresAt, job.CreatedAt, job.UpdatedAt); err != nil {
			return err
		}
		if err := insertEvent(ctx, tx, run.ID, "run_retried", eventPayload(run.CorrelationID, map[string]any{"jobId": job.ID})); err != nil {
			return err
		}
		retried = run
		enqueued = job
		return nil
	})
	return retried, enqueued, err
}

func (s *PostgresStore) withTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRun(row rowScanner) (Run, error) {
	var run Run
	var stateValue string
	var deployment json.RawMessage
	var result json.RawMessage
	var taskID sql.NullString
	var correlationID sql.NullString
	var expiresAt sql.NullTime
	var env json.RawMessage
	if err := row.Scan(
		&run.ID, &run.Adapter, &run.App, &run.Action, &stateValue, &deployment, &run.Input,
		&run.Output, &result, &run.Error, &taskID, &correlationID, &env, &run.CreatedAt, &run.UpdatedAt, &expiresAt,
	); err != nil {
		return Run{}, err
	}
	run.State = RunState(stateValue)
	if err := json.Unmarshal(deployment, &run.Deployment); err != nil {
		return Run{}, err
	}
	if len(result) > 0 {
		var jobResult contract.JobResult
		if err := json.Unmarshal(result, &jobResult); err != nil {
			return Run{}, err
		}
		run.Result = &jobResult
	}
	if taskID.Valid {
		run.TaskID = taskID.String
	}
	if correlationID.Valid {
		run.CorrelationID = correlationID.String
	}
	if expiresAt.Valid {
		run.ExpiresAt = &expiresAt.Time
	}
	if len(env) > 0 {
		_ = json.Unmarshal(env, &run.Env)
	}
	return run, nil
}

func scanJob(row rowScanner) (Job, error) {
	var job Job
	var stateValue string
	var payload json.RawMessage
	var leaseOwner sql.NullString
	var leaseExpiresAt sql.NullTime
	if err := row.Scan(
		&job.ID, &job.RunID, &stateValue, &job.Kind, &payload, &job.Priority, &job.Attempt,
		&leaseOwner, &leaseExpiresAt, &job.CreatedAt, &job.UpdatedAt,
	); err != nil {
		return Job{}, err
	}
	job.State = JobState(stateValue)
	if err := json.Unmarshal(payload, &job.Payload); err != nil {
		return Job{}, err
	}
	if leaseOwner.Valid {
		job.LeaseOwner = leaseOwner.String
	}
	if leaseExpiresAt.Valid {
		job.LeaseExpiresAt = &leaseExpiresAt.Time
	}
	return job, nil
}

func scanHumanTask(row rowScanner) (HumanTask, error) {
	var task HumanTask
	var stateValue string
	var description sql.NullString
	var completedAt sql.NullTime
	var expiresAt sql.NullTime
	if err := row.Scan(
		&task.ID, &task.RunID, &stateValue, &task.Title, &description, &task.Schema,
		&task.ResumeInput, &task.CreatedAt, &completedAt, &expiresAt,
	); err != nil {
		return HumanTask{}, err
	}
	task.State = HumanTaskState(stateValue)
	if description.Valid {
		task.Description = description.String
	}
	if completedAt.Valid {
		task.CompletedAt = &completedAt.Time
	}
	if expiresAt.Valid {
		task.ExpiresAt = &expiresAt.Time
	}
	return task, nil
}

func leasedJobAndRunPostgres(ctx context.Context, tx pgx.Tx, lease Lease) (Job, Run, error) {
	job, err := scanJob(tx.QueryRow(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=$1 FOR UPDATE`, lease.JobID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, Run{}, fmt.Errorf("%w: job %q", ErrNotFound, lease.JobID)
	}
	if err != nil {
		return Job{}, Run{}, err
	}
	now := time.Now().UTC()
	if job.State != JobRunning || job.LeaseOwner != lease.WorkerID {
		return Job{}, Run{}, fmt.Errorf("%w: job %q", ErrInvalidLease, lease.JobID)
	}
	if job.LeaseExpiresAt != nil && job.LeaseExpiresAt.Before(now) {
		return Job{}, Run{}, fmt.Errorf("%w: job %q expired", ErrInvalidLease, lease.JobID)
	}
	run, err := scanRun(tx.QueryRow(ctx, `SELECT `+runColumns+` FROM runs WHERE id=$1 FOR UPDATE`, job.RunID))
	if err != nil {
		return Job{}, Run{}, err
	}
	return job, run, nil
}

func updateJobComplete(ctx context.Context, tx pgx.Tx, jobID string, state JobState, now time.Time) error {
	_, err := tx.Exec(ctx, `
UPDATE jobs
SET state=$1, lease_owner=NULL, lease_expires_at=NULL, updated_at=$2
WHERE id=$3
`, string(state), now, jobID)
	return err
}

func insertEvent(ctx context.Context, tx pgx.Tx, runID string, eventType string, payload any) error {
	_, err := tx.Exec(ctx, `
INSERT INTO run_events (run_id, event_type, payload)
VALUES ($1, $2, $3)
`, runID, eventType, mustRaw(payload))
	return err
}

func requiredRaw(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return json.RawMessage("{}")
	}
	return value
}

func nullableRaw(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func nullableResult(value *contract.JobResult) any {
	if value == nil {
		return nil
	}
	return mustRaw(value)
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableStrings(value []string) any {
	if len(value) == 0 {
		return nil
	}
	return mustRaw(value)
}
