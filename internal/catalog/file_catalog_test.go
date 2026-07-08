package catalog

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/imprun/windforce-lite/internal/contract"
)

func TestFileCatalogUpsertAndGet(t *testing.T) {
	catalog := NewFileCatalog(filepath.Join(t.TempDir(), "catalog.json"))
	deployment := contract.Deployment{
		App:       "echo",
		Commit:    "commit-a",
		ObjectURI: "bundle://echo/commit-a",
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
}
