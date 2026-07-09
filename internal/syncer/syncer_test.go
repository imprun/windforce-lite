package syncer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
		"tag": "app-main",
		"actions": {
			"echo": {
				"tag": "action-fast",
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
	if deployment.Tag != "app-main" || deployment.Actions["echo"].Tag == nil || *deployment.Actions["echo"].Tag != "action-fast" {
		t.Fatalf("route tags were not loaded from manifest: %#v", deployment)
	}
}

func TestSyncRejectsInvalidSchemaReferences(t *testing.T) {
	tests := []struct {
		name        string
		manifest    string
		files       map[string]string
		wantMessage string
	}{
		{
			name: "missing input schema",
			manifest: `{
				"app": "echo",
				"actions": {"echo": {"inputSchema": "missing.schema.json"}}
			}`,
			wantMessage: `manifest references schema "missing.schema.json" but the file is missing`,
		},
		{
			name: "invalid output schema json",
			manifest: `{
				"app": "echo",
				"actions": {"echo": {"outputSchema": "output.schema.json"}}
			}`,
			files:       map[string]string{"output.schema.json": `{bad json`},
			wantMessage: `schema "output.schema.json" is not valid JSON`,
		},
		{
			name: "escaping schema path",
			manifest: `{
				"app": "echo",
				"actions": {"echo": {"inputSchema": "../input.schema.json"}}
			}`,
			wantMessage: `schema path "../input.schema.json" must be a relative path inside the app`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tempDir := t.TempDir()
			sourceDir := filepath.Join(tempDir, "source")
			if err := os.MkdirAll(sourceDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(sourceDir, "windforce.json"), []byte(test.manifest), 0o644); err != nil {
				t.Fatal(err)
			}
			for name, content := range test.files {
				path := filepath.Join(sourceDir, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			store := bundle.NewLocalStore(filepath.Join(tempDir, "store"))
			syncer := Syncer{Store: store}
			_, err := syncer.Sync(context.Background(), Source{
				Workspace:   "workspace-a",
				GitSourceID: "source-a",
				App:         "echo",
				Commit:      "commit-a",
				LocalDir:    sourceDir,
			})
			if err == nil || !strings.Contains(err.Error(), test.wantMessage) {
				t.Fatalf("Sync error = %v, want %q", err, test.wantMessage)
			}
			exists, existsErr := store.Exists(context.Background(), "workspace-a", "source-a", "commit-a")
			if existsErr != nil {
				t.Fatalf("store.Exists returned error: %v", existsErr)
			}
			if exists {
				t.Fatalf("invalid source should not be materialized")
			}
		})
	}
}
