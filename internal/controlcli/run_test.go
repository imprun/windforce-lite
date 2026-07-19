package controlcli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRunJobWaitUsesControlPlaneContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/w/team/jobs/run/demo/health/wait" || r.URL.Query().Get("timeout_ms") != "5000" {
			t.Fatalf("request URL = %s", r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"succeeded"}`))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	exit := Run([]string{"--api-url", server.URL, "--workspace", "team", "job", "run", "demo", "health", "--wait", "--timeout-ms", "5000", "--input", `{"ping":true}`}, strings.NewReader(""), &stdout, &stderr)
	if exit != ExitOK {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != `{"status":"succeeded"}` {
		t.Fatalf("stdout=%s", stdout.String())
	}
}

func TestRunMapsAPIStatusToStableExitCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	exit := Run([]string{"--api-url", server.URL, "app", "list"}, strings.NewReader(""), &stdout, &stderr)
	if exit != ExitAPIClient || !strings.Contains(stderr.String(), "unauthorized") {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
}

func TestProfileSetDoesNotPersistTokenValue(t *testing.T) {
	path := t.TempDir() + "/config.json"
	t.Setenv("WINDFORCE_CONFIG", path)
	t.Setenv("PRIVATE_WF_TOKEN", "must-not-be-written")
	var stdout, stderr bytes.Buffer
	exit := Run([]string{"profile", "set", "local", "--api-url", "http://127.0.0.1:18091", "--token-env", "PRIVATE_WF_TOKEN", "--use"}, strings.NewReader(""), &stdout, &stderr)
	if exit != ExitOK {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	data, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if data.Profiles["local"].TokenEnv != "PRIVATE_WF_TOKEN" || strings.Contains(stdout.String(), "must-not-be-written") {
		t.Fatalf("profile output leaked or lost token env: %s", stdout.String())
	}
}

func TestGlobalHelpIsSuccessful(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exit := Run([]string{"--help"}, strings.NewReader(""), &stdout, &stderr)
	if exit != ExitOK || !strings.Contains(stdout.String(), "source list") || stderr.Len() != 0 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
}
