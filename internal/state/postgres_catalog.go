package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/imprun/windforce-lite/internal/catalog"
	"github.com/imprun/windforce-lite/internal/contract"
	"github.com/imprun/windforce-lite/internal/webhook"
)

var _ catalog.Store = (*PostgresStore)(nil)

func (s *PostgresStore) PublishRelease(ctx context.Context, deployment contract.Deployment, releasedAt time.Time) (contract.Deployment, error) {
	deployment, history, audit := catalog.PreparePublication(deployment, releasedAt)
	deploymentJSON, err := json.Marshal(deployment)
	if err != nil {
		return contract.Deployment{}, err
	}
	historyJSON, err := json.Marshal(history)
	if err != nil {
		return contract.Deployment{}, err
	}
	auditJSON, err := json.Marshal(audit)
	if err != nil {
		return contract.Deployment{}, err
	}
	marker := catalog.SourceReleaseMarker{
		Workspace:   deployment.SourceWorkspace(),
		GitSourceID: deployment.SourceGitSourceID(),
		Commit:      deployment.Commit,
		ReleasedAt:  history.CreatedAt,
	}
	err = s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, history.Workspace+"/"+history.App); err != nil {
			return err
		}
		previous, err := postgresPreviousRelease(ctx, tx, history.Workspace, history.App)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO control_release_history (
    id, workspace_id, git_source_id, app_key, commit_sha, record, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7)
`, history.ID, history.Workspace, history.GitSourceID, history.App, history.Commit, historyJSON, history.CreatedAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO control_active_release (workspace_id, app_key, history_id, deployment, updated_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (workspace_id, app_key) DO UPDATE SET
    history_id = EXCLUDED.history_id,
    deployment = EXCLUDED.deployment,
    updated_at = EXCLUDED.updated_at
`, history.Workspace, history.App, history.ID, deploymentJSON, history.CreatedAt); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
INSERT INTO control_source_release_marker (workspace_id, git_source_id, commit_sha, released_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (workspace_id, git_source_id) DO UPDATE SET
    commit_sha = EXCLUDED.commit_sha,
    released_at = EXCLUDED.released_at
`, marker.Workspace, marker.GitSourceID, marker.Commit, marker.ReleasedAt)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
INSERT INTO control_audit (id, workspace_id, git_source_id, app_key, kind, record, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
`, audit.ID, audit.Workspace, audit.GitSourceID, audit.App, audit.Kind, auditJSON, audit.CreatedAt)
		if err != nil {
			return err
		}
		releaseEvent, err := prepareReleaseEvent(history, previous)
		if err != nil {
			return err
		}
		eventJSON, err := json.Marshal(releaseEvent)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO control_plane_event (id, workspace_id, event_type, subject, body, created_at)
VALUES ($1, $2, $3, $4, $5, $6)
`, releaseEvent.ID, history.Workspace, releaseEvent.Type, releaseEvent.Subject, eventJSON, releaseEvent.Time); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
SELECT `+webhookSubscriptionColumns+`
FROM webhook_subscription
WHERE workspace_id = $1 AND enabled = true AND deleted_at IS NULL
FOR SHARE
`, history.Workspace)
		if err != nil {
			return err
		}
		subscriptions := make([]WebhookSubscriptionRecord, 0)
		for rows.Next() {
			record, err := scanWebhookSubscription(rows)
			if err != nil {
				rows.Close()
				return err
			}
			subscriptions = append(subscriptions, record)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		outboxAt := time.Now().UTC()
		for _, record := range subscriptions {
			candidate := subscriptionFromRecord(record, "", "")
			if !webhook.Matches(candidate, releaseEvent.Type, history.App) {
				continue
			}
			delivery := newWebhookDelivery(releaseEvent, history.Workspace, record.ID, outboxAt)
			if _, err := tx.Exec(ctx, `
INSERT INTO webhook_delivery (
    id, workspace_id, event_id, subscription_id, state, attempt, next_attempt_at,
    created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, 0, $6, $6, $6)
`, delivery.ID, delivery.WorkspaceID, delivery.EventID, delivery.SubscriptionID, delivery.State, delivery.NextAttemptAt); err != nil {
				return err
			}
		}
		return nil
	})
	return deployment, err
}

