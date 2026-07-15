package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/imprun/windforce-lite/internal/contract"
)

func (s *PostgresStore) ListClients(ctx context.Context, workspaceID string) ([]Client, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	rows, err := s.pool.Query(ctx, `
SELECT id, workspace_id, name, external_key, created_by, updated_by, created_at, updated_at
FROM client_registry WHERE workspace_id=$1 ORDER BY name, id
`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	clients := []Client{}
	for rows.Next() {
		client, err := scanClient(rows)
		if err != nil {
			return nil, err
		}
		clients = append(clients, client)
	}
	return clients, rows.Err()
}

func (s *PostgresStore) GetClient(ctx context.Context, workspaceID string, id string) (Client, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	client, err := scanClient(s.pool.QueryRow(ctx, `
SELECT id, workspace_id, name, external_key, created_by, updated_by, created_at, updated_at
FROM client_registry WHERE workspace_id=$1 AND id=$2
`, workspaceID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Client{}, ErrNotFound
	}
	return client, err
}

func (s *PostgresStore) GetClientByExternalKey(ctx context.Context, workspaceID string, externalKey string) (Client, error) {
	client, err := scanClient(s.pool.QueryRow(ctx, `
SELECT id, workspace_id, name, external_key, created_by, updated_by, created_at, updated_at
FROM client_registry
WHERE workspace_id=$1 AND external_key=$2
`, contract.NormalizeWorkspace(workspaceID), externalKey))
	if errors.Is(err, pgx.ErrNoRows) {
		return Client{}, ErrNotFound
	}
	return client, err
}

func (s *PostgresStore) CreateClient(ctx context.Context, workspaceID string, name string, externalKey string, actor string) (Client, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	id := NewID("client")
	var created Client
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var err error
		created, err = scanClient(tx.QueryRow(ctx, `
INSERT INTO client_registry (workspace_id, id, name, external_key, created_by, updated_by)
VALUES ($1, $2, $3, $4, $5, $5)
RETURNING id, workspace_id, name, external_key, created_by, updated_by, created_at, updated_at
`, workspaceID, id, name, externalKey, actor))
		if err != nil {
			return clientPostgresError(err)
		}
		return insertClientAudit(ctx, tx, workspaceID, id, "created", "", actor)
	})
	return created, err
}

func (s *PostgresStore) UpdateClient(ctx context.Context, workspaceID string, id string, name string, externalKey string, actor string) (Client, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	var updated Client
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		current, err := scanClient(tx.QueryRow(ctx, `
SELECT id, workspace_id, name, external_key, created_by, updated_by, created_at, updated_at
FROM client_registry WHERE workspace_id=$1 AND id=$2 FOR UPDATE
`, workspaceID, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		updated, err = scanClient(tx.QueryRow(ctx, `
UPDATE client_registry SET name=$3, external_key=$4, updated_by=$5, updated_at=now()
WHERE workspace_id=$1 AND id=$2
RETURNING id, workspace_id, name, external_key, created_by, updated_by, created_at, updated_at
`, workspaceID, id, name, externalKey, actor))
		if err != nil {
			return clientPostgresError(err)
		}
		return insertClientAudit(ctx, tx, workspaceID, id, "updated", clientChangeDetail(current, name, externalKey), actor)
	})
	return updated, err
}

func (s *PostgresStore) DeleteClient(ctx context.Context, workspaceID string, id string, actor string) error {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	return s.withTx(ctx, func(tx pgx.Tx) error {
		result, err := tx.Exec(ctx, `DELETE FROM client_registry WHERE workspace_id=$1 AND id=$2`, workspaceID, id)
		if err != nil {
			return err
		}
		if result.RowsAffected() == 0 {
			return ErrNotFound
		}
		return insertClientAudit(ctx, tx, workspaceID, id, "deleted", "", actor)
	})
}

func (s *PostgresStore) ListClientAudit(ctx context.Context, workspaceID string, id string) ([]ClientAudit, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	rows, err := s.pool.Query(ctx, `
SELECT id::text, workspace_id, client_id, kind, detail, actor, created_at
FROM client_registry_audit WHERE workspace_id=$1 AND client_id=$2
ORDER BY created_at DESC, id DESC
`, workspaceID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []ClientAudit{}
	for rows.Next() {
		var record ClientAudit
		if err := rows.Scan(&record.ID, &record.WorkspaceID, &record.ClientID, &record.Kind, &record.Detail, &record.Actor, &record.CreatedAt); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

type clientScanner interface {
	Scan(dest ...any) error
}

func scanClient(row clientScanner) (Client, error) {
	var client Client
	err := row.Scan(&client.ID, &client.WorkspaceID, &client.Name, &client.ExternalKey, &client.CreatedBy, &client.UpdatedBy, &client.CreatedAt, &client.UpdatedAt)
	return client, err
}

func insertClientAudit(ctx context.Context, tx pgx.Tx, workspaceID string, id string, kind string, detail string, actor string) error {
	_, err := tx.Exec(ctx, `
INSERT INTO client_registry_audit (workspace_id, client_id, kind, detail, actor)
VALUES ($1, $2, $3, $4, $5)
`, workspaceID, id, kind, detail, actor)
	return err
}

func clientPostgresError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return fmt.Errorf("%w: external key already exists", ErrConflict)
	}
	return err
}
