package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalStoreAPIClients(t *testing.T) {
	store := NewLocalStore(filepath.Join(t.TempDir(), "state.json"))
	exerciseAPIClientStore(t, store, "local-api-clients")
}

func TestPostgresStoreAPIClients(t *testing.T) {
	dsn := os.Getenv("WINDFORCE_LITE_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("WINDFORCE_LITE_POSTGRES_TEST_DSN is not set")
	}
	store, err := OpenPostgresStore(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	workspaceID := "test-api-clients-" + time.Now().UTC().Format("20060102150405.000000000")
	defer func() {
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM api_client_audit WHERE workspace_id=$1`, workspaceID)
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM api_client WHERE workspace_id=$1`, workspaceID)
	}()
	exerciseAPIClientStore(t, store, workspaceID)
}

func exerciseAPIClientStore(t *testing.T, store Store, workspaceID string) {
	t.Helper()
	ctx := context.Background()
	created, err := store.CreateAPIClient(ctx, workspaceID, "Client A", "client-key-a", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.WorkspaceID != workspaceID || created.CreatedBy != "alice" {
		t.Fatalf("created = %#v", created)
	}
	if _, err := store.CreateAPIClient(ctx, workspaceID, "Duplicate", "client-key-a", "alice"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate error = %v, want conflict", err)
	}
	got, err := store.GetAPIClient(ctx, workspaceID, created.ID)
	if err != nil || got.ID != created.ID {
		t.Fatalf("get = %#v, %v", got, err)
	}
	updated, err := store.UpdateAPIClient(ctx, workspaceID, created.ID, "Client B", "client-key-b", "bob")
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != created.ID || updated.UpdatedBy != "bob" || updated.ClientKey != "client-key-b" {
		t.Fatalf("updated = %#v", updated)
	}
	clients, err := store.ListAPIClients(ctx, workspaceID)
	if err != nil || len(clients) != 1 || clients[0].ID != created.ID {
		t.Fatalf("clients = %#v, %v", clients, err)
	}
	audit, err := store.ListAPIClientAudit(ctx, workspaceID, created.ID)
	if err != nil || len(audit) != 2 || audit[0].Kind != "updated" {
		t.Fatalf("audit = %#v, %v", audit, err)
	}
	for _, record := range audit {
		if strings.Contains(record.Detail, "client-key-") {
			t.Fatalf("audit exposes client key: %#v", record)
		}
	}
	if err := store.DeleteAPIClient(ctx, workspaceID, created.ID, "carol"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetAPIClient(ctx, workspaceID, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted error = %v, want not found", err)
	}
}
