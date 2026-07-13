package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/imprun/windforce-lite/internal/contract"
)

func (s *LocalStore) ListAPIClients(ctx context.Context, workspaceID string) ([]APIClient, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return nil, err
	}
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	clients := make([]APIClient, 0, len(snapshot.APIClients[workspaceID]))
	for _, client := range snapshot.APIClients[workspaceID] {
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

func (s *LocalStore) GetAPIClient(ctx context.Context, workspaceID string, id string) (APIClient, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return APIClient{}, err
	}
	client, ok := snapshot.APIClients[contract.NormalizeWorkspace(workspaceID)][id]
	if !ok {
		return APIClient{}, ErrNotFound
	}
	return client, nil
}

func (s *LocalStore) CreateAPIClient(ctx context.Context, workspaceID string, name string, clientKey string, actor string) (APIClient, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	var created APIClient
	err := s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		if snapshot.APIClients[workspaceID] == nil {
			snapshot.APIClients[workspaceID] = map[string]APIClient{}
		}
		if apiClientKeyExists(snapshot.APIClients[workspaceID], clientKey, "") {
			return fmt.Errorf("%w: client key already exists", ErrConflict)
		}
		created = APIClient{
			ID: NewID("client"), WorkspaceID: workspaceID, Name: name, ClientKey: clientKey,
			CreatedBy: actor, UpdatedBy: actor, CreatedAt: now, UpdatedAt: now,
		}
		snapshot.APIClients[workspaceID][created.ID] = created
		appendLocalAPIClientAudit(snapshot, workspaceID, created.ID, "created", "", actor, now)
		return nil
	})
	return created, err
}

func (s *LocalStore) UpdateAPIClient(ctx context.Context, workspaceID string, id string, name string, clientKey string, actor string) (APIClient, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	var updated APIClient
	err := s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		current, ok := snapshot.APIClients[workspaceID][id]
		if !ok {
			return ErrNotFound
		}
		if apiClientKeyExists(snapshot.APIClients[workspaceID], clientKey, id) {
			return fmt.Errorf("%w: client key already exists", ErrConflict)
		}
		detail := apiClientChangeDetail(current, name, clientKey)
		current.Name = name
		current.ClientKey = clientKey
		current.UpdatedBy = actor
		current.UpdatedAt = now
		snapshot.APIClients[workspaceID][id] = current
		appendLocalAPIClientAudit(snapshot, workspaceID, id, "updated", detail, actor, now)
		updated = current
		return nil
	})
	return updated, err
}

func (s *LocalStore) DeleteAPIClient(ctx context.Context, workspaceID string, id string, actor string) error {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	return s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		if _, ok := snapshot.APIClients[workspaceID][id]; !ok {
			return ErrNotFound
		}
		delete(snapshot.APIClients[workspaceID], id)
		appendLocalAPIClientAudit(snapshot, workspaceID, id, "deleted", "", actor, now)
		return nil
	})
}

func (s *LocalStore) ListAPIClientAudit(ctx context.Context, workspaceID string, id string) ([]APIClientAudit, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return nil, err
	}
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	records := []APIClientAudit{}
	for _, record := range snapshot.APIClientAudits[workspaceID] {
		if record.APIClientID == id {
			records = append(records, record)
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].CreatedAt.After(records[j].CreatedAt) })
	return records, nil
}

func apiClientKeyExists(clients map[string]APIClient, clientKey string, exceptID string) bool {
	for id, client := range clients {
		if id != exceptID && client.ClientKey == clientKey {
			return true
		}
	}
	return false
}

func appendLocalAPIClientAudit(snapshot *Snapshot, workspaceID string, id string, kind string, detail string, actor string, now time.Time) {
	snapshot.APIClientAudits[workspaceID] = append(snapshot.APIClientAudits[workspaceID], APIClientAudit{
		ID: NewID("audit"), WorkspaceID: workspaceID, APIClientID: id,
		Kind: kind, Detail: detail, Actor: actor, CreatedAt: now,
	})
}

func apiClientChangeDetail(current APIClient, name string, clientKey string) string {
	changes := ""
	if current.Name != name {
		changes = "name changed"
	}
	if current.ClientKey != clientKey {
		if changes != "" {
			changes += "; "
		}
		changes += "client key changed"
	}
	if changes == "" {
		return "no value change"
	}
	return changes
}
