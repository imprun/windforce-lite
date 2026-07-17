package server

import (
	"bytes"
	"context"
	"encoding/base64"
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
	"strings"
	"testing"
	"time"

	"github.com/imprun/windforce-core/internal/bundle"
	"github.com/imprun/windforce-core/internal/catalog"
	"github.com/imprun/windforce-core/internal/contract"
	litecrypto "github.com/imprun/windforce-core/internal/crypto"
	"github.com/imprun/windforce-core/internal/gitsource"
	"github.com/imprun/windforce-core/internal/state"
	"github.com/imprun/windforce-core/internal/syncer"
	"github.com/imprun/windforce-core/internal/token"
)

func decodeCatalogSchema(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		t.Fatalf("catalog schema is not a base64 JSON string: %s: %v", raw, err)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode catalog schema %q: %v", encoded, err)
	}
	return decoded
}

func TestMetricsEndpointBypassesAPIAuthentication(t *testing.T) {
	handler := New(Config{
		Store:      state.NewLocalStore(filepath.Join(t.TempDir(), "state.json")),
		EnableAPI:  true,
		AdminToken: "admin-secret",
		MetricsHandler: http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(response, "windforce_webhook_pending_deliveries 0\n")
		}),
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "windforce_webhook_pending_deliveries") {
		t.Fatalf("metrics response = %d %q", recorder.Code, recorder.Body.String())
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

	for _, tc := range []struct {
		query string
		want  string
	}{
		{"tail_bytes=-1", "tail_bytes must be a non-negative integer"},
		{"tail_bytes=bad", "tail_bytes must be a non-negative integer"},
		{fmt.Sprintf("tail_bytes=%d", maxTailBytes+1), "tail_bytes exceeds server limit"},
	} {
		resp, err := http.Get(server.URL + "/api/w/ws-a/jobs/" + job.ID + "/logs?" + tc.query)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body struct {
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusBadRequest || body.Error != tc.want {
			t.Fatalf("logs %s response = %d %#v, want 400 %q", tc.query, resp.StatusCode, body, tc.want)
		}
	}
}

func TestCanonicalJobListQueryValidation(t *testing.T) {
	tempDir := t.TempDir()
	server := httptest.NewServer(New(Config{
		Store:     state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		EnableAPI: true,
	}))
	defer server.Close()

	nonUUIDCursor := base64.RawURLEncoding.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano) + "|not-a-uuid"))
	for _, tc := range []struct {
		query string
		want  string
	}{
		{"status=bogus", "invalid status filter"},
		{"order=created_at_asc", "unsupported order"},
		{"limit=0", "limit must be between 1 and 500"},
		{"limit=501", "limit must be between 1 and 500"},
		{"cursor=bad", "invalid cursor"},
		{"cursor=" + nonUUIDCursor, "invalid cursor"},
		{"since=bad", "since must be RFC3339"},
		{"until=bad", "until must be RFC3339"},
	} {
		resp, err := http.Get(server.URL + "/api/w/ws-a/jobs?" + tc.query)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body struct {
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusBadRequest || body.Error != tc.want {
			t.Fatalf("jobs?%s response = %d %#v, want 400 %q", tc.query, resp.StatusCode, body, tc.want)
		}
	}
}

