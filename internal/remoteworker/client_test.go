package remoteworker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/imprun/windforce-core/internal/catalog"
	"github.com/imprun/windforce-core/internal/contract"
	"github.com/imprun/windforce-core/internal/executionbundle"
	"github.com/imprun/windforce-core/internal/server"
	"github.com/imprun/windforce-core/internal/state"
	"github.com/imprun/windforce-core/internal/worker"
)

// The client must satisfy the worker backend and token provider contracts.
var (
	_ worker.Backend          = (*Client)(nil)
	_ worker.JobTokenProvider = (*Client)(nil)
)

func TestClientLifecycleAgainstRealServer(t *testing.T) {
	tempDir := t.TempDir()
	store := state.NewLocalStore(filepath.Join(tempDir, "state.json"))
	srv := httptest.NewServer(server.New(server.Config{
		Store:          store,
		Catalog:        catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json")),
		EnableAPI:      true,
		AdminToken:     "admin-secret",
		JobTokenSecret: "job-secret",
	}))
	defer srv.Close()

	deployment := contract.Deployment{
		Workspace:      "ws-a",
		App:            "echo",
		Commit:         "commit-a",
		RequiredLabels: []string{"browser"},
		Actions:        map[string]contract.Action{"run": {Action: "run", Command: []string{"helper"}}},
	}
	run := state.NewRun("windforce", "run-remote", "echo", "run", deployment, json.RawMessage(`{"message":"hi"}`))
	job := state.NewActionJob(run, nil)
	if err := store.CreateRunAndEnqueue(context.Background(), run, job); err != nil {
		t.Fatal(err)
	}

	client := New(srv.URL, "admin-secret")
	ctx := context.Background()
	if err := client.RegisterWorker(ctx, state.WorkerRecord{ID: "w-remote", Labels: []string{"browser"}, Slots: 1}); err != nil {
		t.Fatal(err)
	}
	if err := client.HeartbeatWorker(ctx, "w-remote"); err != nil {
		t.Fatal(err)
	}

	if _, _, err := client.ClaimJobForWorker(ctx, "w-remote", nil, nil, time.Minute); err != state.ErrNoQueuedJob {
		t.Fatalf("labelless remote claim err = %v, want ErrNoQueuedJob", err)
	}

	claimed, lease, err := client.ClaimJobForWorker(ctx, "w-remote", nil, []string{"browser"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.Payload.App != "echo" || lease.JobID != claimed.ID {
		t.Fatalf("claimed = %#v", claimed.Payload)
	}
	if token := client.JobTokenFor(claimed.ID); !strings.HasPrefix(token, "wfjob_") {
		t.Fatalf("job token = %q, want pre-minted wfjob_ token", token)
	}

	heartbeat, err := client.HeartbeatJob(ctx, lease, time.Minute)
	if err != nil || !heartbeat.StillOwned {
		t.Fatalf("job heartbeat = %#v err=%v", heartbeat, err)
	}
	if err := client.AppendLogs(ctx, claimed.ID, "ws-a", "line-1\n"); err != nil {
		t.Fatal(err)
	}
	if err := client.CompleteJobSucceeded(ctx, lease, contract.JobResult{App: "echo", Action: "run", Output: json.RawMessage(`{"ok":true}`)}); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetRun(ctx, "run-remote")
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != state.RunSucceeded {
		t.Fatalf("run state = %s", stored.State)
	}
	if err := client.DeregisterWorker(ctx, "w-remote"); err != nil {
		t.Fatal(err)
	}

	// Typed store errors survive the wire: recovery logic (errors.Is) must
	// behave exactly as against a local store.
	staleLease := lease
	staleLease.WorkerID = "someone-else"
	if err := client.CompleteJobSucceeded(ctx, staleLease, contract.JobResult{}); !errors.Is(err, state.ErrInvalidLease) {
		t.Fatalf("stale-lease complete err = %v, want ErrInvalidLease", err)
	}
	if err := client.HeartbeatWorker(ctx, "w-never-registered"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unknown worker heartbeat err = %v, want ErrNotFound", err)
	}
}

func TestArtifactStoreFetchesAndExtracts(t *testing.T) {
	tempDir := t.TempDir()
	artifacts := executionbundle.NewLocalStore(filepath.Join(tempDir, "artifacts"))
	sourceDir := filepath.Join(tempDir, "src")
	if err := os.MkdirAll(filepath.Join(sourceDir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "sub", "main.py"), []byte("print('hi')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	descriptor, err := artifacts.Publish(context.Background(), sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server.New(server.Config{
		Store:         state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		Catalog:       catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json")),
		EnableAPI:     true,
		AdminToken:    "admin-secret",
		ArtifactStore: artifacts,
	}))
	defer srv.Close()

	client := New(srv.URL, "admin-secret")
	dest := filepath.Join(tempDir, "fetched")
	if _, err := (ArtifactStore{Client: client}).FetchTo(context.Background(), dest, descriptor.Digest); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(filepath.Join(dest, "sub", "main.py"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), "print") {
		t.Fatalf("fetched content = %q", payload)
	}
}

func TestArtifactStoreRoundTripsSymlinks(t *testing.T) {
	tempDir := t.TempDir()
	sourceDir := filepath.Join(tempDir, "src")
	if err := os.MkdirAll(filepath.Join(sourceDir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "tool.py"), []byte("print('hi')\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "tool.py"), filepath.Join(sourceDir, "bin", "tool")); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}

	artifacts := executionbundle.NewLocalStore(filepath.Join(tempDir, "artifacts"))
	descriptor, err := artifacts.Publish(context.Background(), sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server.New(server.Config{
		Store:         state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		Catalog:       catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json")),
		EnableAPI:     true,
		AdminToken:    "admin-secret",
		ArtifactStore: artifacts,
	}))
	defer srv.Close()

	dest := filepath.Join(tempDir, "fetched")
	if _, err := (ArtifactStore{Client: New(srv.URL, "admin-secret")}).FetchTo(context.Background(), dest, descriptor.Digest); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.Join(dest, "bin", "tool"))
	if err != nil {
		t.Fatalf("symlink missing after fetch: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("bin/tool is not a symlink after fetch (mode %v)", info.Mode())
	}
	payload, err := os.ReadFile(filepath.Join(dest, "bin", "tool"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), "print") {
		t.Fatalf("symlink does not resolve to content: %q", payload)
	}
	// Files after the symlink in walk order must survive too (the old tar
	// writer truncated everything past the first symlink).
	if _, err := os.Stat(filepath.Join(dest, "tool.py")); err != nil {
		t.Fatalf("entry after symlink missing: %v", err)
	}
}
