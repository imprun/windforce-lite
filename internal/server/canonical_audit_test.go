package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/imprun/windforce-core/internal/catalog"
	"github.com/imprun/windforce-core/internal/contract"
	"github.com/imprun/windforce-core/internal/gitsource"
	"github.com/imprun/windforce-core/internal/state"
	"github.com/imprun/windforce-core/internal/syncer"
)

func TestCanonicalGitSourceAuditTrail(t *testing.T) {
	tempDir := t.TempDir()
	repoDir := createTestGitSourceRepo(t, tempDir, "repo", "")
	handler := New(Config{
		Syncer:     &syncer.Syncer{CloneRoot: tempDir},
		GitSources: gitsource.NewFileRegistry(filepath.Join(tempDir, "git-sources.json")),
		Catalog:    catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json")),
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	do := func(method, path, actor, body string, wantStatus int) []byte {
		t.Helper()
		var reader *bytes.Buffer
		if body == "" {
			reader = bytes.NewBufferString("")
		} else {
			reader = bytes.NewBufferString(body)
		}
		req, err := http.NewRequest(method, server.URL+path, reader)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if actor != "" {
			req.Header.Set("X-Windforce-Actor", actor)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(resp.Body); err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != wantStatus {
			t.Fatalf("%s %s status = %d, want %d: %s", method, path, resp.StatusCode, wantStatus, buf.String())
		}
		return buf.Bytes()
	}

	registered := do(http.MethodPost, "/api/w/ws-a/git_sources", "alice@example.test", `{
		"name": "source-a",
		"repo_url": "`+filepath.ToSlash(repoDir)+`",
		"branch": "main"
	}`, http.StatusCreated)
	var source struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(registered, &source); err != nil {
		t.Fatal(err)
	}

	sourcePath := "/api/w/ws-a/git_sources/1"
	do(http.MethodPatch, sourcePath, "bob@example.test", `{"name":"source-b"}`, http.StatusOK)

	audit := do(http.MethodGet, sourcePath+"/audit", "", "", http.StatusOK)
	var records []struct {
		GitSourceID int64  `json:"git_source_id"`
		Kind        string `json:"kind"`
		Detail      string `json:"detail"`
		Actor       string `json:"actor"`
		CreatedAt   string `json:"created_at"`
	}
	if err := json.Unmarshal(audit, &records); err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("audit records = %d, want 2: %s", len(records), audit)
	}
	if records[0].Kind != "settings_changed" || records[0].Actor != "bob@example.test" {
		t.Fatalf("newest record = %#v, want settings_changed by bob", records[0])
	}
	if !bytes.Contains([]byte(records[0].Detail), []byte(`name: "source-a" → "source-b"`)) {
		t.Fatalf("settings_changed detail = %q", records[0].Detail)
	}
	if records[1].Kind != "source_registered" || records[1].Actor != "alice@example.test" {
		t.Fatalf("oldest record = %#v, want source_registered by alice", records[1])
	}
	if records[0].GitSourceID != source.ID || records[1].GitSourceID != source.ID {
		t.Fatalf("audit git_source_id = %d/%d, want %d", records[0].GitSourceID, records[1].GitSourceID, source.ID)
	}

	do(http.MethodDelete, sourcePath, "carol@example.test", "", http.StatusNoContent)
	audit = do(http.MethodGet, sourcePath+"/audit", "", "", http.StatusOK)
	if err := json.Unmarshal(audit, &records); err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 || records[0].Kind != "source_deleted" || records[0].Actor != "carol@example.test" {
		t.Fatalf("audit after delete = %s", audit)
	}
}

func TestCanonicalAuditEventsAggregateAndFilter(t *testing.T) {
	tempDir := t.TempDir()
	ctx := context.Background()
	store := state.NewLocalStore(filepath.Join(tempDir, "state.json"))
	catalogStore := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	actor := "operator@example.test"
	message := "Routine release"
	if err := catalogStore.UpsertDeployment(ctx, contract.Deployment{
		Workspace: "ws-a", GitSourceID: "3", App: "shop", Commit: "abcdef1234567890",
		Entrypoint: "main.py", Source: "deploy", Message: &message, CreatedBy: &actor,
		Actions: map[string]contract.Action{"orders": {}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := catalogStore.AppendAudit(ctx, catalog.AuditRecord{
		ID: "source-a", Workspace: "ws-a", GitSourceID: "3", Kind: "settings_changed",
		Detail: "branch changed", Actor: actor,
	}); err != nil {
		t.Fatal(err)
	}
	client, err := store.CreateClient(ctx, "ws-a", "Client A", state.HashClientToken("external-secret"), actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetInputConfig(ctx, state.InputConfig{
		WorkspaceID: "ws-a", AppKey: "shop", ActionKey: "orders", ClientID: client.ID,
		Config: json.RawMessage(`{"tenant":"server-only"}`), LockedKeys: []string{"tenant"},
	}, actor); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(New(Config{Store: store, Catalog: catalogStore}))
	defer server.Close()
	get := func(path string) []canonicalAuditEvent {
		t.Helper()
		resp, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var payload bytes.Buffer
		_, _ = payload.ReadFrom(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status=%d: %s", path, resp.StatusCode, payload.String())
		}
		if bytes.Contains(payload.Bytes(), []byte("server-only")) || bytes.Contains(payload.Bytes(), []byte("external-secret")) {
			t.Fatalf("audit exposes protected value: %s", payload.String())
		}
		var events []canonicalAuditEvent
		if err := json.Unmarshal(payload.Bytes(), &events); err != nil {
			t.Fatal(err)
		}
		return events
	}

	all := get("/api/w/ws-a/audit-events")
	categories := map[string]bool{}
	releaseEvents := 0
	for _, event := range all {
		categories[event.Category] = true
		if event.Category == "release" {
			releaseEvents++
		}
	}
	for _, category := range []string{"repository", "release", "client", "input_settings"} {
		if !categories[category] {
			t.Fatalf("missing category %q in %#v", category, all)
		}
	}
	if releaseEvents != 1 {
		t.Fatalf("release audit events = %d, want 1: %#v", releaseEvents, all)
	}
	appEvents := get("/api/w/ws-a/audit-events?app_key=shop&git_source_id=3")
	for _, event := range appEvents {
		if event.Category == "client" {
			t.Fatalf("app audit contains unrelated client event: %#v", event)
		}
	}
	clientEvents := get("/api/w/ws-a/audit-events?client_id=" + client.ID)
	if len(clientEvents) != 2 {
		t.Fatalf("client events = %#v, want client registration and input settings", clientEvents)
	}
}

func TestParseCanonicalAuditQueryRejectsInvalidFilters(t *testing.T) {
	for _, rawURL := range []string{
		"/api/w/default/audit-events?category=unknown",
		"/api/w/default/audit-events?limit=0",
		"/api/w/default/audit-events?git_source_id=invalid",
		"/api/w/default/audit-events?since=2026-07-16T10:00:00Z&until=2026-07-16T09:00:00Z",
	} {
		t.Run(rawURL, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, rawURL, nil)
			if _, err := parseCanonicalAuditQuery(request); err == nil {
				t.Fatalf("parseCanonicalAuditQuery(%q) accepted an invalid filter", rawURL)
			}
		})
	}
}
