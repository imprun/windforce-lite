package gitsource

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileRegistryRejectsAbsoluteSubpath(t *testing.T) {
	registry := NewFileRegistry(filepath.Join(t.TempDir(), "git-sources.json"))
	_, err := registry.Create(context.Background(), Source{
		Workspace: "ws-a",
		Name:      "source-a",
		RepoURL:   "https://example.test/repo.git",
		Branch:    "main",
		Subpath:   "/apps/echo",
	})
	if err == nil || !strings.Contains(err.Error(), `source path "/apps/echo" must be a relative path inside the git source`) {
		t.Fatalf("Create error = %v, want absolute subpath validation", err)
	}
}
