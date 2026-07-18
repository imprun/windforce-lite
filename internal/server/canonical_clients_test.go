package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imprun/windforce-core/internal/state"
)

func TestCanonicalClientLifecycle(t *testing.T) {
	server := httptest.NewServer(New(Config{
		Store:     state.NewLocalStore(filepath.Join(t.TempDir(), "state.json")),
		EnableAPI: true,
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

	var created state.Client
	createdBody := do(http.MethodPost, "/api/w/ws-a/clients", "alice@example.test", `{"name":"Acme Operations","external_key":"client-key-a"}`, http.StatusCreated, &created)
	if created.ID == "" || created.WorkspaceID != "ws-a" || created.Name != "Acme Operations" || created.ExternalKey != "client-key-a" {
		t.Fatalf("created = %#v", created)
	}
	if created.CreatedBy != "alice@example.test" || created.UpdatedBy != "alice@example.test" {
		t.Fatalf("created actors = %#v", created)
	}
	if bytes.Contains(createdBody, []byte("client_key")) || !bytes.Contains(createdBody, []byte("external_key")) {
		t.Fatalf("created response uses legacy field: %s", createdBody)
	}

	do(http.MethodPost, "/api/w/ws-a/clients", "alice@example.test", `{"name":"Duplicate","external_key":"client-key-a"}`, http.StatusConflict, nil)
	do(http.MethodPost, "/api/w/ws-b/clients", "alice@example.test", `{"name":"Other workspace","external_key":"client-key-a"}`, http.StatusCreated, nil)
	do(http.MethodPost, "/api/w/ws-a/clients", "alice@example.test", `{"name":"Whitespace","external_key":"bad key"}`, http.StatusBadRequest, nil)

	var clients []state.Client
	do(http.MethodGet, "/api/w/ws-a/clients", "", "", http.StatusOK, &clients)
	if len(clients) != 1 || clients[0].ID != created.ID {
		t.Fatalf("clients = %#v", clients)
	}

	var updated state.Client
	clientPath := "/api/w/ws-a/clients/" + created.ID
	do(http.MethodPatch, clientPath, "bob@example.test", `{"name":"Acme Korea","external_key":"client-key-b"}`, http.StatusOK, &updated)
	if updated.ID != created.ID || updated.Name != "Acme Korea" || updated.ExternalKey != "client-key-b" || updated.UpdatedBy != "bob@example.test" {
		t.Fatalf("updated = %#v", updated)
	}

	var audit []state.ClientAudit
	auditBody := do(http.MethodGet, clientPath+"/audit", "", "", http.StatusOK, &audit)
	if len(audit) != 2 || audit[0].Kind != "updated" || audit[0].Actor != "bob@example.test" || audit[1].Kind != "created" {
		t.Fatalf("audit = %#v", audit)
	}
	if bytes.Contains(auditBody, []byte("client-key-a")) || bytes.Contains(auditBody, []byte("client-key-b")) {
		t.Fatalf("audit exposes client key: %s", auditBody)
	}

	do(http.MethodDelete, clientPath, "carol@example.test", "", http.StatusNoContent, nil)
	do(http.MethodGet, clientPath, "", "", http.StatusNotFound, nil)
	auditBody = do(http.MethodGet, clientPath+"/audit", "", "", http.StatusOK, &audit)
	if len(audit) != 3 || audit[0].Kind != "deleted" || audit[0].Actor != "carol@example.test" {
		t.Fatalf("audit after delete = %#v", audit)
	}
	if strings.Contains(string(auditBody), "client-key-") {
		t.Fatalf("audit exposes deleted client key: %s", auditBody)
	}
}

func TestControlPlaneOpenAPIIncludesClients(t *testing.T) {
	schemas := controlPlaneSchemas()
	for _, name := range []string{"Client", "CreateClientRequest", "UpdateClientRequest", "ClientAudit", "AuditChanges", "AuditEvent", "InputConfig", "SetInputConfigRequest", "InputConfigAudit", "ProvisioningResource", "ProvisioningImportRequest", "ProvisioningResult"} {
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
