package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/imprun/windforce-lite/internal/contract"
)

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

func (s *PostgresStore) GetState(ctx context.Context, workspaceID string, statePath string) (json.RawMessage, bool, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	var value json.RawMessage
	err := s.pool.QueryRow(ctx, `
SELECT value
FROM job_state
WHERE workspace_id=$1 AND state_path=$2
`, workspaceID, statePath).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return json.RawMessage("null"), false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return cloneRaw(value), true, nil
}

func (s *PostgresStore) SetState(ctx context.Context, workspaceID string, statePath string, value json.RawMessage) error {
	if len(value) == 0 {
		value = json.RawMessage("null")
	}
	if !json.Valid(value) {
		return errors.New("state value is not valid JSON")
	}
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	_, err := s.pool.Exec(ctx, `
INSERT INTO job_state (workspace_id, state_path, value, updated_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (workspace_id, state_path)
DO UPDATE SET value=EXCLUDED.value, updated_at=now()
`, workspaceID, statePath, value)
	return err
}

func (s *PostgresStore) ListVariables(ctx context.Context, workspaceID string) ([]Variable, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	rows, err := s.pool.Query(ctx, `
SELECT app_key, path, value, is_secret, description
FROM variable
WHERE workspace_id=$1
ORDER BY app_key, path
`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	variables := []Variable{}
	for rows.Next() {
		var variable Variable
		if err := rows.Scan(&variable.AppKey, &variable.Path, &variable.Value, &variable.IsSecret, &variable.Description); err != nil {
			return nil, err
		}
		variables = append(variables, variable)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return variables, nil
}

func (s *PostgresStore) SetVariable(ctx context.Context, workspaceID string, appKey string, path string, value string, isSecret bool, description string) error {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	_, err := s.pool.Exec(ctx, `
INSERT INTO variable (workspace_id, app_key, path, value, is_secret, description)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (workspace_id, app_key, path)
DO UPDATE SET value=EXCLUDED.value, is_secret=EXCLUDED.is_secret, description=EXCLUDED.description
`, workspaceID, appKey, path, value, isSecret, description)
	return err
}

func (s *PostgresStore) GetVariable(ctx context.Context, workspaceID string, appKey string, path string) (Variable, bool, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	var variable Variable
	err := s.pool.QueryRow(ctx, `
SELECT app_key, path, value, is_secret, description
FROM variable
WHERE workspace_id=$1 AND path=$2 AND (app_key=$3 OR app_key='')
ORDER BY app_key DESC
LIMIT 1
`, workspaceID, path, appKey).Scan(&variable.AppKey, &variable.Path, &variable.Value, &variable.IsSecret, &variable.Description)
	if errors.Is(err, pgx.ErrNoRows) {
		return Variable{}, false, nil
	}
	if err != nil {
		return Variable{}, false, err
	}
	return variable, true, nil
}

func (s *PostgresStore) GetVariableExact(ctx context.Context, workspaceID string, appKey string, path string) (Variable, bool, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	var variable Variable
	err := s.pool.QueryRow(ctx, `
SELECT app_key, path, value, is_secret, description
FROM variable
WHERE workspace_id=$1 AND app_key=$2 AND path=$3
`, workspaceID, appKey, path).Scan(&variable.AppKey, &variable.Path, &variable.Value, &variable.IsSecret, &variable.Description)
	if errors.Is(err, pgx.ErrNoRows) {
		return Variable{}, false, nil
	}
	if err != nil {
		return Variable{}, false, err
	}
	return variable, true, nil
}

func (s *PostgresStore) GetWorkspaceKeyVersioned(ctx context.Context, workspaceID string) (string, int32, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	var key string
	var version int32
	err := s.pool.QueryRow(ctx, `
SELECT key, kek_version
FROM workspace_key
WHERE workspace_id=$1
`, workspaceID).Scan(&key, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, nil
	}
	if err != nil {
		return "", 0, err
	}
	return key, version, nil
}

func (s *PostgresStore) DeleteVariable(ctx context.Context, workspaceID string, appKey string, path string) error {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	_, err := s.pool.Exec(ctx, `
DELETE FROM variable
WHERE workspace_id=$1 AND app_key=$2 AND path=$3
`, workspaceID, appKey, path)
	return err
}

func (s *PostgresStore) SetResource(ctx context.Context, workspaceID string, path string, value json.RawMessage, resourceType string, description string) error {
	if len(value) == 0 {
		value = json.RawMessage("{}")
	}
	if !json.Valid(value) {
		return errors.New("resource value is not valid JSON")
	}
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	_, err := s.pool.Exec(ctx, `
INSERT INTO resource (workspace_id, path, value, resource_type, description)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (workspace_id, path)
DO UPDATE SET value=EXCLUDED.value, resource_type=EXCLUDED.resource_type, description=EXCLUDED.description
`, workspaceID, path, value, resourceType, description)
	return err
}

func (s *PostgresStore) GetResource(ctx context.Context, workspaceID string, path string) (Resource, bool, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	var resource Resource
	err := s.pool.QueryRow(ctx, `
SELECT path, value, resource_type, description
FROM resource
WHERE workspace_id=$1 AND path=$2
`, workspaceID, path).Scan(&resource.Path, &resource.Value, &resource.ResourceType, &resource.Description)
	if errors.Is(err, pgx.ErrNoRows) {
		return Resource{}, false, nil
	}
	if err != nil {
		return Resource{}, false, err
	}
	resource.Value = cloneRaw(resource.Value)
	return resource, true, nil
}
