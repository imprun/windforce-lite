package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/imprun/windforce-core/internal/catalog"
	"github.com/imprun/windforce-core/internal/contract"
	executionpkg "github.com/imprun/windforce-core/internal/execution"
	"github.com/imprun/windforce-core/internal/state"
)

const testExecutionBundleDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestExecutionAPICreatesPinnedRunAndReplaysIdempotencyKey(t *testing.T) {
	tempDir := t.TempDir()
	store := state.NewLocalStore(filepath.Join(tempDir, "state.json"))
	deployment := contract.Deployment{
		Workspace:    "ws-a",
		GitSourceID:  "source-a",
		App:          "echo",
		Commit:       "commit-a",
		BundleDigest: testExecutionBundleDigest,
		Actions: map[string]contract.Action{
			"run": {
				Action:           "run",
				Entrypoint:       "main.py",
				InputSchemaBody:  json.RawMessage(`{"type":"object","required":["message"]}`),
				OutputSchemaBody: json.RawMessage(`{"type":"object","required":["echo"]}`),
			},
		},
	}
	if _, err := store.PublishRelease(context.Background(), deployment, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(New(Config{Store: store, Catalog: store}))
	defer httpServer.Close()

	body := []byte(`{"app":"echo","action":"run","input":{"message":"hello"},"adapter":"queue","correlation_id":"request-a","idempotency_key":"message-a","env":["TRACE=value"]}`)
	response, err := http.Post(httpServer.URL+"/execution/v1/workspaces/ws-a/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want %d", response.StatusCode, http.StatusCreated)
	}
	var created executionRunView
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.RunID == "" || created.JobID == "" || created.PinnedRelease.Commit != "commit-a" {
		t.Fatalf("created run = %#v", created)
	}

	claimed, _, err := store.ClaimJob(context.Background(), "worker-a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.RunID != created.RunID || claimed.Payload.Commit != "commit-a" || claimed.Payload.TriggerKind != "queue" {
		t.Fatalf("claimed job = %#v", claimed)
	}
	var inputSchema struct {
		Type     string   `json:"type"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(claimed.Payload.InputSchema, &inputSchema); err != nil {
		t.Fatal(err)
	}
	if inputSchema.Type != "object" || len(inputSchema.Required) != 1 || inputSchema.Required[0] != "message" {
		t.Fatalf("input schema = %#v", inputSchema)
	}

	replay, err := http.Post(httpServer.URL+"/execution/v1/workspaces/ws-a/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer replay.Body.Close()
	if replay.StatusCode != http.StatusOK {
		t.Fatalf("replay status = %d, want %d", replay.StatusCode, http.StatusOK)
	}
	var replayed executionRunView
	if err := json.NewDecoder(replay.Body).Decode(&replayed); err != nil {
		t.Fatal(err)
	}
	if replayed.RunID != created.RunID || !replayed.Replayed {
		t.Fatalf("replayed run = %#v, created = %#v", replayed, created)
	}

	foreign, err := http.Get(httpServer.URL + "/execution/v1/workspaces/ws-b/runs/" + created.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer foreign.Body.Close()
	if foreign.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign workspace status = %d, want %d", foreign.StatusCode, http.StatusNotFound)
	}
}

func TestExecutionAPIRejectsReleaseWithoutExecutionBundleBeforeEnqueue(t *testing.T) {
	store := state.NewLocalStore(filepath.Join(t.TempDir(), "state.json"))
	deployment := contract.Deployment{
		Workspace: "ws-a",
		App:       "echo",
		Commit:    "commit-a",
		Actions: map[string]contract.Action{
			"run": {Action: "run"},
		},
	}
	if _, err := store.PublishRelease(context.Background(), deployment, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(New(Config{Store: store, Catalog: store}))
	defer httpServer.Close()

	response, err := http.Post(
		httpServer.URL+"/execution/v1/workspaces/ws-a/runs",
		"application/json",
		bytes.NewBufferString(`{"app":"echo","action":"run","input":{}}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("create status = %d, want %d", response.StatusCode, http.StatusServiceUnavailable)
	}
	var fault struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&fault); err != nil {
		t.Fatal(err)
	}
	if fault.Error.Code != string(executionpkg.FaultUnavailable) || !strings.Contains(fault.Error.Message, "publish") {
		t.Fatalf("fault = %#v", fault.Error)
	}
	jobs, err := store.ListJobs(context.Background(), state.JobListQuery{WorkspaceID: "ws-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("jobs = %d, want 0", len(jobs))
	}
}

func TestExecutionAPIDescribesMaterializedActionSchemas(t *testing.T) {
	tempDir := t.TempDir()
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	if err := fileCatalog.UpsertDeployment(context.Background(), contract.Deployment{
		Workspace: "default",
		App:       "echo",
		Commit:    "commit-a",
		Actions: map[string]contract.Action{
			"run": {
				Action:           "run",
				InputSchemaBody:  json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}}}`),
				OutputSchemaBody: json.RawMessage(`{"type":"object","properties":{"echo":{"type":"string"}}}`),
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(New(Config{
		Store:   state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		Catalog: fileCatalog,
	}))
	defer httpServer.Close()

	response, err := http.Get(httpServer.URL + "/execution/v1/workspaces/default/apps/echo")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("describe status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	var description struct {
		Deployment contract.Deployment                   `json:"deployment"`
		Actions    map[string]executionActionDescription `json:"actions"`
	}
	if err := json.NewDecoder(response.Body).Decode(&description); err != nil {
		t.Fatal(err)
	}
	if description.Deployment.Commit != "commit-a" || description.Actions["run"].InputSchema["type"] != "object" {
		t.Fatalf("description = %#v", description)
	}
}

type executionActionDescription struct {
	InputSchema map[string]any `json:"input_schema"`
}
