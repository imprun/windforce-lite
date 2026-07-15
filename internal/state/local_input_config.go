package state

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/imprun/windforce-lite/internal/contract"
)

func (s *LocalStore) ListInputConfigsForApp(ctx context.Context, workspaceID string, appKey string) ([]InputConfig, error) {
	return s.listLocalInputConfigs(ctx, workspaceID, func(config InputConfig) bool { return config.AppKey == appKey })
}

func (s *LocalStore) ListInputConfigsForClient(ctx context.Context, workspaceID string, clientID string) ([]InputConfig, error) {
	if _, err := s.GetClient(ctx, workspaceID, clientID); err != nil {
		return nil, err
	}
	return s.listLocalInputConfigs(ctx, workspaceID, func(config InputConfig) bool { return config.ClientID == clientID })
}

func (s *LocalStore) listLocalInputConfigs(ctx context.Context, workspaceID string, include func(InputConfig) bool) ([]InputConfig, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	snapshot, err := s.Load(ctx)
	if err != nil {
		return nil, err
	}
	configs := []InputConfig{}
	for _, config := range snapshot.InputConfigs[workspaceID] {
		if !include(config) {
			continue
		}
		plain, err := s.decryptInput(ctx, workspaceID, config.Config)
		if err != nil {
			return nil, err
		}
		config.Config = plain
		config.LockedKeys = append([]string(nil), config.LockedKeys...)
		configs = append(configs, config)
	}
	sort.Slice(configs, func(i, j int) bool {
		if configs[i].AppKey != configs[j].AppKey {
			return configs[i].AppKey < configs[j].AppKey
		}
		if configs[i].ActionKey != configs[j].ActionKey {
			return configs[i].ActionKey < configs[j].ActionKey
		}
		return configs[i].ClientID < configs[j].ClientID
	})
	return configs, nil
}

func (s *LocalStore) SetInputConfig(ctx context.Context, config InputConfig, actor string) (InputConfig, error) {
	config.WorkspaceID = contract.NormalizeWorkspace(config.WorkspaceID)
	config.AppKey = strings.TrimSpace(config.AppKey)
	config.ActionKey = strings.TrimSpace(config.ActionKey)
	config.ClientID = strings.TrimSpace(config.ClientID)
	actor = firstNonEmpty(strings.TrimSpace(actor), defaultActorSubject)
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
	err = s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		if config.ClientID != "" {
			if _, ok := snapshot.Clients[config.WorkspaceID][config.ClientID]; !ok {
				return ErrNotFound
			}
		}
		if snapshot.InputConfigs[config.WorkspaceID] == nil {
			snapshot.InputConfigs[config.WorkspaceID] = map[string]InputConfig{}
		}
		key := inputConfigKey(config.AppKey, config.ActionKey, config.ClientID)
		saved = InputConfig{
			WorkspaceID: config.WorkspaceID, AppKey: config.AppKey, ActionKey: config.ActionKey,
			ClientID: config.ClientID, Config: encrypted, LockedKeys: locked, UpdatedBy: actor, UpdatedAt: now,
		}
		snapshot.InputConfigs[config.WorkspaceID][key] = saved
		appendLocalInputConfigAudit(snapshot, saved, "set", inputConfigAuditDetail(InputConfig{Config: plain, LockedKeys: locked}), actor, now)
		return nil
	})
	if err != nil {
		return InputConfig{}, err
	}
	saved.Config = plain
	return saved, nil
}

func (s *LocalStore) DeleteInputConfig(ctx context.Context, workspaceID string, appKey string, actionKey string, clientID string, actor string) error {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	return s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		key := inputConfigKey(strings.TrimSpace(appKey), strings.TrimSpace(actionKey), strings.TrimSpace(clientID))
		config, ok := snapshot.InputConfigs[workspaceID][key]
		if !ok {
			return nil
		}
		delete(snapshot.InputConfigs[workspaceID], key)
		appendLocalInputConfigAudit(snapshot, config, "deleted", "", firstNonEmpty(strings.TrimSpace(actor), defaultActorSubject), now)
		return nil
	})
}

func (s *LocalStore) ListInputConfigAudit(ctx context.Context, workspaceID string, appKey string, clientID string) ([]InputConfigAudit, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return nil, err
	}
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	records := []InputConfigAudit{}
	for _, record := range snapshot.InputConfigAudits[workspaceID] {
		if appKey != "" && record.AppKey != appKey {
			continue
		}
		if clientID != "" && record.ClientID != clientID {
			continue
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].CreatedAt.After(records[j].CreatedAt) })
	return records, nil
}

func (s *LocalStore) ResolveInput(ctx context.Context, workspaceID string, appKey string, actionKey string, clientID string, request json.RawMessage) (json.RawMessage, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	if clientID != "" {
		if _, err := s.GetClient(ctx, workspaceID, clientID); err != nil {
			return nil, err
		}
	}
	configs, err := s.ListInputConfigsForApp(ctx, workspaceID, appKey)
	if err != nil {
		return nil, err
	}
	applicable := make([]InputConfig, 0, 4)
	for _, config := range configs {
		if config.ActionKey != "" && config.ActionKey != actionKey {
			continue
		}
		if config.ClientID != "" && config.ClientID != clientID {
			continue
		}
		applicable = append(applicable, config)
	}
	return resolveInputConfigs(request, applicable)
}

func appendLocalInputConfigAudit(snapshot *Snapshot, config InputConfig, kind string, detail string, actor string, now time.Time) {
	snapshot.InputConfigAudits[config.WorkspaceID] = append(snapshot.InputConfigAudits[config.WorkspaceID], InputConfigAudit{
		ID: NewID("audit"), WorkspaceID: config.WorkspaceID, AppKey: config.AppKey, ActionKey: config.ActionKey,
		ClientID: config.ClientID, Kind: kind, Detail: detail, Actor: actor, CreatedAt: now,
	})
}
