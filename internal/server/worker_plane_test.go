package server

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imprun/windforce-core/internal/catalog"
	"github.com/imprun/windforce-core/internal/contract"
	"github.com/imprun/windforce-core/internal/executionbundle"
	"github.com/imprun/windforce-core/internal/state"
)

func newWorkerPlaneServer(t *testing.T) (*httptest.Server, *state.LocalStore) {
	t.Helper()
	tempDir := t.TempDir()
	store := state.NewLocalStore(filepath.Join(tempDir, "state.json"))
	server := httptest.NewServer(New(Config{
		Store:      store,
		Catalog:    catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json")),
		EnableAPI:  true,
		AdminToken: "admin-secret",
	}))
	t.Cleanup(server.Close)
	return server, store
}

func workerPlanePost(t *testing.T, url, token, body string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(payload)
}

func TestWorkerPlaneClaimAndComplete(t *testing.T) {
	server, store := newWorkerPlaneServer(t)

	deployment := contract.Deployment{
		Workspace:      "ws-a",
		App:            "echo",
		Commit:         "commit-a",
		RequiredLabels: []string{"browser"},
		Actions:        map[string]contract.Action{"run": {Action: "run", Command: []string{"helper"}}},
	}
	run := state.NewRun("windforce", "run-1", "echo", "run", deployment, json.RawMessage(`{"message":"hi"}`))
	job := state.NewActionJob(run, nil)
	if err := store.CreateRunAndEnqueue(context.Background(), run, job); err != nil {
		t.Fatal(err)
	}

	resp, _ := workerPlanePost(t, server.URL+"/worker/v1/workers", "wrong", `{"labels":["browser"]}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad token register = %d, want 401", resp.StatusCode)
	}

	resp, payload := workerPlanePost(t, server.URL+"/worker/v1/workers", "admin-secret",
		`{"id":"w-remote","group":"default","labels":["browser"],"slots":2}`)
	if resp.StatusCode != http.StatusCreated || !strings.Contains(payload, "w-remote") {
		t.Fatalf("register = %d: %s", resp.StatusCode, payload)
	}

	// A claim without the required label yields no job.
	resp, _ = workerPlanePost(t, server.URL+"/worker/v1/claims", "admin-secret",
		`{"worker_id":"w-remote","labels":[]}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("labelless claim = %d, want 204", resp.StatusCode)
	}

	resp, payload = workerPlanePost(t, server.URL+"/worker/v1/claims", "admin-secret",
		`{"worker_id":"w-remote","labels":["browser"]}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("claim = %d: %s", resp.StatusCode, payload)
	}
	var claim struct {
		Job   state.Job       `json:"job"`
		Lease workerLeaseWire `json:"lease"`
	}
	if err := json.Unmarshal([]byte(payload), &claim); err != nil {
		t.Fatal(err)
	}
	if claim.Job.Payload.App != "echo" || claim.Lease.JobID != claim.Job.ID {
		t.Fatalf("claim body = %s", payload)
	}
	if !strings.Contains(string(claim.Job.Payload.Input), "hi") {
		t.Fatalf("claim input not prepared: %s", claim.Job.Payload.Input)
	}

	leaseJSON, _ := json.Marshal(claim.Lease)
	resp, payload = workerPlanePost(t, server.URL+"/worker/v1/jobs/"+claim.Job.ID+"/heartbeat", "admin-secret",
		`{"lease":`+string(leaseJSON)+`}`)
	if resp.StatusCode != http.StatusOK || !strings.Contains(payload, `"still_owned":true`) {
		t.Fatalf("job heartbeat = %d: %s", resp.StatusCode, payload)
	}

	resp, _ = workerPlanePost(t, server.URL+"/worker/v1/jobs/"+claim.Job.ID+"/logs", "admin-secret",
		`{"workspace":"ws-a","chunk":"line-1\n"}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logs = %d", resp.StatusCode)
	}

	resp, payload = workerPlanePost(t, server.URL+"/worker/v1/jobs/"+claim.Job.ID+"/complete", "admin-secret",
		`{"lease":`+string(leaseJSON)+`,"outcome":"succeeded","result":{"app":"echo","action":"run","output":{"ok":true},"exitCode":0}}`)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("complete = %d: %s", resp.StatusCode, payload)
	}
	stored, err := store.GetRun(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != state.RunSucceeded {
		t.Fatalf("run state = %s, want succeeded", stored.State)
	}
}

func TestWorkerPlaneArtifactStreamsTar(t *testing.T) {
	tempDir := t.TempDir()
	store := state.NewLocalStore(filepath.Join(tempDir, "state.json"))
	artifacts := executionbundle.NewLocalStore(filepath.Join(tempDir, "artifacts"))
	sourceDir := filepath.Join(tempDir, "bundle-src")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "main.py"), []byte("print('hi')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	descriptor, err := artifacts.Publish(context.Background(), sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(Config{
		Store:         store,
		Catalog:       catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json")),
		EnableAPI:     true,
		AdminToken:    "admin-secret",
		ArtifactStore: artifacts,
	}))
	defer server.Close()

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/worker/v1/artifacts/"+descriptor.Digest, nil)
	req.Header.Set("Authorization", "Bearer admin-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("artifact = %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	reader := tar.NewReader(bytes.NewReader(raw))
	found := false
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Name == "main.py" {
			found = true
		}
	}
	if !found {
		t.Fatal("tar missing main.py")
	}
}
