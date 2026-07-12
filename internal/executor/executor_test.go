package executor

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRuntimeForGoUsesPreparedBinary(t *testing.T) {
	rt, err := runtimeFor("go")
	if err != nil {
		t.Fatalf("runtimeFor(go): %v", err)
	}
	if rt.label != "go" || rt.wrapperName != "" {
		t.Fatalf("runtimeFor(go) = {label:%q wrapper:%q}, want go/empty", rt.label, rt.wrapperName)
	}
	if rt.wrapperContent != nil {
		t.Fatalf("runtimeFor(go).wrapperContent is not nil")
	}
	bin := filepath.Join(t.TempDir(), "app")
	argv := rt.argv(RunParams{EntrypointAbsPath: bin})
	if len(argv) != 1 || argv[0] != bin {
		t.Fatalf("runtimeFor(go).argv = %#v, want [%s]", argv, bin)
	}
}

func TestRunPythonBuildsCanonicalCtxHelpers(t *testing.T) {
	requirePython(t)
	var stateSetBody map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/custom" && r.Header.Get("Authorization") != "Bearer job-token" {
			t.Errorf("authorization header = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/w/ws-a/variables/get/p/secret" && r.URL.RawQuery == "":
			if r.Header.Get("X-Windforce-Job-ID") != "" {
				t.Errorf("unexpected job id header = %q", r.Header.Get("X-Windforce-Job-ID"))
			}
			writeJSON(w, map[string]string{"value": "var-ok"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/w/ws-a/resources/get/p/browser":
			writeJSON(w, map[string]string{"resource": "browser-ok"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/w/ws-a/state" && r.URL.Query().Get("path") == "demo/echo":
			writeJSON(w, map[string]string{"state": "before"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/w/ws-a/state" && r.URL.Query().Get("path") == "demo/echo":
			if err := json.NewDecoder(r.Body).Decode(&stateSetBody); err != nil {
				t.Errorf("decode state body: %v", err)
			}
			writeJSON(w, map[string]bool{"ok": true})
		case r.Method == http.MethodPost && r.URL.Path == "/custom":
			writeJSON(w, map[string]string{"custom": r.Header.Get("Authorization")})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	entrypoint := filepath.Join(t.TempDir(), "main.py")
	if err := os.WriteFile(entrypoint, []byte(`
async def main(ctx):
    ctx.logger.info("stdout-line", ctx.app, ctx.action)
    variable = await ctx.variables.get("secret")
    resource = await ctx.resources.get("browser")
    before = await ctx.state.get()
    await ctx.state.set({"message": ctx.input["message"]})
    custom = await (await ctx.http.fetch("/custom", method="POST", body={"x": 1})).json()
    return {
        "variable": variable,
        "resource": resource,
        "before": before,
        "custom": custom,
        "has_approval": hasattr(ctx, "approval"),
        "has_flow": hasattr(ctx, "flow"),
        "flow_resume_value": ctx.flow.resume_value,
        "headers": ctx.trigger.headers,
        "job": {"id": ctx.job.id, "workspace": ctx.job.workspace, "tag": ctx.job.tag},
    }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Run(context.Background(), RunParams{
		ScriptLang:        "python",
		BaseDir:           t.TempDir(),
		EntrypointAbsPath: entrypoint,
		Input:             []byte(`{"message":"hello"}`),
		Env: []string{
			"WF_JOB_ID=job-a",
			"WF_WORKSPACE=ws-a",
			"WF_BASE_URL=" + server.URL,
			"WF_TOKEN=job-token",
			"WF_APP=demo",
			"WF_ACTION=echo",
			"WF_TAG=default",
			"WF_STATE_PATH=demo/echo",
			"WF_TRIGGER_KIND=flow_resume",
			`WF_TRIGGER_HEADERS={"X-Test":"ok"}`,
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !res.Success() {
		t.Fatalf("success = false, exit=%d, result=%s, logs=%s", res.ExitCode, res.Result, res.Logs)
	}
	if !strings.Contains(res.Logs, "stdout-line demo echo") {
		t.Fatalf("logs = %q", res.Logs)
	}
	if stateSetBody["message"] != "hello" {
		t.Fatalf("state set body = %#v", stateSetBody)
	}
	var output struct {
		Variable    string            `json:"variable"`
		Resource    map[string]string `json:"resource"`
		Before      map[string]string `json:"before"`
		Custom      map[string]string `json:"custom"`
		HasApproval bool              `json:"has_approval"`
		HasFlow     bool              `json:"has_flow"`
		FlowResume  map[string]string `json:"flow_resume_value"`
		Headers     map[string]string `json:"headers"`
		Job         map[string]string `json:"job"`
	}
	if err := json.Unmarshal(res.Result, &output); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	if output.Variable != "var-ok" || output.Resource["resource"] != "browser-ok" ||
		output.Before["state"] != "before" || output.Custom["custom"] != "Bearer job-token" ||
		!output.HasApproval || !output.HasFlow || output.FlowResume["message"] != "hello" ||
		output.Headers["X-Test"] != "ok" || output.Job["id"] != "job-a" ||
		output.Job["workspace"] != "ws-a" || output.Job["tag"] != "default" {
		t.Fatalf("output = %#v", output)
	}
}

func TestRunPythonLoadsSrcLayoutFromPreparedSourceRoot(t *testing.T) {
	requirePython(t)
	sourceRoot := t.TempDir()
	packageDir := filepath.Join(sourceRoot, "src", "demo_app")
	if err := os.MkdirAll(packageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageDir, "helper.py"), []byte(`VALUE = "src-layout-ok"`), 0o644); err != nil {
		t.Fatal(err)
	}
	entrypoint := filepath.Join(packageDir, "app.py")
	if err := os.WriteFile(entrypoint, []byte(`from demo_app.helper import VALUE

def main(ctx):
    return {"value": VALUE, "app": ctx.app}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Run(context.Background(), RunParams{
		ScriptLang:        "python",
		BaseDir:           t.TempDir(),
		EntrypointAbsPath: entrypoint,
		Env: []string{
			"WF_PY_SOURCE_ROOT=" + sourceRoot,
			"WF_WORKSPACE=ws-a",
			"WF_BASE_URL=http://127.0.0.1",
			"WF_TOKEN=job-token",
			"WF_APP=demo",
			"WF_ACTION=echo",
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !res.Success() {
		t.Fatalf("Run failed: result=%s logs=%s", res.Result, res.Logs)
	}
	var got map[string]string
	if err := json.Unmarshal(res.Result, &got); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	if got["value"] != "src-layout-ok" || got["app"] != "demo" {
		t.Fatalf("result = %#v", got)
	}
}

func TestRunPythonInvalidInputFallsBackToEmptyObject(t *testing.T) {
	requirePython(t)
	entrypoint := filepath.Join(t.TempDir(), "main.py")
	if err := os.WriteFile(entrypoint, []byte(`
def main(ctx):
    return {"input": ctx.input}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Run(context.Background(), RunParams{
		ScriptLang:        "python",
		BaseDir:           t.TempDir(),
		EntrypointAbsPath: entrypoint,
		Input:             []byte(`{`),
		Env: []string{
			"WF_WORKSPACE=ws-a",
			"WF_BASE_URL=http://127.0.0.1",
			"WF_TOKEN=job-token",
			"WF_APP=demo",
			"WF_ACTION=echo",
		},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !res.Success() {
		t.Fatalf("success = false, exit=%d, result=%s, logs=%s", res.ExitCode, res.Result, res.Logs)
	}
	var output struct {
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(res.Result, &output); err != nil {
		t.Fatalf("result is not JSON: %v", err)
	}
	if len(output.Input) != 0 {
		t.Fatalf("input = %#v, want empty object", output.Input)
	}
}

func TestRunTimeoutSynthesizesExecutionError(t *testing.T) {
	requirePython(t)
	entrypoint := filepath.Join(t.TempDir(), "main.py")
	if err := os.WriteFile(entrypoint, []byte(`
import time

def main(ctx):
    time.sleep(10)
    return {"ok": True}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := Run(context.Background(), RunParams{
		ScriptLang:        "python",
		BaseDir:           t.TempDir(),
		EntrypointAbsPath: entrypoint,
		Env: []string{
			"WF_WORKSPACE=ws-a",
			"WF_BASE_URL=http://127.0.0.1",
			"WF_TOKEN=job-token",
			"WF_APP=demo",
			"WF_ACTION=echo",
		},
		Timeout: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !res.TimedOut {
		t.Fatalf("TimedOut = false, result=%s logs=%s", res.Result, res.Logs)
	}
	if res.Success() {
		t.Fatalf("Success = true for timed-out result")
	}
	var got map[string]string
	if err := json.Unmarshal(res.Result, &got); err != nil {
		t.Fatalf("timeout result is not JSON: %v", err)
	}
	if got["name"] != "ExecutionError" || got["message"] != "job timed out" {
		t.Fatalf("timeout result = %#v, want ExecutionError/job timed out", got)
	}
}

func TestRunRejectsWhitespaceScriptLangCanonically(t *testing.T) {
	_, err := Run(context.Background(), RunParams{
		ScriptLang:        " python ",
		BaseDir:           t.TempDir(),
		EntrypointAbsPath: filepath.Join(t.TempDir(), "main.py"),
	})
	if !errors.Is(err, ErrScriptLang) {
		t.Fatalf("Run error = %v, want ErrScriptLang", err)
	}
}

func TestGeneratedWrappersUseJobTokenForVariableReads(t *testing.T) {
	ts := wrapper("main.ts")
	if strings.Contains(ts, `?app=`) {
		t.Fatalf("typescript wrapper still passes app scope to variables.get:\n%s", ts)
	}
	if strings.Contains(ts, `X-Windforce-Job-ID`) {
		t.Fatalf("typescript wrapper should not pass job identity outside WF_TOKEN:\n%s", ts)
	}
	if !strings.Contains(ts, `app: APP`) {
		t.Fatalf("typescript wrapper does not reuse APP in ctx.app:\n%s", ts)
	}
	if !strings.Contains(ts, `approval: {`) || !strings.Contains(ts, `async getResumeUrls(approver)`) ||
		!strings.Contains(ts, `flow: {`) || !strings.Contains(ts, `resumeValue: KIND === "flow_resume" ? input : undefined`) {
		t.Fatalf("typescript wrapper does not expose canonical approval/flow ctx shape:\n%s", ts)
	}

	py := wrapperPy("main.py")
	if strings.Contains(py, `?app=`) {
		t.Fatalf("python wrapper still passes app scope to variables.get:\n%s", py)
	}
	if strings.Contains(py, `X-Windforce-Job-ID`) {
		t.Fatalf("python wrapper should not pass job identity outside WF_TOKEN:\n%s", py)
	}
	if !strings.Contains(py, `app=_APP`) {
		t.Fatalf("python wrapper does not reuse _APP in ctx.app:\n%s", py)
	}
	if !strings.Contains(py, `class _Approval:`) || !strings.Contains(py, `async def get_resume_urls(self, approver=None):`) ||
		!strings.Contains(py, `approval=_Approval(),`) || !strings.Contains(py, `flow=SimpleNamespace(resume_value=(_input if _KIND == "flow_resume" else None))`) {
		t.Fatalf("python wrapper does not expose canonical approval/flow ctx shape:\n%s", py)
	}
	if !strings.Contains(py, `_source_root = _env("WF_PY_SOURCE_ROOT")`) ||
		!strings.Contains(py, `os.path.join(_source_root, "src")`) {
		t.Fatalf("python wrapper does not add source root/src layout import paths:\n%s", py)
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		panic(err)
	}
}

func TestDefaultWindowsPythonPathSkipsWindowsAppsAlias(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows path resolution only")
	}
	tempDir := t.TempDir()
	windowsApps := filepath.Join(tempDir, "Microsoft", "WindowsApps")
	realBin := filepath.Join(tempDir, "Python", "bin")
	if err := os.MkdirAll(windowsApps, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(realBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(windowsApps, "python.exe"), []byte("alias"), 0o755); err != nil {
		t.Fatal(err)
	}
	realPython := filepath.Join(realBin, "python.exe")
	if err := os.WriteFile(realPython, []byte("real"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", windowsApps+string(os.PathListSeparator)+realBin)

	if got := defaultWindowsPythonPath(); got != realPython {
		t.Fatalf("defaultWindowsPythonPath() = %q, want %q", got, realPython)
	}
}

func requirePython(t *testing.T) {
	t.Helper()
	python := "python3"
	if runtime.GOOS == "windows" {
		if defaultWindowsPythonPath() != "" {
			return
		}
		if _, err := exec.LookPath("py"); err == nil {
			return
		}
		python = "python"
	}
	if _, err := exec.LookPath(python); err != nil {
		t.Skipf("%s not found in PATH", python)
	}
}
