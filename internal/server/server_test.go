package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/imprun/windforce-lite/internal/bundle"
	"github.com/imprun/windforce-lite/internal/catalog"
	"github.com/imprun/windforce-lite/internal/contract"
	"github.com/imprun/windforce-lite/internal/gitsource"
	"github.com/imprun/windforce-lite/internal/state"
	"github.com/imprun/windforce-lite/internal/syncer"
)

func TestTriggerCreatesRunAndAPIReadsIt(t *testing.T) {
	tempDir := t.TempDir()
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	if err := fileCatalog.UpsertDeployment(context.Background(), contract.Deployment{
		App:    "echo",
		Commit: "commit-a",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo", Command: []string{"helper"}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	handler := New(Config{
		Store:         state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		Catalog:       fileCatalog,
		EnableTrigger: true,
		EnableAPI:     true,
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/apps/echo/actions/echo", bytes.NewBufferString(`{"message":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("TASKID", "task-a")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusAccepted)
	}
	var triggerResponse map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&triggerResponse); err != nil {
		t.Fatal(err)
	}
	if triggerResponse["runId"] != "task-a" || triggerResponse["state"] != string(state.RunQueued) {
		t.Fatalf("trigger response = %#v", triggerResponse)
	}
	if triggerResponse["correlationId"] != "task-a" {
		t.Fatalf("trigger correlation id = %#v", triggerResponse)
	}

	getResp, err := http.Get(server.URL + "/v1/runs/task-a")
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d, want %d", getResp.StatusCode, http.StatusOK)
	}
	var getResponse map[string]any
	if err := json.NewDecoder(getResp.Body).Decode(&getResponse); err != nil {
		t.Fatal(err)
	}
	if getResponse["runId"] != "task-a" {
		t.Fatalf("GET response = %#v", getResponse)
	}
	if getResponse["correlationId"] != "task-a" {
		t.Fatalf("GET correlation id = %#v", getResponse)
	}

	cancelResp, err := http.Post(server.URL+"/v1/runs/task-a/cancel", "application/json", bytes.NewBufferString(`{"reason":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer cancelResp.Body.Close()
	if cancelResp.StatusCode != http.StatusOK {
		t.Fatalf("cancel status = %d, want %d", cancelResp.StatusCode, http.StatusOK)
	}

	retryResp, err := http.Post(server.URL+"/v1/runs/task-a/retry", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer retryResp.Body.Close()
	if retryResp.StatusCode != http.StatusAccepted {
		t.Fatalf("retry status = %d, want %d", retryResp.StatusCode, http.StatusAccepted)
	}
	var retryResponse map[string]any
	if err := json.NewDecoder(retryResp.Body).Decode(&retryResponse); err != nil {
		t.Fatal(err)
	}
	if retryResponse["state"] != string(state.RunQueued) || retryResponse["jobId"] == "" {
		t.Fatalf("retry response = %#v", retryResponse)
	}
}

func TestJobLogsAPI(t *testing.T) {
	tempDir := t.TempDir()
	store := state.NewLocalStore(filepath.Join(tempDir, "state.json"))
	deployment := contract.Deployment{
		Workspace:   "ws-a",
		GitSourceID: "source-a",
		App:         "echo",
		Commit:      "commit-a",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo", Command: []string{"helper"}},
		},
	}
	run := state.NewRun("windforce", "run-a", "echo", "echo", deployment, json.RawMessage(`{"message":"hello"}`))
	job := state.NewActionJob(run, nil)
	if err := store.CreateRunAndEnqueue(context.Background(), run, job); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendLogs(context.Background(), job.ID, "ws-a", "hello world"); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(New(Config{Store: store, EnableAPI: true}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/w/ws-a/jobs/" + job.ID + "/logs?tail_bytes=5")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "world" {
		t.Fatalf("logs body = %q", body)
	}
}

func TestCanonicalJobRunStatusAndResultAPI(t *testing.T) {
	tempDir := t.TempDir()
	store := state.NewLocalStore(filepath.Join(tempDir, "state.json"))
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	if err := fileCatalog.UpsertDeployment(context.Background(), contract.Deployment{
		Workspace:   "ws-a",
		GitSourceID: "source-a",
		App:         "echo",
		Commit:      "commit-a",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo", Command: []string{"helper"}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(New(Config{Store: store, Catalog: fileCatalog, EnableAPI: true}))
	defer server.Close()

	resp, err := http.Post(server.URL+"/api/w/ws-a/jobs/run/echo/echo", "application/json", bytes.NewBufferString(`{"message":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("run status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	var runResponse struct {
		JobID string `json:"job_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&runResponse); err != nil {
		t.Fatal(err)
	}
	if runResponse.JobID == "" {
		t.Fatalf("missing job id")
	}

	statusResp, err := http.Get(server.URL + "/api/w/ws-a/jobs/" + runResponse.JobID)
	if err != nil {
		t.Fatal(err)
	}
	defer statusResp.Body.Close()
	if statusResp.StatusCode != http.StatusOK {
		t.Fatalf("job status code = %d, want %d", statusResp.StatusCode, http.StatusOK)
	}
	var statusBody map[string]any
	if err := json.NewDecoder(statusResp.Body).Decode(&statusBody); err != nil {
		t.Fatal(err)
	}
	if statusBody["id"] != runResponse.JobID || statusBody["state"] != "queued" || statusBody["app_key"] != "echo" || statusBody["action_key"] != "echo" {
		t.Fatalf("job status = %#v", statusBody)
	}

	resultResp, err := http.Get(server.URL + "/api/w/ws-a/jobs/" + runResponse.JobID + "/result")
	if err != nil {
		t.Fatal(err)
	}
	defer resultResp.Body.Close()
	if resultResp.StatusCode != http.StatusAccepted {
		t.Fatalf("pending result status = %d, want %d", resultResp.StatusCode, http.StatusAccepted)
	}

	claimed, lease, err := store.ClaimJob(context.Background(), "worker-a", 0)
	if err != nil {
		t.Fatalf("ClaimJob returned error: %v", err)
	}
	if claimed.ID != runResponse.JobID {
		t.Fatalf("claimed job = %q, want %q", claimed.ID, runResponse.JobID)
	}
	if err := store.CompleteJobSucceeded(context.Background(), lease, contract.JobResult{
		JobID:      claimed.ID,
		App:        "echo",
		Action:     "echo",
		ExitCode:   0,
		Output:     json.RawMessage(`{"ok":true}`),
		DurationMs: 12,
	}); err != nil {
		t.Fatalf("CompleteJobSucceeded returned error: %v", err)
	}

	doneResp, err := http.Get(server.URL + "/api/w/ws-a/jobs/" + runResponse.JobID + "/result")
	if err != nil {
		t.Fatal(err)
	}
	defer doneResp.Body.Close()
	if doneResp.StatusCode != http.StatusOK {
		t.Fatalf("done result status = %d, want %d", doneResp.StatusCode, http.StatusOK)
	}
	var doneBody struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(doneResp.Body).Decode(&doneBody); err != nil {
		t.Fatal(err)
	}
	var doneResult struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(doneBody.Result, &doneResult); err != nil {
		t.Fatal(err)
	}
	if doneBody.Status != "success" || !doneResult.OK {
		t.Fatalf("done result = %#v result=%s", doneBody, doneBody.Result)
	}

	waitResp, err := http.Post(server.URL+"/api/w/ws-a/jobs/run/echo/echo/wait?timeout_ms=0", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer waitResp.Body.Close()
	if waitResp.StatusCode != http.StatusAccepted {
		t.Fatalf("wait status = %d, want %d", waitResp.StatusCode, http.StatusAccepted)
	}
}

func TestCanonicalJobCancelAPI(t *testing.T) {
	tempDir := t.TempDir()
	store := state.NewLocalStore(filepath.Join(tempDir, "state.json"))
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	if err := fileCatalog.UpsertDeployment(context.Background(), contract.Deployment{
		Workspace:   "ws-a",
		GitSourceID: "source-a",
		App:         "echo",
		Commit:      "commit-a",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo", Command: []string{"helper"}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(New(Config{Store: store, Catalog: fileCatalog, EnableAPI: true}))
	defer server.Close()

	runResp, err := http.Post(server.URL+"/api/w/ws-a/jobs/run/echo/echo", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer runResp.Body.Close()
	var runBody struct {
		JobID string `json:"job_id"`
	}
	if err := json.NewDecoder(runResp.Body).Decode(&runBody); err != nil {
		t.Fatal(err)
	}
	if runBody.JobID == "" {
		t.Fatalf("missing job id")
	}

	cancelResp, err := http.Post(server.URL+"/api/w/ws-a/jobs/"+runBody.JobID+"/cancel", "application/json", bytes.NewBufferString(`{"reason":"operator canceled"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer cancelResp.Body.Close()
	if cancelResp.StatusCode != http.StatusOK {
		t.Fatalf("cancel status = %d, want %d", cancelResp.StatusCode, http.StatusOK)
	}
	var cancelBody state.CancelResult
	if err := json.NewDecoder(cancelResp.Body).Decode(&cancelBody); err != nil {
		t.Fatal(err)
	}
	if !cancelBody.Found || !cancelBody.CompletedNow || cancelBody.SoftCanceled || cancelBody.AlreadyCompleted {
		t.Fatalf("cancel body = %#v", cancelBody)
	}

	resultResp, err := http.Get(server.URL + "/api/w/ws-a/jobs/" + runBody.JobID + "/result")
	if err != nil {
		t.Fatal(err)
	}
	defer resultResp.Body.Close()
	if resultResp.StatusCode != http.StatusOK {
		t.Fatalf("result status = %d, want %d", resultResp.StatusCode, http.StatusOK)
	}
	var resultBody struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(resultResp.Body).Decode(&resultBody); err != nil {
		t.Fatal(err)
	}
	if resultBody.Status != "canceled" || !bytes.Contains(resultBody.Result, []byte("operator canceled")) {
		t.Fatalf("result body = %#v result=%s", resultBody, resultBody.Result)
	}

	secondCancelResp, err := http.Post(server.URL+"/api/w/ws-a/jobs/"+runBody.JobID+"/cancel", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer secondCancelResp.Body.Close()
	var secondCancelBody state.CancelResult
	if err := json.NewDecoder(secondCancelResp.Body).Decode(&secondCancelBody); err != nil {
		t.Fatal(err)
	}
	if !secondCancelBody.Found || !secondCancelBody.AlreadyCompleted {
		t.Fatalf("second cancel body = %#v", secondCancelBody)
	}
}

type fakeTriggerAdapter struct{}

func (fakeTriggerAdapter) Name() string {
	return "external"
}

func (fakeTriggerAdapter) MatchTrigger(path string) (AdapterRoute, bool) {
	parts := SplitPath(path)
	if len(parts) != 4 || parts[0] != "external" || parts[1] != "v1" {
		return AdapterRoute{}, false
	}
	return AdapterRoute{
		App:    parts[2],
		Action: parts[3],
		Env:    []string{"EXTERNAL_ADAPTER=1"},
		Values: map[string]string{"externalApp": parts[2], "externalAction": parts[3]},
	}, true
}

func (fakeTriggerAdapter) MatchSchema(string) (AdapterRoute, bool) {
	return AdapterRoute{}, false
}

func (fakeTriggerAdapter) TriggerResponse(run state.Run, route AdapterRoute) (int, any) {
	return http.StatusOK, map[string]any{
		"externalApp":    route.Values["externalApp"],
		"externalAction": route.Values["externalAction"],
		"runId":          run.ID,
		"state":          run.State,
	}
}

func TestAdapterTriggerReturnsCustomEnvelope(t *testing.T) {
	tempDir := t.TempDir()
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	if err := fileCatalog.UpsertDeployment(context.Background(), contract.Deployment{
		App:    "echo",
		Commit: "commit-a",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo", Command: []string{"helper"}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	handler := New(Config{
		Store:              state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		Catalog:            fileCatalog,
		EnableTrigger:      true,
		DisableCoreTrigger: true,
		TriggerAdapters:    []TriggerAdapter{fakeTriggerAdapter{}},
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	resp, err := http.Post(server.URL+"/external/v1/echo/echo", "application/json", bytes.NewBufferString(`{"message":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var envelope map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if envelope["externalApp"] != "echo" || envelope["externalAction"] != "echo" || envelope["state"] != string(state.RunQueued) {
		t.Fatalf("envelope = %#v", envelope)
	}
}

func TestTriggerTokenAuthorization(t *testing.T) {
	tempDir := t.TempDir()
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	if err := fileCatalog.UpsertDeployment(context.Background(), contract.Deployment{
		App:    "echo",
		Commit: "commit-a",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo", Command: []string{"helper"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(Config{
		Store:         state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		Catalog:       fileCatalog,
		EnableTrigger: true,
		TriggerToken:  "secret-token",
	}))
	defer server.Close()

	resp, err := http.Post(server.URL+"/v1/apps/echo/actions/echo", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}

	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/apps/echo/actions/echo", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("authorized status = %d, want %d", resp.StatusCode, http.StatusAccepted)
	}
}

func TestControlPlaneSyncCatalogDeploymentAndSchema(t *testing.T) {
	tempDir := t.TempDir()
	sourceDir := filepath.Join(tempDir, "source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "windforce.json"), []byte(`{
		"app": "echo",
		"actions": {
			"echo": {
				"command": ["helper"],
				"inputSchema": "input.schema.json",
				"outputSchema": "output.schema.json"
			}
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	handler := New(Config{
		Store:     state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		Catalog:   fileCatalog,
		Syncer:    &syncer.Syncer{Store: bundle.NewLocalStore(filepath.Join(tempDir, "store")), Catalog: fileCatalog},
		EnableAPI: true,
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	syncResp, err := http.Post(server.URL+"/v1/sync", "application/json", bytes.NewBufferString(`{"app":"echo","sourceDir":"`+filepath.ToSlash(sourceDir)+`","commit":"commit-a"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer syncResp.Body.Close()
	if syncResp.StatusCode != http.StatusOK {
		t.Fatalf("sync status = %d, want %d", syncResp.StatusCode, http.StatusOK)
	}

	for _, path := range []string{"/v1/catalog", "/v1/deployments/echo", "/v1/apps/echo/actions/echo/schema"} {
		resp, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d, want %d", path, resp.StatusCode, http.StatusOK)
		}
		_ = resp.Body.Close()
	}
}

func TestControlPlaneRegistersGitSourceAndSyncsIt(t *testing.T) {
	tempDir := t.TempDir()
	repoDir := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "windforce.json"), []byte(`{
		"app": "echo",
		"actions": {
			"echo": {
				"command": ["helper"]
			}
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repoDir, "init")
	runTestGit(t, repoDir, "checkout", "-b", "main")
	runTestGit(t, repoDir, "config", "user.email", "test@example.com")
	runTestGit(t, repoDir, "config", "user.name", "Test User")
	runTestGit(t, repoDir, "add", "windforce.json")
	runTestGit(t, repoDir, "commit", "-m", "initial")

	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	handler := New(Config{
		Store:      state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		Catalog:    fileCatalog,
		Syncer:     &syncer.Syncer{Store: bundle.NewLocalStore(filepath.Join(tempDir, "store")), Catalog: fileCatalog, CloneRoot: tempDir},
		GitSources: gitsource.NewFileRegistry(filepath.Join(tempDir, "git-sources.json")),
		EnableAPI:  true,
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	registerBody, err := json.Marshal(map[string]string{
		"id":      "source-a",
		"repoUrl": filepath.ToSlash(repoDir),
		"branch":  "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	registerResp, err := http.Post(server.URL+"/v1/git-sources", "application/json", bytes.NewReader(registerBody))
	if err != nil {
		t.Fatal(err)
	}
	defer registerResp.Body.Close()
	if registerResp.StatusCode != http.StatusOK {
		t.Fatalf("register status = %d, want %d", registerResp.StatusCode, http.StatusOK)
	}

	getResp, err := http.Get(server.URL + "/v1/git-sources/source-a")
	if err != nil {
		t.Fatal(err)
	}
	_ = getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get git source status = %d, want %d", getResp.StatusCode, http.StatusOK)
	}

	syncResp, err := http.Post(server.URL+"/v1/sync", "application/json", bytes.NewBufferString(`{"app":"echo","gitSourceId":"source-a"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer syncResp.Body.Close()
	if syncResp.StatusCode != http.StatusOK {
		t.Fatalf("sync status = %d, want %d", syncResp.StatusCode, http.StatusOK)
	}
	var deployment contract.Deployment
	if err := json.NewDecoder(syncResp.Body).Decode(&deployment); err != nil {
		t.Fatal(err)
	}
	if deployment.GitSourceID != "source-a" {
		t.Fatalf("deployment gitSourceId = %q, want source-a", deployment.GitSourceID)
	}
}

func TestControlPlaneRegistersGitSourcePathAndSyncsIt(t *testing.T) {
	tempDir := t.TempDir()
	repoDir := filepath.Join(tempDir, "repo")
	appDir := filepath.Join(repoDir, "apps", "echo")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "root.txt"), []byte("root"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "windforce.json"), []byte(`{
		"app": "echo",
		"actions": {
			"echo": {
				"command": ["helper"]
			}
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "action.txt"), []byte("action"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repoDir, "init")
	runTestGit(t, repoDir, "checkout", "-b", "main")
	runTestGit(t, repoDir, "config", "user.email", "test@example.com")
	runTestGit(t, repoDir, "config", "user.name", "Test User")
	runTestGit(t, repoDir, "add", ".")
	runTestGit(t, repoDir, "commit", "-m", "initial")

	store := bundle.NewLocalStore(filepath.Join(tempDir, "store"))
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	handler := New(Config{
		Store:      state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		Catalog:    fileCatalog,
		Syncer:     &syncer.Syncer{Store: store, Catalog: fileCatalog, CloneRoot: tempDir},
		GitSources: gitsource.NewFileRegistry(filepath.Join(tempDir, "git-sources.json")),
		EnableAPI:  true,
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	registerBody, err := json.Marshal(map[string]string{
		"id":      "source-a",
		"repoUrl": filepath.ToSlash(repoDir),
		"branch":  "main",
		"subpath": "apps/echo",
	})
	if err != nil {
		t.Fatal(err)
	}
	registerResp, err := http.Post(server.URL+"/v1/git-sources", "application/json", bytes.NewReader(registerBody))
	if err != nil {
		t.Fatal(err)
	}
	defer registerResp.Body.Close()
	if registerResp.StatusCode != http.StatusOK {
		t.Fatalf("register status = %d, want %d", registerResp.StatusCode, http.StatusOK)
	}

	syncResp, err := http.Post(server.URL+"/v1/sync", "application/json", bytes.NewBufferString(`{"app":"echo","gitSourceId":"source-a"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer syncResp.Body.Close()
	if syncResp.StatusCode != http.StatusOK {
		t.Fatalf("sync status = %d, want %d", syncResp.StatusCode, http.StatusOK)
	}
	var deployment contract.Deployment
	if err := json.NewDecoder(syncResp.Body).Decode(&deployment); err != nil {
		t.Fatal(err)
	}

	fetchDir := filepath.Join(tempDir, "fetch")
	if err := store.FetchTo(context.Background(), fetchDir, deployment.SourceWorkspace(), deployment.SourceGitSourceID(), deployment.Commit); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(fetchDir, "windforce.json")); err != nil {
		t.Fatalf("materialized app root missing windforce.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fetchDir, "action.txt")); err != nil {
		t.Fatalf("materialized app root missing action.txt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fetchDir, "root.txt")); !os.IsNotExist(err) {
		t.Fatalf("materialized app root should not contain repo root file, stat err = %v", err)
	}
}

func runTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, string(out))
	}
}
