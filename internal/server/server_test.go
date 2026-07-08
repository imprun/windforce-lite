package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/imprun/windforce-lite/internal/bundle"
	"github.com/imprun/windforce-lite/internal/catalog"
	"github.com/imprun/windforce-lite/internal/contract"
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
