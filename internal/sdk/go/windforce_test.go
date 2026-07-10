package windforceclient

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runMainInDir(t *testing.T, input string, env map[string]string, h Handler) (int, []byte) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "input.json"), []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}
	for key, value := range env {
		t.Setenv(key, value)
	}
	code := RunMain(h)
	out, _ := os.ReadFile(filepath.Join(dir, "result.json"))
	return code, out
}

func TestRunMainSuccessWritesResult(t *testing.T) {
	code, out := runMainInDir(t, `{"name":"world"}`, map[string]string{"WF_APP": "demo", "WF_ACTION": "demo.hello"},
		func(ctx *Context) (any, error) {
			input, _ := ctx.Input.(map[string]any)
			return map[string]any{"greeting": "hi " + input["name"].(string), "action": ctx.Action}, nil
		})
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (result=%s)", code, out)
	}
	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("result.json not JSON: %v (%s)", err, out)
	}
	if result["greeting"] != "hi world" || result["action"] != "demo.hello" {
		t.Fatalf("result = %s, want greeting=hi world action=demo.hello", out)
	}
}

func TestRunMainNilResultIsNull(t *testing.T) {
	code, out := runMainInDir(t, `{}`, nil, func(ctx *Context) (any, error) { return nil, nil })
	if code != 0 || string(out) != "null" {
		t.Fatalf("exit=%d result=%q, want 0/null", code, out)
	}
}

func TestRunMainErrorReturnWritesFlatError(t *testing.T) {
	code, out := runMainInDir(t, `{}`, nil, func(ctx *Context) (any, error) {
		return nil, errors.New("boom")
	})
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	var result map[string]string
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("result.json not JSON: %v (%s)", err, out)
	}
	if result["message"] != "boom" || result["name"] == "" {
		t.Fatalf("flat error = %s, want message=boom and a name", out)
	}
	if _, hasStack := result["stack"]; hasStack {
		t.Fatalf("returned error should carry no stack, got %s", out)
	}
}

func TestRunMainPanicWritesFlatErrorWithStack(t *testing.T) {
	code, out := runMainInDir(t, `{}`, nil, func(ctx *Context) (any, error) {
		panic("kaboom")
	})
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	var result map[string]string
	_ = json.Unmarshal(out, &result)
	if result["name"] != "panic" || result["message"] != "kaboom" || result["stack"] == "" {
		t.Fatalf("panic flat error = %s, want name=panic message=kaboom and stack", out)
	}
}

func TestCreateAppDispatchesOnAction(t *testing.T) {
	app := CreateApp(App{Actions: Actions{
		"a.one": func(ctx *Context) (any, error) { return "one", nil },
		"a.two": func(ctx *Context) (any, error) { return "two", nil },
	}})
	if value, err := app(&Context{Action: "a.two"}); err != nil || value != "two" {
		t.Fatalf("dispatch a.two = (%v,%v), want (two,nil)", value, err)
	}
	if _, err := app(&Context{Action: "a.nope"}); err == nil || err.Error() != "unknown action: a.nope" {
		t.Fatalf("unknown action err = %v, want 'unknown action: a.nope'", err)
	}
}

