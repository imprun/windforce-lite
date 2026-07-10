package source

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestTreeDigestMatchesSourceCacheExclusions(t *testing.T) {
	root := t.TempDir()
	writeDigestFile(t, root, "windforce.json", `{"app":"echo"}`)

	base := treeDigestForTest(t, root)

	writeDigestFile(t, root, ".git/config", "ignored")
	writeDigestFile(t, root, "node_modules/pkg/index.js", "ignored")
	if got := treeDigestForTest(t, root); got != base {
		t.Fatalf("digest changed for cache-excluded directories: got %q want %q", got, base)
	}

	writeDigestFile(t, root, ".venv/pyvenv.cfg", "home = /python")
	withVenv := treeDigestForTest(t, root)
	if withVenv == base {
		t.Fatalf("digest did not include .venv content")
	}

	writeDigestFile(t, root, "__pycache__/handler.pyc", "compiled")
	withPycache := treeDigestForTest(t, root)
	if withPycache == withVenv {
		t.Fatalf("digest did not include __pycache__ content")
	}
}

func treeDigestForTest(t *testing.T, root string) string {
	t.Helper()
	digest, err := TreeDigest(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func writeDigestFile(t *testing.T, root string, rel string, content string) {
	t.Helper()
	target := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
