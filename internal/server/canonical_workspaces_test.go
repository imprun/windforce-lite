package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/imprun/windforce-core/internal/state"
)

func TestManagedWorkspaceAPIAndAuthorizationBoundary(t *testing.T) {
	store := state.NewLocalStore(filepath.Join(t.TempDir(), "state.json"))
	server := httptest.NewServer(New(Config{
		Store: store, ManagedWorkspaces: true, AdminToken: "instance-admin",
	}))
	defer server.Close()

	create := workspaceRequest(t, server.URL, http.MethodPost, "/api/workspaces", "instance-admin", `{"id":"team-a","name":"Team A"}`)
	if create.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d: %s", create.StatusCode, readResponse(t, create))
	}
	var created struct {
		Workspace workspaceView `json:"workspace"`
		APIToken  string        `json:"api_token"`
	}
	decodeResponse(t, create, &created)
	if created.Workspace.ID != "team-a" || created.APIToken == "" || !created.Workspace.HasToken {
		t.Fatalf("create response = %#v", created)
	}

	unknown := workspaceRequest(t, server.URL, http.MethodGet, "/api/w/typo/apps", "instance-admin", "")
	if unknown.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown workspace status = %d: %s", unknown.StatusCode, readResponse(t, unknown))
	}

	scoped := workspaceRequest(t, server.URL, http.MethodGet, "/api/w/team-a/system/info", created.APIToken, "")
	if scoped.StatusCode != http.StatusOK {
		t.Fatalf("workspace token status = %d: %s", scoped.StatusCode, readResponse(t, scoped))
	}
	global := workspaceRequest(t, server.URL, http.MethodGet, "/api/workspaces", created.APIToken, "")
	if global.StatusCode != http.StatusUnauthorized {
		t.Fatalf("workspace token global status = %d: %s", global.StatusCode, readResponse(t, global))
	}

	createB := workspaceRequest(t, server.URL, http.MethodPost, "/api/workspaces", "instance-admin", `{"id":"team-b","name":"Team B"}`)
	var createdB struct {
		APIToken string `json:"api_token"`
	}
	decodeResponse(t, createB, &createdB)
	cross := workspaceRequest(t, server.URL, http.MethodGet, "/api/w/team-a/system/info", createdB.APIToken, "")
	if cross.StatusCode != http.StatusUnauthorized {
		t.Fatalf("cross-workspace status = %d: %s", cross.StatusCode, readResponse(t, cross))
	}

	rotate := workspaceRequest(t, server.URL, http.MethodPost, "/api/workspaces/team-a/token", "instance-admin", "")
	if rotate.StatusCode != http.StatusOK {
		t.Fatalf("rotate status = %d: %s", rotate.StatusCode, readResponse(t, rotate))
	}
	var rotated struct {
		APIToken string `json:"api_token"`
	}
	decodeResponse(t, rotate, &rotated)
	oldToken := workspaceRequest(t, server.URL, http.MethodGet, "/api/w/team-a/system/info", created.APIToken, "")
	if oldToken.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token status = %d: %s", oldToken.StatusCode, readResponse(t, oldToken))
	}
	newToken := workspaceRequest(t, server.URL, http.MethodGet, "/api/w/team-a/system/info", rotated.APIToken, "")
	if newToken.StatusCode != http.StatusOK {
		t.Fatalf("new token status = %d: %s", newToken.StatusCode, readResponse(t, newToken))
	}

	archive := workspaceRequest(t, server.URL, http.MethodPost, "/api/workspaces/team-a/archive", "instance-admin", "")
	if archive.StatusCode != http.StatusOK {
		t.Fatalf("archive status = %d: %s", archive.StatusCode, readResponse(t, archive))
	}
	readArchived := workspaceRequest(t, server.URL, http.MethodGet, "/api/w/team-a/system/info", "instance-admin", "")
	if readArchived.StatusCode != http.StatusOK {
		t.Fatalf("archived read status = %d: %s", readArchived.StatusCode, readResponse(t, readArchived))
	}
	scopedReadArchived := workspaceRequest(t, server.URL, http.MethodGet, "/api/w/team-a/system/info", rotated.APIToken, "")
	if scopedReadArchived.StatusCode != http.StatusOK {
		t.Fatalf("archived scoped read status = %d: %s", scopedReadArchived.StatusCode, readResponse(t, scopedReadArchived))
	}
	updateArchived := workspaceRequest(t, server.URL, http.MethodPatch, "/api/workspaces/team-a", "instance-admin", `{"name":"Archived Team"}`)
	if updateArchived.StatusCode != http.StatusConflict {
		t.Fatalf("archived update status = %d: %s", updateArchived.StatusCode, readResponse(t, updateArchived))
	}
	rotateArchived := workspaceRequest(t, server.URL, http.MethodPost, "/api/workspaces/team-a/token", "instance-admin", "")
	if rotateArchived.StatusCode != http.StatusConflict {
		t.Fatalf("archived token rotation status = %d: %s", rotateArchived.StatusCode, readResponse(t, rotateArchived))
	}
	writeArchived := workspaceRequest(t, server.URL, http.MethodPost, "/api/w/team-a/variables", "instance-admin", `{"path":"x","value":"y"}`)
	if writeArchived.StatusCode != http.StatusConflict {
		t.Fatalf("archived write status = %d: %s", writeArchived.StatusCode, readResponse(t, writeArchived))
	}
	executeArchived := workspaceRequest(t, server.URL, http.MethodPost, "/execution/v1/workspaces/team-a/runs", "instance-admin", `{"app":"echo","action":"run","input":{}}`)
	if executeArchived.StatusCode != http.StatusConflict {
		t.Fatalf("archived execution status = %d: %s", executeArchived.StatusCode, readResponse(t, executeArchived))
	}

	audit := workspaceRequest(t, server.URL, http.MethodGet, "/api/workspaces/team-a/audit", "instance-admin", "")
	if audit.StatusCode != http.StatusOK {
		t.Fatalf("audit status = %d: %s", audit.StatusCode, readResponse(t, audit))
	}
	var auditBody struct {
		Items []state.WorkspaceAudit `json:"items"`
	}
	decodeResponse(t, audit, &auditBody)
	if len(auditBody.Items) < 3 || auditBody.Items[0].Kind != "archived" {
		t.Fatalf("audit response = %#v", auditBody)
	}
}

