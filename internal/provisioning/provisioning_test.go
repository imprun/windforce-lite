package provisioning

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imprun/windforce-core/internal/gitsource"
	"github.com/imprun/windforce-core/internal/state"
)

func TestApplyProvisioningResources(t *testing.T) {
	t.Setenv("TEST_GIT_TOKEN", "token-a")
	t.Setenv("TEST_CLIENT_KEY", "client-a")
	t.Setenv("TEST_PROXY", "http://proxy.local:8080")
	dir := t.TempDir()
	docs, err := Decode([]byte(`
resources:
  - apiVersion: windforce-lite.imprun.dev/v1
    kind: GitCredential
    metadata:
      name: app-git
    spec:
      method: pat
      storageRef: git/app/credential
      token:
        valueFrom:
          env: TEST_GIT_TOKEN
  - kind: AppSource
    metadata:
      name: app-source
    spec:
      repository:
        url: https://example.test/app.git
        branch: main
        authRef: app-git
  - kind: Client
    metadata:
      name: Client A
    spec:
      externalKey:
        valueFrom:
          env: TEST_CLIENT_KEY
  - kind: InputSettings
    metadata:
      name: app-default
    spec:
      appKey: APP
      actionKey: "1000"
      clientRef: Client A
      lockedKeys:
        - APP_PROXY_URL
      config:
        APP_PROXY_URL:
          valueFrom:
            env: TEST_PROXY
        ROUTE:
          gap: plus
          eul: classic
`), ".yaml")
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	store := state.NewLocalStore(filepath.Join(dir, "state.json"))
	registry := gitsource.NewFileRegistry(filepath.Join(dir, "git-sources.json"))
	result, err := (Service{Store: store, GitSources: registry}).Apply(context.Background(), docs, Options{
		Workspace: "default",
		Actor:     "tester",
	})
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if got := len(result.Applied); got != 4 {
		t.Fatalf("applied count = %d, want 4", got)
	}
	source, err := registry.Get(context.Background(), "default", "app-source")
	if err != nil {
		t.Fatalf("source not stored: %v", err)
	}
	if source.TokenEnv != "git/app/credential" {
		t.Fatalf("TokenEnv = %q", source.TokenEnv)
	}
	variable, found, err := store.GetVariableExact(context.Background(), "default", "", "git/app/credential")
	if err != nil || !found {
		t.Fatalf("credential variable missing: found=%v err=%v", found, err)
	}
	if !variable.IsSecret {
		t.Fatalf("credential variable was not marked secret")
	}
	clients, err := store.ListClients(context.Background(), "default")
	if err != nil {
		t.Fatalf("ListClients: %v", err)
	}
	if len(clients) != 1 || clients[0].ExternalKey != "client-a" {
		t.Fatalf("clients = %#v", clients)
	}
	configs, err := store.ListInputConfigsForClient(context.Background(), "default", clients[0].ID)
	if err != nil {
		t.Fatalf("ListInputConfigsForClient: %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("configs count = %d", len(configs))
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(configs[0].Config, &values); err != nil {
		t.Fatalf("config JSON: %v", err)
	}
	if string(values["APP_PROXY_URL"]) != `"http://proxy.local:8080"` {
		t.Fatalf("APP_PROXY_URL = %s", values["APP_PROXY_URL"])
	}
}

func TestExportRedactsSensitiveProvisioningValues(t *testing.T) {
	dir := t.TempDir()
	store := state.NewLocalStore(filepath.Join(dir, "state.json"))
	registry := gitsource.NewFileRegistry(filepath.Join(dir, "git-sources.json"))
	if err := store.SetVariable(context.Background(), "default", "", "secret/token", "encrypted", true, "secret"); err != nil {
		t.Fatalf("SetVariable: %v", err)
	}
	if _, err := store.CreateClient(context.Background(), "default", "Client A", "external-a", "tester"); err != nil {
		t.Fatalf("CreateClient: %v", err)
	}
	if _, err := registry.Create(context.Background(), gitsource.Source{
		Workspace: "default",
		Name:      "app-source",
		RepoURL:   "https://example.test/app.git",
		TokenEnv:  "secret/token",
	}); err != nil {
		t.Fatalf("Create source: %v", err)
	}
	docs, err := (Service{Store: store, GitSources: registry}).Export(context.Background(), "default", false)
	if err != nil {
		t.Fatalf("Export returned error: %v", err)
	}
	data, err := EncodeYAML(docs)
	if err != nil {
		t.Fatalf("EncodeYAML returned error: %v", err)
	}
	text := string(data)
	if containsAny(text, "external-a", "encrypted") {
		t.Fatalf("export leaked sensitive value:\n%s", text)
	}
	if !containsAny(text, "redacted: true") {
		t.Fatalf("export did not include redaction marker:\n%s", text)
	}
}

func TestLoadDirReadsJSONAndYAML(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.yaml"), []byte("kind: Client\nmetadata:\n  name: A\nspec:\n  externalKey:\n    value: a\n"), 0o644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.json"), []byte(`{"kind":"Client","metadata":{"name":"B"},"spec":{"externalKey":{"value":"b"}}}`), 0o644); err != nil {
		t.Fatalf("write json: %v", err)
	}
	docs, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir returned error: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("docs count = %d", len(docs))
	}
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}
