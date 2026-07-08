package syncer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/imprun/windforce-lite/internal/bundle"
	"github.com/imprun/windforce-lite/internal/contract"
)

type checkingCatalog struct {
	t      *testing.T
	store  bundle.Store
	called bool
}

func (c *checkingCatalog) UpsertDeployment(ctx context.Context, deployment contract.Deployment) error {
	exists, err := c.store.Exists(ctx, deployment.SourceWorkspace(), deployment.SourceGitSourceID(), deployment.Commit)
	if err != nil {
		c.t.Fatalf("store.Exists returned error: %v", err)
	}
	if !exists {
		c.t.Fatalf("catalog updated before bundle materialized")
	}
	c.called = true
	return nil
}

func TestSyncMaterializesBeforeCatalogUpdate(t *testing.T) {
	tempDir := t.TempDir()
	sourceDir := filepath.Join(tempDir, "source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "windforce.json"), []byte(`{
		"app": "echo",
		"actions": {
			"echo": {
				"runtime": "go",
				"command": ["go", "run", "./action.go"]
			}
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := bundle.NewLocalStore(filepath.Join(tempDir, "store"))
	catalog := &checkingCatalog{t: t, store: store}
	syncer := Syncer{Store: store, Catalog: catalog}

	deployment, err := syncer.Sync(context.Background(), Source{
		Workspace:   "workspace-a",
		GitSourceID: "source-a",
		App:         "echo",
		Commit:      "commit-a",
		LocalDir:    sourceDir,
	})
	if err != nil {
		t.Fatalf("Sync returned error: %v", err)
	}
	if !catalog.called {
		t.Fatalf("catalog was not updated")
	}
	if deployment.ObjectURI != "bundle://workspace-a/source-a/commit-a" {
		t.Fatalf("object URI = %q", deployment.ObjectURI)
	}
	if deployment.Actions["echo"].Action != "echo" {
		t.Fatalf("action metadata was not loaded from manifest")
	}
}
