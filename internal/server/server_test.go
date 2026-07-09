package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	if doneBody.Status != "completed" || !doneResult.OK {
		t.Fatalf("done result = %#v result=%s", doneBody, doneBody.Result)
	}

	listResp, err := http.Get(server.URL + "/api/w/ws-a/jobs?status=completed&app=echo&limit=1")
	if err != nil {
		t.Fatal(err)
	}
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want %d", listResp.StatusCode, http.StatusOK)
	}
	var listBody struct {
		Items []struct {
			ID        string `json:"id"`
			AppKey    string `json:"app_key"`
			ActionKey string `json:"action_key"`
			Status    string `json:"status"`
			Completed bool   `json:"completed"`
		} `json:"items"`
		Pagination struct {
			Limit   int  `json:"limit"`
			Count   int  `json:"count"`
			HasMore bool `json:"has_more"`
		} `json:"pagination"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&listBody); err != nil {
		t.Fatal(err)
	}
	if len(listBody.Items) != 1 || listBody.Items[0].ID != runResponse.JobID || listBody.Items[0].Status != "completed" || !listBody.Items[0].Completed {
		t.Fatalf("list body = %#v", listBody)
	}
	if listBody.Pagination.Limit != 1 || listBody.Pagination.Count != 1 {
		t.Fatalf("pagination = %#v", listBody.Pagination)
	}

	summaryResp, err := http.Get(server.URL + "/api/w/ws-a/jobs/summary?recent_seconds=3600")
	if err != nil {
		t.Fatal(err)
	}
	defer summaryResp.Body.Close()
	if summaryResp.StatusCode != http.StatusOK {
		t.Fatalf("summary status = %d, want %d", summaryResp.StatusCode, http.StatusOK)
	}
	var summaryBody struct {
		CompletedCountRecent int `json:"completed_count_recent"`
		ByApp                []struct {
			AppKey               string `json:"app_key"`
			CompletedCountRecent int    `json:"completed_count_recent"`
		} `json:"by_app"`
	}
	if err := json.NewDecoder(summaryResp.Body).Decode(&summaryBody); err != nil {
		t.Fatal(err)
	}
	if summaryBody.CompletedCountRecent < 1 || len(summaryBody.ByApp) == 0 || summaryBody.ByApp[0].AppKey != "echo" {
		t.Fatalf("summary body = %#v", summaryBody)
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

func TestCanonicalJobWebhookAPI(t *testing.T) {
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

	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/w/ws-a/jobs/webhook/echo/echo", bytes.NewBufferString(`{"event":"push"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("X-Hub-Signature-256", "sha256=abc")
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Cookie", "session=secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("webhook status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	var webhookResponse struct {
		JobID string `json:"job_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&webhookResponse); err != nil {
		t.Fatal(err)
	}
	if webhookResponse.JobID == "" {
		t.Fatalf("missing job id")
	}
	job, _, found, err := store.GetJob(context.Background(), "ws-a", webhookResponse.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatalf("webhook job not found")
	}
	if job.Payload.TriggerKind != "webhook" {
		t.Fatalf("trigger kind = %q, want webhook", job.Payload.TriggerKind)
	}
	var raw string
	if err := json.Unmarshal(job.Payload.Input, &raw); err != nil {
		t.Fatalf("webhook input is not a JSON string: %v input=%s", err, job.Payload.Input)
	}
	if raw != `{"event":"push"}` {
		t.Fatalf("webhook raw = %q", raw)
	}
	var headers map[string]string
	if err := json.Unmarshal(job.Payload.TriggerHeaders, &headers); err != nil {
		t.Fatalf("webhook headers are not JSON: %v headers=%s", err, job.Payload.TriggerHeaders)
	}
	if headers["X-Hub-Signature-256"] != "sha256=abc" {
		t.Fatalf("signature header missing: %#v", headers)
	}
	if _, ok := headers["Authorization"]; ok {
		t.Fatalf("authorization header should be denied: %#v", headers)
	}
	if _, ok := headers["Cookie"]; ok {
		t.Fatalf("cookie header should be denied: %#v", headers)
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

func TestCanonicalControlPlaneRegistersSyncsAndExposesSchemas(t *testing.T) {
	tempDir := t.TempDir()
	repoDir := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "windforce.json"), []byte(`{
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
	if err := os.WriteFile(filepath.Join(repoDir, "input.schema.json"), []byte(`{"type":"object","properties":{"message":{"type":"string"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "output.schema.json"), []byte(`{"type":"object","properties":{"ok":{"type":"boolean"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repoDir, "init")
	runTestGit(t, repoDir, "checkout", "-b", "main")
	runTestGit(t, repoDir, "config", "user.email", "test@example.com")
	runTestGit(t, repoDir, "config", "user.name", "Test User")
	runTestGit(t, repoDir, "add", ".")
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
		"name":      "source-a",
		"repo_url":  filepath.ToSlash(repoDir),
		"branch":    "main",
		"creds_ref": "WINDFORCE_LITE_GIT_TOKEN",
	})
	if err != nil {
		t.Fatal(err)
	}
	registerResp, err := http.Post(server.URL+"/api/w/ws-a/git_sources", "application/json", bytes.NewReader(registerBody))
	if err != nil {
		t.Fatal(err)
	}
	defer registerResp.Body.Close()
	if registerResp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d, want %d", registerResp.StatusCode, http.StatusCreated)
	}
	var registered struct {
		ID          string `json:"id"`
		WorkspaceID string `json:"workspace_id"`
		Name        string `json:"name"`
		RepoURL     string `json:"repo_url"`
		CredsRef    string `json:"creds_ref"`
	}
	if err := json.NewDecoder(registerResp.Body).Decode(&registered); err != nil {
		t.Fatal(err)
	}
	if registered.ID != "source-a" || registered.Name != "source-a" || registered.WorkspaceID != "ws-a" ||
		registered.RepoURL != filepath.ToSlash(repoDir) || registered.CredsRef != "WINDFORCE_LITE_GIT_TOKEN" {
		t.Fatalf("registered source = %#v", registered)
	}

	listResp, err := http.Get(server.URL + "/api/w/ws-a/git_sources")
	if err != nil {
		t.Fatal(err)
	}
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want %d", listResp.StatusCode, http.StatusOK)
	}
	var sources []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&sources); err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].Name != "source-a" {
		t.Fatalf("sources = %#v", sources)
	}

	syncResp, err := http.Post(server.URL+"/api/w/ws-a/git_sources/source-a/sync", "application/json", bytes.NewBufferString(`{"app":"echo"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer syncResp.Body.Close()
	if syncResp.StatusCode != http.StatusOK {
		t.Fatalf("sync status = %d, want %d", syncResp.StatusCode, http.StatusOK)
	}
	var syncBody struct {
		Commit  string   `json:"commit"`
		App     string   `json:"app"`
		Actions []string `json:"actions"`
	}
	if err := json.NewDecoder(syncResp.Body).Decode(&syncBody); err != nil {
		t.Fatal(err)
	}
	if syncBody.Commit == "" || syncBody.App != "echo" || len(syncBody.Actions) != 1 || syncBody.Actions[0] != "echo.echo" {
		t.Fatalf("sync body = %#v", syncBody)
	}

	appsResp, err := http.Get(server.URL + "/api/w/ws-a/apps")
	if err != nil {
		t.Fatal(err)
	}
	defer appsResp.Body.Close()
	var apps []string
	if err := json.NewDecoder(appsResp.Body).Decode(&apps); err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || apps[0] != "echo" {
		t.Fatalf("apps = %#v", apps)
	}

	summaryResp, err := http.Get(server.URL + "/api/w/ws-a/apps?view=summary")
	if err != nil {
		t.Fatal(err)
	}
	defer summaryResp.Body.Close()
	var summary struct {
		Apps []struct {
			AppKey       string `json:"app_key"`
			ActionsCount int64  `json:"actions_count"`
		} `json:"apps"`
	}
	if err := json.NewDecoder(summaryResp.Body).Decode(&summary); err != nil {
		t.Fatal(err)
	}
	if len(summary.Apps) != 1 || summary.Apps[0].AppKey != "echo" || summary.Apps[0].ActionsCount != 1 {
		t.Fatalf("summary = %#v", summary)
	}

	actionResp, err := http.Get(server.URL + "/api/w/ws-a/apps/echo/actions/echo")
	if err != nil {
		t.Fatal(err)
	}
	defer actionResp.Body.Close()
	if actionResp.StatusCode != http.StatusOK {
		t.Fatalf("action status = %d, want %d", actionResp.StatusCode, http.StatusOK)
	}
	var actionBody struct {
		AppKey       string          `json:"app_key"`
		ActionKey    string          `json:"action_key"`
		InputSchema  json.RawMessage `json:"input_schema"`
		OutputSchema json.RawMessage `json:"output_schema"`
	}
	if err := json.NewDecoder(actionResp.Body).Decode(&actionBody); err != nil {
		t.Fatal(err)
	}
	if actionBody.AppKey != "echo" || actionBody.ActionKey != "echo" ||
		!bytes.Contains(actionBody.InputSchema, []byte(`"message"`)) || !bytes.Contains(actionBody.OutputSchema, []byte(`"ok"`)) {
		t.Fatalf("action body = %#v input=%s output=%s", actionBody, actionBody.InputSchema, actionBody.OutputSchema)
	}

	appResp, err := http.Get(server.URL + "/api/w/ws-a/apps/echo")
	if err != nil {
		t.Fatal(err)
	}
	defer appResp.Body.Close()
	if appResp.StatusCode != http.StatusOK {
		t.Fatalf("app status = %d, want %d", appResp.StatusCode, http.StatusOK)
	}
	var appBody struct {
		App struct {
			AppKey string `json:"app_key"`
		} `json:"app"`
		Actions []struct {
			ActionKey   string          `json:"action_key"`
			InputSchema json.RawMessage `json:"input_schema"`
		} `json:"actions"`
	}
	if err := json.NewDecoder(appResp.Body).Decode(&appBody); err != nil {
		t.Fatal(err)
	}
	if appBody.App.AppKey != "echo" || len(appBody.Actions) != 1 || appBody.Actions[0].ActionKey != "echo" ||
		!bytes.Contains(appBody.Actions[0].InputSchema, []byte(`"message"`)) {
		t.Fatalf("app body = %#v", appBody)
	}

	openAPIReq, err := http.NewRequest(http.MethodGet, server.URL+"/api/w/ws-a/apps/echo/openapi.json", nil)
	if err != nil {
		t.Fatal(err)
	}
	openAPIReq.Header.Set("X-Forwarded-Proto", "https")
	openAPIReq.Header.Set("X-Forwarded-Host", "api.example.test")
	openAPIResp, err := http.DefaultClient.Do(openAPIReq)
	if err != nil {
		t.Fatal(err)
	}
	defer openAPIResp.Body.Close()
	if openAPIResp.StatusCode != http.StatusOK {
		t.Fatalf("openapi status = %d, want %d", openAPIResp.StatusCode, http.StatusOK)
	}
	var openAPIBody map[string]any
	if err := json.NewDecoder(openAPIResp.Body).Decode(&openAPIBody); err != nil {
		t.Fatal(err)
	}
	if openAPIBody["openapi"] != "3.1.0" {
		t.Fatalf("openapi version = %#v", openAPIBody["openapi"])
	}
	if serverURL := openAPIBody["servers"].([]any)[0].(map[string]any)["url"]; serverURL != "https://api.example.test" {
		t.Fatalf("openapi server url = %#v", serverURL)
	}
	paths := openAPIBody["paths"].(map[string]any)
	runWait := paths["/api/w/ws-a/jobs/run/echo/echo/wait"].(map[string]any)["post"].(map[string]any)
	requestSchema := runWait["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	properties := requestSchema["properties"].(map[string]any)
	if properties["message"] == nil {
		t.Fatalf("openapi request schema missing message: %#v", requestSchema)
	}
	statusEnum := runWait["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)["properties"].(map[string]any)["status"].(map[string]any)["enum"].([]any)
	if fmt.Sprint(statusEnum) != "[completed failed canceled]" {
		t.Fatalf("openapi status enum = %#v", statusEnum)
	}
	if paths["/api/w/ws-a/jobs/run/echo/echo"] == nil || paths["/api/w/ws-a/jobs/webhook/echo/echo"] == nil ||
		paths["/api/w/ws-a/jobs/{id}/result"] == nil {
		t.Fatalf("openapi paths missing: %#v", paths)
	}
	webhook := paths["/api/w/ws-a/jobs/webhook/echo/echo"].(map[string]any)["post"].(map[string]any)
	webhookSchema := webhook["requestBody"].(map[string]any)["content"].(map[string]any)["*/*"].(map[string]any)["schema"].(map[string]any)
	if len(webhookSchema) != 0 {
		t.Fatalf("webhook schema should be permissive: %#v", webhookSchema)
	}

	sourceResp, err := http.Get(server.URL + "/api/w/ws-a/apps/echo/source")
	if err != nil {
		t.Fatal(err)
	}
	defer sourceResp.Body.Close()
	if sourceResp.StatusCode != http.StatusOK {
		t.Fatalf("source status = %d, want %d", sourceResp.StatusCode, http.StatusOK)
	}
	var sourceBody struct {
		AppKey    string            `json:"app_key"`
		CommitSha string            `json:"commit_sha"`
		Files     map[string]string `json:"files"`
		Skipped   []string          `json:"skipped"`
	}
	if err := json.NewDecoder(sourceResp.Body).Decode(&sourceBody); err != nil {
		t.Fatal(err)
	}
	if sourceBody.AppKey != "echo" || sourceBody.CommitSha == "" ||
		!bytes.Contains([]byte(sourceBody.Files["windforce.json"]), []byte(`"app": "echo"`)) ||
		!bytes.Contains([]byte(sourceBody.Files["input.schema.json"]), []byte(`"message"`)) ||
		len(sourceBody.Skipped) != 0 {
		t.Fatalf("source body = %#v", sourceBody)
	}

	historyResp, err := http.Get(server.URL + "/api/w/ws-a/apps/echo/history")
	if err != nil {
		t.Fatal(err)
	}
	defer historyResp.Body.Close()
	if historyResp.StatusCode != http.StatusOK {
		t.Fatalf("history status = %d, want %d", historyResp.StatusCode, http.StatusOK)
	}
	var history []struct {
		ID           string `json:"id"`
		CommitSha    string `json:"commit_sha"`
		Source       string `json:"source"`
		GitSourceKey string `json:"git_source_key"`
		Status       string `json:"status"`
	}
	if err := json.NewDecoder(historyResp.Body).Decode(&history); err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].ID == "" || history[0].CommitSha != syncBody.Commit ||
		history[0].Source != "external_sync" || history[0].GitSourceKey != "source-a" || history[0].Status != "deployed" {
		t.Fatalf("history = %#v", history)
	}
}

func TestCanonicalGitSourceProbePatchAndDelete(t *testing.T) {
	tempDir := t.TempDir()
	repoDir := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "windforce.json"), []byte(`{"app":"echo","actions":{"echo":{"command":["helper"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repoDir, "init")
	runTestGit(t, repoDir, "checkout", "-b", "main")
	runTestGit(t, repoDir, "config", "user.email", "test@example.com")
	runTestGit(t, repoDir, "config", "user.name", "Test User")
	runTestGit(t, repoDir, "add", "windforce.json")
	runTestGit(t, repoDir, "commit", "-m", "initial")
	runTestGit(t, repoDir, "checkout", "-b", "feature")

	registry := gitsource.NewFileRegistry(filepath.Join(tempDir, "git-sources.json"))
	server := httptest.NewServer(New(Config{GitSources: registry, EnableAPI: true}))
	defer server.Close()

	probeBody, err := json.Marshal(map[string]string{
		"repo_url": filepath.ToSlash(repoDir),
		"branch":   "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	probeResp, err := http.Post(server.URL+"/api/w/ws-a/git_sources/probe", "application/json", bytes.NewReader(probeBody))
	if err != nil {
		t.Fatal(err)
	}
	defer probeResp.Body.Close()
	if probeResp.StatusCode != http.StatusOK {
		t.Fatalf("probe status = %d, want %d", probeResp.StatusCode, http.StatusOK)
	}
	var probe struct {
		Reachable    bool     `json:"reachable"`
		Branch       string   `json:"branch"`
		BranchExists bool     `json:"branch_exists"`
		Branches     []string `json:"branches"`
	}
	if err := json.NewDecoder(probeResp.Body).Decode(&probe); err != nil {
		t.Fatal(err)
	}
	if !probe.Reachable || probe.Branch != "main" || !probe.BranchExists || len(probe.Branches) != 2 {
		t.Fatalf("probe = %#v", probe)
	}

	registerBody, err := json.Marshal(map[string]string{
		"name":     "source-a",
		"repo_url": filepath.ToSlash(repoDir),
		"branch":   "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	registerResp, err := http.Post(server.URL+"/api/w/ws-a/git_sources", "application/json", bytes.NewReader(registerBody))
	if err != nil {
		t.Fatal(err)
	}
	_ = registerResp.Body.Close()
	if registerResp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d, want %d", registerResp.StatusCode, http.StatusCreated)
	}

	patchBody, err := json.Marshal(map[string]string{
		"name":      "source-b",
		"branch":    "feature",
		"creds_ref": "WINDFORCE_LITE_GIT_TOKEN",
	})
	if err != nil {
		t.Fatal(err)
	}
	patchReq, err := http.NewRequest(http.MethodPatch, server.URL+"/api/w/ws-a/git_sources/source-a", bytes.NewReader(patchBody))
	if err != nil {
		t.Fatal(err)
	}
	patchReq.Header.Set("Content-Type", "application/json")
	patchResp, err := http.DefaultClient.Do(patchReq)
	if err != nil {
		t.Fatal(err)
	}
	defer patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d, want %d", patchResp.StatusCode, http.StatusOK)
	}
	var patched struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Branch   string `json:"branch"`
		CredsRef string `json:"creds_ref"`
	}
	if err := json.NewDecoder(patchResp.Body).Decode(&patched); err != nil {
		t.Fatal(err)
	}
	if patched.ID != "source-b" || patched.Name != "source-b" || patched.Branch != "feature" || patched.CredsRef != "WINDFORCE_LITE_GIT_TOKEN" {
		t.Fatalf("patched = %#v", patched)
	}
	if _, err := registry.Get(context.Background(), "ws-a", "source-a"); !errors.Is(err, gitsource.ErrGitSourceNotFound) {
		t.Fatalf("old source lookup err = %v, want not found", err)
	}

	deleteReq, err := http.NewRequest(http.MethodDelete, server.URL+"/api/w/ws-a/git_sources/source-b", nil)
	if err != nil {
		t.Fatal(err)
	}
	deleteResp, err := http.DefaultClient.Do(deleteReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = deleteResp.Body.Close()
	if deleteResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d", deleteResp.StatusCode, http.StatusNoContent)
	}
	deleteAgainReq, err := http.NewRequest(http.MethodDelete, server.URL+"/api/w/ws-a/git_sources/source-b", nil)
	if err != nil {
		t.Fatal(err)
	}
	deleteAgainResp, err := http.DefaultClient.Do(deleteAgainReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = deleteAgainResp.Body.Close()
	if deleteAgainResp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete again status = %d, want %d", deleteAgainResp.StatusCode, http.StatusNotFound)
	}
}

func TestCanonicalWorkerTagsAPI(t *testing.T) {
	tempDir := t.TempDir()
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
	if err := fileCatalog.UpsertDeployment(context.Background(), contract.Deployment{
		Workspace:   "ws-b",
		GitSourceID: "source-b",
		App:         "other",
		Commit:      "commit-b",
		Actions: map[string]contract.Action{
			"other": {Action: "other", Command: []string{"helper"}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(New(Config{Catalog: fileCatalog, EnableAPI: true}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/w/ws-a/worker-tags")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("worker-tags status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var body struct {
		Tags []struct {
			Tag          string   `json:"tag"`
			LiveWorkers  int64    `json:"live_workers"`
			Capabilities []string `json:"capabilities"`
			Workers      []any    `json:"workers"`
		} `json:"tags"`
		DedicatedTag *string `json:"dedicated_tag"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Tags) != 1 || body.Tags[0].Tag != "default" || body.Tags[0].LiveWorkers != 0 ||
		len(body.Tags[0].Capabilities) != 0 || len(body.Tags[0].Workers) != 0 || body.DedicatedTag != nil {
		t.Fatalf("worker-tags body = %#v", body)
	}
}

func TestCanonicalAppAndActionTagOverrideAPI(t *testing.T) {
	tempDir := t.TempDir()
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
	server := httptest.NewServer(New(Config{Catalog: fileCatalog, EnableAPI: true}))
	defer server.Close()

	patchAppReq, err := http.NewRequest(http.MethodPatch, server.URL+"/api/w/ws-a/apps/echo", bytes.NewBufferString(`{"tag_override":"app-blue"}`))
	if err != nil {
		t.Fatal(err)
	}
	patchAppReq.Header.Set("Content-Type", "application/json")
	patchAppResp, err := http.DefaultClient.Do(patchAppReq)
	if err != nil {
		t.Fatal(err)
	}
	defer patchAppResp.Body.Close()
	if patchAppResp.StatusCode != http.StatusOK {
		t.Fatalf("patch app status = %d, want %d", patchAppResp.StatusCode, http.StatusOK)
	}
	var patchedApp struct {
		TagOverride       string `json:"tag_override"`
		EffectiveRouteTag string `json:"effective_route_tag"`
	}
	if err := json.NewDecoder(patchAppResp.Body).Decode(&patchedApp); err != nil {
		t.Fatal(err)
	}
	if patchedApp.TagOverride != "app-blue" || patchedApp.EffectiveRouteTag != "app-blue" {
		t.Fatalf("patched app = %#v", patchedApp)
	}

	actionResp, err := http.Get(server.URL + "/api/w/ws-a/apps/echo/actions/echo")
	if err != nil {
		t.Fatal(err)
	}
	defer actionResp.Body.Close()
	var inheritedAction struct {
		EffectiveRouteTag string `json:"effective_route_tag"`
	}
	if err := json.NewDecoder(actionResp.Body).Decode(&inheritedAction); err != nil {
		t.Fatal(err)
	}
	if inheritedAction.EffectiveRouteTag != "app-blue" {
		t.Fatalf("inherited action = %#v", inheritedAction)
	}

	patchActionReq, err := http.NewRequest(http.MethodPatch, server.URL+"/api/w/ws-a/apps/echo/actions/echo", bytes.NewBufferString(`{"tag_override":"action-fast"}`))
	if err != nil {
		t.Fatal(err)
	}
	patchActionReq.Header.Set("Content-Type", "application/json")
	patchActionResp, err := http.DefaultClient.Do(patchActionReq)
	if err != nil {
		t.Fatal(err)
	}
	defer patchActionResp.Body.Close()
	if patchActionResp.StatusCode != http.StatusOK {
		t.Fatalf("patch action status = %d, want %d", patchActionResp.StatusCode, http.StatusOK)
	}
	var patchedAction struct {
		TagOverride       string `json:"tag_override"`
		EffectiveRouteTag string `json:"effective_route_tag"`
	}
	if err := json.NewDecoder(patchActionResp.Body).Decode(&patchedAction); err != nil {
		t.Fatal(err)
	}
	if patchedAction.TagOverride != "action-fast" || patchedAction.EffectiveRouteTag != "action-fast" {
		t.Fatalf("patched action = %#v", patchedAction)
	}

	tagsResp, err := http.Get(server.URL + "/api/w/ws-a/worker-tags")
	if err != nil {
		t.Fatal(err)
	}
	defer tagsResp.Body.Close()
	var tagsBody struct {
		Tags []struct {
			Tag string `json:"tag"`
		} `json:"tags"`
	}
	if err := json.NewDecoder(tagsResp.Body).Decode(&tagsBody); err != nil {
		t.Fatal(err)
	}
	seenTags := map[string]bool{}
	for _, item := range tagsBody.Tags {
		seenTags[item.Tag] = true
	}
	for _, tag := range []string{"default", "app-blue", "action-fast"} {
		if !seenTags[tag] {
			t.Fatalf("worker tags missing %q: %#v", tag, tagsBody.Tags)
		}
	}

	clearActionReq, err := http.NewRequest(http.MethodPatch, server.URL+"/api/w/ws-a/apps/echo/actions/echo", bytes.NewBufferString(`{"tag_override":null}`))
	if err != nil {
		t.Fatal(err)
	}
	clearActionReq.Header.Set("Content-Type", "application/json")
	clearActionResp, err := http.DefaultClient.Do(clearActionReq)
	if err != nil {
		t.Fatal(err)
	}
	defer clearActionResp.Body.Close()
	var clearedAction struct {
		TagOverride       *string `json:"tag_override"`
		EffectiveRouteTag string  `json:"effective_route_tag"`
	}
	if err := json.NewDecoder(clearActionResp.Body).Decode(&clearedAction); err != nil {
		t.Fatal(err)
	}
	if clearedAction.TagOverride != nil || clearedAction.EffectiveRouteTag != "app-blue" {
		t.Fatalf("cleared action = %#v", clearedAction)
	}
}

func TestCanonicalJobRunPinsTagAndRequeueUsesCurrentEffectiveTag(t *testing.T) {
	tempDir := t.TempDir()
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	if err := fileCatalog.UpsertDeployment(context.Background(), contract.Deployment{
		Workspace: "ws-a",
		App:       "echo",
		Tag:       "app-main",
		Commit:    "commit-a",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo", Command: []string{"helper"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	store := state.NewLocalStore(filepath.Join(tempDir, "state.json"))
	server := httptest.NewServer(New(Config{
		Store:     store,
		Catalog:   fileCatalog,
		EnableAPI: true,
	}))
	defer server.Close()

	runResp, err := http.Post(server.URL+"/api/w/ws-a/jobs/run/echo/echo", "application/json", bytes.NewBufferString(`{"message":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer runResp.Body.Close()
	if runResp.StatusCode != http.StatusCreated {
		t.Fatalf("run status = %d, want %d", runResp.StatusCode, http.StatusCreated)
	}
	var runBody struct {
		JobID string `json:"job_id"`
	}
	if err := json.NewDecoder(runResp.Body).Decode(&runBody); err != nil {
		t.Fatal(err)
	}

	statusResp, err := http.Get(server.URL + "/api/w/ws-a/jobs/" + runBody.JobID)
	if err != nil {
		t.Fatal(err)
	}
	defer statusResp.Body.Close()
	var statusBody struct {
		Tag string `json:"tag"`
	}
	if err := json.NewDecoder(statusResp.Body).Decode(&statusBody); err != nil {
		t.Fatal(err)
	}
	if statusBody.Tag != "app-main" {
		t.Fatalf("initial job tag = %#v, want app-main", statusBody.Tag)
	}

	patchAppReq, err := http.NewRequest(http.MethodPatch, server.URL+"/api/w/ws-a/apps/echo", bytes.NewBufferString(`{"tag_override":"app-blue"}`))
	if err != nil {
		t.Fatal(err)
	}
	patchAppReq.Header.Set("Content-Type", "application/json")
	patchAppResp, err := http.DefaultClient.Do(patchAppReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = patchAppResp.Body.Close()
	if patchAppResp.StatusCode != http.StatusOK {
		t.Fatalf("patch app status = %d, want %d", patchAppResp.StatusCode, http.StatusOK)
	}

	requeueResp, err := http.Post(server.URL+"/api/w/ws-a/apps/echo/requeue", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer requeueResp.Body.Close()
	if requeueResp.StatusCode != http.StatusOK {
		t.Fatalf("requeue status = %d, want %d", requeueResp.StatusCode, http.StatusOK)
	}
	var requeueBody struct {
		Requeued int64 `json:"requeued"`
	}
	if err := json.NewDecoder(requeueResp.Body).Decode(&requeueBody); err != nil {
		t.Fatal(err)
	}
	if requeueBody.Requeued != 1 {
		t.Fatalf("requeued = %d, want 1", requeueBody.Requeued)
	}

	statusResp, err = http.Get(server.URL + "/api/w/ws-a/jobs/" + runBody.JobID)
	if err != nil {
		t.Fatal(err)
	}
	defer statusResp.Body.Close()
	statusBody = struct {
		Tag string `json:"tag"`
	}{}
	if err := json.NewDecoder(statusResp.Body).Decode(&statusBody); err != nil {
		t.Fatal(err)
	}
	if statusBody.Tag != "app-blue" {
		t.Fatalf("requeued app tag = %#v, want app-blue", statusBody.Tag)
	}

	patchActionReq, err := http.NewRequest(http.MethodPatch, server.URL+"/api/w/ws-a/apps/echo/actions/echo", bytes.NewBufferString(`{"tag_override":"action-fast"}`))
	if err != nil {
		t.Fatal(err)
	}
	patchActionReq.Header.Set("Content-Type", "application/json")
	patchActionResp, err := http.DefaultClient.Do(patchActionReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = patchActionResp.Body.Close()
	if patchActionResp.StatusCode != http.StatusOK {
		t.Fatalf("patch action status = %d, want %d", patchActionResp.StatusCode, http.StatusOK)
	}

	requeueResp, err = http.Post(server.URL+"/api/w/ws-a/apps/echo/requeue", "application/json", bytes.NewBufferString(`{"action":"echo"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer requeueResp.Body.Close()
	requeueBody = struct {
		Requeued int64 `json:"requeued"`
	}{}
	if err := json.NewDecoder(requeueResp.Body).Decode(&requeueBody); err != nil {
		t.Fatal(err)
	}
	if requeueBody.Requeued != 1 {
		t.Fatalf("action requeued = %d, want 1", requeueBody.Requeued)
	}

	statusResp, err = http.Get(server.URL + "/api/w/ws-a/jobs/" + runBody.JobID)
	if err != nil {
		t.Fatal(err)
	}
	defer statusResp.Body.Close()
	statusBody = struct {
		Tag string `json:"tag"`
	}{}
	if err := json.NewDecoder(statusResp.Body).Decode(&statusBody); err != nil {
		t.Fatal(err)
	}
	if statusBody.Tag != "action-fast" {
		t.Fatalf("requeued action tag = %#v, want action-fast", statusBody.Tag)
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
	if err := os.WriteFile(filepath.Join(sourceDir, "input.schema.json"), []byte(`{"type":"object","properties":{"message":{"type":"string"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "output.schema.json"), []byte(`{"type":"object","properties":{"ok":{"type":"boolean"}}}`), 0o644); err != nil {
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

	schemaResp, err := http.Get(server.URL + "/v1/apps/echo/actions/echo/schema")
	if err != nil {
		t.Fatal(err)
	}
	defer schemaResp.Body.Close()
	var schemaBody struct {
		InputSchema     json.RawMessage `json:"inputSchema"`
		OutputSchema    json.RawMessage `json:"outputSchema"`
		InputSchemaPath string          `json:"inputSchemaPath"`
	}
	if err := json.NewDecoder(schemaResp.Body).Decode(&schemaBody); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(schemaBody.InputSchema, []byte(`"message"`)) ||
		!bytes.Contains(schemaBody.OutputSchema, []byte(`"ok"`)) ||
		schemaBody.InputSchemaPath != "input.schema.json" {
		t.Fatalf("schema body = %#v input=%s output=%s", schemaBody, schemaBody.InputSchema, schemaBody.OutputSchema)
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
