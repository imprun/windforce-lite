package state

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestApplyInputOverlayUsesShallowPrecedenceAndLocks(t *testing.T) {
	layers := []ConfigLayer{
		{Config: rawObject(t, `{"region":"kr","options":{"source":"app"}}`)},
		{Config: rawObject(t, `{"mode":"action"}`)},
		{Config: rawObject(t, `{"region":"client"}`)},
		{Config: rawObject(t, `{"tenant":"client-a","options":{"source":"client"}}`), LockedKeys: []string{"tenant"}},
	}
	effective, rejected := ApplyInputOverlay(rawObject(t, `{"region":"request","extra":1}`), layers)
	if len(rejected) != 0 {
		t.Fatalf("rejected = %v", rejected)
	}
	assertRawObject(t, effective, `{"region":"request","mode":"action","tenant":"client-a","options":{"source":"client"},"extra":1}`)

	_, rejected = ApplyInputOverlay(rawObject(t, `{"tenant":"spoofed"}`), layers)
	if len(rejected) != 1 || rejected[0] != "tenant" {
		t.Fatalf("rejected = %v, want tenant", rejected)
	}
}

func TestLocalStoreInputConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := NewLocalStore(path)
	store.ConfigureInputCrypto("test-secret", "")
	exerciseInputConfigStore(t, store, "local-input-config")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "server-only-value") {
		t.Fatal("state snapshot contains plaintext input setting")
	}
}

func TestPostgresStoreInputConfig(t *testing.T) {
	dsn := os.Getenv("WINDFORCE_LITE_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("WINDFORCE_LITE_POSTGRES_TEST_DSN is not set")
	}
	store, err := OpenPostgresStore(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.ConfigureInputCrypto("test-secret", "")
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	workspaceID := "test-input-config-" + time.Now().UTC().Format("20060102150405.000000000")
	defer func() {
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM input_config_audit WHERE workspace_id=$1`, workspaceID)
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM input_config WHERE workspace_id=$1`, workspaceID)
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM client_registry_audit WHERE workspace_id=$1`, workspaceID)
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM client_registry WHERE workspace_id=$1`, workspaceID)
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM workspace_key WHERE workspace_id=$1`, workspaceID)
	}()
	exerciseInputConfigStore(t, store, workspaceID)
	var stored string
	if err := store.pool.QueryRow(context.Background(), `
SELECT config::text FROM input_config
WHERE workspace_id=$1 AND app_key='shop' AND action_key='' AND client_id IS NULL
`, workspaceID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored, `"__wf_enc"`) || strings.Contains(stored, "region") {
		t.Fatalf("input config is not encrypted at rest: %s", stored)
	}
}

func exerciseInputConfigStore(t *testing.T, store Store, workspaceID string) {
	t.Helper()
	ctx := context.Background()
	client, err := store.CreateClient(ctx, workspaceID, "Client A", "external-a", "alice")
	if err != nil {
		t.Fatal(err)
	}
	byKey, err := store.GetClientByExternalKey(ctx, workspaceID, "external-a")
	if err != nil || byKey.ID != client.ID {
		t.Fatalf("client by key = %#v, %v", byKey, err)
	}
	settings := []InputConfig{
		{WorkspaceID: workspaceID, AppKey: "shop", Config: json.RawMessage(`{"region":"kr","mode":"app"}`)},
		{WorkspaceID: workspaceID, AppKey: "shop", ActionKey: "orders", Config: json.RawMessage(`{"mode":"action"}`)},
		{WorkspaceID: workspaceID, AppKey: "shop", ClientID: client.ID, Config: json.RawMessage(`{"customer":"client-a"}`)},
		{WorkspaceID: workspaceID, AppKey: "shop", ActionKey: "orders", ClientID: client.ID, Config: json.RawMessage(`{"tenant":"server-only-value"}`), LockedKeys: []string{"tenant"}},
	}
	for _, setting := range settings {
		if _, err := store.SetInputConfig(ctx, setting, "alice"); err != nil {
			t.Fatal(err)
		}
	}
	appConfigs, err := store.ListInputConfigsForApp(ctx, workspaceID, "shop")
	if err != nil || len(appConfigs) != 4 {
		t.Fatalf("app configs = %#v, %v", appConfigs, err)
	}
	clientConfigs, err := store.ListInputConfigsForClient(ctx, workspaceID, client.ID)
	if err != nil || len(clientConfigs) != 2 {
		t.Fatalf("client configs = %#v, %v", clientConfigs, err)
	}
	effective, err := store.ResolveInput(ctx, workspaceID, "shop", "orders", client.ID, json.RawMessage(`{"region":"request","extra":1}`))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(effective, &got); err != nil {
		t.Fatal(err)
	}
	assertRawObject(t, got, `{"region":"request","mode":"action","customer":"client-a","tenant":"server-only-value","extra":1}`)

	_, err = store.ResolveInput(ctx, workspaceID, "shop", "orders", client.ID, json.RawMessage(`{"tenant":"spoofed"}`))
	var locked *LockedKeysError
	if !errors.As(err, &locked) || len(locked.Keys) != 1 || locked.Keys[0] != "tenant" {
		t.Fatalf("locked error = %#v", err)
	}
	audit, err := store.ListInputConfigAudit(ctx, workspaceID, "shop", client.ID)
	if err != nil || len(audit) != 2 {
		t.Fatalf("audit = %#v, %v", audit, err)
	}
	for _, record := range audit {
		if strings.Contains(record.Detail, "server-only-value") {
			t.Fatalf("audit exposes config value: %#v", record)
		}
	}
	if err := store.DeleteClient(ctx, workspaceID, client.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveInput(ctx, workspaceID, "shop", "orders", client.ID, json.RawMessage(`{}`)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("resolve after client deletion = %v, want not found", err)
	}
}

func rawObject(t *testing.T, value string) map[string]json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(value), &object); err != nil {
		t.Fatal(err)
	}
	return object
}

func assertRawObject(t *testing.T, got map[string]json.RawMessage, wantJSON string) {
	t.Helper()
	want := rawObject(t, wantJSON)
	gotJSON, _ := json.Marshal(got)
	wantBytes, _ := json.Marshal(want)
	if string(gotJSON) != string(wantBytes) {
		t.Fatalf("object = %s, want %s", gotJSON, wantBytes)
	}
}