func postgresPreviousRelease(ctx context.Context, tx pgx.Tx, workspaceID string, appKey string) (*catalog.DeploymentHistory, error) {
	var historyID *string
	var deploymentJSON []byte
	err := tx.QueryRow(ctx, `
SELECT history_id, deployment
FROM control_active_release
WHERE workspace_id = $1 AND app_key = $2
FOR UPDATE
`, contract.NormalizeWorkspace(workspaceID), appKey).Scan(&historyID, &deploymentJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var deployment contract.Deployment
	if err := json.Unmarshal(deploymentJSON, &deployment); err != nil {
		return nil, err
	}
	previous := &catalog.DeploymentHistory{
		Workspace: contract.NormalizeWorkspace(workspaceID),
		App:       appKey,
		Commit:    deployment.Commit,
	}
	if historyID != nil {
		previous.ID = *historyID
	}
	return previous, nil
}

func (s *PostgresStore) GetDeployment(ctx context.Context, app string) (contract.Deployment, error) {
	return s.GetDeploymentForWorkspace(ctx, contract.DefaultWorkspace, app)
}

func (s *PostgresStore) GetDeploymentForWorkspace(ctx context.Context, workspace string, app string) (contract.Deployment, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `
SELECT deployment
FROM control_active_release
WHERE workspace_id = $1 AND app_key = $2
`, contract.NormalizeWorkspace(workspace), app).Scan(&raw)
	if err == pgx.ErrNoRows {
		return contract.Deployment{}, catalog.ErrDeploymentNotFound
	}
	if err != nil {
		return contract.Deployment{}, err
	}
	var deployment contract.Deployment
	if err := json.Unmarshal(raw, &deployment); err != nil {
		return contract.Deployment{}, err
	}
	return deployment, nil
}

func (s *PostgresStore) LoadCatalog(ctx context.Context) (catalog.Snapshot, error) {
	snapshot := catalog.NewSnapshot()
	rows, err := s.pool.Query(ctx, `SELECT workspace_id, app_key, deployment FROM control_active_release`)
	if err != nil {
		return catalog.Snapshot{}, err
	}
	for rows.Next() {
		var workspace string
		var app string
		var raw []byte
		if err := rows.Scan(&workspace, &app, &raw); err != nil {
			rows.Close()
			return catalog.Snapshot{}, err
		}
		var deployment contract.Deployment
		if err := json.Unmarshal(raw, &deployment); err != nil {
			rows.Close()
			return catalog.Snapshot{}, err
		}
		snapshot.Deployments[catalog.DeploymentKey(workspace, app)] = deployment
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return catalog.Snapshot{}, err
	}
	rows.Close()

	rows, err = s.pool.Query(ctx, `SELECT record FROM control_release_history ORDER BY created_at`)
	if err != nil {
		return catalog.Snapshot{}, err
	}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return catalog.Snapshot{}, err
		}
		var record catalog.DeploymentHistory
		if err := json.Unmarshal(raw, &record); err != nil {
			rows.Close()
			return catalog.Snapshot{}, err
		}
		snapshot.History = append(snapshot.History, record)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return catalog.Snapshot{}, err
	}
	rows.Close()

	rows, err = s.pool.Query(ctx, `SELECT record FROM control_audit ORDER BY created_at`)
	if err != nil {
		return catalog.Snapshot{}, err
	}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			rows.Close()
			return catalog.Snapshot{}, err
		}
		var record catalog.AuditRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			rows.Close()
			return catalog.Snapshot{}, err
		}
		snapshot.Audit = append(snapshot.Audit, record)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return catalog.Snapshot{}, err
	}
	rows.Close()

	markers, err := s.ListSourceReleaseMarkers(ctx)
	if err != nil {
		return catalog.Snapshot{}, err
	}
	snapshot.SourceMarkers = markers
	return snapshot, nil
}