func TestCanonicalJobListDoesNotLeakResultOrLogs(t *testing.T) {
	tempDir := t.TempDir()
	store := state.NewLocalStore(filepath.Join(tempDir, "state.json"))
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	if err := fileCatalog.UpsertDeployment(context.Background(), contract.Deployment{
		Workspace:    "ws-a",
		GitSourceID:  "1",
		App:          "echo",
		Commit:       "commit-a",
		BundleDigest: testExecutionBundleDigest,
		Actions: map[string]contract.Action{
			"echo": {Action: "echo", Entrypoint: "main.ts"},
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
		Output:     json.RawMessage(`{"secret":"result-secret"}`),
		DurationMs: 12,
	}); err != nil {
		t.Fatalf("CompleteJobSucceeded returned error: %v", err)
	}
	if err := store.AppendLogs(context.Background(), claimed.ID, "ws-a", "log-secret"); err != nil {
		t.Fatalf("AppendLogs returned error: %v", err)
	}

	listResp, err := http.Get(server.URL + "/api/w/ws-a/jobs?status=all")
	if err != nil {
		t.Fatal(err)
	}
	defer listResp.Body.Close()
	body, err := io.ReadAll(listResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want %d: %s", listResp.StatusCode, http.StatusOK, body)
	}
	if bytes.Contains(body, []byte("result-secret")) || bytes.Contains(body, []byte("log-secret")) {
		t.Fatalf("job list leaked result or logs: %s", body)
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

	rawSetResp, err := http.Post(server.URL+"/api/w/ws-a/state?path=flow/raw", "text/plain", bytes.NewBufferString(`not-json`))
	if err != nil {
		t.Fatal(err)
	}
	defer rawSetResp.Body.Close()
	if rawSetResp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("set raw state status = %d, want %d", rawSetResp.StatusCode, http.StatusInternalServerError)
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
	store := state.NewLocalStore(filepath.Join(tempDir, "state.json"))
	secretKey := "test-secret"
	server := httptest.NewServer(New(Config{
		Store:          store,
		EnableAPI:      true,
		JobTokenSecret: "job-secret",
		SecretKey:      secretKey,
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

	invalidVariableResp, err := http.Post(server.URL+"/api/w/ws-a/variables", "application/json", bytes.NewBufferString(`{`))
	if err != nil {
		t.Fatal(err)
	}
	defer invalidVariableResp.Body.Close()
	var invalidVariableBody struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(invalidVariableResp.Body).Decode(&invalidVariableBody); err != nil {
		t.Fatal(err)
	}
	if invalidVariableResp.StatusCode != http.StatusBadRequest || invalidVariableBody.Error != "path required" {
		t.Fatalf("invalid variable response = %d %#v, want 400 path required", invalidVariableResp.StatusCode, invalidVariableBody)
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

	sharedVariableResp, err := http.Get(server.URL + "/api/w/ws-a/variables/get/p/config/token")
	if err != nil {
		t.Fatal(err)
	}
	defer sharedVariableResp.Body.Close()
	if sharedVariableResp.StatusCode != http.StatusOK {
		t.Fatalf("shared variable status = %d, want %d", sharedVariableResp.StatusCode, http.StatusOK)
	}
	var sharedVariableBody struct {
		Path     string `json:"path"`
		Value    string `json:"value"`
		IsSecret bool   `json:"is_secret"`
	}
	if err := json.NewDecoder(sharedVariableResp.Body).Decode(&sharedVariableBody); err != nil {
		t.Fatal(err)
	}
	if sharedVariableBody.Path != "config/token" || sharedVariableBody.Value != "shared" || !sharedVariableBody.IsSecret {
		t.Fatalf("shared variable body = %#v", sharedVariableBody)
	}
	storedShared, found, err := store.GetVariableExact(context.Background(), "ws-a", "", "config/token")
	if err != nil {
		t.Fatal(err)
	}
	if !found || !storedShared.IsSecret {
		t.Fatalf("stored shared secret found=%v variable=%#v", found, storedShared)
	}
	if storedShared.Value == "shared" {
		t.Fatalf("secret variable was stored in plaintext")
	}
	decryptedShared, err := litecrypto.Decrypt(litecrypto.DeriveWorkspaceKey(secretKey, "ws-a"), storedShared.Value)
	if err != nil {
		t.Fatalf("decrypt stored secret: %v", err)
	}
	if decryptedShared != "shared" {
		t.Fatalf("stored secret decrypts to %q, want shared", decryptedShared)
	}

	foreignVariableResp, err := http.Get(server.URL + "/api/w/ws-a/variables/get/p/config/token?app=other")
	if err != nil {
		t.Fatal(err)
	}
	defer foreignVariableResp.Body.Close()
	if foreignVariableResp.StatusCode != http.StatusNotFound {
		t.Fatalf("foreign exact variable status = %d, want %d", foreignVariableResp.StatusCode, http.StatusNotFound)
	}

	deployment := contract.Deployment{
		Workspace: "ws-a",
		App:       "echo",
		Commit:    "commit-a",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo"},
		},
	}
	run := state.NewRun("windforce", "run-variable-scope", "echo", "echo", deployment, json.RawMessage(`{}`))
	job := state.NewActionJob(run, nil)
	if err := store.CreateRunAndEnqueue(context.Background(), run, job); err != nil {
		t.Fatal(err)
	}
	forgedJobReq, err := http.NewRequest(http.MethodGet, server.URL+"/api/w/ws-a/variables/get/p/config/token", nil)
	if err != nil {
		t.Fatal(err)
	}
	forgedJobReq.Header.Set("X-Windforce-Job-ID", job.ID)
	forgedJobResp, err := http.DefaultClient.Do(forgedJobReq)
	if err != nil {
		t.Fatal(err)
	}
	defer forgedJobResp.Body.Close()
	if forgedJobResp.StatusCode != http.StatusOK {
		t.Fatalf("forged job header variable status = %d, want %d", forgedJobResp.StatusCode, http.StatusOK)
	}
	var forgedJobBody struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(forgedJobResp.Body).Decode(&forgedJobBody); err != nil {
		t.Fatal(err)
	}
	if forgedJobBody.Value != "shared" {
		t.Fatalf("forged job header variable body = %#v, want shared scope", forgedJobBody)
	}

	jobToken := token.MintJob("job-secret", token.JobClaims{
		Workspace: "ws-a",
		JobID:     job.ID,
		Subject:   "runner@example.test",
		Exp:       time.Now().Add(time.Minute).Unix(),
	})
	jobVariableReq, err := http.NewRequest(http.MethodGet, server.URL+"/api/w/ws-a/variables/get/p/config/token", nil)
	if err != nil {
		t.Fatal(err)
	}
	jobVariableReq.Header.Set("Authorization", "Bearer "+jobToken)
	jobVariableResp, err := http.DefaultClient.Do(jobVariableReq)
	if err != nil {
		t.Fatal(err)
	}
	defer jobVariableResp.Body.Close()
	if jobVariableResp.StatusCode != http.StatusOK {
		t.Fatalf("job scoped variable status = %d, want %d", jobVariableResp.StatusCode, http.StatusOK)
	}
	var jobVariableBody struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(jobVariableResp.Body).Decode(&jobVariableBody); err != nil {
		t.Fatal(err)
	}
	if jobVariableBody.Value != "scoped" {
		t.Fatalf("job scoped variable body = %#v", jobVariableBody)
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
	invalidResourceResp, err := http.Post(server.URL+"/api/w/ws-a/resources", "application/json", bytes.NewBufferString(`{`))
	if err != nil {
		t.Fatal(err)
	}
	defer invalidResourceResp.Body.Close()
	var invalidResourceBody struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(invalidResourceResp.Body).Decode(&invalidResourceBody); err != nil {
		t.Fatal(err)
	}
	if invalidResourceResp.StatusCode != http.StatusBadRequest || invalidResourceBody.Error != "path required" {
		t.Fatalf("invalid resource response = %d %#v, want 400 path required", invalidResourceResp.StatusCode, invalidResourceBody)
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

	setNullResourceResp, err := http.Post(server.URL+"/api/w/ws-a/resources", "application/json", bytes.NewBufferString(`{"path":"browser/default"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer setNullResourceResp.Body.Close()
	if setNullResourceResp.StatusCode != http.StatusOK {
		t.Fatalf("set null resource status = %d, want %d", setNullResourceResp.StatusCode, http.StatusOK)
	}
	getNullResourceResp, err := http.Get(server.URL + "/api/w/ws-a/resources/get/p/browser/default")
	if err != nil {
		t.Fatal(err)
	}
	defer getNullResourceResp.Body.Close()
	nullBody, err := io.ReadAll(getNullResourceResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(bytes.TrimSpace(nullBody)) != "{}" {
		t.Fatalf("default resource body = %q, want {}", nullBody)
	}
}

func TestCanonicalVariableAppScopeShadowing(t *testing.T) {
	tempDir := t.TempDir()
	store := state.NewLocalStore(filepath.Join(tempDir, "state.json"))
	server := httptest.NewServer(New(Config{
		Store:          store,
		EnableAPI:      true,
		JobTokenSecret: "job-secret",
	}))
	defer server.Close()

	set := func(appKey, path, value string) {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"app_key": appKey,
			"path":    path,
			"value":   value,
		})
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.Post(server.URL+"/api/w/ws-a/variables", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("set %s@%q status = %d, want %d", path, appKey, resp.StatusCode, http.StatusOK)
		}
	}
	reveal := func(path, query, bearerToken string) (string, int) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, server.URL+"/api/w/ws-a/variables/get/p/"+path+query, nil)
		if err != nil {
			t.Fatal(err)
		}
		if bearerToken != "" {
			req.Header.Set("Authorization", "Bearer "+bearerToken)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body struct {
			Value string `json:"value"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return body.Value, resp.StatusCode
	}
	enqueue := func(appKey string) string {
		t.Helper()
		deployment := contract.Deployment{
			Workspace: "ws-a",
			App:       appKey,
			Commit:    "commit-" + appKey,
			Actions: map[string]contract.Action{
				"run": {Action: "run"},
			},
		}
		run := state.NewRun("windforce", "run-"+appKey, appKey, "run", deployment, json.RawMessage(`{}`))
		job := state.NewActionJob(run, nil)
		if err := store.CreateRunAndEnqueue(context.Background(), run, job); err != nil {
			t.Fatal(err)
		}
		return job.ID
	}
	mintJobToken := func(jobID string) string {
		t.Helper()
		return token.MintJob("job-secret", token.JobClaims{
			Workspace: "ws-a",
			JobID:     jobID,
			Subject:   "runner@example.test",
			Exp:       time.Now().Add(time.Minute).Unix(),
		})
	}

	set("", "token", "shared-value")
	set("appa", "token", "appa-value")
	set("appa", "only-a", "secret-a")

	if value, code := reveal("token", "", ""); code != http.StatusOK || value != "shared-value" {
		t.Fatalf("console shared = %d %q, want shared-value", code, value)
	}
	if value, code := reveal("token", "?app=appa", ""); code != http.StatusOK || value != "appa-value" {
		t.Fatalf("console appa = %d %q, want appa-value", code, value)
	}

	jobA := mintJobToken(enqueue("appa"))
	jobB := mintJobToken(enqueue("appb"))
	if value, code := reveal("token", "", jobA); code != http.StatusOK || value != "appa-value" {
		t.Fatalf("job appa shadowed read = %d %q, want appa-value", code, value)
	}
	if value, code := reveal("token", "", jobB); code != http.StatusOK || value != "shared-value" {
		t.Fatalf("job appb fallback read = %d %q, want shared-value", code, value)
	}
	if value, code := reveal("only-a", "", jobA); code != http.StatusOK || value != "secret-a" {
		t.Fatalf("job appa scoped read = %d %q", code, value)
	}
	if _, code := reveal("only-a", "", jobB); code != http.StatusNotFound {
		t.Fatalf("job appb foreign read = %d, want %d", code, http.StatusNotFound)
	}

	req, err := http.NewRequest(http.MethodDelete, server.URL+"/api/w/ws-a/variables/p/token?app=appa", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete scoped status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}
	if value, code := reveal("token", "", jobA); code != http.StatusOK || value != "shared-value" {
		t.Fatalf("post-delete appa read = %d %q, want shared fallback", code, value)
	}
}

func TestJobTokenAuthorizesOnlySDKCallbacks(t *testing.T) {
	tempDir := t.TempDir()
	store := state.NewLocalStore(filepath.Join(tempDir, "state.json"))
	secretKey := "job-token-test-secret"
	dek := litecrypto.DeriveWorkspaceKey(secretKey, "ws-a")
	sharedSecret, err := litecrypto.Encrypt(dek, "shared")
	if err != nil {
		t.Fatal(err)
	}
	scopedSecret, err := litecrypto.Encrypt(dek, "scoped")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetVariable(context.Background(), "ws-a", "", "config/token", sharedSecret, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.SetVariable(context.Background(), "ws-a", "echo", "config/token", scopedSecret, true, ""); err != nil {
		t.Fatal(err)
	}
	deployment := contract.Deployment{
		Workspace: "ws-a",
		App:       "echo",
		Commit:    "commit-a",
		Actions: map[string]contract.Action{
			"run": {Action: "run"},
		},
	}
	run := state.NewRun("windforce", "run-job-token", "echo", "run", deployment, json.RawMessage(`{}`))
	job := state.NewActionJob(run, nil)
	if err := store.CreateRunAndEnqueue(context.Background(), run, job); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(Config{
		Store:          store,
		EnableAPI:      true,
		AdminToken:     "admin-token",
		JobTokenSecret: "job-secret",
		SecretKey:      secretKey,
	}))
	defer server.Close()

	jobToken := token.MintJob("job-secret", token.JobClaims{
		Workspace: "ws-a",
		JobID:     job.ID,
		Subject:   "runner@example.test",
		Exp:       time.Now().Add(time.Minute).Unix(),
	})
	req, err := http.NewRequest(http.MethodGet, server.URL+"/api/w/ws-a/variables/get/p/config/token", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+jobToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("job token variable status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var body struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Value != "scoped" {
		t.Fatalf("job-scoped variable = %q, want scoped", body.Value)
	}

	forbiddenReq, err := http.NewRequest(http.MethodGet, server.URL+"/api/w/ws-a/apps", nil)
	if err != nil {
		t.Fatal(err)
	}
	forbiddenReq.Header.Set("Authorization", "Bearer "+jobToken)
	forbiddenResp, err := http.DefaultClient.Do(forbiddenReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = forbiddenResp.Body.Close()
	if forbiddenResp.StatusCode != http.StatusForbidden {
		t.Fatalf("job token control-plane status = %d, want %d", forbiddenResp.StatusCode, http.StatusForbidden)
	}

	resumeReq, err := http.NewRequest(http.MethodPost, server.URL+"/api/w/ws-a/flow/resume-urls", nil)
	if err != nil {
		t.Fatal(err)
	}
	resumeReq.Header.Set("Authorization", "Bearer "+jobToken)
	resumeResp, err := http.DefaultClient.Do(resumeReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = resumeResp.Body.Close()
	if resumeResp.StatusCode != http.StatusNotFound {
		t.Fatalf("job token flow resume URL status = %d, want %d", resumeResp.StatusCode, http.StatusNotFound)
	}

	crossWorkspaceReq, err := http.NewRequest(http.MethodGet, server.URL+"/api/w/ws-b/variables/get/p/config/token", nil)
	if err != nil {
		t.Fatal(err)
	}
	crossWorkspaceReq.Header.Set("Authorization", "Bearer "+jobToken)
	crossWorkspaceResp, err := http.DefaultClient.Do(crossWorkspaceReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = crossWorkspaceResp.Body.Close()
	if crossWorkspaceResp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-workspace job token status = %d, want %d", crossWorkspaceResp.StatusCode, http.StatusForbidden)
	}
}

func TestAdminTokenRequiresAuthorizationBearer(t *testing.T) {
	server := httptest.NewServer(New(Config{
		EnableAPI:  true,
		AdminToken: "admin-token",
	}))
	defer server.Close()

	missingResp, err := http.Get(server.URL + "/api/w/ws-a/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	_ = missingResp.Body.Close()
	if missingResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want %d", missingResp.StatusCode, http.StatusUnauthorized)
	}

	headerReq, err := http.NewRequest(http.MethodGet, server.URL+"/api/w/ws-a/openapi.json", nil)
	if err != nil {
		t.Fatal(err)
	}
	headerReq.Header.Set("X-Windforce-Token", "admin-token")
	headerResp, err := http.DefaultClient.Do(headerReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = headerResp.Body.Close()
	if headerResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("x-windforce token status = %d, want %d", headerResp.StatusCode, http.StatusUnauthorized)
	}

	bearerReq, err := http.NewRequest(http.MethodGet, server.URL+"/api/w/ws-a/openapi.json", nil)
	if err != nil {
		t.Fatal(err)
	}
	bearerReq.Header.Set("Authorization", "Bearer admin-token")
	bearerResp, err := http.DefaultClient.Do(bearerReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = bearerResp.Body.Close()
	if bearerResp.StatusCode != http.StatusOK {
		t.Fatalf("bearer token status = %d, want %d", bearerResp.StatusCode, http.StatusOK)
	}
}

func TestGitSourceCredsRefResolvesWorkspaceVariableOnly(t *testing.T) {
	tempDir := t.TempDir()
	store := state.NewLocalStore(filepath.Join(tempDir, "state.json"))
	secretKey := "git-source-test-secret"
	dek := litecrypto.DeriveWorkspaceKey(secretKey, "ws-a")
	scopedToken, err := litecrypto.Encrypt(dek, "scoped-token")
	if err != nil {
		t.Fatal(err)
	}
	sharedToken, err := litecrypto.Encrypt(dek, "shared-token")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetVariable(context.Background(), "ws-a", "echo", "secrets/git/token", scopedToken, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.SetVariable(context.Background(), "ws-a", "", "secrets/git/token", sharedToken, true, ""); err != nil {
		t.Fatal(err)
	}
	t.Setenv("secrets/git/missing", "env-token")
	handler := New(Config{Store: store, SecretKey: secretKey}).(*Handler)

	token, err := handler.resolveGitSourceCreds(context.Background(), "ws-a", "secrets/git/token")
	if err != nil {
		t.Fatal(err)
	}
	if token != "shared-token" {
		t.Fatalf("resolved token = %q, want workspace-shared variable", token)
	}

	missing, err := handler.resolveGitSourceCreds(context.Background(), "ws-a", "secrets/git/missing")
	if err != nil {
		t.Fatal(err)
	}
	if missing != "" {
		t.Fatalf("missing creds_ref resolved from env = %q, want empty", missing)
	}
}

func TestCanonicalJobRunStatusAndResultAPI(t *testing.T) {
	tempDir := t.TempDir()
	store := state.NewLocalStore(filepath.Join(tempDir, "state.json"))
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	if err := fileCatalog.UpsertDeployment(context.Background(), contract.Deployment{
		Workspace:    "ws-a",
		GitSourceID:  "1",
		App:          "echo",
		Commit:       "commit-a",
		BundleDigest: testExecutionBundleDigest,
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
	if !isCanonicalUUID(runResponse.JobID) {
		t.Fatalf("job id = %q, want UUID", runResponse.JobID)
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
		State       string     `json:"state"`
		Status      string     `json:"status"`
		Worker      string     `json:"worker"`
		StartedAt   *time.Time `json:"started_at"`
		CompletedAt *time.Time `json:"completed_at"`
	}
	if err := json.NewDecoder(doneStatusResp.Body).Decode(&doneStatusBody); err != nil {
		t.Fatal(err)
	}
	if doneStatusBody.State != "completed" || doneStatusBody.Status != "success" {
		t.Fatalf("done job status = %#v", doneStatusBody)
	}
	if doneStatusBody.Worker != "worker-a" {
		t.Fatalf("done job worker = %q, want worker-a", doneStatusBody.Worker)
	}
	if doneStatusBody.StartedAt == nil || doneStatusBody.CompletedAt == nil {
		t.Fatalf("done job timestamps = %#v", doneStatusBody)
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
			ID          string     `json:"id"`
			AppKey      string     `json:"app_key"`
			ActionKey   string     `json:"action_key"`
			GitSourceID int64      `json:"git_source_id"`
			Status      string     `json:"status"`
			Completed   bool       `json:"completed"`
			Worker      string     `json:"worker"`
			StartedAt   *time.Time `json:"started_at"`
			CompletedAt *time.Time `json:"completed_at"`
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
		listBody.Items[0].Status != "success" || !listBody.Items[0].Completed ||
		listBody.Items[0].Worker != "worker-a" {
		t.Fatalf("list body = %#v", listBody)
	}
	if listBody.Items[0].StartedAt == nil || listBody.Items[0].CompletedAt == nil {
		t.Fatalf("list timestamps = %#v", listBody.Items[0])
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
		Workspace:    "ws-a",
		GitSourceID:  "source-a",
		App:          "echo",
		Commit:       "commit-a",
		BundleDigest: testExecutionBundleDigest,
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

	rawReq, err := http.NewRequest(http.MethodPost, server.URL+"/api/w/ws-a/jobs/webhook/echo/echo", bytes.NewBufferString(`not-json <xml/>`))
	if err != nil {
		t.Fatal(err)
	}
	rawResp, err := http.DefaultClient.Do(rawReq)
	if err != nil {
		t.Fatal(err)
	}
	defer rawResp.Body.Close()
	if rawResp.StatusCode != http.StatusCreated {
		t.Fatalf("raw webhook status = %d, want %d", rawResp.StatusCode, http.StatusCreated)
	}
	var rawWebhookResponse struct {
		JobID string `json:"job_id"`
	}
	if err := json.NewDecoder(rawResp.Body).Decode(&rawWebhookResponse); err != nil {
		t.Fatal(err)
	}
	rawJob, _, found, err := store.GetJob(context.Background(), "ws-a", rawWebhookResponse.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatalf("raw webhook job not found")
	}
	if err := json.Unmarshal(rawJob.Payload.Input, &raw); err != nil {
		t.Fatalf("raw webhook input is not a JSON string: %v input=%s", err, rawJob.Payload.Input)
	}
	if raw != `not-json <xml/>` {
		t.Fatalf("raw webhook payload = %q", raw)
	}
}

func TestCanonicalJobRunBodyValidationMatchesCanonicalAPI(t *testing.T) {
	tempDir := t.TempDir()
	store := state.NewLocalStore(filepath.Join(tempDir, "state.json"))
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	if err := fileCatalog.UpsertDeployment(context.Background(), contract.Deployment{
		Workspace:    "ws-a",
		App:          "echo",
		Commit:       "commit-a",
		BundleDigest: testExecutionBundleDigest,
		Actions: map[string]contract.Action{
			"echo": {Action: "echo", Command: []string{"helper"}},
		},
	}); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(New(Config{Store: store, Catalog: fileCatalog, EnableAPI: true}))
	defer server.Close()

	for _, body := range []string{`[1,2,3]`, `"a string"`, `42`, `null`, `{not json`} {
		resp, err := http.Post(server.URL+"/api/w/ws-a/jobs/run/echo/echo", "application/json", bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusBadRequest {
			_ = resp.Body.Close()
			t.Fatalf("run body %s status = %d, want %d", body, resp.StatusCode, http.StatusBadRequest)
		}
		_ = resp.Body.Close()
	}

	reservedResp, err := http.Post(server.URL+"/api/w/ws-a/jobs/run/echo/echo", "application/json", bytes.NewBufferString(`{"__wf_enc":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer reservedResp.Body.Close()
	var reservedBody struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(reservedResp.Body).Decode(&reservedBody); err != nil {
		t.Fatal(err)
	}
	if reservedResp.StatusCode != http.StatusBadRequest || reservedBody.Error != `"__wf_enc" is a reserved top-level input key` {
		t.Fatalf("reserved input response = %d %#v, want reserved-key 400", reservedResp.StatusCode, reservedBody)
	}

	emptyResp, err := http.Post(server.URL+"/api/w/ws-a/jobs/run/echo/echo", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer emptyResp.Body.Close()
	if emptyResp.StatusCode != http.StatusCreated {
		t.Fatalf("empty body status = %d, want %d", emptyResp.StatusCode, http.StatusCreated)
	}
	var created struct {
		JobID string `json:"job_id"`
	}
	if err := json.NewDecoder(emptyResp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	job, _, found, err := store.GetJob(context.Background(), "ws-a", created.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if !found || !bytes.Equal(bytes.TrimSpace(job.Payload.Input), []byte(`{}`)) {
		t.Fatalf("empty body job input = found:%v input:%s", found, job.Payload.Input)
	}

	oversize := bytes.Repeat([]byte("a"), maxRunBodyBytes+1024)
	for _, path := range []string{"/api/w/ws-a/jobs/run/echo/echo", "/api/w/ws-a/jobs/webhook/echo/echo"} {
		resp, err := http.Post(server.URL+path, "application/json", bytes.NewReader(oversize))
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusRequestEntityTooLarge {
			_ = resp.Body.Close()
			t.Fatalf("oversize %s status = %d, want %d", path, resp.StatusCode, http.StatusRequestEntityTooLarge)
		}
		_ = resp.Body.Close()
	}
}

func TestCanonicalJobCancelAPI(t *testing.T) {
	tempDir := t.TempDir()
	store := state.NewLocalStore(filepath.Join(tempDir, "state.json"))
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	if err := fileCatalog.UpsertDeployment(context.Background(), contract.Deployment{
		Workspace:    "ws-a",
		GitSourceID:  "source-a",
		App:          "echo",
		Commit:       "commit-a",
		BundleDigest: testExecutionBundleDigest,
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

	cancelReq, err := http.NewRequest(http.MethodPost, server.URL+"/api/w/ws-a/jobs/"+runBody.JobID+"/cancel", bytes.NewBufferString(`{"reason":"operator canceled"}`))
	if err != nil {
		t.Fatal(err)
	}
	cancelReq.Header.Set("Content-Type", "application/json")
	cancelReq.Header.Set("X-Windforce-Actor", "operator@example.test")
	cancelResp, err := http.DefaultClient.Do(cancelReq)
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
	if resultBody.Status != "canceled" || !bytes.Contains(resultBody.Result, []byte("job canceled before execution")) {
		t.Fatalf("result body = %#v result=%s", resultBody, resultBody.Result)
	}

	statusResp, err := http.Get(server.URL + "/api/w/ws-a/jobs/" + runBody.JobID)
	if err != nil {
		t.Fatal(err)
	}
	defer statusResp.Body.Close()
	var statusBody struct {
		State          string  `json:"state"`
		Status         string  `json:"status"`
		CanceledBy     *string `json:"canceled_by"`
		CanceledReason *string `json:"canceled_reason"`
	}
	if err := json.NewDecoder(statusResp.Body).Decode(&statusBody); err != nil {
		t.Fatal(err)
	}
	if statusBody.State != "completed" || statusBody.Status != "canceled" {
		t.Fatalf("job status = %#v", statusBody)
	}
	if statusBody.CanceledBy == nil || *statusBody.CanceledBy != "operator@example.test" {
		t.Fatalf("canceled_by = %v, want operator@example.test", statusBody.CanceledBy)
	}
	if statusBody.CanceledReason == nil || *statusBody.CanceledReason != "operator canceled" {
		t.Fatalf("canceled_reason = %v, want operator canceled", statusBody.CanceledReason)
	}

	secondCancelResp, err := http.Post(server.URL+"/api/w/ws-a/jobs/"+runBody.JobID+"/cancel", "application/json", bytes.NewBufferString(`{`))
	if err != nil {
		t.Fatal(err)
	}
	defer secondCancelResp.Body.Close()
	if secondCancelResp.StatusCode != http.StatusOK {
		t.Fatalf("second cancel status = %d, want %d", secondCancelResp.StatusCode, http.StatusOK)
	}
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
		{http.MethodGet, "/api/w/ws-a/apps/Bad!/documentation", "", "invalid app key"},
		{http.MethodGet, "/api/w/ws-a/apps/Bad!/history", "", "invalid app key"},
		{http.MethodGet, "/api/w/ws-a/apps/Bad!/openapi.json", "", "invalid app key"},
		{http.MethodGet, "/api/w/ws-a/apps/echo/actions/Bad!", "", "invalid app/action key"},
		{http.MethodGet, "/api/w/ws-a/apps/echo/actions/Bad!/schema", "", "invalid app/action key"},
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

func TestCanonicalControlPlaneOpenAPIExposesSchemaDiscovery(t *testing.T) {
	server := httptest.NewServer(New(Config{EnableAPI: true}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/api/w/ws-a/openapi.json", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "api.example.test")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("control openapi status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["openapi"] != "3.1.0" {
		t.Fatalf("control openapi version = %#v", body["openapi"])
	}
	if serverURL := body["servers"].([]any)[0].(map[string]any)["url"]; serverURL != "https://api.example.test" {
		t.Fatalf("control openapi server url = %#v", serverURL)
	}

	paths := body["paths"].(map[string]any)
	actionPath := paths["/api/w/{workspace}/apps/{app}/actions/{action}"].(map[string]any)
	actionGet := actionPath["get"].(map[string]any)
	actionDescription := actionGet["description"].(string)
	if !bytes.Contains([]byte(actionDescription), []byte("canonical action metadata")) ||
		!bytes.Contains([]byte(actionDescription), []byte("base64")) ||
		!bytes.Contains([]byte(actionDescription), []byte("sibling /schema endpoint")) {
		t.Fatalf("action discovery description = %q", actionDescription)
	}
	if paths["/api/w/{workspace}/apps/{app}/openapi.json"] == nil {
		t.Fatalf("app invocation openapi path missing: %#v", paths)
	}
	schemaPath := paths["/api/w/{workspace}/apps/{app}/actions/{action}/schema"].(map[string]any)
	schemaGet := schemaPath["get"].(map[string]any)
	if schemaGet["operationId"] != "getActionSchema" {
		t.Fatalf("action schema operation = %#v", schemaGet)
	}
	if paths["/api/w/{workspace}/deployments/{deploymentId}"] != nil {
		t.Fatalf("unsupported deployment status route should not be advertised: %#v", paths["/api/w/{workspace}/deployments/{deploymentId}"])
	}
	for _, path := range []string{
		"/api/w/{workspace}/state",
		"/api/w/{workspace}/variables",
		"/api/w/{workspace}/variables/get/p/{path}",
		"/api/w/{workspace}/variables/p/{path}",
		"/api/w/{workspace}/resources",
		"/api/w/{workspace}/resources/get/p/{path}",
		"/api/w/{workspace}/jobs/run/{app}/{action}",
		"/api/w/{workspace}/jobs/run/{app}/{action}/wait",
		"/api/w/{workspace}/jobs/webhook/{app}/{action}",
		"/api/w/{workspace}/jobs",
		"/api/w/{workspace}/jobs/summary",
		"/api/w/{workspace}/jobs/{jobId}",
		"/api/w/{workspace}/jobs/{jobId}/result",
		"/api/w/{workspace}/jobs/{jobId}/logs",
		"/api/w/{workspace}/jobs/{jobId}/cancel",
	} {
		if paths[path] == nil {
			t.Fatalf("control-plane path %s missing: %#v", path, paths)
		}
	}

	components := body["components"].(map[string]any)
	schemas := components["schemas"].(map[string]any)
	jsonSchema := schemas["JSONSchema"].(map[string]any)
	if jsonSchema["additionalProperties"] != true {
		t.Fatalf("JSONSchema component must expose materialized action schemas as free-form JSON Schema: %#v", jsonSchema)
	}
	base64JSONSchema := schemas["Base64JSONSchema"].(map[string]any)
	if base64JSONSchema["format"] != "byte" {
		t.Fatalf("Base64JSONSchema component must document canonical catalog schema encoding: %#v", base64JSONSchema)
	}
	if schemas["Deployment"] != nil {
		t.Fatalf("unsupported deployment status schema should not be advertised: %#v", schemas["Deployment"])
	}
	assertSchemaFields := func(schemaName string, fields []string) {
		t.Helper()
		schema := schemas[schemaName].(map[string]any)
		properties := schema["properties"].(map[string]any)
		for _, field := range fields {
			if properties[field] == nil {
				t.Fatalf("%s schema missing canonical field %s: %#v", schemaName, field, properties)
			}
		}
	}
	assertSchemaFields("GitSource", []string{
		"id", "workspace_id", "name", "repo_url", "branch", "subpath", "creds_ref", "kind",
		"last_synced_commit", "last_synced_at", "created_at",
	})
	assertSchemaFields("App", []string{
		"id", "workspace_id", "app_key", "git_source_id", "commit_sha", "entrypoint", "tag",
		"tag_override", "timeout_s", "script_lang", "bundle_status", "bundle_digest", "bundle_uri",
		"required_capabilities", "max_concurrent", "updated_at",
	})
	assertSchemaFields("AppView", []string{
		"id", "workspace_id", "app_key", "git_source_id", "commit_sha", "entrypoint", "tag",
		"tag_override", "timeout_s", "script_lang", "bundle_status", "bundle_digest", "bundle_uri",
		"required_capabilities", "max_concurrent", "updated_at",
		"effective_route_tag",
	})
	assertSchemaFields("Action", []string{
		"id", "workspace_id", "app_key", "action_key", "input_schema", "output_schema", "tag",
		"tag_override", "timeout_s", "required_capabilities", "updated_at",
	})
	assertSchemaFields("AppAction", []string{
		"id", "workspace_id", "app_key", "action_key", "input_schema", "output_schema", "tag",
		"tag_override", "timeout_s", "required_capabilities", "updated_at",
		"effective_capabilities", "effective_route_tag",
	})
	assertSchemaRef := func(owner string, schema map[string]any, field string, want string) {
		t.Helper()
		properties := schema["properties"].(map[string]any)
		fieldSchema, ok := properties[field].(map[string]any)
		if !ok {
			t.Fatalf("%s.%s schema is not an object: %#v", owner, field, properties[field])
		}
		ref, ok := fieldSchema["$ref"].(string)
		if !ok || ref != want {
			t.Fatalf("%s.%s schema ref = %#v, want %s", owner, field, fieldSchema["$ref"], want)
		}
	}
	for _, schemaName := range []string{"Action", "AppAction"} {
		for _, field := range []string{"input_schema", "output_schema"} {
			assertSchemaRef(schemaName, schemas[schemaName].(map[string]any), field, "#/components/schemas/Base64JSONSchema")
		}
	}
	assertSchemaFields("ActionSchema", []string{
		"workspace_id", "app_key", "action_key", "input_schema", "output_schema",
	})
	assertSchemaRef("ActionSchema", schemas["ActionSchema"].(map[string]any), "input_schema", "#/components/schemas/JSONSchema")
	assertSchemaRef("ActionSchema", schemas["ActionSchema"].(map[string]any), "output_schema", "#/components/schemas/JSONSchema")
	assertSchemaFields("AppHistoryItem", []string{
		"id", "commit_sha", "entrypoint", "source", "deployment_id", "message", "created_by", "created_at",
	})
	assertSchemaFields("JobStatus", []string{
		"id", "workspace_id", "state", "status", "worker", "app_key", "action_key", "trigger_kind", "kind",
		"git_source_id", "commit_sha", "entrypoint", "input_schema", "output_schema", "input", "tag",
		"timeout_s", "created_by", "permissioned_as", "created_at", "started_at", "completed_at",
		"duration_ms", "canceled_by", "canceled_reason", "flow_run_id", "flow_key", "flow_step_key",
	})
	assertSchemaFields("JobListItem", []string{
		"id", "workspace_id", "app_key", "action_key", "trigger_kind", "status", "queued", "running",
		"completed", "created_at", "started_at", "completed_at", "duration_ms", "worker", "git_source_id",
		"commit_sha", "entrypoint", "tag", "created_by", "permissioned_as", "canceled_by", "canceled_reason",
		"flow_run_id", "flow_step_id", "error_snippet",
	})
	assertSchemaFields("CancelResult", []string{"found", "completed_now", "soft_canceled", "already_completed"})
	assertSchemaFields("JobSummaryCounts", []string{
		"queued_count", "running_count", "completed_count_recent", "failed_count_recent", "canceled_count_recent",
	})
	gitSource := schemas["GitSource"].(map[string]any)
	gitSourceProperties := gitSource["properties"].(map[string]any)
	for _, field := range []string{"kind", "last_synced_commit", "last_synced_at", "created_at"} {
		if gitSourceProperties[field] == nil {
			t.Fatalf("git source schema missing %s: %#v", field, gitSourceProperties)
		}
	}
	if gitSourceProperties["updated_at"] != nil {
		t.Fatalf("git source schema must match canonical response without updated_at: %#v", gitSourceProperties)
	}
	registerRequest := schemas["RegisterGitSourceRequest"].(map[string]any)["properties"].(map[string]any)
	for _, field := range []string{"name", "repo_url", "branch", "subpath", "creds_ref"} {
		if registerRequest[field] == nil {
			t.Fatalf("register request schema missing %s: %#v", field, registerRequest)
		}
	}
	for _, field := range []string{"auth_method", "access_token", "username", "password"} {
		if registerRequest[field] != nil {
			t.Fatalf("register request schema must not accept inline credential field %s: %#v", field, registerRequest)
		}
	}
	probeRequest := schemas["ProbeGitSourceRequest"].(map[string]any)["properties"].(map[string]any)
	for _, field := range []string{"auth_method", "access_token", "username", "password", "creds_ref"} {
		if probeRequest[field] == nil {
			t.Fatalf("probe request schema missing %s: %#v", field, probeRequest)
		}
	}
	if probeRequest["access_token"] == nil || probeRequest["creds_ref"] == nil {
		t.Fatalf("probe request schema = %#v", probeRequest)
	}
	probeResult := schemas["GitSourceProbeResult"].(map[string]any)["properties"].(map[string]any)
	for _, field := range []string{"reachable", "branch", "branch_exists", "branches", "error"} {
		if probeResult[field] == nil {
			t.Fatalf("probe result schema missing %s: %#v", field, probeResult)
		}
	}
	if probeResult["commit"] != nil {
		t.Fatalf("probe result schema must match canonical response without commit: %#v", probeResult)
	}
	deployRequest := schemas["DeployGitSourceRequest"].(map[string]any)["properties"].(map[string]any)
	for _, field := range []string{"confirm", "message"} {
		if deployRequest[field] == nil {
			t.Fatalf("deploy request schema missing %s: %#v", field, deployRequest)
		}
	}
	if deployRequest["commit"] != nil {
		t.Fatalf("deploy request must always use the latest synchronized revision: %#v", deployRequest)
	}
	sampleSyncResponse := schemas["SampleSyncResponse"].(map[string]any)
	sampleSyncProperties := sampleSyncResponse["properties"].(map[string]any)
	if sampleSyncProperties["source"] == nil || sampleSyncProperties["sync_result"] == nil {
		t.Fatalf("sample sync response schema properties = %#v", sampleSyncProperties)
	}
	syncResultProperties := schemas["GitSourceSyncResult"].(map[string]any)["properties"].(map[string]any)
	for _, field := range []string{
		"commit", "app", "actions", "runtime", "sync_status", "synced_at", "validation_checks",
	} {
		if syncResultProperties[field] == nil {
			t.Fatalf("sync result schema missing %s: %#v", field, syncResultProperties)
		}
	}
	if syncResultProperties["bundle_status"] != nil || syncResultProperties["bundle_digest"] != nil {
		t.Fatalf("sync result must not expose a deployment artifact: %#v", syncResultProperties)
	}
	deployResultProperties := schemas["GitSourceDeployResult"].(map[string]any)["properties"].(map[string]any)
	for _, field := range []string{"bundle_status", "bundle_digest", "bundle_uri", "runtime", "validation_checks"} {
		if deployResultProperties[field] == nil {
			t.Fatalf("deploy result schema missing %s: %#v", field, deployResultProperties)
		}
	}
	appSummary := schemas["AppSummary"].(map[string]any)["properties"].(map[string]any)
	for _, field := range []string{
		"bundle_status", "bundle_digest", "bundle_uri", "actions_count", "schedules_count", "flows_count",
	} {
		if appSummary[field] == nil {
			t.Fatalf("app summary schema missing %s: %#v", field, appSummary)
		}
	}
	appSchemaProperties := schemas["App"].(map[string]any)["properties"].(map[string]any)
	if appSchemaProperties["effective_route_tag"] != nil {
		t.Fatalf("base App schema must match canonical patch response without effective_route_tag: %#v", appSchemaProperties)
	}
	actionSchema := schemas["Action"].(map[string]any)
	properties := actionSchema["properties"].(map[string]any)
	if properties["effective_capabilities"] != nil || properties["effective_route_tag"] != nil {
		t.Fatalf("base Action schema must match canonical action response without effective fields: %#v", properties)
	}
	assertSchemaRef("Action", actionSchema, "input_schema", "#/components/schemas/Base64JSONSchema")
	assertSchemaRef("Action", actionSchema, "output_schema", "#/components/schemas/Base64JSONSchema")
	appDetail := schemas["AppDetailResponse"].(map[string]any)
	appDetailApp := appDetail["properties"].(map[string]any)["app"].(map[string]any)
	if appDetailApp["$ref"] != "#/components/schemas/AppView" {
		t.Fatalf("app detail app schema = %#v", appDetailApp)
	}
	appDetailActions := appDetail["properties"].(map[string]any)["actions"].(map[string]any)
	if appDetailActions["items"].(map[string]any)["$ref"] != "#/components/schemas/AppAction" {
		t.Fatalf("app detail actions schema = %#v", appDetailActions)
	}
	for _, schemaName := range []string{
		"JSONValue",
		"PathResponse",
		"Variable",
		"SetVariableRequest",
		"VariableValueResponse",
		"SetResourceRequest",
		"JobInput",
		"JobHandleResponse",
		"JobPendingResponse",
		"JobWaitResultResponse",
		"JobResultResponse",
		"JobStatus",
		"JobListItem",
		"JobListResponse",
		"JobSummary",
		"CancelJobRequest",
		"CancelResult",
	} {
		if schemas[schemaName] == nil {
			t.Fatalf("schema %s missing", schemaName)
		}
	}
	jobStatus := schemas["JobStatus"].(map[string]any)["properties"].(map[string]any)
	for _, field := range []string{"input_schema", "output_schema", "canceled_by", "canceled_reason", "flow_run_id", "flow_key", "flow_step_key"} {
		if jobStatus[field] == nil {
			t.Fatalf("job status schema missing %s: %#v", field, jobStatus)
		}
	}
	assertSchemaRef("JobStatus", schemas["JobStatus"].(map[string]any), "input_schema", "#/components/schemas/JSONSchema")
	assertSchemaRef("JobStatus", schemas["JobStatus"].(map[string]any), "output_schema", "#/components/schemas/JSONSchema")
	jobListItem := schemas["JobListItem"].(map[string]any)["properties"].(map[string]any)
	for _, field := range []string{"flow_run_id", "flow_step_id"} {
		if jobListItem[field] == nil {
			t.Fatalf("job list item schema missing %s: %#v", field, jobListItem)
		}
	}
	jobLogs := paths["/api/w/{workspace}/jobs/{jobId}/logs"].(map[string]any)["get"].(map[string]any)
	logContent := jobLogs["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)
	if logContent["text/plain"] == nil {
		t.Fatalf("job logs response must be text/plain: %#v", logContent)
	}
}

func TestCanonicalControlPlaneNotFoundMessagesMatchCanonicalAPI(t *testing.T) {
	tempDir := t.TempDir()
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	if err := fileCatalog.UpsertDeployment(context.Background(), contract.Deployment{
		Workspace:   "ws-a",
		GitSourceID: "1",
		App:         "echo",
		Entrypoint:  "main.ts",
		Commit:      "commit-a",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo"},
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

	for _, tc := range []struct {
		method string
		path   string
		body   string
		want   string
	}{
		{http.MethodGet, "/api/w/ws-a/apps/missing", "", "app not found"},
		{http.MethodGet, "/api/w/ws-a/apps/missing/source", "", "app not found"},
		{http.MethodGet, "/api/w/ws-a/apps/missing/openapi.json", "", "app not found"},
		{http.MethodPatch, "/api/w/ws-a/apps/missing", `{"tag_override":null}`, "app not found"},
		{http.MethodPost, "/api/w/ws-a/apps/missing/requeue", `{}`, "app not found"},
		{http.MethodGet, "/api/w/ws-a/apps/missing/actions/echo", "", "action not found"},
		{http.MethodGet, "/api/w/ws-a/apps/missing/actions/echo/schema", "", "action not found"},
		{http.MethodPatch, "/api/w/ws-a/apps/missing/actions/echo", `{"tag_override":null}`, "action not found"},
		{http.MethodGet, "/api/w/ws-a/apps/echo/actions/missing", "", "action not found"},
		{http.MethodGet, "/api/w/ws-a/apps/echo/actions/missing/schema", "", "action not found"},
		{http.MethodPatch, "/api/w/ws-a/apps/echo/actions/missing", `{"tag_override":null}`, "action not found"},
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
		if resp.StatusCode != http.StatusNotFound || body.Error != tc.want {
			t.Fatalf("%s %s = %d %#v, want 404 %q", tc.method, tc.path, resp.StatusCode, body, tc.want)
		}
	}
}

func TestLegacyV1ControlPlaneRoutesAreNotExposed(t *testing.T) {
	tempDir := t.TempDir()
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	server := httptest.NewServer(New(Config{
		Store:      state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		Catalog:    fileCatalog,
		Syncer:     &syncer.Syncer{Store: bundle.NewLocalStore(filepath.Join(tempDir, "store"))},
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
		Store:   state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		Catalog: fileCatalog,
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
		decoded := decodeCatalogSchema(t, schema)
		if !bytes.Equal(bytes.TrimSpace(decoded), []byte(`{}`)) {
			t.Fatalf("%s = %s decoded=%s, want {}", field, schema, decoded)
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
	inputSchema := decodeCatalogSchema(t, actionBody.InputSchema)
	outputSchema := decodeCatalogSchema(t, actionBody.OutputSchema)
	if !bytes.Contains(inputSchema, []byte(`"message"`)) ||
		!bytes.Contains(outputSchema, []byte(`"ok"`)) {
		t.Fatalf("action schemas = input:%s decoded:%s output:%s decoded:%s", actionBody.InputSchema, inputSchema, actionBody.OutputSchema, outputSchema)
	}
}

func TestCanonicalActionSourceFallbackUsesManifestSchemaPathVerbatim(t *testing.T) {
	tempDir := t.TempDir()
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	sourceStore := bundle.NewLocalStore(filepath.Join(tempDir, "store"))
	sourceDir := filepath.Join(tempDir, "source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "input.schema.json"), []byte(`{"type":"object"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sourceStore.Materialize(context.Background(), "ws-a", "1", "commit-a", sourceDir); err != nil {
		t.Fatal(err)
	}
	if err := fileCatalog.UpsertDeployment(context.Background(), contract.Deployment{
		Workspace:   "ws-a",
		GitSourceID: "1",
		App:         "echo",
		Commit:      "commit-a",
		Actions: map[string]contract.Action{
			"echo": {
				Action:      "echo",
				InputSchema: " input.schema.json ",
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(New(Config{
		Catalog:   fileCatalog,
		Syncer:    &syncer.Syncer{Store: sourceStore},
		EnableAPI: true,
	}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/w/ws-a/apps/echo/actions/echo")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("action status = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.Error, `manifest references schema " input.schema.json " but the file is missing`) {
		t.Fatalf("error = %q", body.Error)
	}
}

func TestCanonicalControlPlaneUsesMaterializedActionSchemas(t *testing.T) {
	tempDir := t.TempDir()
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	inputSchema := json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}}}`)
	outputSchema := json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}}}`)
	operatorSettingsSchema := json.RawMessage(`{"type":"object","properties":{"REGION":{"type":"string","enum":["kr","us"]}}}`)
	if err := fileCatalog.UpsertDeployment(context.Background(), contract.Deployment{
		Workspace:    "ws-a",
		GitSourceID:  "1",
		App:          "echo",
		Commit:       "commit-a",
		Entrypoint:   "main.ts",
		BundleDigest: testExecutionBundleDigest,
		Actions: map[string]contract.Action{
			"echo": {
				Action:                     "echo",
				InputSchema:                "input.schema.json",
				OutputSchema:               "output.schema.json",
				OperatorSettingsSchema:     "operator-settings.schema.json",
				InputSchemaBody:            inputSchema,
				OutputSchemaBody:           outputSchema,
				OperatorSettingsSchemaBody: operatorSettingsSchema,
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
	actionInputSchema := decodeCatalogSchema(t, actionBody.InputSchema)
	actionOutputSchema := decodeCatalogSchema(t, actionBody.OutputSchema)
	if !bytes.Contains(actionInputSchema, []byte(`"message"`)) ||
		!bytes.Contains(actionOutputSchema, []byte(`"ok"`)) {
		t.Fatalf("action schemas = input:%s decoded:%s output:%s decoded:%s", actionBody.InputSchema, actionInputSchema, actionBody.OutputSchema, actionOutputSchema)
	}

	schemaResp, err := http.Get(server.URL + "/api/w/ws-a/apps/echo/actions/echo/schema")
	if err != nil {
		t.Fatal(err)
	}
	defer schemaResp.Body.Close()
	if schemaResp.StatusCode != http.StatusOK {
		t.Fatalf("schema status = %d, want %d", schemaResp.StatusCode, http.StatusOK)
	}
	var schemaBody struct {
		WorkspaceID            string          `json:"workspace_id"`
		AppKey                 string          `json:"app_key"`
		ActionKey              string          `json:"action_key"`
		InputSchema            json.RawMessage `json:"input_schema"`
		OutputSchema           json.RawMessage `json:"output_schema"`
		OperatorSettingsSchema json.RawMessage `json:"operator_settings_schema"`
	}
	if err := json.NewDecoder(schemaResp.Body).Decode(&schemaBody); err != nil {
		t.Fatal(err)
	}
	if schemaBody.WorkspaceID != "ws-a" || schemaBody.AppKey != "echo" || schemaBody.ActionKey != "echo" {
		t.Fatalf("schema identity = %#v", schemaBody)
	}
	if !bytes.Contains(schemaBody.InputSchema, []byte(`"message"`)) ||
		!bytes.Contains(schemaBody.OutputSchema, []byte(`"ok"`)) ||
		!bytes.Contains(schemaBody.OperatorSettingsSchema, []byte(`"REGION"`)) {
		t.Fatalf("raw action schemas = input:%s output:%s operator settings:%s", schemaBody.InputSchema, schemaBody.OutputSchema, schemaBody.OperatorSettingsSchema)
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

	runReq, err := http.NewRequest(http.MethodPost, server.URL+"/api/w/ws-a/jobs/run/echo/echo", bytes.NewBufferString(`{"message":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	runReq.Header.Set("Content-Type", "application/json")
	runReq.Header.Set("X-Windforce-Actor", "runner@example.test")
	runResp, err := http.DefaultClient.Do(runReq)
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
		CreatedBy      string          `json:"created_by"`
		PermissionedAs string          `json:"permissioned_as"`
	}
	if err := json.NewDecoder(statusResp.Body).Decode(&statusBody); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(statusBody.InputSchema, []byte(`"message"`)) ||
		!bytes.Contains(statusBody.OutputSchema, []byte(`"ok"`)) {
		t.Fatalf("job schemas = input:%s output:%s", statusBody.InputSchema, statusBody.OutputSchema)
	}
	if statusBody.CreatedBy != "runner@example.test" || statusBody.PermissionedAs != "runner@example.test" {
		t.Fatalf("job actor = %q/%q, want runner@example.test", statusBody.CreatedBy, statusBody.PermissionedAs)
	}
}

func TestCanonicalSampleGitSourceRegistersAndSyncs(t *testing.T) {
	tempDir := t.TempDir()
	store := bundle.NewLocalStore(filepath.Join(tempDir, "store"))
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	handler := New(Config{
		Store:            state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		Catalog:          fileCatalog,
		Syncer:           &syncer.Syncer{Store: store, CloneRoot: tempDir},
		ExecutionBundles: readyExecutionBundleManager(),
		GitSources:       gitsource.NewFileRegistry(filepath.Join(tempDir, "git-sources.json")),
		EnableAPI:        true,
		SampleRoot:       filepath.Join(tempDir, "samples"),
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
	sampleInputSchema := decodeCatalogSchema(t, actionBody.InputSchema)
	if !bytes.Contains(sampleInputSchema, []byte(`"message"`)) {
		t.Fatalf("sample input schema = %s decoded=%s", actionBody.InputSchema, sampleInputSchema)
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

func TestCanonicalRegisterGitSourceRejectsInvalidSubpathBeforePersisting(t *testing.T) {
	tempDir := t.TempDir()
	repoDir := createTestGitSourceRepo(t, tempDir, "repo", "")
	registry := gitsource.NewFileRegistry(filepath.Join(tempDir, "git-sources.json"))
	handler := New(Config{
		Syncer:     &syncer.Syncer{CloneRoot: tempDir},
		GitSources: registry,
		EnableAPI:  true,
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	registerResp, err := http.Post(server.URL+"/api/w/ws-a/git_sources", "application/json", bytes.NewBufferString(`{
		"name": "source-a",
		"repo_url": "`+filepath.ToSlash(repoDir)+`",
		"branch": "main",
		"subpath": "/apps/echo"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	defer registerResp.Body.Close()
	if registerResp.StatusCode != http.StatusUnprocessableEntity {
		body, _ := io.ReadAll(registerResp.Body)
		t.Fatalf("register status = %d, want %d: %s", registerResp.StatusCode, http.StatusUnprocessableEntity, body)
	}
	snapshot, err := registry.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Sources) != 0 {
		t.Fatalf("registered sources = %#v, want none", snapshot.Sources)
	}
}

func TestCanonicalRegisterGitSourcePreservesValidSubpath(t *testing.T) {
	tempDir := t.TempDir()
	repoDir := createTestGitSourceRepo(t, tempDir, "repo", "apps/echo")
	handler := New(Config{
		Syncer:     &syncer.Syncer{CloneRoot: tempDir},
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
		body, _ := io.ReadAll(registerResp.Body)
		t.Fatalf("register status = %d, want %d: %s", registerResp.StatusCode, http.StatusCreated, body)
	}
	var registered struct {
		Subpath string `json:"subpath"`
	}
	if err := json.NewDecoder(registerResp.Body).Decode(&registered); err != nil {
		t.Fatal(err)
	}
	if registered.Subpath != "apps/echo" {
		t.Fatalf("subpath = %q, want raw value", registered.Subpath)
	}
}

func TestCanonicalRegisterGitSourceRequiresExistingBranch(t *testing.T) {
	tempDir := t.TempDir()
	repoDir := createTestGitSourceRepo(t, tempDir, "repo", "")
	registry := gitsource.NewFileRegistry(filepath.Join(tempDir, "git-sources.json"))
	handler := New(Config{
		Syncer:     &syncer.Syncer{CloneRoot: tempDir},
		GitSources: registry,
		EnableAPI:  true,
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	registerBody, err := json.Marshal(map[string]string{
		"name":     "source-a",
		"repo_url": filepath.ToSlash(repoDir),
		"branch":   "missing",
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(server.URL+"/api/w/ws-a/git_sources", "application/json", bytes.NewReader(registerBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("register status = %d, want %d: %s", resp.StatusCode, http.StatusUnprocessableEntity, body)
	}
	snapshot, err := registry.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Sources) != 0 {
		t.Fatalf("registered sources = %#v, want none", snapshot.Sources)
	}
}

func TestCanonicalRegisterGitSourceValidatesManifestBeforePersisting(t *testing.T) {
	tempDir := t.TempDir()
	repoDir := createTestGitSourceRepo(t, tempDir, "repo", "")
	if err := os.WriteFile(filepath.Join(repoDir, "windforce.json"), []byte(`{"app":"bad-app","actions":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repoDir, "add", "windforce.json")
	runTestGit(t, repoDir, "commit", "-m", "invalid manifest")

	registry := gitsource.NewFileRegistry(filepath.Join(tempDir, "git-sources.json"))
	handler := New(Config{
		Syncer:     &syncer.Syncer{CloneRoot: tempDir},
		GitSources: registry,
		EnableAPI:  true,
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	registerBody, err := json.Marshal(map[string]string{
		"name":     "source-a",
		"repo_url": filepath.ToSlash(repoDir),
		"branch":   "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(server.URL+"/api/w/ws-a/git_sources", "application/json", bytes.NewReader(registerBody))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("register status = %d, want %d: %s", resp.StatusCode, http.StatusUnprocessableEntity, body)
	}
	snapshot, err := registry.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Sources) != 0 {
		t.Fatalf("registered sources = %#v, want none", snapshot.Sources)
	}
}

func TestCanonicalRegisterGitSourceRejectsCredentialFromRequest(t *testing.T) {
	tempDir := t.TempDir()
	repoDir := createTestGitSourceRepo(t, tempDir, "repo", "")
	store := state.NewLocalStore(filepath.Join(tempDir, "state.json"))
	registry := gitsource.NewFileRegistry(filepath.Join(tempDir, "git-sources.json"))
	handler := New(Config{
		Store:      store,
		Syncer:     &syncer.Syncer{CloneRoot: tempDir},
		GitSources: registry,
		EnableAPI:  true,
		SecretKey:  "git-source-register-secret",
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	registerResp, err := http.Post(server.URL+"/api/w/ws-a/git_sources", "application/json", bytes.NewBufferString(`{
		"name": "Coupang Eats",
		"repo_url": "`+filepath.ToSlash(repoDir)+`",
		"branch": "main",
		"auth_method": "basic",
		"username": "deploy-user",
		"password": "deploy-token"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	defer registerResp.Body.Close()
	if registerResp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(registerResp.Body)
		t.Fatalf("register status = %d, want %d: %s", registerResp.StatusCode, http.StatusBadRequest, body)
	}
	snapshot, err := registry.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Sources) != 0 {
		t.Fatalf("registered sources = %#v, want none", snapshot.Sources)
	}
	variables, err := store.ListVariables(context.Background(), "ws-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(variables) != 0 {
		t.Fatalf("stored variables = %#v, want none", variables)
	}
}

func TestCanonicalGitSourcesListOrdersByID(t *testing.T) {
	tempDir := t.TempDir()
	repoA := createTestGitSourceRepo(t, tempDir, "repo-a", "")
	repoZ := createTestGitSourceRepo(t, tempDir, "repo-z", "")
	handler := New(Config{
		Syncer:     &syncer.Syncer{CloneRoot: tempDir},
		GitSources: gitsource.NewFileRegistry(filepath.Join(tempDir, "git-sources.json")),
		EnableAPI:  true,
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	for _, body := range []string{
		`{"name":"z-source","repo_url":"` + filepath.ToSlash(repoZ) + `"}`,
		`{"name":"a-source","repo_url":"` + filepath.ToSlash(repoA) + `"}`,
	} {
		resp, err := http.Post(server.URL+"/api/w/ws-a/git_sources", "application/json", bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusCreated {
			data, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			t.Fatalf("register status = %d, want %d: %s", resp.StatusCode, http.StatusCreated, data)
		}
		_ = resp.Body.Close()
	}

	resp, err := http.Get(server.URL + "/api/w/ws-a/git_sources")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var sources []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&sources); err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 ||
		sources[0].ID != 1 || sources[0].Name != "z-source" ||
		sources[1].ID != 2 || sources[1].Name != "a-source" {
		t.Fatalf("sources = %#v, want id order", sources)
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
	if err := os.WriteFile(filepath.Join(repoDir, "input.schema.json"), []byte(`{"title":"Echo message","type":"object","properties":{"message":{"type":"string"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "output.schema.json"), []byte(`{"type":"object","properties":{"ok":{"type":"boolean"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# Echo\n\nReleased documentation.\n"), 0o644); err != nil {
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
	stateStore := state.NewLocalStore(filepath.Join(tempDir, "state.json"))
	handler := New(Config{
		Store:            stateStore,
		Catalog:          fileCatalog,
		Syncer:           &syncer.Syncer{Store: bundle.NewLocalStore(filepath.Join(tempDir, "store")), CloneRoot: tempDir},
		ExecutionBundles: readyExecutionBundleManager(),
		GitSources:       gitsource.NewFileRegistry(filepath.Join(tempDir, "git-sources.json")),
		EnableAPI:        true,
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	invalidAppResp, err := http.Get(server.URL + "/api/w/ws-a/apps/bad-app")
	if err != nil {
		t.Fatal(err)
	}
	defer invalidAppResp.Body.Close()
	if invalidAppResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid app status = %d, want %d", invalidAppResp.StatusCode, http.StatusBadRequest)
	}
	invalidRunResp, err := http.Post(server.URL+"/api/w/ws-a/jobs/run/bad-app/echo", "application/json", bytes.NewBufferString(`{}`))
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

	inlineCredRegisterResp, err := http.Post(server.URL+"/api/w/ws-a/git_sources", "application/json", bytes.NewBufferString(`{
		"name": "source-inline-credential",
		"repo_url": "`+filepath.ToSlash(repoDir)+`",
		"branch": "main",
		"auth_method": "token",
		"access_token": "secret-token"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	defer inlineCredRegisterResp.Body.Close()
	if inlineCredRegisterResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("inline credential register status = %d, want %d", inlineCredRegisterResp.StatusCode, http.StatusBadRequest)
	}
	if _, found, err := stateStore.GetVariableExact(context.Background(), "ws-a", "", "git/source-inline-credential/credential"); err != nil || found {
		t.Fatalf("inline credential register stored variable found=%v err=%v", found, err)
	}

	registerBody, err := json.Marshal(map[string]string{
		"name":      "source-a",
		"repo_url":  filepath.ToSlash(repoDir),
		"branch":    "main",
		"creds_ref": "secrets/git/token",
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
		registered.RepoURL != filepath.ToSlash(repoDir) || registered.CredsRef != "secrets/git/token" ||
		registered.CreatedAt.IsZero() {
		t.Fatalf("registered source = %#v", registered)
	}
	registeredID := fmt.Sprint(registered.ID)

	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/api/w/ws-a/git_sources/source-a/sync", ""},
		{http.MethodPost, "/api/w/ws-a/git_sources/source-a/deploy", ""},
		{http.MethodPatch, "/api/w/ws-a/git_sources/source-a", `{}`},
		{http.MethodDelete, "/api/w/ws-a/git_sources/source-a", ""},
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
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || body.Error != "bad git source id" {
			t.Fatalf("%s %s = %d %#v, want 400 bad git source id", tc.method, tc.path, resp.StatusCode, body)
		}
	}

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

	unconfirmedResp, err := http.Post(server.URL+"/api/w/ws-a/git_sources/"+registeredID+"/deploy", "application/json", bytes.NewBufferString(`{"confirm":false}`))
	if err != nil {
		t.Fatal(err)
	}
	defer unconfirmedResp.Body.Close()
	if unconfirmedResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unconfirmed deploy status = %d, want %d", unconfirmedResp.StatusCode, http.StatusBadRequest)
	}
	var unconfirmedBody struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(unconfirmedResp.Body).Decode(&unconfirmedBody); err != nil {
		t.Fatal(err)
	}
	if unconfirmedBody.Error != "deploy confirmation is required" {
		t.Fatalf("unconfirmed deploy error = %#v", unconfirmedBody)
	}

	commitSelectionResp, err := http.Post(server.URL+"/api/w/ws-a/git_sources/"+registeredID+"/deploy", "application/json", bytes.NewBufferString(`{"confirm":true,"commit":"stale-commit"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer commitSelectionResp.Body.Close()
	if commitSelectionResp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(commitSelectionResp.Body)
		t.Fatalf("commit-selecting deploy status = %d, want %d: %s", commitSelectionResp.StatusCode, http.StatusBadRequest, body)
	}
	var pinnedBody struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(commitSelectionResp.Body).Decode(&pinnedBody); err != nil {
		t.Fatal(err)
	}
	if pinnedBody.Error != "deploy always uses the latest synchronized revision; omit commit" {
		t.Fatalf("commit-selecting deploy error = %#v", pinnedBody)
	}

	missingActorResp, err := http.Post(server.URL+"/api/w/ws-a/git_sources/"+registeredID+"/deploy", "application/json", bytes.NewBufferString(`{"confirm":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer missingActorResp.Body.Close()
	if missingActorResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing actor deploy status = %d, want %d", missingActorResp.StatusCode, http.StatusBadRequest)
	}
	var missingActorBody struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(missingActorResp.Body).Decode(&missingActorBody); err != nil {
		t.Fatal(err)
	}
	if missingActorBody.Error != "deploy actor is required" {
		t.Fatalf("missing actor deploy error = %#v", missingActorBody)
	}

	missingCandidateReq, err := http.NewRequest(http.MethodPost, server.URL+"/api/w/ws-a/git_sources/"+registeredID+"/deploy", bytes.NewBufferString(`{"confirm":true}`))
	if err != nil {
		t.Fatal(err)
	}
	missingCandidateReq.Header.Set("Content-Type", "application/json")
	missingCandidateReq.Header.Set("X-Windforce-Actor", "deployer@example.test")
	missingCandidateResp, err := http.DefaultClient.Do(missingCandidateReq)
	if err != nil {
		t.Fatal(err)
	}
	defer missingCandidateResp.Body.Close()
	if missingCandidateResp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(missingCandidateResp.Body)
		t.Fatalf("deploy before sync status = %d, want %d: %s", missingCandidateResp.StatusCode, http.StatusConflict, body)
	}

	stageResp, err := http.Post(server.URL+"/api/w/ws-a/git_sources/"+registeredID+"/sync", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stageResp.Body.Close()
	if stageResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(stageResp.Body)
		t.Fatalf("sync status = %d, want %d: %s", stageResp.StatusCode, http.StatusOK, body)
	}
	var staged struct {
		Commit           string    `json:"commit"`
		App              string    `json:"app"`
		Actions          []string  `json:"actions"`
		Runtime          string    `json:"runtime"`
		SyncStatus       string    `json:"sync_status"`
		SyncedAt         time.Time `json:"synced_at"`
		ValidationChecks []string  `json:"validation_checks"`
	}
	if err := json.NewDecoder(stageResp.Body).Decode(&staged); err != nil {
		t.Fatal(err)
	}
	if staged.Commit == "" || staged.App != "echo" || len(staged.Actions) != 1 || staged.Actions[0] != "echo.echo" {
		t.Fatalf("staged candidate = %#v", staged)
	}
	if staged.SyncStatus != "synced" || staged.SyncedAt.IsZero() || staged.Runtime != "typescript" || len(staged.ValidationChecks) != 4 {
		t.Fatalf("synchronized source = %#v", staged)
	}
	if _, err := fileCatalog.GetDeploymentForWorkspace(context.Background(), "ws-a", "echo"); !errors.Is(err, catalog.ErrDeploymentNotFound) {
		t.Fatalf("sync activated deployment: %v", err)
	}
	if candidate, err := fileCatalog.GetReleaseCandidate(context.Background(), "ws-a", registeredID, staged.Commit); err != nil || candidate.Deployment.Commit != staged.Commit || candidate.Deployment.BundleDigest != "" {
		t.Fatalf("staged candidate lookup = %#v, err=%v", candidate, err)
	}

	deployBody := bytes.NewBufferString(`{"confirm":true,"message":"audit note"}`)
	deployReq, err := http.NewRequest(http.MethodPost, server.URL+"/api/w/ws-a/git_sources/"+registeredID+"/deploy", deployBody)
	if err != nil {
		t.Fatal(err)
	}
	deployReq.Header.Set("Content-Type", "application/json")
	deployReq.Header.Set("X-Windforce-Actor", "deployer@example.test")
	syncResp, err := http.DefaultClient.Do(deployReq)
	if err != nil {
		t.Fatal(err)
	}
	defer syncResp.Body.Close()
	if syncResp.StatusCode != http.StatusOK {
		t.Fatalf("deploy status = %d, want %d", syncResp.StatusCode, http.StatusOK)
	}
	var syncBody struct {
		Commit       string   `json:"commit"`
		App          string   `json:"app"`
		Actions      []string `json:"actions"`
		Source       string   `json:"source"`
		DeploymentID *string  `json:"deployment_id"`
		CreatedBy    *string  `json:"created_by"`
		Message      *string  `json:"message"`
		BundleStatus string   `json:"bundle_status"`
		BundleDigest string   `json:"bundle_digest"`
	}
	if err := json.NewDecoder(syncResp.Body).Decode(&syncBody); err != nil {
		t.Fatal(err)
	}
	if syncBody.Commit == "" || syncBody.App != "echo" || len(syncBody.Actions) != 1 || syncBody.Actions[0] != "echo.echo" {
		t.Fatalf("sync body = %#v", syncBody)
	}
	if syncBody.Source != "deploy" || syncBody.DeploymentID == nil || *syncBody.DeploymentID == "" ||
		syncBody.CreatedBy == nil || *syncBody.CreatedBy != "deployer@example.test" ||
		syncBody.Message == nil || *syncBody.Message != "audit note" ||
		syncBody.BundleStatus != "ready" || syncBody.BundleDigest == "" {
		t.Fatalf("deploy audit body = %#v", syncBody)
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
		summary.Apps[0].EffectiveRouteTag != "default" || !reflect.DeepEqual(summary.Apps[0].Capabilities, []string{"browser"}) {
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
		DisplayName  string          `json:"display_name"`
		InputSchema  json.RawMessage `json:"input_schema"`
		OutputSchema json.RawMessage `json:"output_schema"`
		TimeoutS     *int32          `json:"timeout_s"`
		UpdatedAt    time.Time       `json:"updated_at"`
	}
	if err := json.NewDecoder(actionResp.Body).Decode(&actionBody); err != nil {
		t.Fatal(err)
	}
	actionInputSchema := decodeCatalogSchema(t, actionBody.InputSchema)
	actionOutputSchema := decodeCatalogSchema(t, actionBody.OutputSchema)
	if actionBody.AppKey != "echo" || actionBody.ActionKey != "echo" || actionBody.DisplayName != "Echo message" ||
		actionBody.TimeoutS != nil || actionBody.UpdatedAt.IsZero() ||
		!bytes.Contains(actionInputSchema, []byte(`"message"`)) || !bytes.Contains(actionOutputSchema, []byte(`"ok"`)) {
		t.Fatalf("action body = %#v input=%s decoded=%s output=%s decoded=%s", actionBody, actionBody.InputSchema, actionInputSchema, actionBody.OutputSchema, actionOutputSchema)
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
			DisplayName           string          `json:"display_name"`
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
		!reflect.DeepEqual(appBody.App.RequiredCapabilities, []string{"browser"}) || appBody.App.EffectiveRouteTag != "default" ||
		appBody.App.UpdatedAt.IsZero() ||
		len(appBody.Actions) != 1 || appBody.Actions[0].ActionKey != "echo" || appBody.Actions[0].DisplayName != "Echo message" ||
		!reflect.DeepEqual(appBody.Actions[0].EffectiveCapabilities, []string{"browser"}) || appBody.Actions[0].EffectiveRouteTag != "default" {
		t.Fatalf("app body = %#v", appBody)
	}
	appActionInputSchema := decodeCatalogSchema(t, appBody.Actions[0].InputSchema)
	if !bytes.Contains(appActionInputSchema, []byte(`"message"`)) {
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
	// Labels no longer synthesize route tags: the capability app routes on
	// its default tag and its labels are matched separately at claim time.
	if len(workerTags.Tags) != 1 || !seenTags["default"] || seenTags["browser"] {
		t.Fatalf("worker tags = %#v, want only default", workerTags.Tags)
	}

	runReq, err := http.NewRequest(http.MethodPost, server.URL+"/api/w/ws-a/jobs/run/echo/echo", bytes.NewBufferString(`{"message":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	runReq.Header.Set("Content-Type", "application/json")
	runReq.Header.Set("X-Windforce-Actor", "runner@example.test")
	runResp, err := http.DefaultClient.Do(runReq)
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
		statusBody.Tag != "default" ||
		statusBody.CreatedBy != "runner@example.test" ||
		statusBody.PermissionedAs != "runner@example.test" {
		t.Fatalf("status body = %#v input_schema:%s output_schema:%s input:%s", statusBody, statusBody.InputSchema, statusBody.OutputSchema, statusBody.Input)
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
	resultResponses := paths["/api/w/ws-a/jobs/{id}/result"].(map[string]any)["get"].(map[string]any)["responses"].(map[string]any)
	if resultResponses["401"] == nil || resultResponses["403"] == nil {
		t.Fatalf("openapi result poll must document canonical auth failures: %#v", resultResponses)
	}
	if resultResponses["404"] != nil {
		t.Fatalf("openapi result poll must match canonical app OpenAPI without 404: %#v", resultResponses)
	}
	appResponses := openAPIBody["components"].(map[string]any)["responses"].(map[string]any)
	if appResponses["Conflict"] != nil || appResponses["RequestEntityTooLarge"] != nil {
		t.Fatalf("app openapi responses must match canonical invocation components: %#v", appResponses)
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

	documentationResp, err := http.Get(server.URL + "/api/w/ws-a/apps/echo/documentation")
	if err != nil {
		t.Fatal(err)
	}
	defer documentationResp.Body.Close()
	if documentationResp.StatusCode != http.StatusOK {
		t.Fatalf("documentation status = %d, want %d", documentationResp.StatusCode, http.StatusOK)
	}
	var documentationBody struct {
		AppKey    string `json:"app_key"`
		CommitSHA string `json:"commit_sha"`
		Available bool   `json:"available"`
		Path      string `json:"path"`
		Markdown  string `json:"markdown"`
	}
	if err := json.NewDecoder(documentationResp.Body).Decode(&documentationBody); err != nil {
		t.Fatal(err)
	}
	if documentationBody.AppKey != "echo" || documentationBody.CommitSHA != syncBody.Commit || !documentationBody.Available ||
		documentationBody.Path != "README.md" || documentationBody.Markdown != "# Echo\n\nReleased documentation.\n" {
		t.Fatalf("documentation body = %#v", documentationBody)
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
		history[0]["source"] != "deploy" || history[0]["created_by"] != "deployer@example.test" ||
		history[0]["deployment_id"] == "" || history[0]["message"] != "audit note" {
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
	if deploymentError["error"] != "not found" {
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
	resyncedSourcesResp, err := http.Get(server.URL + "/api/w/ws-a/git_sources")
	if err != nil {
		t.Fatal(err)
	}
	defer resyncedSourcesResp.Body.Close()
	var resyncedSources []struct {
		LastSyncedCommit *string `json:"last_synced_commit"`
	}
	if err := json.NewDecoder(resyncedSourcesResp.Body).Decode(&resyncedSources); err != nil {
		t.Fatal(err)
	}
	if len(resyncedSources) != 1 || resyncedSources[0].LastSyncedCommit == nil ||
		*resyncedSources[0].LastSyncedCommit != resyncBody.Commit {
		t.Fatalf("resynced sources = %#v, want latest synchronized commit %q", resyncedSources, resyncBody.Commit)
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
		pinned.Tag != "default" ||
		!bytes.Contains(pinned.InputSchema, []byte(`"message"`)) ||
		bytes.Contains(pinned.InputSchema, []byte(`"changed"`)) {
		t.Fatalf("queued job pins changed after resync: %#v input_schema=%s", pinned, pinned.InputSchema)
	}
}

func TestCanonicalAppHistoryPreservesDeploymentMetadata(t *testing.T) {
	tempDir := t.TempDir()
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	deploymentID := "11111111-1111-4111-8111-111111111111"
	message := "deployed through control plane"
	createdBy := "deployer@example.test"
	createdAt := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	snapshot := catalog.Snapshot{
		History: []catalog.DeploymentHistory{{
			ID:           "22222222-2222-4222-8222-222222222222",
			Workspace:    "ws-a",
			App:          "echo",
			Commit:       "commit-a",
			Entrypoint:   "main.ts",
			Source:       "deploy",
			DeploymentID: &deploymentID,
			Message:      &message,
			CreatedBy:    &createdBy,
			CreatedAt:    createdAt,
		}},
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileCatalog.Path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(New(Config{Catalog: fileCatalog, EnableAPI: true}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/w/ws-a/apps/echo/history")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("history status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var history []struct {
		ID           string    `json:"id"`
		CommitSha    string    `json:"commit_sha"`
		Entrypoint   string    `json:"entrypoint"`
		Source       string    `json:"source"`
		DeploymentID *string   `json:"deployment_id"`
		Message      *string   `json:"message"`
		CreatedBy    *string   `json:"created_by"`
		CreatedAt    time.Time `json:"created_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&history); err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].ID == "" || history[0].CommitSha != "commit-a" ||
		history[0].Entrypoint != "main.ts" || history[0].Source != "deploy" ||
		history[0].DeploymentID == nil || *history[0].DeploymentID != deploymentID ||
		history[0].Message == nil || *history[0].Message != message ||
		history[0].CreatedBy == nil || *history[0].CreatedBy != createdBy ||
		!history[0].CreatedAt.Equal(createdAt) {
		t.Fatalf("history = %#v", history)
	}
}

func TestCanonicalGitSourceSyncReturnsConflictWhenOperationInProgress(t *testing.T) {
	tempDir := t.TempDir()
	registry := gitsource.NewFileRegistry(filepath.Join(tempDir, "git-sources.json"))
	source, err := registry.Create(context.Background(), gitsource.Source{
		Workspace: "ws-a",
		Name:      "source-a",
		RepoURL:   "https://example.invalid/repo.git",
		Branch:    "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := New(Config{
		Syncer:     &syncer.Syncer{},
		GitSources: registry,
		EnableAPI:  true,
	}).(*Handler)
	_, release, err := handler.acquireGitSourceOperation(context.Background(), "ws-a", source)
	if err != nil {
		t.Fatalf("initial operation lock was not acquired: %v", err)
	}
	defer release()
	server := httptest.NewServer(handler)
	defer server.Close()

	resp, err := http.Post(server.URL+"/api/w/ws-a/git_sources/"+source.ID+"/sync", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusConflict || body.Error != "git source operation already in progress" {
		t.Fatalf("sync conflict response = %d %#v, want 409 operation in progress", resp.StatusCode, body)
	}
}

func TestCanonicalGitSourceSyncReturnsRegistryErrors(t *testing.T) {
	server := httptest.NewServer(New(Config{
		Syncer:     &syncer.Syncer{},
		GitSources: failingGitSourceRegistry{getErr: errors.New("registry unavailable")},
		EnableAPI:  true,
	}))
	defer server.Close()

	resp, err := http.Post(server.URL+"/api/w/ws-a/git_sources/1/sync", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusInternalServerError || body.Error != "registry unavailable" {
		t.Fatalf("sync registry error response = %d %#v, want 500 registry unavailable", resp.StatusCode, body)
	}

	server = httptest.NewServer(New(Config{
		Syncer:     &syncer.Syncer{},
		GitSources: failingGitSourceRegistry{getErr: gitsource.ErrGitSourceNotFound},
		EnableAPI:  true,
	}))
	defer server.Close()

	resp, err = http.Post(server.URL+"/api/w/ws-a/git_sources/1/sync", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound || body.Error != "git source not found" {
		t.Fatalf("sync missing registry response = %d %#v, want 404 git source not found", resp.StatusCode, body)
	}
}

func TestCanonicalAppMaterializationErrors(t *testing.T) {
	for _, tc := range []struct {
		name       string
		store      failingBundleStore
		wantStatus int
		wantError  string
	}{
		{
			name:       "missing materialized source",
			store:      failingBundleStore{exists: false},
			wantStatus: http.StatusNotFound,
			wantError:  "source commit is not materialized \u2014 re-sync the app",
		},
		{
			name:       "exists failure",
			store:      failingBundleStore{existsErr: errors.New("stat failed")},
			wantStatus: http.StatusInternalServerError,
			wantError:  "stat failed",
		},
		{
			name:       "fetch failure",
			store:      failingBundleStore{exists: true, fetchErr: errors.New("copy failed")},
			wantStatus: http.StatusInternalServerError,
			wantError:  "copy failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tempDir := t.TempDir()
			fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
			if err := fileCatalog.UpsertDeployment(context.Background(), contract.Deployment{
				Workspace:   "ws-a",
				GitSourceID: "1",
				App:         "echo",
				Entrypoint:  "main.ts",
				Commit:      "commit-a",
				Actions: map[string]contract.Action{
					"echo": {Action: "echo"},
				},
			}); err != nil {
				t.Fatal(err)
			}

			server := httptest.NewServer(New(Config{
				Catalog:   fileCatalog,
				Syncer:    &syncer.Syncer{Store: tc.store},
				EnableAPI: true,
			}))
			defer server.Close()

			for _, endpoint := range []string{"source", "documentation"} {
				resp, err := http.Get(server.URL + "/api/w/ws-a/apps/echo/" + endpoint)
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
				if resp.StatusCode != tc.wantStatus || body.Error != tc.wantError {
					t.Fatalf("%s response = %d %#v, want %d %q", endpoint, resp.StatusCode, body, tc.wantStatus, tc.wantError)
				}
			}
		})
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
	server := httptest.NewServer(New(Config{
		Syncer:     &syncer.Syncer{CloneRoot: tempDir},
		GitSources: registry,
		EnableAPI:  true,
	}))
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
	if _, err := registry.MarkSynced(context.Background(), "ws-a", registeredID, "commit-a", time.Now().UTC()); err != nil {
		t.Fatalf("mark synced: %v", err)
	}

	lockedLocationReq, err := http.NewRequest(
		http.MethodPatch,
		server.URL+"/api/w/ws-a/git_sources/"+registeredID,
		bytes.NewBufferString(`{"repo_url":"`+filepath.ToSlash(filepath.Join(tempDir, "other-repo"))+`"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	lockedLocationReq.Header.Set("Content-Type", "application/json")
	lockedLocationResp, err := http.DefaultClient.Do(lockedLocationReq)
	if err != nil {
		t.Fatal(err)
	}
	defer lockedLocationResp.Body.Close()
	if lockedLocationResp.StatusCode != http.StatusConflict {
		t.Fatalf("locked location patch status = %d, want %d", lockedLocationResp.StatusCode, http.StatusConflict)
	}

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
		"creds_ref": "secrets/git/token",
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
		ID               int64      `json:"id"`
		Name             string     `json:"name"`
		Branch           string     `json:"branch"`
		CredsRef         string     `json:"creds_ref"`
		LastSyncedCommit *string    `json:"last_synced_commit"`
		LastSyncedAt     *time.Time `json:"last_synced_at"`
	}
	if err := json.NewDecoder(patchResp.Body).Decode(&patched); err != nil {
		t.Fatal(err)
	}
	if patched.ID != registered.ID || patched.Name != "source-b" || patched.Branch != "feature" || patched.CredsRef != "secrets/git/token" {
		t.Fatalf("patched = %#v", patched)
	}
	if patched.LastSyncedCommit != nil || patched.LastSyncedAt != nil {
		t.Fatalf("patch should clear sync metadata after repo/ref change: %#v", patched)
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
	ctx := context.Background()
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	deployment := contract.Deployment{
		Workspace:   "ws-a",
		GitSourceID: "source-a",
		App:         "echo",
		Commit:      "commit-a",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo", Command: []string{"helper"}},
		},
	}
	if err := fileCatalog.UpsertDeployment(ctx, deployment); err != nil {
		t.Fatal(err)
	}
	foreignDeployment := contract.Deployment{
		Workspace:   "ws-b",
		GitSourceID: "source-b",
		App:         "other",
		Commit:      "commit-b",
		Actions: map[string]contract.Action{
			"other": {Action: "other", Command: []string{"helper"}},
		},
	}
	if err := fileCatalog.UpsertDeployment(ctx, foreignDeployment); err != nil {
		t.Fatal(err)
	}
	store := state.NewLocalStore(filepath.Join(tempDir, "state.json"))
	run := state.NewRun("windforce", "run-a", "echo", "echo", deployment, json.RawMessage(`{}`))
	job := state.NewActionJob(run, nil)
	job.Payload.Tag = "legacy-queue"
	if err := store.CreateRunAndEnqueue(ctx, run, job); err != nil {
		t.Fatal(err)
	}
	foreignRun := state.NewRun("windforce", "run-b", "other", "other", foreignDeployment, json.RawMessage(`{}`))
	foreignJob := state.NewActionJob(foreignRun, nil)
	foreignJob.Payload.Tag = "foreign-queue"
	if err := store.CreateRunAndEnqueue(ctx, foreignRun, foreignJob); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(New(Config{Store: store, Catalog: fileCatalog, EnableAPI: true}))
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
	seen := map[string]bool{}
	for _, item := range body.Tags {
		seen[item.Tag] = true
		if item.LiveWorkers != 0 || len(item.Capabilities) != 0 || len(item.Workers) != 0 {
			t.Fatalf("worker-tags item = %#v", item)
		}
	}
	if len(body.Tags) != 2 || !seen["default"] || !seen["legacy-queue"] || seen["foreign-queue"] || body.DedicatedTag != nil {
		t.Fatalf("worker-tags body = %#v", body)
	}
}

func TestCanonicalWorkerTagsDoesNotInventDefaultRoute(t *testing.T) {
	tempDir := t.TempDir()
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
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
			Tag string `json:"tag"`
		} `json:"tags"`
		DedicatedTag *string `json:"dedicated_tag"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Tags) != 0 || body.DedicatedTag != nil {
		t.Fatalf("worker-tags body = %#v, want no invented routes", body)
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

	for _, body := range []string{
		`{"tag_override":" app-blue"}`,
		`{"tag_override":"app-blue "}`,
		`{"tag_override":"Has Space"}`,
	} {
		req, err := http.NewRequest(http.MethodPatch, server.URL+"/api/w/ws-a/apps/echo", bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var errorBody struct {
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&errorBody); err != nil {
			_ = resp.Body.Close()
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest ||
			errorBody.Error != "tag_override must be a valid tag (lowercase alphanumeric, _ or -, max 64) or null" {
			t.Fatalf("patch app body %s response = %d %#v, want invalid tag", body, resp.StatusCode, errorBody)
		}
	}

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
	if len(tagsBody.Tags) != 1 || !seenTags["action-fast"] || seenTags["default"] || seenTags["app-blue"] {
		t.Fatalf("worker tags = %#v, want only action-fast", tagsBody.Tags)
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

func TestCanonicalJobRunAllowsTagOverrideWithLabels(t *testing.T) {
	tempDir := t.TempDir()
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	if err := fileCatalog.UpsertDeployment(context.Background(), contract.Deployment{
		Workspace:            "ws-a",
		GitSourceID:          "source-a",
		App:                  "echo",
		Tag:                  contract.DefaultRouteTag,
		Commit:               "commit-a",
		BundleDigest:         testExecutionBundleDigest,
		RequiredCapabilities: []string{"browser"},
		Actions: map[string]contract.Action{
			"echo": {Action: "echo", Command: []string{"helper"}},
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

	runResp, err := http.Post(server.URL+"/api/w/ws-a/jobs/run/echo/echo", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = runResp.Body.Close()
	if runResp.StatusCode != http.StatusCreated {
		t.Fatalf("run status = %d, want %d", runResp.StatusCode, http.StatusCreated)
	}

	patchReq, err := http.NewRequest(http.MethodPatch, server.URL+"/api/w/ws-a/apps/echo", bytes.NewBufferString(`{"tag_override":"app-blue"}`))
	if err != nil {
		t.Fatal(err)
	}
	patchReq.Header.Set("Content-Type", "application/json")
	patchResp, err := http.DefaultClient.Do(patchReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d, want %d", patchResp.StatusCode, http.StatusOK)
	}

	secondResp, err := http.Post(server.URL+"/api/w/ws-a/jobs/run/echo/echo", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = secondResp.Body.Close()
	// Tags and labels are orthogonal claim dimensions (ADR 0009): an explicit
	// tag override no longer conflicts with capability labels.
	if secondResp.StatusCode != http.StatusCreated {
		t.Fatalf("run with tag override and labels = %d, want %d", secondResp.StatusCode, http.StatusCreated)
	}
}

func TestCanonicalDeploymentModelPreservesStoredValues(t *testing.T) {
	if got := canonicalDeploymentEntrypoint(contract.Deployment{Entrypoint: " main.ts "}); got != " main.ts " {
		t.Fatalf("entrypoint = %q, want stored value", got)
	}
	if got := canonicalDeploymentScriptLang(contract.Deployment{}); got != "typescript" {
		t.Fatalf("empty scriptLang = %q, want typescript", got)
	}
	if got := canonicalDeploymentScriptLang(contract.Deployment{ScriptLang: " python "}); got != " python " {
		t.Fatalf("scriptLang = %q, want stored value", got)
	}
}

func TestCanonicalJobRunPinsTagAndRequeueUsesCurrentEffectiveTag(t *testing.T) {
	tempDir := t.TempDir()
	fileCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	if err := fileCatalog.UpsertDeployment(context.Background(), contract.Deployment{
		Workspace:    "ws-a",
		App:          "echo",
		Tag:          "app-main",
		Commit:       "commit-a",
		BundleDigest: testExecutionBundleDigest,
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

func TestCanonicalRequeueInvalidJSONMatchesCanonicalAPI(t *testing.T) {
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
	server := httptest.NewServer(New(Config{
		Store:     state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		Catalog:   fileCatalog,
		EnableAPI: true,
	}))
	defer server.Close()

	for _, body := range []string{`{`, `"not-object"`} {
		resp, err := http.Post(server.URL+"/api/w/ws-a/apps/echo/requeue", "application/json", bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var response struct {
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusBadRequest || response.Error != "invalid JSON" {
			t.Fatalf("requeue body %q response = %d %#v, want 400 invalid JSON", body, resp.StatusCode, response)
		}
	}
}

func TestCanonicalRequeueRejectsUntrimmedActionKey(t *testing.T) {
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
	server := httptest.NewServer(New(Config{
		Store:     state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		Catalog:   fileCatalog,
		EnableAPI: true,
	}))
	defer server.Close()

	for _, body := range []string{`{"action":" echo"}`, `{"action":"echo "}`} {
		resp, err := http.Post(server.URL+"/api/w/ws-a/apps/echo/requeue", "application/json", bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var response struct {
			Error string `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusBadRequest || response.Error != "invalid action key" {
			t.Fatalf("requeue body %q response = %d %#v, want 400 invalid action key", body, resp.StatusCode, response)
		}
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
		Store:            state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		Catalog:          fileCatalog,
		Syncer:           &syncer.Syncer{Store: store, CloneRoot: tempDir},
		ExecutionBundles: readyExecutionBundleManager(),
		GitSources:       gitsource.NewFileRegistry(filepath.Join(tempDir, "git-sources.json")),
		EnableAPI:        true,
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
	var registered struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(registerResp.Body).Decode(&registered); err != nil {
		t.Fatal(err)
	}

	syncResp, err := http.Post(server.URL+"/api/w/ws-a/git_sources/"+fmt.Sprint(registered.ID)+"/sync", "", nil)
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
	if _, err := fileCatalog.GetDeploymentForWorkspace(context.Background(), "ws-a", "echo"); !errors.Is(err, catalog.ErrDeploymentNotFound) {
		t.Fatalf("sync activated deployment: %v", err)
	}
	deployment, err := fileCatalog.GetReleaseCandidate(context.Background(), "ws-a", fmt.Sprint(registered.ID), syncBody.Commit)
	if err != nil {
		t.Fatal(err)
	}

	fetchDir := filepath.Join(tempDir, "fetch")
	if err := store.FetchTo(context.Background(), fetchDir, deployment.Deployment.SourceWorkspace(), deployment.Deployment.SourceGitSourceID(), deployment.Deployment.Commit); err != nil {
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

type failingBundleStore struct {
	exists    bool
	existsErr error
	fetchErr  error
}

func (s failingBundleStore) Exists(context.Context, string, string, string) (bool, error) {
	return s.exists, s.existsErr
}

func (s failingBundleStore) Materialize(context.Context, string, string, string, string) error {
	return nil
}

func (s failingBundleStore) FetchTo(context.Context, string, string, string, string) error {
	return s.fetchErr
}

type failingGitSourceRegistry struct {
	getErr error
}

func (r failingGitSourceRegistry) Upsert(context.Context, gitsource.Source) error {
	return errors.New("unexpected upsert")
}

func (r failingGitSourceRegistry) Get(context.Context, string, string) (gitsource.Source, error) {
	return gitsource.Source{}, r.getErr
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

func createTestGitSourceRepo(t *testing.T, parent string, name string, subpath string) string {
	t.Helper()
	repoDir := filepath.Join(parent, name)
	appDir := repoDir
	if subpath != "" {
		appDir = filepath.Join(repoDir, filepath.FromSlash(subpath))
	}
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "windforce.json"), []byte(`{
		"app": "echo",
		"entrypoint": "main.ts",
		"scriptLang": "typescript",
		"actions": {
			"echo": {
				"inputSchema": "input.schema.json",
				"outputSchema": "output.schema.json"
			}
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "main.ts"), []byte(`export async function main(ctx) { return ctx.input }`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "input.schema.json"), []byte(`{"type":"object"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "output.schema.json"), []byte(`{"type":"object"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repoDir, "init")
	runTestGit(t, repoDir, "checkout", "-b", "main")
	runTestGit(t, repoDir, "config", "user.email", "test@example.com")
	runTestGit(t, repoDir, "config", "user.name", "Test User")
	runTestGit(t, repoDir, "add", ".")
	runTestGit(t, repoDir, "commit", "-m", "initial")
	return repoDir
}

func TestCanonicalWorkersEndpointServesRegistry(t *testing.T) {
	tempDir := t.TempDir()
	store := state.NewLocalStore(filepath.Join(tempDir, "state.json"))
	if err := store.RegisterWorker(context.Background(), state.WorkerRecord{
		ID: "w-live", Group: "default", Labels: []string{"browser", "kr"}, Slots: 4,
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(Config{
		Store:     store,
		Catalog:   catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json")),
		EnableAPI: true,
	}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/w/ws-a/workers")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Workers []struct {
			ID     string   `json:"id"`
			Labels []string `json:"labels"`
			Slots  int      `json:"slots"`
			Live   bool     `json:"live"`
		} `json:"workers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Workers) != 1 || body.Workers[0].ID != "w-live" ||
		!reflect.DeepEqual(body.Workers[0].Labels, []string{"browser", "kr"}) ||
		body.Workers[0].Slots != 4 || !body.Workers[0].Live {
		t.Fatalf("workers body = %#v", body)
	}
}
