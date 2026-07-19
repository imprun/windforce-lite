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

func TestExportedRedactedProvisioningCanRoundTrip(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	store := state.NewLocalStore(filepath.Join(dir, "state.json"))
	registry := gitsource.NewFileRegistry(filepath.Join(dir, "git-sources.json"))
	if err := store.SetVariable(ctx, "default", "", "git/app/credential", "stored-secret", true, "credential"); err != nil {
		t.Fatalf("SetVariable credential: %v", err)
	}
	client, err := store.CreateClient(ctx, "default", "Client A", "external-a", "tester")
	if err != nil {
		t.Fatalf("CreateClient: %v", err)
	}
	configJSON := json.RawMessage(`{"CACHE":{"TEST":"123"},"PLAIN":"visible"}`)
	if _, err := store.SetInputConfig(ctx, state.InputConfig{
		WorkspaceID: "default",
		AppKey:      "APP",
		ActionKey:   "1000",
		ClientID:    client.ID,
		Config:      configJSON,
		LockedKeys:  []string{"CACHE"},
	}, "tester"); err != nil {
		t.Fatalf("SetInputConfig: %v", err)
	}
	if _, err := registry.Create(ctx, gitsource.Source{
		Workspace: "default",
		Name:      "app-source",
		RepoURL:   "https://example.test/app.git",
		Branch:    "main",
		TokenEnv:  "git/app/credential",
	}); err != nil {
		t.Fatalf("Create source: %v", err)
	}
	service := Service{Store: store, GitSources: registry, AppKeys: []string{"APP"}}
	exported, err := service.Export(ctx, "default", false)
	if err != nil {
		t.Fatalf("Export returned error: %v", err)
	}
	data, err := EncodeYAML(exported)
	if err != nil {
		t.Fatalf("EncodeYAML returned error: %v", err)
	}
	imported, err := Decode(data, ".yaml")
	if err != nil {
		t.Fatalf("Decode exported YAML returned error: %v", err)
	}
	if _, err := service.Apply(ctx, imported, Options{Workspace: "default", Actor: "tester", DryRun: true}); err != nil {
		t.Fatalf("dry-run of exported redacted provisioning failed: %v\n%s", err, data)
	}
	if _, err := service.Apply(ctx, imported, Options{Workspace: "default", Actor: "tester"}); err != nil {
		t.Fatalf("apply of exported redacted provisioning failed: %v\n%s", err, data)
	}
	variable, found, err := store.GetVariableExact(ctx, "default", "", "git/app/credential")
	if err != nil || !found {
		t.Fatalf("credential after apply: found=%v err=%v", found, err)
	}
	if variable.Value != "stored-secret" || !variable.IsSecret {
		t.Fatalf("credential variable changed: %#v", variable)
	}
	preserved, err := store.ListInputConfigsForClient(ctx, "default", client.ID)
	if err != nil {
		t.Fatalf("ListInputConfigsForClient: %v", err)
	}
	if len(preserved) != 1 {
		t.Fatalf("input configs count = %d", len(preserved))
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(preserved[0].Config, &values); err != nil {
		t.Fatalf("config JSON: %v", err)
	}
	var cache map[string]string
	if err := json.Unmarshal(values["CACHE"], &cache); err != nil {
		t.Fatalf("CACHE JSON: %v", err)
	}
	if cache["TEST"] != "123" {
		t.Fatalf("redacted CACHE was not preserved: %#v", cache)
	}
	if string(values["PLAIN"]) != `"visible"` {
		t.Fatalf("redacted PLAIN was not preserved: %s", values["PLAIN"])
	}
}

func TestRedactedProvisioningRequiresExistingValue(t *testing.T) {
	dir := t.TempDir()
	docs, err := Decode([]byte(`
kind: Variable
metadata:
  name: missing-secret
spec:
  path: secret/missing
  secret: true
  value:
    redacted: true
`), ".yaml")
	if err != nil {
		t.Fatalf("Decode returned error: %v", err)
	}
	store := state.NewLocalStore(filepath.Join(dir, "state.json"))
	_, err = (Service{Store: store}).Apply(context.Background(), docs, Options{Workspace: "default", DryRun: true})
	if err == nil || !strings.Contains(err.Error(), "redacted") {
		t.Fatalf("Apply error = %v, want redacted existing value error", err)
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