func (s *PostgresStore) AppendAudit(ctx context.Context, record catalog.AuditRecord) error {
	record = catalog.PrepareAuditRecord(record, time.Now().UTC())
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
INSERT INTO control_audit (id, workspace_id, git_source_id, app_key, kind, record, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
`, record.ID, contract.NormalizeWorkspace(record.Workspace), record.GitSourceID, record.App, record.Kind, raw, record.CreatedAt)
	return err
}

func (s *PostgresStore) AuditTrail(ctx context.Context, workspace string, gitSourceID string) ([]catalog.AuditRecord, error) {
	rows, err := s.pool.Query(ctx, `
SELECT record
FROM control_audit
WHERE workspace_id = $1 AND git_source_id = $2
ORDER BY created_at
`, contract.NormalizeWorkspace(workspace), gitSourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]catalog.AuditRecord, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var record catalog.AuditRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *PostgresStore) SetAppTagOverride(ctx context.Context, workspace string, app string, tagOverride *string) (contract.Deployment, error) {
	var updated contract.Deployment
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		deployment, err := postgresDeploymentForUpdate(ctx, tx, workspace, app)
		if err != nil {
			return err
		}
		deployment.TagOverride = cloneCatalogString(tagOverride)
		deployment.UpdatedAt = catalogTimePtr(time.Now().UTC())
		if err := updatePostgresDeployment(ctx, tx, workspace, app, deployment); err != nil {
			return err
		}
		updated = deployment
		return nil
	})
	return updated, err
}

func (s *PostgresStore) SetActionTagOverride(ctx context.Context, workspace string, app string, actionKey string, tagOverride *string) (contract.Action, error) {
	var updated contract.Action
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		deployment, err := postgresDeploymentForUpdate(ctx, tx, workspace, app)
		if err != nil {
			return err
		}
		action, ok := deployment.Actions[actionKey]
		if !ok {
			return catalog.ErrActionNotFound
		}
		action.TagOverride = cloneCatalogString(tagOverride)
		action.UpdatedAt = catalogTimePtr(time.Now().UTC())
		deployment.Actions[actionKey] = action
		if err := updatePostgresDeployment(ctx, tx, workspace, app, deployment); err != nil {
			return err
		}
		updated = action
		return nil
	})
	return updated, err
}

func (s *PostgresStore) ListSourceReleaseMarkers(ctx context.Context) (map[string]catalog.SourceReleaseMarker, error) {
	rows, err := s.pool.Query(ctx, `
SELECT workspace_id, git_source_id, commit_sha, released_at
FROM control_source_release_marker
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	markers := map[string]catalog.SourceReleaseMarker{}
	for rows.Next() {
		var marker catalog.SourceReleaseMarker
		if err := rows.Scan(&marker.Workspace, &marker.GitSourceID, &marker.Commit, &marker.ReleasedAt); err != nil {
			return nil, err
		}
		markers[catalog.SourceReleaseKey(marker.Workspace, marker.GitSourceID)] = marker
	}
	return markers, rows.Err()
}

func (s *PostgresStore) ImportCatalog(ctx context.Context, imported catalog.Snapshot) error {
	catalog.NormalizeSnapshot(&imported)
	return s.withTx(ctx, func(tx pgx.Tx) error {
		for _, history := range imported.History {
			raw, err := json.Marshal(history)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
INSERT INTO control_release_history (id, workspace_id, git_source_id, app_key, commit_sha, record, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (id) DO NOTHING
`, history.ID, contract.NormalizeWorkspace(history.Workspace), history.GitSourceID, history.App, history.Commit, raw, history.CreatedAt); err != nil {
				return err
			}
		}
		for _, deployment := range imported.Deployments {
			raw, err := json.Marshal(deployment)
			if err != nil {
				return err
			}
			historyID := importedHistoryID(imported.History, deployment)
			updatedAt := deploymentUpdatedAt(deployment)
			if _, err := tx.Exec(ctx, `
INSERT INTO control_active_release (workspace_id, app_key, history_id, deployment, updated_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (workspace_id, app_key) DO NOTHING
`, deployment.SourceWorkspace(), deployment.App, nullableCatalogString(historyID), raw, updatedAt); err != nil {
				return err
			}
		}
		for _, record := range imported.Audit {
			raw, err := json.Marshal(record)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
INSERT INTO control_audit (id, workspace_id, git_source_id, app_key, kind, record, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (id) DO NOTHING
`, record.ID, contract.NormalizeWorkspace(record.Workspace), record.GitSourceID, record.App, record.Kind, raw, record.CreatedAt); err != nil {
				return err
			}
		}
		for _, marker := range imported.SourceMarkers {
			if _, err := tx.Exec(ctx, `
INSERT INTO control_source_release_marker (workspace_id, git_source_id, commit_sha, released_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (workspace_id, git_source_id) DO NOTHING
`, contract.NormalizeWorkspace(marker.Workspace), marker.GitSourceID, marker.Commit, marker.ReleasedAt); err != nil {
				return err
			}
		}
		return nil
	})
}

func postgresDeploymentForUpdate(ctx context.Context, tx pgx.Tx, workspace string, app string) (contract.Deployment, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `
SELECT deployment
FROM control_active_release
WHERE workspace_id = $1 AND app_key = $2
FOR UPDATE
`, contract.NormalizeWorkspace(workspace), app).Scan(&raw)
	if err == pgx.ErrNoRows {
		return contract.Deployment{}, catalog.ErrDeploymentNotFound
	}
	if err != nil {
		return contract.Deployment{}, err
	}
	var deployment contract.Deployment
	if err := json.Unmarshal(raw, &deployment); err != nil {
		return contract.Deployment{}, err
	}
	return deployment, nil
}

func updatePostgresDeployment(ctx context.Context, tx pgx.Tx, workspace string, app string, deployment contract.Deployment) error {
	raw, err := json.Marshal(deployment)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
UPDATE control_active_release
SET deployment = $3, updated_at = $4
WHERE workspace_id = $1 AND app_key = $2
`, contract.NormalizeWorkspace(workspace), app, raw, deploymentUpdatedAt(deployment))
	return err
}

func importedHistoryID(history []catalog.DeploymentHistory, deployment contract.Deployment) string {
	workspace := deployment.SourceWorkspace()
	for i := len(history) - 1; i >= 0; i-- {
		record := history[i]
		if contract.NormalizeWorkspace(record.Workspace) == workspace && record.App == deployment.App && record.Commit == deployment.Commit {
			return record.ID
		}
	}
	return ""
}

func deploymentUpdatedAt(deployment contract.Deployment) time.Time {
	if deployment.UpdatedAt != nil && !deployment.UpdatedAt.IsZero() {
		return deployment.UpdatedAt.UTC()
	}
	return time.Now().UTC()
}

func nullableCatalogString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
