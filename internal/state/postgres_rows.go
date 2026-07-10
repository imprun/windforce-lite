package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/imprun/windforce-lite/internal/contract"
)

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
	var startedAt sql.NullTime
	var canceledBy sql.NullString
	var canceledReason sql.NullString
	if err := row.Scan(
		&job.ID, &job.RunID, &stateValue, &job.Kind, &payload, &job.Priority, &job.Attempt,
		&leaseOwner, &leaseExpiresAt, &startedAt, &canceledBy, &canceledReason, &job.CreatedAt, &job.UpdatedAt,
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
	if startedAt.Valid {
		job.StartedAt = &startedAt.Time
	}
	if canceledBy.Valid {
		job.CanceledBy = &canceledBy.String
	}
	if canceledReason.Valid {
		job.CanceledReason = &canceledReason.String
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
	if job.State != JobRunning || job.LeaseOwner != lease.WorkerID || job.Attempt != lease.Attempt {
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

func (s *PostgresStore) completeCanceledJob(ctx context.Context, tx pgx.Tx, job Job, run Run, by string, reason string, message string, now time.Time) error {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "job canceled"
	}
	run.State = RunCanceled
	run.Result = canceledJobResult(job, run, message)
	storedResult, err := s.encryptJobResult(ctx, normalizedJobWorkspace("", job), *run.Result)
	if err != nil {
		return err
	}
	run.Result = cloneResult(storedResult)
	run.Error = mustRaw(map[string]string{
		"message":        message,
		"canceledBy":     by,
		"canceledReason": reason,
	})
	run.UpdatedAt = now
	if _, err := tx.Exec(ctx, `
UPDATE jobs
SET state=$1, lease_expires_at=NULL, canceled_by=$2, canceled_reason=$3, updated_at=$4
WHERE id=$5
`, string(JobFailed), by, reason, now, job.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
UPDATE runs
SET state=$1, result=$2, error=$3, updated_at=$4
WHERE id=$5
`, string(run.State), mustRaw(run.Result), run.Error, run.UpdatedAt, run.ID); err != nil {
		return err
	}
	return insertEvent(ctx, tx, run.ID, "run_canceled", eventPayload(run.CorrelationID, map[string]any{"jobId": job.ID, "by": by, "reason": reason}))
}

func updateJobComplete(ctx context.Context, tx pgx.Tx, jobID string, state JobState, now time.Time) error {
	_, err := tx.Exec(ctx, `
UPDATE jobs
SET state=$1, lease_expires_at=NULL, updated_at=$2
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

func nullableStringPtr(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableStrings(value []string) any {
	if len(value) == 0 {
		return nil
	}
	return mustRaw(value)
}
