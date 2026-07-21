package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/imprun/windforce-core/internal/contract"
	actionruntime "github.com/imprun/windforce-core/internal/runtime"
	"github.com/imprun/windforce-core/internal/state"
	"github.com/imprun/windforce-core/internal/worker"
)

func TestPublicAPIClientAuthenticationRotationInputAndArchive(t *testing.T) {
	ctx := context.Background()
	store := state.NewLocalStore(filepath.Join(t.TempDir(), "state.json"))
	if _, err := store.CreateWorkspace(ctx, "ws-a", "Workspace A", "", "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateWorkspace(ctx, "ws-b", "Workspace B", "", "admin"); err != nil {
		t.Fatal(err)
	}
	firstToken, err := newClientToken()
	if err != nil {
		t.Fatal(err)
	}
	client, err := store.CreateClient(ctx, "ws-a", "Client A", state.HashClientToken(firstToken), "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetInputConfig(ctx, state.InputConfig{
		WorkspaceID: "ws-a", AppKey: "shop", Config: json.RawMessage(`{"region":"kr"}`),
	}, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetInputConfig(ctx, state.InputConfig{
		WorkspaceID: "ws-a", AppKey: "shop", ActionKey: "orders", ClientID: client.ID,
		Config: json.RawMessage(`{"tenant":"fixed"}`), LockedKeys: []string{"tenant"},
	}, "admin"); err != nil {
		t.Fatal(err)
	}
	deployment := contract.Deployment{
		Workspace: "ws-a", App: "shop", Commit: "commit-a", BundleDigest: testExecutionBundleDigest,
		Actions: map[string]contract.Action{"orders": {
			Action: "orders",
			InputSchemaBody: json.RawMessage(`{
				"type":"object",
				"required":["message","region","tenant"],
				"properties":{"message":{"type":"string"},"region":{"type":"string"},"tenant":{"type":"string"}},
				"additionalProperties":false
			}`),
		}},
	}
	httpServer := httptest.NewServer(New(Config{
		Store: store, Catalog: inputConfigTestCatalog{deployment: deployment}, EnablePublicAPI: true,
		EnableControlAPI: true, ManagedWorkspaces: true, AdminToken: "admin", PublicAPIRPS: 1000, PublicAPIBurst: 1000,
	}))
	defer httpServer.Close()

	do := func(path string, token string, body string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, httpServer.URL+path, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return resp, data
	}

	path := "/api/v1/w/ws-a/run/shop/orders"
	resp, body := do(path, "wfk_invalid", `{"message":"hello"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid token status=%d body=%s", resp.StatusCode, body)
	}
	resp, body = do("/api/v1/w/ws-b/run/shop/orders", firstToken, `{"message":"other workspace"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("cross-workspace token status=%d body=%s", resp.StatusCode, body)
	}
	resp, body = do(path, firstToken, `{"tenant":"spoofed"}`)
	if resp.StatusCode != http.StatusBadRequest || !bytes.Contains(body, []byte("locked input keys")) {
		t.Fatalf("locked input status=%d body=%s", resp.StatusCode, body)
	}
	resp, body = do(path, firstToken, `{"message":42}`)
	if resp.StatusCode != http.StatusBadRequest || !bytes.Contains(body, []byte("input does not match action schema")) {
		t.Fatalf("schema validation status=%d body=%s", resp.StatusCode, body)
	}
	rejectedJobs, err := store.ListJobs(ctx, state.JobListQuery{WorkspaceID: "ws-a", Limit: 100})
	if err != nil || len(rejectedJobs) != 0 {
		t.Fatalf("rejected inputs admitted jobs=%#v err=%v", rejectedJobs, err)
	}
	resp, body = do(path, firstToken, `{"message":"hello"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("valid trigger status=%d body=%s", resp.StatusCode, body)
	}
	var admitted struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(body, &admitted); err != nil {
		t.Fatal(err)
	}
	if admitted.JobID == "" || resp.Header.Get(publicJobIDHeader) != admitted.JobID {
		t.Fatalf("job identity body=%q header=%q", admitted.JobID, resp.Header.Get(publicJobIDHeader))
	}
	job, run, found, err := store.GetJob(ctx, "ws-a", admitted.JobID)
	if err != nil || !found || run.ClientID != client.ID || job.Payload.ClientID != client.ID || job.Payload.TriggerKind != "http" || !job.Payload.InputConfigResolved {
		t.Fatalf("public job=%#v run=%#v found=%v err=%v", job, run, found, err)
	}
	if jsonStringField(job.Payload.Input, "region") != "kr" || jsonStringField(job.Payload.Input, "tenant") != "fixed" || !equalJSON(job.Payload.Input, run.Input) {
		t.Fatalf("pinned input job=%s run=%s", job.Payload.Input, run.Input)
	}

	controlReq, err := http.NewRequest(http.MethodGet, httpServer.URL+"/api/w/ws-a/clients", nil)
	if err != nil {
		t.Fatal(err)
	}
	controlReq.Header.Set("Authorization", "Bearer "+firstToken)
	controlResp, err := http.DefaultClient.Do(controlReq)
	if err != nil {
		t.Fatal(err)
	}
	controlResp.Body.Close()
	if controlResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("client token control status=%d", controlResp.StatusCode)
	}

	secondToken, err := newClientToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RotateClientToken(ctx, "ws-a", client.ID, state.HashClientToken(secondToken), "admin"); err != nil {
		t.Fatal(err)
	}
	resp, _ = do(path, firstToken, `{"message":"old"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("rotated token status=%d", resp.StatusCode)
	}
	resp, body = do(path, secondToken, `{"message":"new"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("new token status=%d body=%s", resp.StatusCode, body)
	}
	audit, err := store.ListClientAudit(ctx, "ws-a", "")
	if err != nil {
		t.Fatal(err)
	}
	if !hasClientAudit(audit, "trigger_rejected", "locked input keys") || !hasClientAudit(audit, "trigger_rejected", "input does not match action schema") || !hasClientAudit(audit, "trigger_admitted", admitted.JobID) {
		t.Fatalf("trigger audit=%#v", audit)
	}
	failures := 0
	for _, record := range audit {
		if strings.Contains(record.Detail, `"message":42`) {
			t.Fatal("client audit exposed rejected input")
		}
		if record.Kind == "trigger_auth_failed" {
			failures++
			if strings.Contains(record.Detail, firstToken) {
				t.Fatal("client audit exposed a raw token")
			}
		}
	}
	if failures != 2 {
		t.Fatalf("workspace A auth failure audits=%d want=2: %#v", failures, audit)
	}
	otherAudit, err := store.ListClientAudit(ctx, "ws-b", "")
	if err != nil || len(otherAudit) != 1 || otherAudit[0].Kind != "trigger_auth_failed" {
		t.Fatalf("workspace B auth audit=%#v err=%v", otherAudit, err)
	}
	if _, err := store.ArchiveWorkspace(ctx, "ws-a", "admin"); err != nil {
		t.Fatal(err)
	}
	resp, body = do(path, secondToken, `{"message":"archived"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("archived status=%d body=%s", resp.StatusCode, body)
	}
	if _, err := store.RevokeClientToken(ctx, "ws-a", client.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	resp, body = do(path, secondToken, `{"message":"revoked"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked token status=%d body=%s", resp.StatusCode, body)
	}
	audit, err = store.ListClientAudit(ctx, "ws-a", "")
	if err != nil {
		t.Fatal(err)
	}
	failures = 0
	for _, record := range audit {
		if record.Kind == "trigger_auth_failed" {
			failures++
		}
	}
	if failures != 3 {
		t.Fatalf("auth failure audits after revoke=%d want=3: %#v", failures, audit)
	}
}

func TestPublicAPIWaitRunsWorkerWithPinnedInputAndReplaysJob(t *testing.T) {
	ctx := context.Background()
	store := state.NewLocalStore(filepath.Join(t.TempDir(), "state.json"))
	value, err := newClientToken()
	if err != nil {
		t.Fatal(err)
	}
	client, err := store.CreateClient(ctx, "default", "Client A", state.HashClientToken(value), "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetInputConfig(ctx, state.InputConfig{
		WorkspaceID: "default", AppKey: "echo", ActionKey: "run", ClientID: client.ID,
		Config: json.RawMessage(`{"tenant":"fixed"}`), LockedKeys: []string{"tenant"},
	}, "admin"); err != nil {
		t.Fatal(err)
	}
	deployment := contract.Deployment{
		Workspace: "default", App: "echo", Commit: "commit-a", BundleDigest: testExecutionBundleDigest,
		Actions: map[string]contract.Action{"run": {
			Action:          "run",
			InputSchemaBody: json.RawMessage(`{"type":"object","required":["message","tenant"],"properties":{"message":{"type":"string"},"tenant":{"type":"string"}},"additionalProperties":false}`),
		}},
	}
	httpServer := httptest.NewServer(New(Config{
		Store: store, Catalog: inputConfigTestCatalog{deployment: deployment}, EnablePublicAPI: true,
		PublicAPIRPS: 1000, PublicAPIBurst: 1000,
	}))
	defer httpServer.Close()

	do := func(path string, idempotencyKey string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, httpServer.URL+path, bytes.NewBufferString(`{"message":"hello"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+value)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", idempotencyKey)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return resp, body
	}

	resp, body := do("/api/v1/w/default/run/echo/run/wait?timeout=invalid", "invalid-timeout")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid timeout status=%d body=%s", resp.StatusCode, body)
	}
	jobs, err := store.ListJobs(ctx, state.JobListQuery{WorkspaceID: "default", Limit: 100})
	if err != nil || len(jobs) != 0 {
		t.Fatalf("invalid timeout admitted jobs=%#v err=%v", jobs, err)
	}

	resp, body = do("/api/v1/w/default/run/echo/run", "replay-a")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("async status=%d body=%s", resp.StatusCode, body)
	}
	var admitted struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(body, &admitted); err != nil || admitted.JobID == "" {
		t.Fatalf("admission body=%s err=%v", body, err)
	}
	if _, err := store.SetInputConfig(ctx, state.InputConfig{
		WorkspaceID: "default", AppKey: "echo", ActionKey: "run", ClientID: client.ID,
		Config: json.RawMessage(`{"tenant":"changed"}`), LockedKeys: []string{"tenant"},
	}, "admin"); err != nil {
		t.Fatal(err)
	}
	runner := &publicAPIRecordingRunner{inputs: make(chan json.RawMessage, 1)}
	processed, err := (&worker.Processor{Store: store, Runner: runner, WorkerID: "worker-a"}).ProcessOne(ctx)
	if err != nil || !processed {
		t.Fatalf("worker processed=%v err=%v", processed, err)
	}
	workerInput := <-runner.inputs
	if jsonStringField(workerInput, "tenant") != "fixed" {
		t.Fatalf("worker input was not pinned at admission: %s", workerInput)
	}

	resp, body = do("/api/v1/w/default/run/echo/run/wait?timeout=1s", "replay-a")
	if resp.StatusCode != http.StatusOK || resp.Header.Get(publicJobIDHeader) != admitted.JobID || !equalJSON(body, workerInput) {
		t.Fatalf("replay wait status=%d header=%q want=%q body=%s input=%s", resp.StatusCode, resp.Header.Get(publicJobIDHeader), admitted.JobID, body, workerInput)
	}
	jobs, err = store.ListJobs(ctx, state.JobListQuery{WorkspaceID: "default", Limit: 100})
	if err != nil || len(jobs) != 1 || jobs[0].ID != admitted.JobID {
		t.Fatalf("idempotent jobs=%#v err=%v", jobs, err)
	}
	audit, err := store.ListClientAudit(ctx, "default", client.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !hasClientAudit(audit, "trigger_rejected", `"reason":"invalid wait timeout"`) || !hasClientAudit(audit, "trigger_admitted", `"replayed":true`) {
		t.Fatalf("public trigger audit=%#v", audit)
	}
}

type publicAPIRecordingRunner struct {
	inputs chan json.RawMessage
}

func (r *publicAPIRecordingRunner) Run(_ context.Context, request actionruntime.RunRequest) (contract.JobResult, error) {
	input := append(json.RawMessage(nil), request.Input...)
	r.inputs <- input
	return contract.JobResult{Output: input, ExitCode: 0}, nil
}

func hasClientAudit(records []state.ClientAudit, kind string, detail string) bool {
	for _, record := range records {
		if record.Kind == kind && strings.Contains(record.Detail, detail) {
			return true
		}
	}
	return false
}

func equalJSON(left []byte, right []byte) bool {
	var leftValue any
	var rightValue any
	return json.Unmarshal(left, &leftValue) == nil && json.Unmarshal(right, &rightValue) == nil && reflect.DeepEqual(leftValue, rightValue)
}

func jsonStringField(data []byte, key string) string {
	var value map[string]any
	if json.Unmarshal(data, &value) != nil {
		return ""
	}
	result, _ := value[key].(string)
	return result
}

func TestPublicAPIRateLimitRunsBeforeAuthentication(t *testing.T) {
	store := state.NewLocalStore(filepath.Join(t.TempDir(), "state.json"))
	httpServer := httptest.NewServer(New(Config{Store: store, EnablePublicAPI: true, PublicAPIRPS: 0.01, PublicAPIBurst: 1}))
	defer httpServer.Close()

	request := func() int {
		req, err := http.NewRequest(http.MethodPost, httpServer.URL+"/api/v1/w/default/run/echo/run", bytes.NewBufferString(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if status := request(); status != http.StatusUnauthorized {
		t.Fatalf("first status=%d", status)
	}
	if status := request(); status != http.StatusTooManyRequests {
		t.Fatalf("second status=%d", status)
	}
}
