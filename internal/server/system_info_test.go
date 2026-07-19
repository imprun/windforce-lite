package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/imprun/windforce-core/internal/state"
)

func TestSystemInfoExposesSafeServiceConfiguration(t *testing.T) {
	store := state.NewLocalStore(filepath.Join(t.TempDir(), "state.json"))
	server := httptest.NewServer(New(Config{
		Store:              store,
		EnableControlAPI:   true,
		EnableExecutionAPI: true,
		EnableWebUI:        true,
		AdminToken:         "secret-admin-token",
		WorkerToken:        "secret-worker-token",
		JobTokenSecret:     "secret-job-token",
		SecretKey:          "secret-key-value",
		Wait:               250 * time.Millisecond,
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/api/w/ws-a/system/info", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret-admin-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Service       string          `json:"service"`
		Workspace     string          `json:"workspace"`
		Ready         bool            `json:"ready"`
		Planes        map[string]bool `json:"planes"`
		Backends      map[string]bool `json:"backends"`
		Auth          map[string]bool `json:"auth"`
		RuntimeConfig struct {
			WaitMS float64 `json:"wait_ms"`
		} `json:"runtime_config"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Service != "windforce-lite" || body.Workspace != "ws-a" || !body.Ready {
		t.Fatalf("body identity = %#v", body)
	}
	if !body.Planes["control_api"] || !body.Planes["execution_api"] || !body.Planes["web_ui"] {
		t.Fatalf("planes = %#v", body.Planes)
	}
	if !body.Backends["state_store"] {
		t.Fatalf("backends = %#v", body.Backends)
	}
	if !body.Auth["admin_token_configured"] || !body.Auth["worker_token_configured"] || !body.Auth["job_token_configured"] {
		t.Fatalf("auth = %#v", body.Auth)
	}
	if body.RuntimeConfig.WaitMS != 250 {
		t.Fatalf("wait_ms = %#v", body.RuntimeConfig.WaitMS)
	}
}
