package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/imprun/windforce-core/internal/contract"
	"github.com/imprun/windforce-core/internal/state"
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
		Actions: map[string]contract.Action{"orders": {Action: "orders"}},
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
	if err != nil || !found || run.ClientID != client.ID || job.Payload.ClientID != client.ID || job.Payload.TriggerKind != "http" {
		t.Fatalf("public job=%#v run=%#v found=%v err=%v", job, run, found, err)
	}
	effective, err := store.ResolveInput(ctx, "ws-a", "shop", "orders", client.ID, json.RawMessage(`{"message":"hello"}`))
	if err != nil || !bytes.Contains(effective, []byte(`"region":"kr"`)) || !bytes.Contains(effective, []byte(`"tenant":"fixed"`)) {
		t.Fatalf("effective input=%s err=%v", effective, err)
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
	failures := 0
	for _, record := range audit {
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

func TestPublicAPIWaitReturnsRawResult(t *testing.T) {
	ctx := context.Background()
	store := state.NewLocalStore(filepath.Join(t.TempDir(), "state.json"))
	value, err := newClientToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateClient(ctx, "default", "Client A", state.HashClientToken(value), "admin"); err != nil {
		t.Fatal(err)
	}
	deployment := contract.Deployment{
		Workspace: "default", App: "echo", Commit: "commit-a", BundleDigest: testExecutionBundleDigest,
		Actions: map[string]contract.Action{"run": {Action: "run"}},
	}
	httpServer := httptest.NewServer(New(Config{
		Store: store, Catalog: inputConfigTestCatalog{deployment: deployment}, EnablePublicAPI: true,
		PublicAPIRPS: 1000, PublicAPIBurst: 1000,
	}))
	defer httpServer.Close()

	done := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			job, lease, err := store.ClaimJob(ctx, "worker-a", time.Second)
			if errors.Is(err, state.ErrNoQueuedJob) {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			if err != nil {
				done <- err
				return
			}
			done <- store.CompleteJobSucceeded(ctx, lease, contract.JobResult{
				JobID: job.ID, App: job.Payload.App, Action: job.Payload.Action,
				Output: json.RawMessage(`{"echo":"hello"}`), ExitCode: 0,
			})
			return
		}
		done <- errors.New("timed out waiting for public job")
	}()

	req, err := http.NewRequest(http.MethodPost, httpServer.URL+"/api/v1/w/default/run/echo/run/wait?timeout=2s", bytes.NewBufferString(`{"message":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+value)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if workerErr := <-done; workerErr != nil {
		t.Fatal(workerErr)
	}
	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || result["echo"] != "hello" || resp.Header.Get(publicJobIDHeader) == "" {
		t.Fatalf("wait status=%d header=%q body=%s", resp.StatusCode, resp.Header.Get(publicJobIDHeader), body)
	}
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
