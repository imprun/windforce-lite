package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/imprun/windforce-lite/internal/contract"
)

func (s *PostgresStore) ListAPIClients(ctx context.Context, workspaceID string) ([]APIClient, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	rows, err := s.pool.Query(ctx, `
SELECT id, workspace_id, name, client_key, created_by, updated_by, created_at, updated_at
FROM api_client WHERE workspace_id=$1 ORDER BY name, id
`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	clients := []APIClient{}
	for rows.Next() {
		client, err := scanAPIClient(rows)
		if err != nil {
			return nil, err
		}
		clients = append(clients, client)
	}
	return clients, rows.Err()
}

func (s *PostgresStore) GetAPIClient(ctx context.Context, workspaceID string, id string) (APIClient, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	client, err := scanAPIClient(s.pool.QueryRow(ctx, `
SELECT id, workspace_id, name, client_key, created_by, updated_by, created_at, updated_at
FROM api_client WHERE workspace_id=$1 AND id=$2
`, workspaceID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return APIClient{}, ErrNotFound
	}
	return client, err
}

func (s *PostgresStore) CreateAPIClient(ctx context.Context, workspaceID string, name string, clientKey string, actor string) (APIClient, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	id := NewID("client")
	var created APIClient
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		created, err = scanAPIClient(tx.QueryRow(ctx, `
INSERT INTO api_client (workspace_id, id, name, client_key, created_by, updated_by)
VALUES ($1, $2, $3, $4, $5, $5)
RETURNING id, workspace_id, name, client_key, created_by, updated_by, created_at, updated_at
`, workspaceID, id, name, clientKey, actor))
		if err != nil {
			return apiClientPostgresError(err)
		}
		return insertAPIClientAudit(ctx, tx, workspaceID, id, "created", "", actor)
	})
	return created, err
}

func (s *PostgresStore) UpdateAPIClient(ctx context.Context, workspaceID string, id string, name string, clientKey string, actor string) (APIClient, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	var updated APIClient
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		current, err := scanAPIClient(tx.QueryRow(ctx, `
SELECT id, workspace_id, name, client_key, created_by, updated_by, created_at, updated_at
FROM api_client WHERE workspace_id=$1 AND id=$2 FOR UPDATE
`, workspaceID, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		updated, err = scanAPIClient(tx.QueryRow(ctx, `
UPDATE api_client SET name=$3, client_key=$4, updated_by=$5, updated_at=now()
WHERE workspace_id=$1 AND id=$2
RETURNING id, workspace_id, name, client_key, created_by, updated_by, created_at, updated_at
`, workspaceID, id, name, clientKey, actor))
		if err != nil {
			return apiClientPostgresError(err)
		}
		return insertAPIClientAudit(ctx, tx, workspaceID, id, "updated", apiClientChangeDetail(current, name, clientKey), actor)
	})
	return updated, err
}

func (s *PostgresStore) DeleteAPIClient(ctx context.Context, workspaceID string, id string, actor string) error {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	return s.withTx(ctx, func(tx pgx.Tx) error {
		result, err := tx.Exec(ctx, `DELETE FROM api_client WHERE workspace_id=$1 AND id=$2`, workspaceID, id)
		if err != nil {
			return err
		}
		if result.RowsAffected() == 0 {
			return ErrNotFound
		}
		return insertAPIClientAudit(ctx, tx, workspaceID, id, "deleted", "", actor)
	})
}

func (s *PostgresStore) ListAPIClientAudit(ctx context.Context, workspaceID string, id string) ([]APIClientAudit, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	rows, err := s.pool.Query(ctx, `
SELECT id::text, workspace_id, api_client_id, kind, detail, actor, created_at
FROM api_client_audit WHERE workspace_id=$1 AND api_client_id=$2
ORDER BY created_at DESC, id DESC
`, workspaceID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []APIClientAudit{}
	for rows.Next() {
		var record APIClientAudit
		if err := rows.Scan(&record.ID, &record.WorkspaceID, &record.APIClientID, &record.Kind, &record.Detail, &record.Actor, &record.CreatedAt); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

type apiClientScanner interface {
	Scan(dest ...any) error
}

func scanAPIClient(row apiClientScanner) (APIClient, error) {
	var client APIClient
	err := row.Scan(&client.ID, &client.WorkspaceID, &client.Name, &client.ClientKey, &client.CreatedBy, &client.UpdatedBy, &client.CreatedAt, &client.UpdatedAt)
	return client, err
}

func insertAPIClientAudit(ctx context.Context, tx pgx.Tx, workspaceID string, id string, kind string, detail string, actor string) error {
	_, err := tx.Exec(ctx, `
INSERT INTO api_client_audit (workspace_id, api_client_id, kind, detail, actor)
VALUES ($1, $2, $3, $4, $5)
`, workspaceID, id, kind, detail, actor)
	return err
}

func apiClientPostgresError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return fmt.Errorf("%w: client key already exists", ErrConflict)
	}
	return err
}
