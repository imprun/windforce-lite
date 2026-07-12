package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/imprun/windforce-lite/internal/catalog"
	"github.com/imprun/windforce-lite/internal/gitsource"
	"github.com/imprun/windforce-lite/internal/syncer"
)

func TestCanonicalGitSourceAuditTrail(t *testing.T) {
	tempDir := t.TempDir()
	repoDir := createTestGitSourceRepo(t, tempDir, "repo", "")
	handler := New(Config{
		Syncer:     &syncer.Syncer{CloneRoot: tempDir},
		GitSources: gitsource.NewFileRegistry(filepath.Join(tempDir, "git-sources.json")),
		Catalog:    catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json")),
		EnableAPI:  true,
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
