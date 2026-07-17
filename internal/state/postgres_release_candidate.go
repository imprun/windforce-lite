package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/imprun/windforce-lite/internal/catalog"
	"github.com/imprun/windforce-lite/internal/contract"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) SaveReleaseCandidate(ctx context.Context, deployment contract.Deployment, syncedAt time.Time) (catalog.ReleaseCandidate, error) {
	candidate, err := catalog.PrepareReleaseCandidate(deployment, syncedAt)
	if err != nil {
		return catalog.ReleaseCandidate{}, err
	}
	record, err := json.Marshal(candidate.Deployment)
	if err != nil {
		return catalog.ReleaseCandidate{}, err
	}
	_, err = s.pool.Exec(ctx, `
INSERT INTO control_release_candidate (
    workspace_id, git_source_id, commit_sha, app_key, deployment, synced_at
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (workspace_id, git_source_id, commit_sha) DO NOTHING
`, candidate.Deployment.SourceWorkspace(), candidate.Deployment.SourceGitSourceID(), candidate.Deployment.Commit,
		candidate.Deployment.App, record, candidate.SyncedAt)
	if err != nil {
		return catalog.ReleaseCandidate{}, err
	}
	return s.GetReleaseCandidate(ctx, candidate.Deployment.SourceWorkspace(), candidate.Deployment.SourceGitSourceID(), candidate.Deployment.Commit)
}

func (s *PostgresStore) GetReleaseCandidate(ctx context.Context, workspace string, gitSourceID string, commit string) (catalog.ReleaseCandidate, error) {
	return scanReleaseCandidate(s.pool.QueryRow(ctx, `
SELECT deployment, synced_at
FROM control_release_candidate
WHERE workspace_id = $1 AND git_source_id = $2 AND commit_sha = $3
`, contract.NormalizeWorkspace(workspace), contract.NormalizeGitSourceID(gitSourceID, ""), strings.TrimSpace(commit)))
}

func (s *PostgresStore) GetLatestReleaseCandidate(ctx context.Context, workspace string, gitSourceID string) (catalog.ReleaseCandidate, error) {
	return scanReleaseCandidate(s.pool.QueryRow(ctx, `
SELECT deployment, synced_at
FROM control_release_candidate
WHERE workspace_id = $1 AND git_source_id = $2
ORDER BY synced_at DESC, commit_sha DESC
LIMIT 1
`, contract.NormalizeWorkspace(workspace), contract.NormalizeGitSourceID(gitSourceID, "")))
}

func scanReleaseCandidate(row pgx.Row) (catalog.ReleaseCandidate, error) {
	var deploymentJSON []byte
	var candidate catalog.ReleaseCandidate
	if err := row.Scan(&deploymentJSON, &candidate.SyncedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return catalog.ReleaseCandidate{}, catalog.ErrReleaseCandidateNotFound
		}
		return catalog.ReleaseCandidate{}, err
	}
	if err := json.Unmarshal(deploymentJSON, &candidate.Deployment); err != nil {
		return catalog.ReleaseCandidate{}, err
	}
	return candidate, nil
}

func (s *PostgresStore) AcquireSourceOperationLease(ctx context.Context, workspace string, gitSourceID string, holder string, ttl time.Duration) (bool, error) {
	var acquiredHolder string
	err := s.pool.QueryRow(ctx, `
INSERT INTO control_source_operation_lease (
    workspace_id, git_source_id, holder, expires_at, updated_at
) VALUES ($1, $2, $3, now() + ($4 * interval '1 millisecond'), now())
ON CONFLICT (workspace_id, git_source_id) DO UPDATE SET
    holder = EXCLUDED.holder,
    expires_at = EXCLUDED.expires_at,
    updated_at = now()
WHERE control_source_operation_lease.expires_at <= now()
RETURNING holder
`, contract.NormalizeWorkspace(workspace), contract.NormalizeGitSourceID(gitSourceID, ""), strings.TrimSpace(holder), ttl.Milliseconds()).Scan(&acquiredHolder)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return acquiredHolder == strings.TrimSpace(holder), nil
}

func (s *PostgresStore) RenewSourceOperationLease(ctx context.Context, workspace string, gitSourceID string, holder string, ttl time.Duration) (bool, error) {
	command, err := s.pool.Exec(ctx, `
UPDATE control_source_operation_lease
SET expires_at = now() + ($4 * interval '1 millisecond'), updated_at = now()
WHERE workspace_id = $1 AND git_source_id = $2 AND holder = $3 AND expires_at > now()
`, contract.NormalizeWorkspace(workspace), contract.NormalizeGitSourceID(gitSourceID, ""), strings.TrimSpace(holder), ttl.Milliseconds())
	if err != nil {
		return false, err
	}
	return command.RowsAffected() == 1, nil
}

func (s *PostgresStore) ReleaseSourceOperationLease(ctx context.Context, workspace string, gitSourceID string, holder string) error {
	_, err := s.pool.Exec(ctx, `
DELETE FROM control_source_operation_lease
WHERE workspace_id = $1 AND git_source_id = $2 AND holder = $3
`, contract.NormalizeWorkspace(workspace), contract.NormalizeGitSourceID(gitSourceID, ""), strings.TrimSpace(holder))
	return err
}

var _ catalog.ReleaseCandidateStore = (*PostgresStore)(nil)
var _ catalog.SourceOperationLeaseStore = (*PostgresStore)(nil)
