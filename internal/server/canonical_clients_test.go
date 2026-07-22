package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imprun/windforce-core/internal/contract"
	"github.com/imprun/windforce-core/internal/state"
)

func TestCanonicalClientLifecycle(t *testing.T) {
	server := httptest.NewServer(New(Config{
		Store: state.NewLocalStore(filepath.Join(t.TempDir(), "state.json")),
	}))
	defer server.Close()

	do := func(method string, path string, actor string, body string, wantStatus int, target any) []byte {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if actor != "" {
			req.Header.Set("X-Windforce-Actor", actor)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var payload bytes.Buffer
		if _, err := payload.ReadFrom(resp.Body); err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != wantStatus {
			t.Fatalf("%s %s status = %d, want %d: %s", method, path, resp.StatusCode, wantStatus, payload.String())
		}
		if target != nil && payload.Len() > 0 {
			if err := json.Unmarshal(payload.Bytes(), target); err != nil {
				t.Fatalf("decode %s %s: %v: %s", method, path, err, payload.String())
			}
		}
		return payload.Bytes()
	}

	var issued struct {
		Client   clientView `json:"client"`
		APIToken string     `json:"api_token"`
	}
	createdBody := do(http.MethodPost, "/api/w/ws-a/clients", "alice@example.test", `{"name":"Acme Operations"}`, http.StatusCreated, &issued)
	created := issued.Client
	if created.ID == "" || created.WorkspaceID != "ws-a" || created.Name != "Acme Operations" || !created.HasToken || !strings.HasPrefix(issued.APIToken, contract.ClientTokenPrefix) {
		t.Fatalf("created = %#v", created)
	}
	if created.CreatedBy != "alice@example.test" || created.UpdatedBy != "alice@example.test" {
		t.Fatalf("created actors = %#v", created)
	}
	if bytes.Contains(createdBody, []byte("token_hash")) || bytes.Contains(createdBody, []byte("external_key")) {
		t.Fatalf("created response exposes stored credential data: %s", createdBody)
	}

	do(http.MethodPost, "/api/w/ws-a/clients", "alice@example.test", `{"name":"   "}`, http.StatusBadRequest, nil)

	var clients []clientView
	do(http.MethodGet, "/api/w/ws-a/clients", "", "", http.StatusOK, &clients)
	if len(clients) != 1 || clients[0].ID != created.ID {
		t.Fatalf("clients = %#v", clients)
	}

	var updated clientView
	clientPath := "/api/w/ws-a/clients/" + created.ID
	do(http.MethodPatch, clientPath, "bob@example.test", `{"name":"Acme Korea"}`, http.StatusOK, &updated)
	if updated.ID != created.ID || updated.Name != "Acme Korea" || !updated.HasToken || updated.UpdatedBy != "bob@example.test" {
		t.Fatalf("updated = %#v", updated)
	}
	var rotated struct {
		Client   clientView `json:"client"`
		APIToken string     `json:"api_token"`
	}
	do(http.MethodPost, clientPath+"/token", "bob@example.test", "", http.StatusOK, &rotated)
	if rotated.APIToken == issued.APIToken || !strings.HasPrefix(rotated.APIToken, contract.ClientTokenPrefix) {
		t.Fatalf("rotated token was not replaced")
	}

	var audit []state.ClientAudit
	auditBody := do(http.MethodGet, clientPath+"/audit", "", "", http.StatusOK, &audit)
	if len(audit) != 3 || audit[0].Kind != "token_rotated" || audit[1].Kind != "updated" || audit[2].Kind != "created" {
		t.Fatalf("audit = %#v", audit)
	}
	if bytes.Contains(auditBody, []byte(issued.APIToken)) || bytes.Contains(auditBody, []byte(rotated.APIToken)) {
		t.Fatalf("audit exposes client key: %s", auditBody)
	}

	do(http.MethodDelete, clientPath, "carol@example.test", "", http.StatusConflict, nil)
	do(http.MethodDelete, clientPath+"/token", "carol@example.test", "", http.StatusOK, &updated)
	if updated.HasToken {
		t.Fatalf("revoked client still has a token: %#v", updated)
	}
	do(http.MethodDelete, clientPath, "carol@example.test", "", http.StatusNoContent, nil)
	do(http.MethodGet, clientPath, "", "", http.StatusNotFound, nil)
	auditBody = do(http.MethodGet, clientPath+"/audit", "", "", http.StatusOK, &audit)
	if len(audit) != 5 || audit[0].Kind != "deleted" || audit[1].Kind != "token_revoked" || audit[0].Actor != "carol@example.test" {
		t.Fatalf("audit after delete = %#v", audit)
	}
	if bytes.Contains(auditBody, []byte(issued.APIToken)) || bytes.Contains(auditBody, []byte(rotated.APIToken)) {
		t.Fatalf("audit exposes deleted client key: %s", auditBody)
	}
}

func TestControlPlaneOpenAPIIncludesClients(t *testing.T) {
	schemas := controlPlaneSchemas()
	for _, name := range []string{"Client", "ClientTokenResult", "CreateClientRequest", "UpdateClientRequest", "ClientAudit", "AuditChanges", "AuditEvent", "InputConfig", "SetInputConfigRequest", "InputConfigAudit", "ProvisioningResource", "ProvisioningImportRequest", "ProvisioningResult"} {
		if schemas[name] == nil {
			t.Fatalf("missing schema %s", name)
		}
	}
	paths := buildControlPlaneOpenAPI("http://example.test", "default")["paths"].(map[string]any)
	for _, path := range []string{
		"/api/w/{workspace}/audit-events",
		"/api/w/{workspace}/provisioning/import",
		"/api/w/{workspace}/provisioning/export",
		"/api/w/{workspace}/clients",
		"/api/w/{workspace}/clients/{client_id}",
		"/api/w/{workspace}/clients/{client_id}/token",
		"/api/w/{workspace}/clients/{client_id}/audit",
		"/api/w/{workspace}/clients/{client_id}/input-configs",
		"/api/w/{workspace}/clients/{client_id}/input-config-audit",
		"/api/w/{workspace}/apps/{app}/input-configs",
		"/api/w/{workspace}/apps/{app}/input-config-audit",
	} {
		if paths[path] == nil {
			t.Fatalf("missing path %s", path)
		}
	}
}
