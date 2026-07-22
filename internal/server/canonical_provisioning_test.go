package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imprun/windforce-core/internal/gitsource"
	"github.com/imprun/windforce-core/internal/state"
)

func TestCanonicalProvisioningImportExport(t *testing.T) {
	store := state.NewLocalStore(filepath.Join(t.TempDir(), "state.json"))
	registry := gitsource.NewFileRegistry(filepath.Join(t.TempDir(), "git-sources.json"))
	server := httptest.NewServer(New(Config{
		Store:      store,
		GitSources: registry,
	}))
	defer server.Close()

	body := `
resources:
  - kind: AppSource
    metadata:
      name: app-source
    spec:
      repository:
        url: https://example.test/app.git
        branch: main
  - kind: Client
    metadata:
      name: Client A
`
	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/w/default/provisioning/import", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/yaml")
	req.Header.Set("X-Windforce-Actor", "alice@example.test")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var payload struct {
		Applied []struct {
			Kind   string `json:"kind"`
			Action string `json:"action"`
		} `json:"applied"`
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import status = %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Applied) != 2 || payload.Applied[0].Action != "created" || payload.Applied[1].Kind != "Client" {
		t.Fatalf("payload = %#v", payload)
	}

	resp, err = http.Get(server.URL + "/api/w/default/provisioning/export?format=yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var exported bytes.Buffer
	if _, err := exported.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export status = %d: %s", resp.StatusCode, exported.String())
	}
	if !strings.Contains(exported.String(), "kind: AppSource") || strings.Contains(exported.String(), "client-a") {
		t.Fatalf("unexpected export:\n%s", exported.String())
	}
}
