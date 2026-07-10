package state

import (
	"context"
)

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
    started_at TIMESTAMPTZ,
    canceled_by TEXT,
    canceled_reason TEXT,
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

CREATE TABLE IF NOT EXISTS job_state (
    workspace_id TEXT NOT NULL,
    state_path TEXT NOT NULL,
    value JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, state_path)
);

CREATE TABLE IF NOT EXISTS variable (
    workspace_id TEXT NOT NULL,
    app_key TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL,
    value TEXT NOT NULL,
    is_secret BOOLEAN NOT NULL DEFAULT false,
    description TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (workspace_id, app_key, path)
);

CREATE TABLE IF NOT EXISTS resource (
    workspace_id TEXT NOT NULL,
    path TEXT NOT NULL,
    value JSONB NOT NULL,
    resource_type TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (workspace_id, path)
);

CREATE TABLE IF NOT EXISTS workspace_key (
    workspace_id TEXT PRIMARY KEY,
    key TEXT NOT NULL,
    kek_version INTEGER NOT NULL DEFAULT 0
);

ALTER TABLE runs ADD COLUMN IF NOT EXISTS result JSONB;
ALTER TABLE runs ADD COLUMN IF NOT EXISTS correlation_id TEXT;
ALTER TABLE runs ADD COLUMN IF NOT EXISTS env JSONB;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS started_at TIMESTAMPTZ;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS canceled_by TEXT;
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS canceled_reason TEXT;
ALTER TABLE job_logs ADD COLUMN IF NOT EXISTS workspace_id TEXT NOT NULL DEFAULT 'default';
ALTER TABLE workspace_key ADD COLUMN IF NOT EXISTS kek_version INTEGER NOT NULL DEFAULT 0;

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
