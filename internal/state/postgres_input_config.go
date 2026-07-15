package state

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/imprun/windforce-lite/internal/contract"
)

type inputConfigScanner interface {
	Scan(dest ...any) error
}

func (s *PostgresStore) ListInputConfigsForApp(ctx context.Context, workspaceID string, appKey string) ([]InputConfig, error) {
	rows, err := s.pool.Query(ctx, `
SELECT workspace_id, app_key, action_key, COALESCE(client_id, ''), config, locked_keys, updated_by, updated_at
FROM input_config
WHERE workspace_id=$1 AND app_key=$2
ORDER BY action_key, client_id NULLS FIRST
`, contract.NormalizeWorkspace(workspaceID), appKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanInputConfigs(ctx, rows, workspaceID)
}

func (s *PostgresStore) ListInputConfigsForClient(ctx context.Context, workspaceID string, clientID string) ([]InputConfig, error) {
	if _, err := s.GetClient(ctx, workspaceID, clientID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
SELECT workspace_id, app_key, action_key, COALESCE(client_id, ''), config, locked_keys, updated_by, updated_at
FROM input_config
WHERE workspace_id=$1 AND client_id=$2
ORDER BY app_key, action_key
`, contract.NormalizeWorkspace(workspaceID), clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return s.scanInputConfigs(ctx, rows, workspaceID)
}

func (s *PostgresStore) scanInputConfigs(ctx context.Context, rows pgx.Rows, workspaceID string) ([]InputConfig, error) {
	configs := []InputConfig{}
	for rows.Next() {
		config, err := scanInputConfig(rows)
		if err != nil {
			return nil, err
		}
		plain, err := s.decryptInput(ctx, workspaceID, config.Config)
		if err != nil {
			return nil, err
		}
		config.Config = plain
		configs = append(configs, config)
	}
	return configs, rows.Err()
}

func (s *PostgresStore) SetInputConfig(ctx context.Context, config InputConfig, actor string) (InputConfig, error) {
	config.WorkspaceID = contract.NormalizeWorkspace(config.WorkspaceID)
	config.AppKey = strings.TrimSpace(config.AppKey)
	config.ActionKey = strings.TrimSpace(config.ActionKey)
	config.ClientID = strings.TrimSpace(config.ClientID)
	actor = firstNonEmpty(strings.TrimSpace(actor), defaultActorSubject)
	if config.ClientID != "" {
		if _, err := s.GetClient(ctx, config.WorkspaceID, config.ClientID); err != nil {
			return InputConfig{}, err
		}
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(canonicalJSONInput(config.Config), &values); err != nil || values == nil {
		return InputConfig{}, ErrInvalidInputConfig
	}
	locked, err := normalizedLockedKeys(values, config.LockedKeys)
	if err != nil {
		return InputConfig{}, err
	}
	plain, err := json.Marshal(values)
	if err != nil {
		return InputConfig{}, err
	}
	encrypted, err := s.encryptInput(ctx, config.WorkspaceID, plain)
	if err != nil {
		return InputConfig{}, err
	}
	var saved InputConfig
	err = s.withTx(ctx, func(tx pgx.Tx) error {
		var scanErr error
		saved, scanErr = scanInputConfig(tx.QueryRow(ctx, `
INSERT INTO input_config (workspace_id, app_key, action_key, client_id, config, locked_keys, updated_by, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, now())
ON CONFLICT (workspace_id, app_key, action_key, client_id)
DO UPDATE SET config=EXCLUDED.config, locked_keys=EXCLUDED.locked_keys, updated_by=EXCLUDED.updated_by, updated_at=now()
RETURNING workspace_id, app_key, action_key, COALESCE(client_id, ''), config, locked_keys, updated_by, updated_at
`, config.WorkspaceID, config.AppKey, config.ActionKey, nullableString(config.ClientID), encrypted, locked, actor))
		if scanErr != nil {
			return scanErr
		}
		_, err := tx.Exec(ctx, `
INSERT INTO input_config_audit (workspace_id, app_key, action_key, client_id, kind, detail, actor)
VALUES ($1, $2, $3, $4, 'set', $5, $6)
`, config.WorkspaceID, config.AppKey, config.ActionKey, nullableString(config.ClientID), inputConfigAuditDetail(InputConfig{Config: plain, LockedKeys: locked}), actor)
		return err
	})
	if err != nil {
		return InputConfig{}, err
	}
	saved.Config = plain
	return saved, nil
}

func (s *PostgresStore) DeleteInputConfig(ctx context.Context, workspaceID string, appKey string, actionKey string, clientID string, actor string) error {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var deleted InputConfig
		deleted, err := scanInputConfig(tx.QueryRow(ctx, `
DELETE FROM input_config
WHERE workspace_id=$1 AND app_key=$2 AND action_key=$3 AND client_id IS NOT DISTINCT FROM $4
RETURNING workspace_id, app_key, action_key, COALESCE(client_id, ''), config, locked_keys, updated_by, updated_at
`, workspaceID, strings.TrimSpace(appKey), strings.TrimSpace(actionKey), nullableString(strings.TrimSpace(clientID))))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
INSERT INTO input_config_audit (workspace_id, app_key, action_key, client_id, kind, detail, actor)
VALUES ($1, $2, $3, $4, 'deleted', '', $5)
`, deleted.WorkspaceID, deleted.AppKey, deleted.ActionKey, nullableString(deleted.ClientID), firstNonEmpty(strings.TrimSpace(actor), defaultActorSubject))
		return err
	})
}

func (s *PostgresStore) ListInputConfigAudit(ctx context.Context, workspaceID string, appKey string, clientID string) ([]InputConfigAudit, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id::text, workspace_id, app_key, action_key, COALESCE(client_id, ''), kind, detail, actor, created_at
FROM input_config_audit
WHERE workspace_id=$1 AND ($2='' OR app_key=$2) AND ($3='' OR client_id=$3)
ORDER BY created_at DESC, id DESC
`, contract.NormalizeWorkspace(workspaceID), appKey, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []InputConfigAudit{}
	for rows.Next() {
		var record InputConfigAudit
		if err := rows.Scan(&record.ID, &record.WorkspaceID, &record.AppKey, &record.ActionKey, &record.ClientID, &record.Kind, &record.Detail, &record.Actor, &record.CreatedAt); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *PostgresStore) ResolveInput(ctx context.Context, workspaceID string, appKey string, actionKey string, clientID string, request json.RawMessage) (json.RawMessage, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	if clientID != "" {
		if _, err := s.GetClient(ctx, workspaceID, clientID); err != nil {
			return nil, err
		}
	}
	rows, err := s.pool.Query(ctx, `
SELECT workspace_id, app_key, action_key, COALESCE(client_id, ''), config, locked_keys, updated_by, updated_at
FROM input_config
WHERE workspace_id=$1 AND app_key=$2 AND (action_key='' OR action_key=$3)
  AND (client_id IS NULL OR client_id=$4)
`, workspaceID, appKey, actionKey, nullableString(clientID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	configs, err := s.scanInputConfigs(ctx, rows, workspaceID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(configs, func(i, j int) bool { return inputConfigRank(configs[i]) < inputConfigRank(configs[j]) })
	return resolveInputConfigs(request, configs)
}

func scanInputConfig(row inputConfigScanner) (InputConfig, error) {
	var config InputConfig
	err := row.Scan(&config.WorkspaceID, &config.AppKey, &config.ActionKey, &config.ClientID, &config.Config, &config.LockedKeys, &config.UpdatedBy, &config.UpdatedAt)
	return config, err
}
