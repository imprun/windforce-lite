package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/imprun/windforce-core/internal/catalog"
	"github.com/imprun/windforce-core/internal/contract"
	"github.com/imprun/windforce-core/internal/state"
)

type inputConfigTestCatalog struct {
	deployment contract.Deployment
}

func (c inputConfigTestCatalog) GetDeployment(_ context.Context, app string) (contract.Deployment, error) {
	if app != c.deployment.App {
		return contract.Deployment{}, catalog.ErrDeploymentNotFound
	}
	return c.deployment, nil
}

func TestCanonicalInputConfigLifecycleAndExecutionAdmission(t *testing.T) {
	store := state.NewLocalStore(filepath.Join(t.TempDir(), "state.json"))
	deployment := contract.Deployment{
		Workspace: "ws-a", App: "shop", Commit: "abc123", Entrypoint: "main.py",
		BundleDigest: testExecutionBundleDigest,
		Actions:      map[string]contract.Action{"orders": {}},
	}
	server := httptest.NewServer(New(Config{
		Store: store, Catalog: inputConfigTestCatalog{deployment: deployment},
		EnableAPI: true, EnableExecutionAPI: true,
	}))
	defer server.Close()

	do := func(method string, path string, body string, wantStatus int, target any) []byte {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("X-Windforce-Actor", "operator@example.test")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var payload bytes.Buffer
		_, _ = payload.ReadFrom(resp.Body)
		if resp.StatusCode != wantStatus {
			t.Fatalf("%s %s status=%d want=%d: %s", method, path, resp.StatusCode, wantStatus, payload.String())
		}
		if target != nil && payload.Len() > 0 {
			if err := json.Unmarshal(payload.Bytes(), target); err != nil {
				t.Fatalf("decode response: %v: %s", err, payload.String())
			}
		}
		return payload.Bytes()
	}

	var created struct {
		Client   clientView `json:"client"`
		APIToken string     `json:"api_token"`
	}
	do(http.MethodPost, "/api/w/ws-a/clients", `{"name":"Client A"}`, http.StatusCreated, &created)
	client := created.Client
	do(http.MethodPut, "/api/w/ws-a/apps/shop/input-configs", `{"config":{"region":"kr"},"locked_keys":[]}`, http.StatusOK, nil)
	do(http.MethodPut, "/api/w/ws-a/apps/shop/input-configs", `{"config":{"_SCRAPING_RUNTIME":{"authSession":{"serviceUrl":"http://example.invalid"}}},"locked_keys":[]}`, http.StatusBadRequest, nil)
	do(http.MethodPut, "/api/w/ws-a/apps/shop/input-configs", `{"action_key":"orders","client_id":"`+client.ID+`","config":{"tenant":"server-only"},"locked_keys":["tenant"]}`, http.StatusOK, nil)
	do(http.MethodPut, "/api/w/ws-a/apps/shop/input-configs", `{"config":{},"locked_keys":["missing"]}`, http.StatusBadRequest, nil)

	var appConfigs []state.InputConfig
	do(http.MethodGet, "/api/w/ws-a/apps/shop/input-configs", "", http.StatusOK, &appConfigs)
	if len(appConfigs) != 2 {
		t.Fatalf("app configs = %#v", appConfigs)
	}
	var clientConfigs []state.InputConfig
	do(http.MethodGet, "/api/w/ws-a/clients/"+client.ID+"/input-configs", "", http.StatusOK, &clientConfigs)
	if len(clientConfigs) != 1 || clientConfigs[0].ActionKey != "orders" {
		t.Fatalf("client configs = %#v", clientConfigs)
	}

	do(http.MethodPost, "/execution/v1/workspaces/ws-a/runs", `{"app":"shop","action":"orders","client_id":"`+client.ID+`","input":{"tenant":"spoofed"}}`, http.StatusBadRequest, nil)
	var admitted executionRunView
	do(http.MethodPost, "/execution/v1/workspaces/ws-a/runs", `{"app":"shop","action":"orders","client_id":"`+client.ID+`","input":{"message":"hello"}}`, http.StatusCreated, &admitted)
	job, run, _, err := store.GetJob(context.Background(), "ws-a", admitted.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if run.ClientID != client.ID || job.Payload.ClientID != client.ID {
		t.Fatalf("client identity run=%q job=%q want=%q", run.ClientID, job.Payload.ClientID, client.ID)
	}

	var audits []state.InputConfigAudit
	do(http.MethodGet, "/api/w/ws-a/clients/"+client.ID+"/input-config-audit", "", http.StatusOK, &audits)
	if len(audits) != 1 || audits[0].Actor != "operator@example.test" || bytes.Contains(mustJSON(t, audits), []byte("server-only")) {
		t.Fatalf("audit = %#v", audits)
	}

	do(http.MethodDelete, "/api/w/ws-a/apps/shop/input-configs?action_key=orders&client_id="+client.ID, "", http.StatusNoContent, nil)
	do(http.MethodGet, "/api/w/ws-a/clients/"+client.ID+"/input-configs", "", http.StatusOK, &clientConfigs)
	if len(clientConfigs) != 0 {
		t.Fatalf("client configs after delete = %#v", clientConfigs)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
