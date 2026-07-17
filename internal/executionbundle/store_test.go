package executionbundle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalStorePublishesAndFetchesContentAddressedBundle(t *testing.T) {
	tempDir := t.TempDir()
	sourceDir := filepath.Join(tempDir, "prepared")
	if err := os.MkdirAll(filepath.Join(sourceDir, ".windforce", "site-packages"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "main.py"), []byte("def main(ctx): return ctx.input\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, ".windforce", "site-packages", "dependency.py"), []byte("VERSION = '1.0'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, ".windforce", "site-packages", markerFile), []byte("application-owned\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewLocalStore(filepath.Join(tempDir, "store"))
	first, err := store.Publish(context.Background(), sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Publish(context.Background(), sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest == "" || first.Digest != second.Digest {
		t.Fatalf("digests = %q and %q", first.Digest, second.Digest)
	}
	if first.URI != "execution-bundle://"+strings.Replace(first.Digest, ":", "/", 1) {
		t.Fatalf("URI = %q", first.URI)
	}
	if first.FileCount != 3 || first.SizeBytes == 0 {
		t.Fatalf("descriptor = %#v", first)
	}
	exists, err := store.Exists(context.Background(), first.Digest)
	if err != nil || !exists {
		t.Fatalf("Exists = %v, %v", exists, err)
	}
	if _, err := store.Verify(context.Background(), first.Digest); err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}

	destination := filepath.Join(tempDir, "worker-cache")
	if _, err := store.FetchTo(context.Background(), destination, first.Digest); err != nil {
		t.Fatal(err)
	}
	dependency, err := os.ReadFile(filepath.Join(destination, ".windforce", "site-packages", "dependency.py"))
	if err != nil {
		t.Fatal(err)
	}
	if string(dependency) != "VERSION = '1.0'\n" {
		t.Fatalf("dependency = %q", dependency)
	}
	if nestedMarker, err := os.ReadFile(filepath.Join(destination, ".windforce", "site-packages", markerFile)); err != nil || string(nestedMarker) != "application-owned\n" {
		t.Fatalf("nested application file = %q, %v", nestedMarker, err)
	}
	if _, err := os.Stat(filepath.Join(destination, markerFile)); !os.IsNotExist(err) {
		t.Fatalf("internal descriptor was copied to worker cache: %v", err)
	}
}

func TestLocalStoreDetectsStoredBundleMutation(t *testing.T) {
	tempDir := t.TempDir()
	sourceDir := filepath.Join(tempDir, "prepared")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "app.js"), []byte("export const value = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewLocalStore(filepath.Join(tempDir, "store"))
	descriptor, err := store.Publish(context.Background(), sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	bundleDir, err := store.bundleDir(descriptor.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "app.js"), []byte("export const value = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Verify(context.Background(), descriptor.Digest); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("Verify error = %v, want digest mismatch", err)
	}
}

func TestLocalStoreRejectsUnknownDigest(t *testing.T) {
	store := NewLocalStore(t.TempDir())
	if _, err := store.Verify(context.Background(), "sha256:"+strings.Repeat("0", 64)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Verify error = %v, want ErrNotFound", err)
	}
	if _, err := store.Verify(context.Background(), "not-a-digest"); err == nil {
		t.Fatal("Verify accepted an invalid digest")
	}
}

func TestLocalStoreRejectsOverlappingFetchDestination(t *testing.T) {
	tempDir := t.TempDir()
	sourceDir := filepath.Join(tempDir, "prepared")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "app.py"), []byte("def main(ctx): return ctx.input\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := NewLocalStore(filepath.Join(tempDir, "store"))
	descriptor, err := store.Publish(context.Background(), sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	bundleDir, err := store.bundleDir(descriptor.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.FetchTo(context.Background(), bundleDir, descriptor.Digest); err == nil || !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("FetchTo error = %v, want overlap rejection", err)
	}
	if _, err := store.FetchTo(context.Background(), "", descriptor.Digest); err == nil || !strings.Contains(err.Error(), "destination is required") {
		t.Fatalf("FetchTo empty destination error = %v", err)
	}
}
