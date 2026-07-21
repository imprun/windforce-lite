package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/imprun/windforce-core/internal/contract"
)

func (s *LocalStore) ListClients(ctx context.Context, workspaceID string) ([]Client, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return nil, err
	}
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	clients := make([]Client, 0, len(snapshot.Clients[workspaceID]))
	for _, client := range snapshot.Clients[workspaceID] {
		clients = append(clients, client)
	}
	sort.Slice(clients, func(i, j int) bool {
		if clients[i].Name != clients[j].Name {
			return clients[i].Name < clients[j].Name
		}
		return clients[i].ID < clients[j].ID
	})
	return clients, nil
}

func (s *LocalStore) GetClient(ctx context.Context, workspaceID string, id string) (Client, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return Client{}, err
	}
	client, ok := snapshot.Clients[contract.NormalizeWorkspace(workspaceID)][id]
	if !ok {
		return Client{}, ErrNotFound
	}
	return client, nil
}

func (s *LocalStore) GetClientByTokenHash(ctx context.Context, workspaceID string, tokenHash string) (Client, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return Client{}, err
	}
	for _, client := range snapshot.Clients[contract.NormalizeWorkspace(workspaceID)] {
		if tokenHash != "" && client.TokenHash == tokenHash {
			return client, nil
		}
	}
	return Client{}, ErrNotFound
}

func (s *LocalStore) CreateClient(ctx context.Context, workspaceID string, name string, tokenHash string, actor string) (Client, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	var created Client
	err := s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		if snapshot.Clients[workspaceID] == nil {
			snapshot.Clients[workspaceID] = map[string]Client{}
		}
		if clientTokenHashExists(snapshot.Clients[workspaceID], tokenHash, "") {
			return fmt.Errorf("%w: client token already exists", ErrConflict)
		}
		created = Client{
			ID: NewID("client"), WorkspaceID: workspaceID, Name: name, TokenHash: tokenHash,
			CreatedBy: actor, UpdatedBy: actor, CreatedAt: now, UpdatedAt: now,
		}
		snapshot.Clients[workspaceID][created.ID] = created
		appendLocalClientAudit(snapshot, workspaceID, created.ID, "created", "", actor, now)
		return nil
	})
	return created, err
}

func (s *LocalStore) UpdateClient(ctx context.Context, workspaceID string, id string, name string, actor string) (Client, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	var updated Client
	err := s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		current, ok := snapshot.Clients[workspaceID][id]
		if !ok {
			return ErrNotFound
		}
		detail := clientChangeDetail(current, name)
		current.Name = name
		current.UpdatedBy = actor
		current.UpdatedAt = now
		snapshot.Clients[workspaceID][id] = current
		appendLocalClientAudit(snapshot, workspaceID, id, "updated", detail, actor, now)
		updated = current
		return nil
	})
	return updated, err
}

func (s *LocalStore) RotateClientToken(ctx context.Context, workspaceID string, id string, tokenHash string, actor string) (Client, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	if tokenHash == "" {
		return Client{}, ErrInvalidState
	}
	var updated Client
	err := s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		current, ok := snapshot.Clients[workspaceID][id]
		if !ok {
			return ErrNotFound
		}
		if clientTokenHashExists(snapshot.Clients[workspaceID], tokenHash, id) {
			return fmt.Errorf("%w: client token already exists", ErrConflict)
		}
		current.TokenHash = tokenHash
		current.UpdatedBy = actor
		current.UpdatedAt = now
		snapshot.Clients[workspaceID][id] = current
		appendLocalClientAudit(snapshot, workspaceID, id, "token_rotated", "", actor, now)
		updated = current
		return nil
	})
	return updated, err
}

func (s *LocalStore) RevokeClientToken(ctx context.Context, workspaceID string, id string, actor string) (Client, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	var updated Client
	err := s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		current, ok := snapshot.Clients[workspaceID][id]
		if !ok {
			return ErrNotFound
		}
		if current.TokenHash == "" {
			return ErrInvalidState
		}
		current.TokenHash = ""
		current.UpdatedBy = actor
		current.UpdatedAt = now
		snapshot.Clients[workspaceID][id] = current
		appendLocalClientAudit(snapshot, workspaceID, id, "token_revoked", "", actor, now)
		updated = current
		return nil
	})
	return updated, err
}

func (s *LocalStore) DeleteClient(ctx context.Context, workspaceID string, id string, actor string) error {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	return s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		client, ok := snapshot.Clients[workspaceID][id]
		if !ok {
			return ErrNotFound
		}
		if client.TokenHash != "" {
			return fmt.Errorf("%w: revoke the active client token before deleting the client", ErrConflict)
		}
		delete(snapshot.Clients[workspaceID], id)
		for key, config := range snapshot.InputConfigs[workspaceID] {
			if config.ClientID == id {
				delete(snapshot.InputConfigs[workspaceID], key)
			}
		}
		appendLocalClientAudit(snapshot, workspaceID, id, "deleted", "", actor, now)
		return nil
	})
}

func (s *LocalStore) AppendClientAudit(ctx context.Context, workspaceID string, id string, kind string, detail string, actor string) error {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	return s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		appendLocalClientAudit(snapshot, workspaceID, id, kind, detail, actor, now)
		return nil
	})
}

func (s *LocalStore) ListClientAudit(ctx context.Context, workspaceID string, id string) ([]ClientAudit, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return nil, err
	}
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	records := []ClientAudit{}
	for _, record := range snapshot.ClientAudits[workspaceID] {
		if id == "" || record.ClientID == id {
			records = append(records, record)
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].CreatedAt.After(records[j].CreatedAt) })
	return records, nil
}

func clientTokenHashExists(clients map[string]Client, tokenHash string, exceptID string) bool {
	if tokenHash == "" {
		return false
	}
	for id, client := range clients {
		if id != exceptID && client.TokenHash == tokenHash {
			return true
		}
	}
	return false
}

func appendLocalClientAudit(snapshot *Snapshot, workspaceID string, id string, kind string, detail string, actor string, now time.Time) {
	snapshot.ClientAudits[workspaceID] = append(snapshot.ClientAudits[workspaceID], ClientAudit{
		ID: NewID("audit"), WorkspaceID: workspaceID, ClientID: id,
		Kind: kind, Detail: detail, Actor: actor, CreatedAt: now,
	})
}

func clientChangeDetail(current Client, name string) string {
	if current.Name != name {
		return "name changed"
	}
	return "no value change"
}
