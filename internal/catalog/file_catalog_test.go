package catalog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/imprun/windforce-lite/internal/contract"
)

func TestFileCatalogUpsertAndGet(t *testing.T) {
	catalog := NewFileCatalog(filepath.Join(t.TempDir(), "catalog.json"))
	deployment := contract.Deployment{
		App:       "echo",
		Commit:    "commit-a",
		ObjectURI: "bundle://echo/commit-a",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo"},
		},
	}
	if err := catalog.UpsertDeployment(context.Background(), deployment); err != nil {
		t.Fatalf("UpsertDeployment returned error: %v", err)
	}

	got, err := catalog.GetDeployment(context.Background(), "echo")
	if err != nil {
		t.Fatalf("GetDeployment returned error: %v", err)
	}
	if got.Commit != "commit-a" {
		t.Fatalf("commit = %q", got.Commit)
	}
	if got.Tag != "default" || got.TimeoutS != 300 || got.ScriptLang != "typescript" {
		t.Fatalf("defaults = tag:%q timeout:%d scriptLang:%q", got.Tag, got.TimeoutS, got.ScriptLang)
	}
	if got.UpdatedAt == nil {
		t.Fatalf("deployment updatedAt was not set")
	}
	if got.Actions["echo"].UpdatedAt == nil {
		t.Fatalf("action updatedAt was not set")
	}
	snapshot, err := catalog.Load(context.Background())
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(snapshot.History) != 1 {
		t.Fatalf("history count = %d, want 1", len(snapshot.History))
	}
	if snapshot.History[0].Commit != "commit-a" || snapshot.History[0].Status != "deployed" {
		t.Fatalf("history item = %#v", snapshot.History[0])
	}
	if snapshot.History[0].Deployment.Tag != "default" ||
		snapshot.History[0].Deployment.TimeoutS != 300 ||
		snapshot.History[0].Deployment.ScriptLang != "typescript" {
		t.Fatalf("history deployment defaults = %#v", snapshot.History[0].Deployment)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(snapshot.History[0].ID) {
		t.Fatalf("history id = %q, want UUID app version id", snapshot.History[0].ID)
	}
}

func TestFileCatalogScopesDeploymentsByWorkspace(t *testing.T) {
	catalog := NewFileCatalog(filepath.Join(t.TempDir(), "catalog.json"))
	for _, deployment := range []contract.Deployment{
		{Workspace: "ws-a", App: "echo", Commit: "commit-a", Entrypoint: "main.ts", Actions: map[string]contract.Action{"echo": {Action: "echo"}}},
		{Workspace: "ws-b", App: "echo", Commit: "commit-b", Entrypoint: "main.ts", Actions: map[string]contract.Action{"echo": {Action: "echo"}}},
	} {
		if err := catalog.UpsertDeployment(context.Background(), deployment); err != nil {
			t.Fatalf("UpsertDeployment returned error: %v", err)
		}
	}

	gotA, err := catalog.GetDeploymentForWorkspace(context.Background(), "ws-a", "echo")
	if err != nil {
		t.Fatalf("GetDeploymentForWorkspace(ws-a) returned error: %v", err)
	}
	gotB, err := catalog.GetDeploymentForWorkspace(context.Background(), "ws-b", "echo")
	if err != nil {
		t.Fatalf("GetDeploymentForWorkspace(ws-b) returned error: %v", err)
	}
	if gotA.Commit != "commit-a" || gotB.Commit != "commit-b" {
		t.Fatalf("workspace deployments crossed: ws-a=%q ws-b=%q", gotA.Commit, gotB.Commit)
	}
	if _, err := catalog.GetDeployment(context.Background(), "echo"); err != ErrDeploymentNotFound {
		t.Fatalf("legacy default lookup error = %v, want ErrDeploymentNotFound", err)
	}
}

func TestFileCatalogMigratesLegacyAppKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.json")
	legacy := Snapshot{
		Deployments: map[string]contract.Deployment{
			"echo": {Workspace: "ws-a", App: "echo", Commit: "commit-a", Entrypoint: "main.ts", Actions: map[string]contract.Action{"echo": {Action: "echo"}}},
		},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	catalog := NewFileCatalog(path)
	got, err := catalog.GetDeploymentForWorkspace(context.Background(), "ws-a", "echo")
	if err != nil {
		t.Fatalf("GetDeploymentForWorkspace returned error: %v", err)
	}
	if got.Commit != "commit-a" {
		t.Fatalf("commit = %q, want commit-a", got.Commit)
	}
	snapshot, err := catalog.Load(context.Background())
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if _, ok := snapshot.Deployments["ws-a/echo"]; !ok {
		t.Fatalf("normalized deployment key missing: %#v", snapshot.Deployments)
	}
}
