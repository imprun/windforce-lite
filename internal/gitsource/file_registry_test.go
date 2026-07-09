package gitsource

import (
	"context"
	"path/filepath"
	"testing"
)

func TestFileRegistryPreservesRawSubpath(t *testing.T) {
	registry := NewFileRegistry(filepath.Join(t.TempDir(), "git-sources.json"))
	created, err := registry.Create(context.Background(), Source{
		Workspace: "ws-a",
		Name:      "source-a",
		RepoURL:   "https://example.test/repo.git",
		Branch:    "main",
		Subpath:   "/apps/echo",
	})
	if err != nil {
		t.Fatalf("Create error = %v", err)
	}
	if created.Subpath != "/apps/echo" {
		t.Fatalf("Subpath = %q, want raw value", created.Subpath)
	}
}
