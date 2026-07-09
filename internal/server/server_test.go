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
	"reflect"
	"testing"
	"time"

	"github.com/imprun/windforce-lite/internal/bundle"
	"github.com/imprun/windforce-lite/internal/catalog"
	"github.com/imprun/windforce-lite/internal/contract"
	"github.com/imprun/windforce-lite/internal/gitsource"
	"github.com/imprun/windforce-lite/internal/state"
	"github.com/imprun/windforce-lite/internal/syncer"
)

func TestAdapterTriggerCreatesRunAndAPIReadsIt(t *testing.T) {
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
		Store:           state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		Catalog:         fileCatalog,
		EnableTrigger:   true,
		EnableAPI:       true,
		TriggerAdapters: []TriggerAdapter{fakeTriggerAdapter{}},
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	req, err := http.NewRequest(http.MethodPost, server.URL+"/external/v1/echo/echo", bytes.NewBufferString(`{"message":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("TASKID", "task-a")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var triggerResponse map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&triggerResponse); err != nil {
		t.Fatal(err)
	}
	if triggerResponse["runId"] != "task-a" || triggerResponse["state"] != string(state.RunQueued) {
		t.Fatalf("trigger response = %#v", triggerResponse)
	}
	if triggerResponse["externalApp"] != "echo" || triggerResponse["externalAction"] != "echo" {
		t.Fatalf("adapter trigger response = %#v", triggerResponse)
	}

	listResp, err := http.Get(server.URL + "/api/w/default/jobs?status=queued&app=echo&action=echo&trigger_kind=external&limit=1")
	if err != nil {
		t.Fatal(err)
	}
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("job list status = %d, want %d", listResp.StatusCode, http.StatusOK)
	}
	var listResponse struct {
		Items []struct {
			ID          string `json:"id"`
			Status      string `json:"status"`
			AppKey      string `json:"app_key"`
			ActionKey   string `json:"action_key"`
			TriggerKind string `json:"trigger_kind"`
		} `json:"items"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&listResponse); err != nil {
		t.Fatal(err)
	}
	if len(listResponse.Items) != 1 ||
		listResponse.Items[0].Status != "queued" ||
		listResponse.Items[0].AppKey != "echo" ||
		listResponse.Items[0].ActionKey != "echo" ||
		listResponse.Items[0].TriggerKind != "external" {
		t.Fatalf("job list response = %#v", listResponse)
	}

	jobID := listResponse.Items[0].ID
	getResp, err := http.Get(server.URL + "/api/w/default/jobs/" + jobID)
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET job status = %d, want %d", getResp.StatusCode, http.StatusOK)
	}

	cancelResp, err := http.Post(server.URL+"/api/w/default/jobs/"+jobID+"/cancel", "application/json", bytes.NewBufferString(`{"reason":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer cancelResp.Body.Close()
	if cancelResp.StatusCode != http.StatusOK {
		t.Fatalf("cancel status = %d, want %d", cancelResp.StatusCode, http.StatusOK)
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

	missingResp, err := http.Get(server.URL + "/api/w/ws-a/jobs/missing/logs")
	if err != nil {
		t.Fatal(err)
	}
	defer missingResp.Body.Close()
	if missingResp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing logs status = %d, want %d", missingResp.StatusCode, http.StatusNotFound)
	}
	var missingBody struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(missingResp.Body).Decode(&missingBody); err != nil {
		t.Fatal(err)
	}
	if missingBody.Error != "job not found" {
		t.Fatalf("missing logs body = %#v", missingBody)
	}
}

func TestCanonicalStateAPI(t *testing.T) {
	tempDir := t.TempDir()
	server := httptest.NewServer(New(Config{
		Store:     state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		EnableAPI: true,
	}))
	defer server.Close()

	getResp, err := http.Get(server.URL + "/api/w/ws-a/state?path=flow/count")
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("missing state status = %d, want %d", getResp.StatusCode, http.StatusOK)
	}
	body, err := io.ReadAll(getResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "null" {
		t.Fatalf("missing state body = %q, want null", body)
	}

	setResp, err := http.Post(server.URL+"/api/w/ws-a/state?path=flow/count", "application/json", bytes.NewBufferString(`{"count":1}`))
	if err != nil {
		t.Fatal(err)
	}
	defer setResp.Body.Close()
	if setResp.StatusCode != http.StatusOK {
		t.Fatalf("set state status = %d, want %d", setResp.StatusCode, http.StatusOK)
	}
	var setBody struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(setResp.Body).Decode(&setBody); err != nil {
		t.Fatal(err)
	}
	if setBody.Path != "flow/count" {
		t.Fatalf("set state body = %#v", setBody)
	}

	getResp, err = http.Get(server.URL + "/api/w/ws-a/state?path=flow/count")
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()
	body, err = io.ReadAll(getResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]int
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("state body is not JSON object: %v", err)
	}
	if got["count"] != 1 {
		t.Fatalf("state body = %q", body)
	}

	getResp, err = http.Get(server.URL + "/api/w/ws-b/state?path=flow/count")
	if err != nil {
		t.Fatal(err)
	}
	defer getResp.Body.Close()
	body, err = io.ReadAll(getResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "null" {
		t.Fatalf("other workspace state body = %q, want null", body)
	}

	missingPathResp, err := http.Get(server.URL + "/api/w/ws-a/state")
	if err != nil {
		t.Fatal(err)
	}
	defer missingPathResp.Body.Close()
	if missingPathResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing path status = %d, want %d", missingPathResp.StatusCode, http.StatusBadRequest)
	}
}

func TestCanonicalVariablesAndResourcesAPI(t *testing.T) {
	tempDir := t.TempDir()
	server := httptest.NewServer(New(Config{
		Store:     state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		EnableAPI: true,
	}))
	defer server.Close()

	setVariableResp, err := http.Post(server.URL+"/api/w/ws-a/variables", "application/json", bytes.NewBufferString(`{"path":"config/token","value":"shared","is_secret":true,"description":"shared token"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer setVariableResp.Body.Close()
	if setVariableResp.StatusCode != http.StatusOK {
		t.Fatalf("set variable status = %d, want %d", setVariableResp.StatusCode, http.StatusOK)
	}
	setVariableResp, err = http.Post(server.URL+"/api/w/ws-a/variables", "application/json", bytes.NewBufferString(`{"app_key":"echo","path":"config/token","value":"scoped"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer setVariableResp.Body.Close()
	if setVariableResp.StatusCode != http.StatusOK {
		t.Fatalf("set scoped variable status = %d, want %d", setVariableResp.StatusCode, http.StatusOK)
	}

	getVariableResp, err := http.Get(server.URL + "/api/w/ws-a/variables/get/p/config/token?app=echo")
	if err != nil {
		t.Fatal(err)
	}
	defer getVariableResp.Body.Close()
	if getVariableResp.StatusCode != http.StatusOK {
		t.Fatalf("get variable status = %d, want %d", getVariableResp.StatusCode, http.StatusOK)
	}
	var variableBody struct {
		Path     string `json:"path"`
		Value    string `json:"value"`
		IsSecret bool   `json:"is_secret"`
	}
	if err := json.NewDecoder(getVariableResp.Body).Decode(&variableBody); err != nil {
		t.Fatal(err)
	}
	if variableBody.Path != "config/token" || variableBody.Value != "scoped" || variableBody.IsSecret {
		t.Fatalf("variable body = %#v", variableBody)
	}

	listResp, err := http.Get(server.URL + "/api/w/ws-a/variables")
	if err != nil {
		t.Fatal(err)
	}
	defer listResp.Body.Close()
	var variables []struct {
		AppKey   string `json:"app_key"`
		Path     string `json:"path"`
		Value    string `json:"value"`
		IsSecret bool   `json:"is_secret"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&variables); err != nil {
		t.Fatal(err)
	}
	secretHidden := false
	for _, variable := range variables {
		if variable.Path == "config/token" && variable.AppKey == "" && variable.IsSecret && variable.Value == "" {
			secretHidden = true
		}
	}
	if !secretHidden {
		t.Fatalf("variables list did not hide secret value: %#v", variables)
	}

	req, err := http.NewRequest(http.MethodDelete, server.URL+"/api/w/ws-a/variables/p/config/token?app=echo", nil)
	if err != nil {
		t.Fatal(err)
	}
	deleteResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer deleteResp.Body.Close()
	if deleteResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete variable status = %d, want %d", deleteResp.StatusCode, http.StatusNoContent)
	}

	setResourceResp, err := http.Post(server.URL+"/api/w/ws-a/resources", "application/json", bytes.NewBufferString(`{"path":"browser/profile","value":{"headless":true},"resource_type":"json"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer setResourceResp.Body.Close()
	if setResourceResp.StatusCode != http.StatusOK {
		t.Fatalf("set resource status = %d, want %d", setResourceResp.StatusCode, http.StatusOK)
	}
	getResourceResp, err := http.Get(server.URL + "/api/w/ws-a/resources/get/p/browser/profile")
	if err != nil {
		t.Fatal(err)
	}
	defer getResourceResp.Body.Close()
	body, err := io.ReadAll(getResourceResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var resource map[string]bool
	if err := json.Unmarshal(body, &resource); err != nil {
		t.Fatalf("resource body is not JSON object: %v", err)
	}
	if !resource["headless"] {
		t.Fatalf("resource body = %q", body)
	}
}

func TestCanonicalJobRunStatusAndResultAPI(t *testing.T) {
	tempDir := t.TempDir()
	store := state.NewLocalStore(filepath.Join(tempDir, "state.json"))
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	if err := fileCatalog.UpsertDeployment(context.Background(), contract.Deployment{
		Workspace:   "ws-a",
		GitSourceID: "1",
		App:         "echo",
		Commit:      "commit-a",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo", Entrypoint: "main.ts", Command: []string{"helper"}, TimeoutMs: 45000},
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
	if statusBody["id"] != runResponse.JobID || statusBody["state"] != "queued" || statusBody["app_key"] != "echo" ||
		statusBody["action_key"] != "echo" || statusBody["trigger_kind"] != "api" || statusBody["entrypoint"] != "main.ts" ||
		statusBody["git_source_id"] != float64(1) || statusBody["timeout_s"] != float64(45) {
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

	doneStatusResp, err := http.Get(server.URL + "/api/w/ws-a/jobs/" + runResponse.JobID)
	if err != nil {
		t.Fatal(err)
	}
	defer doneStatusResp.Body.Close()
	if doneStatusResp.StatusCode != http.StatusOK {
		t.Fatalf("done job status = %d, want %d", doneStatusResp.StatusCode, http.StatusOK)
	}
	var doneStatusBody struct {
		State  string `json:"state"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(doneStatusResp.Body).Decode(&doneStatusBody); err != nil {
		t.Fatal(err)
	}
	if doneStatusBody.State != "completed" || doneStatusBody.Status != "success" {
		t.Fatalf("done job status = %#v", doneStatusBody)
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
			ID          string `json:"id"`
			AppKey      string `json:"app_key"`
			ActionKey   string `json:"action_key"`
			GitSourceID int64  `json:"git_source_id"`
			Status      string `json:"status"`
			Completed   bool   `json:"completed"`
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
	if len(listBody.Items) != 1 || listBody.Items[0].ID != runResponse.JobID ||
		listBody.Items[0].GitSourceID != 1 ||
		listBody.Items[0].Status != "success" || !listBody.Items[0].Completed {
		t.Fatalf("list body = %#v", listBody)
	}
	if listBody.Pagination.Limit != 1 || listBody.Pagination.Count != 1 {
		t.Fatalf("pagination = %#v", listBody.Pagination)
	}

	failedFilterResp, err := http.Get(server.URL + "/api/w/ws-a/jobs?status=failed")
	if err != nil {
		t.Fatal(err)
	}
	defer failedFilterResp.Body.Close()
	if failedFilterResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("failed filter status = %d, want %d", failedFilterResp.StatusCode, http.StatusBadRequest)
	}

	failResp, err := http.Post(server.URL+"/api/w/ws-a/jobs/run/echo/echo", "application/json", bytes.NewBufferString(`{"message":"fail"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer failResp.Body.Close()
	if failResp.StatusCode != http.StatusCreated {
		t.Fatalf("failed run enqueue status = %d, want %d", failResp.StatusCode, http.StatusCreated)
	}
	var failRunResponse struct {
		JobID string `json:"job_id"`
	}
	if err := json.NewDecoder(failResp.Body).Decode(&failRunResponse); err != nil {
		t.Fatal(err)
	}
	failedClaim, failedLease, err := store.ClaimJob(context.Background(), "worker-a", 0)
	if err != nil {
		t.Fatalf("ClaimJob for failed run returned error: %v", err)
	}
	if failedClaim.ID != failRunResponse.JobID {
		t.Fatalf("claimed failed job = %q, want %q", failedClaim.ID, failRunResponse.JobID)
	}
	if err := store.CompleteJobFailed(context.Background(), failedLease, contract.JobResult{
		JobID:      failedClaim.ID,
		App:        "echo",
		Action:     "echo",
		Output:     json.RawMessage(`{"name":"TargetError","message":"target rejected"}`),
		ExitCode:   7,
		DurationMs: 34,
	}); err != nil {
		t.Fatalf("CompleteJobFailed returned error: %v", err)
	}
	failedResultResp, err := http.Get(server.URL + "/api/w/ws-a/jobs/" + failRunResponse.JobID + "/result")
	if err != nil {
		t.Fatal(err)
	}
	defer failedResultResp.Body.Close()
	if failedResultResp.StatusCode != http.StatusOK {
		t.Fatalf("failed result status = %d, want %d", failedResultResp.StatusCode, http.StatusOK)
	}
	var failedResultBody struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(failedResultResp.Body).Decode(&failedResultBody); err != nil {
		t.Fatal(err)
	}
	var failedResult struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(failedResultBody.Result, &failedResult); err != nil {
		t.Fatal(err)
	}
	if failedResultBody.Status != "failure" || failedResult.Name != "TargetError" || failedResult.Message != "target rejected" {
		t.Fatalf("failed result body = %#v result=%s", failedResultBody, failedResultBody.Result)
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

	statusResp, err := http.Get(server.URL + "/api/w/ws-a/jobs/" + runBody.JobID)
	if err != nil {
		t.Fatal(err)
	}
	defer statusResp.Body.Close()
	var statusBody struct {
		State  string `json:"state"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(statusResp.Body).Decode(&statusBody); err != nil {
		t.Fatal(err)
	}
	if statusBody.State != "completed" || statusBody.Status != "canceled" {
		t.Fatalf("job status = %#v", statusBody)
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

func TestCanonicalControlPlaneRejectsInvalidAppAndActionKeys(t *testing.T) {
	server := httptest.NewServer(New(Config{EnableAPI: true}))
	defer server.Close()

	for _, tc := range []struct {
		method string
		path   string
		body   string
		want   string
	}{
		{http.MethodGet, "/api/w/ws-a/apps/Bad!", "", "invalid app key"},
		{http.MethodGet, "/api/w/ws-a/apps/Bad!/source", "", "invalid app key"},
		{http.MethodGet, "/api/w/ws-a/apps/Bad!/history", "", "invalid app key"},
		{http.MethodGet, "/api/w/ws-a/apps/Bad!/openapi.json", "", "invalid app key"},
		{http.MethodGet, "/api/w/ws-a/apps/echo/actions/Bad!", "", "invalid app/action key"},
		{http.MethodPatch, "/api/w/ws-a/apps/Bad!", `{"tag_override":null}`, "invalid app key"},
		{http.MethodPatch, "/api/w/ws-a/apps/echo/actions/Bad!", `{"tag_override":null}`, "invalid app/action key"},
		{http.MethodPost, "/api/w/ws-a/apps/Bad!/requeue", `{}`, "invalid app key"},
	} {
		req, err := http.NewRequest(tc.method, server.URL+tc.path, bytes.NewBufferString(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			_ = resp.Body.Close()
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || body.Error != tc.want {
			t.Fatalf("%s %s = %d %#v, want 400 %q", tc.method, tc.path, resp.StatusCode, body, tc.want)
		}
	}
}

func TestLegacyV1ControlPlaneRoutesAreNotExposed(t *testing.T) {
	tempDir := t.TempDir()
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	server := httptest.NewServer(New(Config{
		Store:      state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		Catalog:    fileCatalog,
		Syncer:     &syncer.Syncer{Store: bundle.NewLocalStore(filepath.Join(tempDir, "store")), Catalog: fileCatalog},
		GitSources: gitsource.NewFileRegistry(filepath.Join(tempDir, "git-sources.json")),
		EnableAPI:  true,
	}))
	defer server.Close()

	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/v1/sync", `{}`},
		{http.MethodGet, "/v1/catalog", ""},
		{http.MethodPost, "/v1/git-sources", `{}`},
		{http.MethodGet, "/v1/git-sources/source-a", ""},
		{http.MethodGet, "/v1/deployments/echo", ""},
		{http.MethodGet, "/v1/apps/echo/actions/echo/schema", ""},
		{http.MethodGet, "/v1/runs/run-a", ""},
		{http.MethodPost, "/v1/runs/run-a/cancel", `{}`},
		{http.MethodPost, "/v1/runs/run-a/retry", `{}`},
		{http.MethodGet, "/v1/human-tasks/task-a", ""},
		{http.MethodPost, "/v1/human-tasks/task-a/resume", `{}`},
	} {
		req, err := http.NewRequest(tc.method, server.URL+tc.path, bytes.NewBufferString(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s %s status = %d, want %d", tc.method, tc.path, resp.StatusCode, http.StatusNotFound)
		}
	}
}

func TestLegacyCoreTriggerRouteIsNotExposed(t *testing.T) {
	tempDir := t.TempDir()
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	server := httptest.NewServer(New(Config{
		Store:         state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		Catalog:       fileCatalog,
		EnableTrigger: true,
	}))
	defer server.Close()

	resp, err := http.Post(server.URL+"/v1/apps/echo/actions/echo", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("legacy core trigger status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

func TestCanonicalActionExposesEmptySchemas(t *testing.T) {
	tempDir := t.TempDir()
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	if err := fileCatalog.UpsertDeployment(context.Background(), contract.Deployment{
		Workspace: "ws-a",
		App:       "echo",
		Commit:    "commit-a",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(New(Config{Catalog: fileCatalog, EnableAPI: true}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/w/ws-a/apps/echo/actions/echo")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("action status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var body map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"input_schema", "output_schema"} {
		schema, ok := body[field]
		if !ok {
			t.Fatalf("%s missing from action body: %#v", field, body)
		}
		if !bytes.Equal(bytes.TrimSpace(schema), []byte(`{}`)) {
			t.Fatalf("%s = %s, want {}", field, schema)
		}
	}
}

func TestCanonicalActionExposesPinnedSchemaBodiesWithoutSourceStore(t *testing.T) {
	tempDir := t.TempDir()
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	if err := fileCatalog.UpsertDeployment(context.Background(), contract.Deployment{
		Workspace: "ws-a",
		App:       "echo",
		Commit:    "commit-a",
		Actions: map[string]contract.Action{
			"echo": {
				Action:           "echo",
				InputSchema:      "input.schema.json",
				OutputSchema:     "output.schema.json",
				InputSchemaBody:  json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}}}`),
				OutputSchemaBody: json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}}}`),
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(New(Config{Catalog: fileCatalog, EnableAPI: true}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/w/ws-a/apps/echo/actions/echo")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("action status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var actionBody struct {
		InputSchema  json.RawMessage `json:"input_schema"`
		OutputSchema json.RawMessage `json:"output_schema"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&actionBody); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(actionBody.InputSchema, []byte(`"message"`)) ||
		!bytes.Contains(actionBody.OutputSchema, []byte(`"ok"`)) {
		t.Fatalf("action schemas = input:%s output:%s", actionBody.InputSchema, actionBody.OutputSchema)
	}
}

func TestCanonicalControlPlaneUsesMaterializedActionSchemas(t *testing.T) {
	tempDir := t.TempDir()
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	inputSchema := json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}}}`)
	outputSchema := json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}}}`)
	if err := fileCatalog.UpsertDeployment(context.Background(), contract.Deployment{
		Workspace:   "ws-a",
		GitSourceID: "1",
		App:         "echo",
		Commit:      "commit-a",
		Entrypoint:  "main.ts",
		Actions: map[string]contract.Action{
			"echo": {
				Action:           "echo",
				InputSchema:      "input.schema.json",
				OutputSchema:     "output.schema.json",
				InputSchemaBody:  inputSchema,
				OutputSchemaBody: outputSchema,
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(New(Config{
		Store:     state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		Catalog:   fileCatalog,
		EnableAPI: true,
	}))
	defer server.Close()

	actionResp, err := http.Get(server.URL + "/api/w/ws-a/apps/echo/actions/echo")
	if err != nil {
		t.Fatal(err)
	}
	defer actionResp.Body.Close()
	if actionResp.StatusCode != http.StatusOK {
		t.Fatalf("action status = %d, want %d", actionResp.StatusCode, http.StatusOK)
	}
	var actionBody struct {
		InputSchema  json.RawMessage `json:"input_schema"`
		OutputSchema json.RawMessage `json:"output_schema"`
	}
	if err := json.NewDecoder(actionResp.Body).Decode(&actionBody); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(actionBody.InputSchema, []byte(`"message"`)) ||
		!bytes.Contains(actionBody.OutputSchema, []byte(`"ok"`)) {
		t.Fatalf("action schemas = input:%s output:%s", actionBody.InputSchema, actionBody.OutputSchema)
	}

	openAPIResp, err := http.Get(server.URL + "/api/w/ws-a/apps/echo/openapi.json")
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
	paths := openAPIBody["paths"].(map[string]any)
	runWait := paths["/api/w/ws-a/jobs/run/echo/echo/wait"].(map[string]any)["post"].(map[string]any)
	requestSchema := runWait["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if requestSchema["properties"].(map[string]any)["message"] == nil {
		t.Fatalf("openapi request schema = %#v", requestSchema)
	}

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
	if statusResp.StatusCode != http.StatusOK {
		t.Fatalf("status status = %d, want %d", statusResp.StatusCode, http.StatusOK)
	}
	var statusBody struct {
		InputSchema  json.RawMessage `json:"input_schema"`
		OutputSchema json.RawMessage `json:"output_schema"`
	}
	if err := json.NewDecoder(statusResp.Body).Decode(&statusBody); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(statusBody.InputSchema, []byte(`"message"`)) ||
		!bytes.Contains(statusBody.OutputSchema, []byte(`"ok"`)) {
		t.Fatalf("job schemas = input:%s output:%s", statusBody.InputSchema, statusBody.OutputSchema)
	}
}

func TestCanonicalSampleGitSourceRegistersAndSyncs(t *testing.T) {
	tempDir := t.TempDir()
	store := bundle.NewLocalStore(filepath.Join(tempDir, "store"))
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	handler := New(Config{
		Store:      state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		Catalog:    fileCatalog,
		Syncer:     &syncer.Syncer{Store: store, Catalog: fileCatalog, CloneRoot: tempDir},
		GitSources: gitsource.NewFileRegistry(filepath.Join(tempDir, "git-sources.json")),
		EnableAPI:  true,
		SampleRoot: filepath.Join(tempDir, "samples"),
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	resp, err := http.Post(server.URL+"/api/w/ws-a/git_sources/sample", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("sample status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	var body struct {
		Source struct {
			ID          int64  `json:"id"`
			WorkspaceID string `json:"workspace_id"`
			Name        string `json:"name"`
			Kind        string `json:"kind"`
			RepoURL     string `json:"repo_url"`
		} `json:"source"`
		SyncResult struct {
			App     string   `json:"app"`
			Commit  string   `json:"commit"`
			Actions []string `json:"actions"`
		} `json:"sync_result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Source.ID <= 0 || body.Source.Name != "sample-hello" ||
		body.Source.WorkspaceID != "ws-a" || body.Source.Kind != "managed" || body.Source.RepoURL == "" {
		t.Fatalf("sample source = %#v", body.Source)
	}
	if body.SyncResult.App != "sample_hello" || body.SyncResult.Commit == "" ||
		len(body.SyncResult.Actions) != 1 || body.SyncResult.Actions[0] != "sample_hello.echo" {
		t.Fatalf("sample sync result = %#v", body.SyncResult)
	}

	actionResp, err := http.Get(server.URL + "/api/w/ws-a/apps/sample_hello/actions/echo")
	if err != nil {
		t.Fatal(err)
	}
	defer actionResp.Body.Close()
	if actionResp.StatusCode != http.StatusOK {
		t.Fatalf("sample action status = %d, want %d", actionResp.StatusCode, http.StatusOK)
	}
	var actionBody struct {
		InputSchema json.RawMessage `json:"input_schema"`
	}
	if err := json.NewDecoder(actionResp.Body).Decode(&actionBody); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(actionBody.InputSchema, []byte(`"message"`)) {
		t.Fatalf("sample input schema = %s", actionBody.InputSchema)
	}

	againResp, err := http.Post(server.URL+"/api/w/ws-a/git_sources/sample", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer againResp.Body.Close()
	if againResp.StatusCode != http.StatusOK {
		t.Fatalf("second sample status = %d, want %d", againResp.StatusCode, http.StatusOK)
	}
}

func TestCanonicalAppLookupIsWorkspaceScoped(t *testing.T) {
	tempDir := t.TempDir()
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	for _, deployment := range []contract.Deployment{
		{
			Workspace:  "ws-a",
			App:        "echo",
			Commit:     "commit-a",
			Entrypoint: "main.ts",
			Actions: map[string]contract.Action{
				"echo": {Action: "echo"},
			},
		},
		{
			Workspace:  "ws-b",
			App:        "echo",
			Commit:     "commit-b",
			Entrypoint: "main.ts",
			Actions: map[string]contract.Action{
				"echo": {Action: "echo"},
			},
		},
	} {
		if err := fileCatalog.UpsertDeployment(context.Background(), deployment); err != nil {
			t.Fatal(err)
		}
	}

	server := httptest.NewServer(New(Config{Catalog: fileCatalog, EnableAPI: true}))
	defer server.Close()

	for _, tc := range []struct {
		workspace string
		commit    string
	}{
		{workspace: "ws-a", commit: "commit-a"},
		{workspace: "ws-b", commit: "commit-b"},
	} {
		resp, err := http.Get(server.URL + "/api/w/" + tc.workspace + "/apps/echo")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s app status = %d, want %d", tc.workspace, resp.StatusCode, http.StatusOK)
		}
		var body struct {
			App struct {
				WorkspaceID string `json:"workspace_id"`
				CommitSha   string `json:"commit_sha"`
			} `json:"app"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.App.WorkspaceID != tc.workspace || body.App.CommitSha != tc.commit {
			t.Fatalf("%s app = %#v, want commit %s", tc.workspace, body.App, tc.commit)
		}
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
		"entrypoint": "main.ts",
		"scriptLang": "typescript",
		"timeout": 120,
		"maxConcurrent": 2,
		"capabilities": ["browser"],
		"actions": {
			"echo": {
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
	if err := os.WriteFile(filepath.Join(repoDir, "logo.bin"), []byte{0xff, 0xfe, 0x00, 0x01}, 0o644); err != nil {
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

	invalidAppResp, err := http.Get(server.URL + "/api/w/ws-a/apps/Echo")
	if err != nil {
		t.Fatal(err)
	}
	defer invalidAppResp.Body.Close()
	if invalidAppResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid app status = %d, want %d", invalidAppResp.StatusCode, http.StatusBadRequest)
	}
	invalidRunResp, err := http.Post(server.URL+"/api/w/ws-a/jobs/run/Echo/echo", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer invalidRunResp.Body.Close()
	if invalidRunResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid run status = %d, want %d", invalidRunResp.StatusCode, http.StatusBadRequest)
	}

	legacyRegisterResp, err := http.Post(server.URL+"/api/w/ws-a/git_sources", "application/json", bytes.NewBufferString(`{"id":"source-a","repoUrl":"`+filepath.ToSlash(repoDir)+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer legacyRegisterResp.Body.Close()
	if legacyRegisterResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("legacy canonical register status = %d, want %d", legacyRegisterResp.StatusCode, http.StatusBadRequest)
	}

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
		body, _ := io.ReadAll(registerResp.Body)
		t.Fatalf("register status = %d, want %d: %s", registerResp.StatusCode, http.StatusCreated, body)
	}
	var registered struct {
		ID          int64     `json:"id"`
		WorkspaceID string    `json:"workspace_id"`
		Name        string    `json:"name"`
		RepoURL     string    `json:"repo_url"`
		CredsRef    string    `json:"creds_ref"`
		CreatedAt   time.Time `json:"created_at"`
	}
	if err := json.NewDecoder(registerResp.Body).Decode(&registered); err != nil {
		t.Fatal(err)
	}
	if registered.ID <= 0 || registered.Name != "source-a" || registered.WorkspaceID != "ws-a" ||
		registered.RepoURL != filepath.ToSlash(repoDir) || registered.CredsRef != "WINDFORCE_LITE_GIT_TOKEN" ||
		registered.CreatedAt.IsZero() {
		t.Fatalf("registered source = %#v", registered)
	}
	registeredID := fmt.Sprint(registered.ID)

	duplicateResp, err := http.Post(server.URL+"/api/w/ws-a/git_sources", "application/json", bytes.NewReader(registerBody))
	if err != nil {
		t.Fatal(err)
	}
	defer duplicateResp.Body.Close()
	if duplicateResp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate register status = %d, want %d", duplicateResp.StatusCode, http.StatusConflict)
	}
	var duplicateBody struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(duplicateResp.Body).Decode(&duplicateBody); err != nil {
		t.Fatal(err)
	}
	if duplicateBody.Error != "git source name already exists" {
		t.Fatalf("duplicate register error = %q", duplicateBody.Error)
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

	syncResp, err := http.Post(server.URL+"/api/w/ws-a/git_sources/"+registeredID+"/sync", "", nil)
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

	syncedSourcesResp, err := http.Get(server.URL + "/api/w/ws-a/git_sources")
	if err != nil {
		t.Fatal(err)
	}
	defer syncedSourcesResp.Body.Close()
	if syncedSourcesResp.StatusCode != http.StatusOK {
		t.Fatalf("synced sources status = %d, want %d", syncedSourcesResp.StatusCode, http.StatusOK)
	}
	var syncedSources []struct {
		Name             string     `json:"name"`
		LastSyncedCommit *string    `json:"last_synced_commit"`
		LastSyncedAt     *time.Time `json:"last_synced_at"`
	}
	if err := json.NewDecoder(syncedSourcesResp.Body).Decode(&syncedSources); err != nil {
		t.Fatal(err)
	}
	if len(syncedSources) != 1 || syncedSources[0].Name != "source-a" ||
		syncedSources[0].LastSyncedCommit == nil || *syncedSources[0].LastSyncedCommit != syncBody.Commit ||
		syncedSources[0].LastSyncedAt == nil || syncedSources[0].LastSyncedAt.IsZero() {
		t.Fatalf("synced sources = %#v", syncedSources)
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
			AppKey            string   `json:"app_key"`
			GitSourceID       int64    `json:"git_source_id"`
			ActionsCount      int64    `json:"actions_count"`
			EffectiveRouteTag string   `json:"effective_route_tag"`
			Capabilities      []string `json:"required_capabilities"`
		} `json:"apps"`
	}
	if err := json.NewDecoder(summaryResp.Body).Decode(&summary); err != nil {
		t.Fatal(err)
	}
	if len(summary.Apps) != 1 || summary.Apps[0].AppKey != "echo" ||
		summary.Apps[0].GitSourceID != registered.ID || summary.Apps[0].ActionsCount != 1 ||
		summary.Apps[0].EffectiveRouteTag != "browser" || !reflect.DeepEqual(summary.Apps[0].Capabilities, []string{"browser"}) {
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
		TimeoutS     int32           `json:"timeout_s"`
		UpdatedAt    time.Time       `json:"updated_at"`
	}
	if err := json.NewDecoder(actionResp.Body).Decode(&actionBody); err != nil {
		t.Fatal(err)
	}
	if actionBody.AppKey != "echo" || actionBody.ActionKey != "echo" ||
		actionBody.TimeoutS != 120 || actionBody.UpdatedAt.IsZero() ||
		!bytes.Contains(actionBody.InputSchema, []byte(`"message"`)) || !bytes.Contains(actionBody.OutputSchema, []byte(`"ok"`)) {
		t.Fatalf("action body = %#v input=%s output=%s", actionBody, actionBody.InputSchema, actionBody.OutputSchema)
	}

	invalidActionResp, err := http.Get(server.URL + "/api/w/ws-a/apps/echo/actions/bad-action")
	if err != nil {
		t.Fatal(err)
	}
	defer invalidActionResp.Body.Close()
	if invalidActionResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid action status = %d, want %d", invalidActionResp.StatusCode, http.StatusBadRequest)
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
			AppKey               string    `json:"app_key"`
			GitSourceID          int64     `json:"git_source_id"`
			Entrypoint           string    `json:"entrypoint"`
			ScriptLang           string    `json:"script_lang"`
			TimeoutS             int32     `json:"timeout_s"`
			MaxConcurrent        *int32    `json:"max_concurrent"`
			RequiredCapabilities []string  `json:"required_capabilities"`
			EffectiveRouteTag    string    `json:"effective_route_tag"`
			UpdatedAt            time.Time `json:"updated_at"`
		} `json:"app"`
		Actions []struct {
			ActionKey             string          `json:"action_key"`
			InputSchema           json.RawMessage `json:"input_schema"`
			EffectiveCapabilities []string        `json:"effective_capabilities"`
			EffectiveRouteTag     string          `json:"effective_route_tag"`
		} `json:"actions"`
	}
	if err := json.NewDecoder(appResp.Body).Decode(&appBody); err != nil {
		t.Fatal(err)
	}
	if appBody.App.AppKey != "echo" || appBody.App.GitSourceID != registered.ID ||
		appBody.App.Entrypoint != "main.ts" || appBody.App.ScriptLang != "typescript" ||
		appBody.App.TimeoutS != 120 || appBody.App.MaxConcurrent == nil || *appBody.App.MaxConcurrent != 2 ||
		!reflect.DeepEqual(appBody.App.RequiredCapabilities, []string{"browser"}) || appBody.App.EffectiveRouteTag != "browser" ||
		appBody.App.UpdatedAt.IsZero() ||
		len(appBody.Actions) != 1 || appBody.Actions[0].ActionKey != "echo" ||
		!reflect.DeepEqual(appBody.Actions[0].EffectiveCapabilities, []string{"browser"}) || appBody.Actions[0].EffectiveRouteTag != "browser" ||
		!bytes.Contains(appBody.Actions[0].InputSchema, []byte(`"message"`)) {
		t.Fatalf("app body = %#v", appBody)
	}

	workerTagsResp, err := http.Get(server.URL + "/api/w/ws-a/worker-tags")
	if err != nil {
		t.Fatal(err)
	}
	defer workerTagsResp.Body.Close()
	if workerTagsResp.StatusCode != http.StatusOK {
		t.Fatalf("worker-tags status = %d, want %d", workerTagsResp.StatusCode, http.StatusOK)
	}
	var workerTags struct {
		Tags []struct {
			Tag string `json:"tag"`
		} `json:"tags"`
	}
	if err := json.NewDecoder(workerTagsResp.Body).Decode(&workerTags); err != nil {
		t.Fatal(err)
	}
	seenTags := map[string]bool{}
	for _, item := range workerTags.Tags {
		seenTags[item.Tag] = true
	}
	if !seenTags["default"] || !seenTags["browser"] {
		t.Fatalf("worker tags = %#v, want default and browser", workerTags.Tags)
	}

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
	if statusResp.StatusCode != http.StatusOK {
		t.Fatalf("status status = %d, want %d", statusResp.StatusCode, http.StatusOK)
	}
	var statusBody struct {
		InputSchema    json.RawMessage `json:"input_schema"`
		OutputSchema   json.RawMessage `json:"output_schema"`
		Input          json.RawMessage `json:"input"`
		CommitSha      string          `json:"commit_sha"`
		Entrypoint     string          `json:"entrypoint"`
		Tag            string          `json:"tag"`
		CreatedBy      string          `json:"created_by"`
		PermissionedAs string          `json:"permissioned_as"`
	}
	if err := json.NewDecoder(statusResp.Body).Decode(&statusBody); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(statusBody.InputSchema, []byte(`"message"`)) ||
		!bytes.Contains(statusBody.OutputSchema, []byte(`"ok"`)) ||
		!bytes.Contains(statusBody.Input, []byte(`"hello"`)) ||
		statusBody.CommitSha != syncBody.Commit ||
		statusBody.Entrypoint != "main.ts" ||
		statusBody.Tag != "browser" ||
		statusBody.CreatedBy != "system" ||
		statusBody.PermissionedAs != "system" {
		t.Fatalf("status body schemas/input = input_schema:%s output_schema:%s input:%s", statusBody.InputSchema, statusBody.OutputSchema, statusBody.Input)
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
	infoDescription := openAPIBody["info"].(map[string]any)["description"].(string)
	if !bytes.Contains([]byte(infoDescription), []byte(`status "failed"`)) ||
		!bytes.Contains([]byte(infoDescription), []byte("enqueue-time errors")) {
		t.Fatalf("openapi info description = %q", infoDescription)
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
	webhookDescription := webhook["description"].(string)
	if !bytes.Contains([]byte(webhookDescription), []byte("ctx.trigger.raw")) ||
		!bytes.Contains([]byte(webhookDescription), []byte("request headers are pinned")) {
		t.Fatalf("webhook description = %q", webhookDescription)
	}
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
		AppKey      string            `json:"app_key"`
		GitSourceID int64             `json:"git_source_id"`
		CommitSha   string            `json:"commit_sha"`
		Files       map[string]string `json:"files"`
		Skipped     []string          `json:"skipped"`
	}
	if err := json.NewDecoder(sourceResp.Body).Decode(&sourceBody); err != nil {
		t.Fatal(err)
	}
	if sourceBody.AppKey != "echo" || sourceBody.GitSourceID != registered.ID || sourceBody.CommitSha == "" ||
		!bytes.Contains([]byte(sourceBody.Files["windforce.json"]), []byte(`"app": "echo"`)) ||
		!bytes.Contains([]byte(sourceBody.Files["input.schema.json"]), []byte(`"message"`)) ||
		len(sourceBody.Skipped) != 1 || sourceBody.Skipped[0] != "logo.bin" {
		t.Fatalf("source body = %#v", sourceBody)
	}
	if _, ok := sourceBody.Files[".windforce_clone_complete"]; ok {
		t.Fatalf("source body leaked materialization marker: %#v", sourceBody)
	}

	historyResp, err := http.Get(server.URL + "/api/w/ws-a/apps/echo/history")
	if err != nil {
		t.Fatal(err)
	}
	defer historyResp.Body.Close()
	if historyResp.StatusCode != http.StatusOK {
		t.Fatalf("history status = %d, want %d", historyResp.StatusCode, http.StatusOK)
	}
	var history []map[string]any
	if err := json.NewDecoder(historyResp.Body).Decode(&history); err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0]["id"] == "" || history[0]["commit_sha"] != syncBody.Commit ||
		history[0]["source"] != "external_sync" {
		t.Fatalf("history = %#v", history)
	}
	if _, ok := history[0]["git_source_key"]; ok {
		t.Fatalf("history contains noncanonical git_source_key: %#v", history)
	}
	if _, ok := history[0]["status"]; ok {
		t.Fatalf("history = %#v", history)
	}

	deploymentResp, err := http.Get(server.URL + "/api/w/ws-a/deployments/echo")
	if err != nil {
		t.Fatal(err)
	}
	defer deploymentResp.Body.Close()
	if deploymentResp.StatusCode != http.StatusNotFound {
		t.Fatalf("deployment status = %d, want %d", deploymentResp.StatusCode, http.StatusNotFound)
	}
	var deploymentError map[string]string
	if err := json.NewDecoder(deploymentResp.Body).Decode(&deploymentError); err != nil {
		t.Fatal(err)
	}
	if deploymentError["error"] != "deployment not found" {
		t.Fatalf("deployment error = %#v", deploymentError)
	}

	if err := os.WriteFile(filepath.Join(repoDir, "windforce.json"), []byte(`{
		"app": "echo",
		"entrypoint": "main.v2.ts",
		"scriptLang": "typescript",
		"timeout": 240,
		"tag": "batch",
		"actions": {
			"echo": {
				"inputSchema": "input.schema.json",
				"outputSchema": "output.schema.json"
			}
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "input.schema.json"), []byte(`{"type":"object","properties":{"changed":{"type":"number"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repoDir, "add", "windforce.json", "input.schema.json")
	runTestGit(t, repoDir, "commit", "-m", "resync changes catalog")
	resyncResp, err := http.Post(server.URL+"/api/w/ws-a/git_sources/"+registeredID+"/sync", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resyncResp.Body.Close()
	if resyncResp.StatusCode != http.StatusOK {
		t.Fatalf("resync status = %d, want %d", resyncResp.StatusCode, http.StatusOK)
	}
	var resyncBody struct {
		Commit string `json:"commit"`
	}
	if err := json.NewDecoder(resyncResp.Body).Decode(&resyncBody); err != nil {
		t.Fatal(err)
	}
	if resyncBody.Commit == "" || resyncBody.Commit == syncBody.Commit {
		t.Fatalf("resync commit = %q, initial = %q", resyncBody.Commit, syncBody.Commit)
	}
	pinnedResp, err := http.Get(server.URL + "/api/w/ws-a/jobs/" + runBody.JobID)
	if err != nil {
		t.Fatal(err)
	}
	defer pinnedResp.Body.Close()
	if pinnedResp.StatusCode != http.StatusOK {
		t.Fatalf("pinned status = %d, want %d", pinnedResp.StatusCode, http.StatusOK)
	}
	var pinned struct {
		CommitSha   string          `json:"commit_sha"`
		Entrypoint  string          `json:"entrypoint"`
		InputSchema json.RawMessage `json:"input_schema"`
		Tag         string          `json:"tag"`
	}
	if err := json.NewDecoder(pinnedResp.Body).Decode(&pinned); err != nil {
		t.Fatal(err)
	}
	if pinned.CommitSha != syncBody.Commit ||
		pinned.Entrypoint != "main.ts" ||
		pinned.Tag != "browser" ||
		!bytes.Contains(pinned.InputSchema, []byte(`"message"`)) ||
		bytes.Contains(pinned.InputSchema, []byte(`"changed"`)) {
		t.Fatalf("queued job pins changed after resync: %#v input_schema=%s", pinned, pinned.InputSchema)
	}
}

func TestCanonicalGitSourceProbePatchAndDelete(t *testing.T) {
	tempDir := t.TempDir()
	repoDir := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "windforce.json"), []byte(`{"app":"echo","entrypoint":"main.ts","actions":{"echo":{}}}`), 0o644); err != nil {
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

	legacyProbeResp, err := http.Post(server.URL+"/api/w/ws-a/git_sources/probe", "application/json", bytes.NewBufferString(`{"repoUrl":"`+filepath.ToSlash(repoDir)+`","branch":"main"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer legacyProbeResp.Body.Close()
	if legacyProbeResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("legacy canonical probe status = %d, want %d", legacyProbeResp.StatusCode, http.StatusBadRequest)
	}

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
	var registered struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(registerResp.Body).Decode(&registered); err != nil {
		t.Fatal(err)
	}
	_ = registerResp.Body.Close()
	if registerResp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d, want %d", registerResp.StatusCode, http.StatusCreated)
	}
	if registered.ID <= 0 {
		t.Fatalf("registered = %#v", registered)
	}
	registeredID := fmt.Sprint(registered.ID)

	emptyPatchReq, err := http.NewRequest(http.MethodPatch, server.URL+"/api/w/ws-a/git_sources/"+registeredID, nil)
	if err != nil {
		t.Fatal(err)
	}
	emptyPatchResp, err := http.DefaultClient.Do(emptyPatchReq)
	if err != nil {
		t.Fatal(err)
	}
	defer emptyPatchResp.Body.Close()
	if emptyPatchResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty patch status = %d, want %d", emptyPatchResp.StatusCode, http.StatusBadRequest)
	}

	patchBody, err := json.Marshal(map[string]string{
		"name":      "source-b",
		"branch":    "feature",
		"creds_ref": "WINDFORCE_LITE_GIT_TOKEN",
	})
	if err != nil {
		t.Fatal(err)
	}
	patchReq, err := http.NewRequest(http.MethodPatch, server.URL+"/api/w/ws-a/git_sources/"+registeredID, bytes.NewReader(patchBody))
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
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		Branch   string `json:"branch"`
		CredsRef string `json:"creds_ref"`
	}
	if err := json.NewDecoder(patchResp.Body).Decode(&patched); err != nil {
		t.Fatal(err)
	}
	if patched.ID != registered.ID || patched.Name != "source-b" || patched.Branch != "feature" || patched.CredsRef != "WINDFORCE_LITE_GIT_TOKEN" {
		t.Fatalf("patched = %#v", patched)
	}
	if _, err := registry.Get(context.Background(), "ws-a", "source-a"); !errors.Is(err, gitsource.ErrGitSourceNotFound) {
		t.Fatalf("old source lookup err = %v, want not found", err)
	}

	deleteReq, err := http.NewRequest(http.MethodDelete, server.URL+"/api/w/ws-a/git_sources/"+registeredID, nil)
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
	deleteAgainReq, err := http.NewRequest(http.MethodDelete, server.URL+"/api/w/ws-a/git_sources/"+registeredID, nil)
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

	getNestedActionRouteTag := func() string {
		t.Helper()
		resp, err := http.Get(server.URL + "/api/w/ws-a/apps/echo")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body struct {
			Actions []struct {
				ActionKey         string `json:"action_key"`
				EffectiveRouteTag string `json:"effective_route_tag"`
			} `json:"actions"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		for _, action := range body.Actions {
			if action.ActionKey == "echo" {
				return action.EffectiveRouteTag
			}
		}
		t.Fatalf("echo action missing from app body: %#v", body.Actions)
		return ""
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
	defer patchAppResp.Body.Close()
	if patchAppResp.StatusCode != http.StatusOK {
		t.Fatalf("patch app status = %d, want %d", patchAppResp.StatusCode, http.StatusOK)
	}
	var patchedApp map[string]json.RawMessage
	if err := json.NewDecoder(patchAppResp.Body).Decode(&patchedApp); err != nil {
		t.Fatal(err)
	}
	var patchedAppTag string
	if err := json.Unmarshal(patchedApp["tag_override"], &patchedAppTag); err != nil {
		t.Fatal(err)
	}
	if patchedAppTag != "app-blue" || patchedApp["effective_route_tag"] != nil {
		t.Fatalf("patched app = %#v", patchedApp)
	}

	actionResp, err := http.Get(server.URL + "/api/w/ws-a/apps/echo/actions/echo")
	if err != nil {
		t.Fatal(err)
	}
	defer actionResp.Body.Close()
	var inheritedAction map[string]json.RawMessage
	if err := json.NewDecoder(actionResp.Body).Decode(&inheritedAction); err != nil {
		t.Fatal(err)
	}
	if inheritedAction["effective_route_tag"] != nil || getNestedActionRouteTag() != "app-blue" {
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
	var patchedAction map[string]json.RawMessage
	if err := json.NewDecoder(patchActionResp.Body).Decode(&patchedAction); err != nil {
		t.Fatal(err)
	}
	var patchedActionTag string
	if err := json.Unmarshal(patchedAction["tag_override"], &patchedActionTag); err != nil {
		t.Fatal(err)
	}
	if patchedActionTag != "action-fast" || patchedAction["effective_route_tag"] != nil || getNestedActionRouteTag() != "action-fast" {
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
	var clearedAction map[string]json.RawMessage
	if err := json.NewDecoder(clearActionResp.Body).Decode(&clearedAction); err != nil {
		t.Fatal(err)
	}
	if clearedAction["tag_override"] != nil || clearedAction["effective_route_tag"] != nil || getNestedActionRouteTag() != "app-blue" {
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
		Store:           state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		Catalog:         fileCatalog,
		EnableTrigger:   true,
		TriggerAdapters: []TriggerAdapter{fakeTriggerAdapter{}},
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
		Store:           state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		Catalog:         fileCatalog,
		EnableTrigger:   true,
		TriggerAdapters: []TriggerAdapter{fakeTriggerAdapter{}},
		TriggerToken:    "secret-token",
	}))
	defer server.Close()

	resp, err := http.Post(server.URL+"/external/v1/echo/echo", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}

	req, err := http.NewRequest(http.MethodPost, server.URL+"/external/v1/echo/echo", bytes.NewBufferString(`{}`))
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
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authorized status = %d, want %d", resp.StatusCode, http.StatusOK)
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
		"entrypoint": "main.ts",
		"scriptLang": "typescript",
		"actions": {
			"echo": {}
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
		"name":     "source-a",
		"repo_url": filepath.ToSlash(repoDir),
		"branch":   "main",
		"subpath":  "apps/echo",
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

	syncResp, err := http.Post(server.URL+"/api/w/ws-a/git_sources/source-a/sync", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer syncResp.Body.Close()
	if syncResp.StatusCode != http.StatusOK {
		t.Fatalf("sync status = %d, want %d", syncResp.StatusCode, http.StatusOK)
	}
	var syncBody struct {
		App     string   `json:"app"`
		Commit  string   `json:"commit"`
		Actions []string `json:"actions"`
	}
	if err := json.NewDecoder(syncResp.Body).Decode(&syncBody); err != nil {
		t.Fatal(err)
	}
	if syncBody.App != "echo" || syncBody.Commit == "" || len(syncBody.Actions) != 1 || syncBody.Actions[0] != "echo.echo" {
		t.Fatalf("sync body = %#v", syncBody)
	}
	deployment, err := fileCatalog.GetDeploymentForWorkspace(context.Background(), "ws-a", "echo")
	if err != nil {
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