func TestManagedWorkspaceRejectsInvalidIDAndDefaultArchive(t *testing.T) {
	store := state.NewLocalStore(filepath.Join(t.TempDir(), "state.json"))
	server := httptest.NewServer(New(Config{Store: store, ManagedWorkspaces: true}))
	defer server.Close()

	invalid := workspaceRequest(t, server.URL, http.MethodPost, "/api/workspaces", "", `{"id":"Team A","name":"Team A"}`)
	if invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid id status = %d: %s", invalid.StatusCode, readResponse(t, invalid))
	}
	archive := workspaceRequest(t, server.URL, http.MethodPost, "/api/workspaces/default/archive", "", "")
	if archive.StatusCode != http.StatusConflict {
		t.Fatalf("archive default status = %d: %s", archive.StatusCode, readResponse(t, archive))
	}
}

func workspaceRequest(t *testing.T, baseURL string, method string, path string, token string, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, baseURL+path, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Windforce-Actor", "test-admin")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeResponse(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

func readResponse(t *testing.T, response *http.Response) string {
	t.Helper()
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestWorkspaceManagementOpenAPI(t *testing.T) {
	paths := buildControlPlaneOpenAPI("http://example.test", "default")["paths"].(map[string]any)
	for _, path := range []string{
		"/api/workspaces",
		"/api/workspaces/{workspace_id}",
		"/api/workspaces/{workspace_id}/archive",
		"/api/workspaces/{workspace_id}/token",
		"/api/workspaces/{workspace_id}/audit",
	} {
		if _, ok := paths[path]; !ok {
			t.Errorf("OpenAPI path %q is missing", path)
		}
	}

	schemas := controlPlaneSchemas()
	for _, name := range []string{"Workspace", "WorkspaceListResponse", "CreateWorkspaceRequest", "UpdateWorkspaceRequest", "WorkspaceTokenResult", "WorkspaceAudit"} {
		if _, ok := schemas[name]; !ok {
			t.Errorf("OpenAPI schema %q is missing", name)
		}
	}
}
