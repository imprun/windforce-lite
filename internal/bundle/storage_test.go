package bundle

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalStoreMaterializeAndFetch(t *testing.T) {
	tempDir := t.TempDir()
	sourceDir := filepath.Join(tempDir, "source")
	if err := os.MkdirAll(filepath.Join(sourceDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sourceDir, "action"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "action", "handler.py"), []byte("print('ok')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, ".git", "config"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewLocalStore(filepath.Join(tempDir, "store"))
	if err := store.Materialize(context.Background(), "workspace-a", "source-a", "commit-a", sourceDir); err != nil {
		t.Fatalf("Materialize returned error: %v", err)
	}

	exists, err := store.Exists(context.Background(), "workspace-a", "source-a", "commit-a")
	if err != nil {
		t.Fatalf("Exists returned error: %v", err)
	}
	if !exists {
		t.Fatalf("expected materialized bundle to exist")
	}

	fetchDir := filepath.Join(tempDir, "fetch")
	if err := store.FetchTo(context.Background(), fetchDir, "workspace-a", "source-a", "commit-a"); err != nil {
		t.Fatalf("FetchTo returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fetchDir, "action", "handler.py")); err != nil {
		t.Fatalf("fetched handler missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fetchDir, ".git", "config")); !os.IsNotExist(err) {
		t.Fatalf(".git directory should not be copied, stat err=%v", err)
	}
}
