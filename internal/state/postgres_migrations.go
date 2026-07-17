package state

import (
	"context"
)

const postgresMigrationAdvisoryLockID int64 = 0x57464c4d49475241

func (s *PostgresStore) Migrate(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, postgresMigrationAdvisoryLockID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
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
    client_id TEXT,
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

DO $$
BEGIN
    IF to_regclass('client_registry') IS NULL AND to_regclass('api_client') IS NOT NULL THEN
        ALTER TABLE api_client RENAME TO client_registry;
    END IF;
    IF to_regclass('client_registry_audit') IS NULL AND to_regclass('api_client_audit') IS NOT NULL THEN
        ALTER TABLE api_client_audit RENAME TO client_registry_audit;
    END IF;
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema() AND table_name = 'client_registry' AND column_name = 'client_key'
    ) AND NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema() AND table_name = 'client_registry' AND column_name = 'external_key'
    ) THEN
        ALTER TABLE client_registry RENAME COLUMN client_key TO external_key;
    END IF;
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema() AND table_name = 'client_registry_audit' AND column_name = 'api_client_id'
    ) AND NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema() AND table_name = 'client_registry_audit' AND column_name = 'client_id'
    ) THEN
        ALTER TABLE client_registry_audit RENAME COLUMN api_client_id TO client_id;
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS client_registry (
    workspace_id TEXT NOT NULL,
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    external_key TEXT NOT NULL,
    created_by TEXT NOT NULL,
    updated_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, id),
    UNIQUE (workspace_id, external_key)
);