func TestCreateAppMiddlewareOnionOrder(t *testing.T) {
	var order []string
	middleware := func(tag string) Middleware {
		return func(ctx *Context, next func() (any, error)) (any, error) {
			order = append(order, "enter:"+tag)
			value, err := next()
			order = append(order, "exit:"+tag)
			return value, err
		}
	}
	app := CreateApp(App{
		Use:     []Middleware{middleware("outer"), middleware("inner")},
		Actions: Actions{"a": func(ctx *Context) (any, error) { order = append(order, "handler"); return nil, nil }},
	})
	_, _ = app(&Context{Action: "a"})
	want := []string{"enter:outer", "enter:inner", "handler", "exit:inner", "exit:outer"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestNewContextMapsEnvAndInput(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "input.json"), []byte(`{"k":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"WF_APP":          "demo",
		"WF_ACTION":       "demo.sync",
		"WF_JOB_ID":       "job1",
		"WF_WORKSPACE":    "ws-a",
		"WF_TAG":          "default",
		"WF_EMAIL":        "u@x.io",
		"WF_TRIGGER_KIND": "webhook",
	} {
		t.Setenv(key, value)
	}
	ctx, err := newContext()
	if err != nil {
		t.Fatal(err)
	}
	if ctx.App != "demo" || ctx.Action != "demo.sync" || ctx.Job.ID != "job1" ||
		ctx.Job.Workspace != "ws-a" || ctx.Job.Tag != "default" || ctx.Actor.Email != "u@x.io" {
		t.Fatalf("ctx ids/actor wrong: %+v", ctx)
	}
	if ctx.Trigger.Kind != "webhook" || ctx.Trigger.Raw != `{"k":1}` {
		t.Fatalf("webhook trigger wrong: kind=%q raw=%q", ctx.Trigger.Kind, ctx.Trigger.Raw)
	}
	if input, ok := ctx.Input.(map[string]any); !ok || input["k"] != float64(1) {
		t.Fatalf("ctx.Input = %#v, want map{k:1}", ctx.Input)
	}
}

func TestNewContextSetsFlowResumeValue(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "input.json"), []byte(`{"approved":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WF_TRIGGER_KIND", "flow_resume")
	ctx, err := newContext()
	if err != nil {
		t.Fatal(err)
	}
	value, ok := ctx.Flow.ResumeValue.(map[string]any)
	if !ok || value["approved"] != true {
		t.Fatalf("Flow.ResumeValue = %#v, want approved=true", ctx.Flow.ResumeValue)
	}
}

func TestControlPlaneEndpoints(t *testing.T) {
	type recorded struct{ method, path, query, auth, body string }
	var last recorded
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		last = recorded{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), string(body)}
		switch {
		case strings.HasSuffix(r.URL.Path, "/variables/get/p/u/me/tok"):
			_, _ = w.Write([]byte(`{"value":"s3cr3t"}`))
		case strings.HasSuffix(r.URL.Path, "/resources/get/p/u/me/db"):
			_, _ = w.Write([]byte(`{"host":"db.local"}`))
		case strings.HasSuffix(r.URL.Path, "/state") && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"runs":3}`))
		}
	}))
	defer server.Close()

	t.Setenv("WF_BASE_URL", server.URL)
	t.Setenv("WF_WORKSPACE", "ws-x")
	t.Setenv("WF_TOKEN", "tok-1")
	t.Setenv("WF_STATE_PATH", "demo/act")

	value, err := Variables{}.Get("u/me/tok")
	if err != nil || value != "s3cr3t" {
		t.Fatalf("Variables.Get = %q, %v; want s3cr3t", value, err)
	}
	if last.method != http.MethodGet || last.path != "/api/w/ws-x/variables/get/p/u/me/tok" || last.auth != "Bearer tok-1" {
		t.Fatalf("variables req = %+v", last)
	}

	resource, err := Resources{}.Get("u/me/db")
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := resource.(map[string]any); m["host"] != "db.local" {
		t.Fatalf("Resources.Get = %v, want host=db.local", resource)
	}
	if last.path != "/api/w/ws-x/resources/get/p/u/me/db" {
		t.Fatalf("resources path = %s", last.path)
	}

	state, err := State{}.Get()
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := state.(map[string]any); m["runs"] != float64(3) {
		t.Fatalf("State.Get = %v, want runs=3", state)
	}
	if last.method != http.MethodGet || last.path != "/api/w/ws-x/state" || last.query != "path=demo%2Fact" {
		t.Fatalf("state get req = %+v", last)
	}

	if err := (State{}).Set(map[string]any{"runs": 4}); err != nil {
		t.Fatal(err)
	}
	if last.method != http.MethodPost || last.path != "/api/w/ws-x/state" || last.query != "path=demo%2Fact" ||
		!strings.Contains(last.body, `"runs":4`) {
		t.Fatalf("state set req = %+v", last)
	}

	resp, err := Http{}.Fetch("/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if last.path != "/healthz" || last.auth != "Bearer tok-1" {
		t.Fatalf("http.fetch relative req = %+v", last)
	}
}

func TestApprovalGetResumeURLs(t *testing.T) {
	type recorded struct{ method, path, query, auth string }
	var last recorded
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last = recorded{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization")}
		_, _ = w.Write([]byte(`{"approve":"https://p/api/flow/resume?t=a","reject":"https://p/api/flow/resume?t=r","resume_id":7,"step_index":2,"expires_at":1719000000}`))
	}))
	defer server.Close()

	t.Setenv("WF_BASE_URL", server.URL)
	t.Setenv("WF_WORKSPACE", "ws-x")
	t.Setenv("WF_TOKEN", "job-tok")

	urls, err := Approval{}.GetResumeURLs("alice@x.test")
	if err != nil {
		t.Fatal(err)
	}
	if last.method != http.MethodPost || last.path != "/api/w/ws-x/flow/resume-urls" ||
		last.query != "approver=alice%40x.test" || last.auth != "Bearer job-tok" {
		t.Fatalf("mint req = %+v", last)
	}
	if urls.Approve != "https://p/api/flow/resume?t=a" || urls.Reject != "https://p/api/flow/resume?t=r" ||
		urls.ResumeID != 7 || urls.StepIndex != 2 || urls.ExpiresAt != 1719000000 {
		t.Fatalf("parsed urls = %+v", urls)
	}

	if _, err := (Approval{}).GetResumeURLs(""); err != nil {
		t.Fatal(err)
	}
	if last.query != "" {
		t.Fatalf("empty approver should send no query param, got %q", last.query)
	}
}

func TestHTTPFetchForeignOriginNoToken(t *testing.T) {
	var gotAuth string
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
	}))
	defer foreign.Close()

	t.Setenv("WF_BASE_URL", "http://platform.invalid")
	t.Setenv("WF_TOKEN", "tok-secret")
	resp, err := Http{}.Fetch(foreign.URL + "/x")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if gotAuth != "" {
		t.Fatalf("foreign origin received Authorization %q", gotAuth)
	}
}