CREATE TABLE IF NOT EXISTS client_registry_audit (
    id BIGSERIAL PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    client_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    detail TEXT NOT NULL DEFAULT '',
    actor TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS input_config (
    workspace_id TEXT NOT NULL,
    app_key TEXT NOT NULL,
    action_key TEXT NOT NULL DEFAULT '',
    client_id TEXT,
    config JSONB NOT NULL,
    locked_keys TEXT[] NOT NULL DEFAULT '{}',
    updated_by TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE NULLS NOT DISTINCT (workspace_id, app_key, action_key, client_id),
    FOREIGN KEY (workspace_id, client_id)
        REFERENCES client_registry(workspace_id, id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS input_config_audit (
    id BIGSERIAL PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    app_key TEXT NOT NULL,
    action_key TEXT NOT NULL DEFAULT '',
    client_id TEXT,
    kind TEXT NOT NULL,
    detail TEXT NOT NULL DEFAULT '',
    actor TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS control_release_history (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    git_source_id TEXT NOT NULL,
    app_key TEXT NOT NULL,
    commit_sha TEXT NOT NULL,
    record JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS control_active_release (
    workspace_id TEXT NOT NULL,
    app_key TEXT NOT NULL,
    history_id TEXT,
    deployment JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (workspace_id, app_key)
);

CREATE TABLE IF NOT EXISTS control_audit (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    git_source_id TEXT NOT NULL,
    app_key TEXT NOT NULL DEFAULT '',
    kind TEXT NOT NULL,
    record JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS control_source_release_marker (
    workspace_id TEXT NOT NULL,
    git_source_id TEXT NOT NULL,
    commit_sha TEXT NOT NULL,
    released_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (workspace_id, git_source_id)
);

CREATE TABLE IF NOT EXISTS control_release_candidate (
    workspace_id TEXT NOT NULL,
    git_source_id TEXT NOT NULL,
    commit_sha TEXT NOT NULL,
    app_key TEXT NOT NULL,
    deployment JSONB NOT NULL,
    synced_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (workspace_id, git_source_id, commit_sha)
);

CREATE TABLE IF NOT EXISTS control_source_operation_lease (
    workspace_id TEXT NOT NULL,
    git_source_id TEXT NOT NULL,
    holder TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (workspace_id, git_source_id)
);

CREATE TABLE IF NOT EXISTS control_plane_event (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    subject TEXT NOT NULL,
    body JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS webhook_subscription (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    name TEXT NOT NULL,
    endpoint_encrypted JSONB NOT NULL,
    signing_secret_encrypted JSONB NOT NULL,
    event_types JSONB NOT NULL,
    app_keys JSONB NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_by TEXT NOT NULL,
    updated_by TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS webhook_subscription_active_name_idx
    ON webhook_subscription (workspace_id, name)
    WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS webhook_delivery (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    event_id TEXT NOT NULL REFERENCES control_plane_event(id),
    subscription_id TEXT NOT NULL REFERENCES webhook_subscription(id),
    state TEXT NOT NULL,
    attempt INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL,
    lease_owner TEXT,
    lease_expires_at TIMESTAMPTZ,
    response_status INTEGER,
    latency_ms BIGINT,
    error_summary TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    UNIQUE (event_id, subscription_id)
);

CREATE TABLE IF NOT EXISTS webhook_audit (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    subscription_id TEXT,
    delivery_id TEXT,
    kind TEXT NOT NULL,
    detail TEXT NOT NULL,
    actor TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

ALTER TABLE runs ADD COLUMN IF NOT EXISTS result JSONB;
ALTER TABLE runs ADD COLUMN IF NOT EXISTS correlation_id TEXT;
ALTER TABLE runs ADD COLUMN IF NOT EXISTS env JSONB;
ALTER TABLE runs ADD COLUMN IF NOT EXISTS client_id TEXT;
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

CREATE INDEX IF NOT EXISTS client_registry_audit_client_idx
    ON client_registry_audit (workspace_id, client_id, created_at DESC);

CREATE INDEX IF NOT EXISTS input_config_lookup_idx
    ON input_config (workspace_id, app_key, action_key, client_id);

CREATE INDEX IF NOT EXISTS input_config_audit_lookup_idx
    ON input_config_audit (workspace_id, app_key, client_id, created_at DESC);

CREATE INDEX IF NOT EXISTS control_release_history_source_idx
    ON control_release_history (workspace_id, git_source_id, created_at DESC);

CREATE INDEX IF NOT EXISTS control_release_history_app_idx
    ON control_release_history (workspace_id, app_key, created_at DESC);

CREATE INDEX IF NOT EXISTS control_release_candidate_latest_idx
    ON control_release_candidate (workspace_id, git_source_id, synced_at DESC);

CREATE INDEX IF NOT EXISTS control_audit_source_idx
    ON control_audit (workspace_id, git_source_id, created_at DESC);

CREATE INDEX IF NOT EXISTS control_plane_event_lookup_idx
    ON control_plane_event (workspace_id, event_type, created_at DESC);

CREATE INDEX IF NOT EXISTS webhook_delivery_claim_idx
    ON webhook_delivery (state, next_attempt_at, created_at);

CREATE INDEX IF NOT EXISTS webhook_delivery_lease_idx
    ON webhook_delivery (lease_expires_at)
    WHERE state = 'delivering';

CREATE INDEX IF NOT EXISTS webhook_delivery_subscription_idx
    ON webhook_delivery (workspace_id, subscription_id, created_at DESC);

CREATE INDEX IF NOT EXISTS webhook_delivery_retention_idx
    ON webhook_delivery (state, completed_at, updated_at, id)
    WHERE state IN ('succeeded', 'failed', 'canceled');

CREATE TABLE IF NOT EXISTS worker_registry (
    id                text PRIMARY KEY,
    worker_group      text NOT NULL DEFAULT '',
    tags              jsonb NOT NULL DEFAULT '[]'::jsonb,
    labels            jsonb NOT NULL DEFAULT '[]'::jsonb,
    slots             integer NOT NULL DEFAULT 1,
    started_at        timestamptz NOT NULL,
    last_heartbeat_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS webhook_audit_workspace_idx
    ON webhook_audit (workspace_id, created_at DESC, id DESC);
`); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
