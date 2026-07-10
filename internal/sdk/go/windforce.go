// Package windforceclient is the Go author SDK for windforce actions (ADR-0040,
// the Go surface of the ctx-first contract in ADR-0014/ADR-0038). Authors import
// it as "windforce-client" (the uniform package name; the worker injects this
// source plus a go.mod naming the module windforce-client). The surface mirrors
// sdk/typescript and sdk/python: a Context carrying the run, and CreateApp to build
// the dispatcher.
//
//	import wf "windforce-client"
//
//	var Main = wf.CreateApp(wf.App{Actions: wf.Actions{
//		"approval.sync": func(ctx *wf.Context) (any, error) { return ctx.Input, nil },
//	}})
//
// The worker compiles a generated wrapper alongside the author code; that wrapper's
// func main calls RunMain(Main). Unlike the interpreted runtimes (where the worker
// wrapper builds ctx), the compiled Go SDK owns the runtime client itself: the
// methods read WF_TOKEN/WF_BASE_URL from the environment and call the control plane.
// The auth boundary holds — no token/baseURL is exposed to authors; the only
// outbound escape hatch is ctx.Http (ADR-0003).
package windforceclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime/debug"
	"strings"
)

// Handler is one action handler (or the whole app). It receives the single ctx and
// returns a JSON-serializable value or an error. Errors are returned, not thrown —
// a non-nil error (or a panic) becomes a job failure (ADR-0040).
type Handler func(ctx *Context) (any, error)

// Middleware wraps a handler onion-style (outer to inner): enrich ctx, short-circuit,
// or reshape the result. Call next to continue.
type Middleware func(ctx *Context, next func() (any, error)) (any, error)

// Actions maps action_key -> handler.
type Actions map[string]Handler

// App is the CreateApp config: the action map plus optional middleware and onError.
type App struct {
	Actions Actions
	Use     []Middleware
	OnError func(ctx *Context, err error) (any, error)
}

// CreateApp builds the entrypoint Handler that dispatches on ctx.Action through the
// middleware onion. An action_key absent from Actions yields "unknown action: <k>"
// (the manifest<->code guard, mirroring createApp/create_app).
func CreateApp(app App) Handler {
	return func(ctx *Context) (any, error) {
		dispatch := func() (any, error) {
			h, ok := app.Actions[ctx.Action]
			if !ok {
				return nil, fmt.Errorf("unknown action: %s", ctx.Action)
			}
			return h(ctx)
		}
		chain := dispatch
		for i := len(app.Use) - 1; i >= 0; i-- {
			mw, next := app.Use[i], chain
			chain = func() (any, error) { return mw(ctx, next) }
		}
		res, err := chain()
		if err != nil && app.OnError != nil {
			return app.OnError(ctx, err)
		}
		return res, err
	}
}

// Context is the only execution surface an author sees (ADR-0014 §2). The worker
// constructs it (via newContext) and RunMain passes it to the handler. Field case is
// Go-idiomatic PascalCase; the values mirror the TS/Python ctx exactly. Cancellation
// is by process kill (ADR-0011), so the SDK methods take no context.Context.
type Context struct {
	Input   any     // the run payload (ctx.input); author casts it
	Trigger Trigger // what created this run
	App     string  // app_key (Application Project)
	Action  string  // action_key — dispatched on
	Job     Job     // id / workspace / tag
	Actor   Actor   // email / username / permissioned_as
	Logger  Logger  // log channel (stdout/stderr)

	Variables Variables // ctx.Variables.Get(path) — secrets/variables
	Resources Resources // ctx.Resources.Get(path) — resources (json)
	State     State     // per-action persisted state
	Http      Http      // auth-preset fetch (the only escape hatch)
	Approval  Approval  // mint approve/reject resume URLs for an upcoming approval (flow HITL, Model A)
	Flow      Flow      // flow-step context; Flow.ResumeValue is set on the step after an approval
}

// Flow carries flow-step context an action may read. ResumeValue is the approver's
// submitted value, set ONLY on the action that runs immediately after an approval
// resolves (it is also delivered as ctx.Input); nil for any other step.
type Flow struct {
	ResumeValue any
}

// Trigger describes what created the run (ADR-0014 §5).
type Trigger struct {
	Kind         string            // "api" | "webhook" | "schedule" | "manual"
	Raw          string            // webhook raw body (JSON string); "" otherwise
	Headers      map[string]string // pinned webhook headers; nil otherwise
	ScheduledFor string            // schedule-only scheduled time; "" otherwise
}

// Job identifies the run.
type Job struct {
	ID        string
	Workspace string
	Tag       string
}

// Actor is the permissioned identity the job runs as.
type Actor struct {
	Email          string
	Username       string
	PermissionedAs string
}

// Logger writes to stdout/stderr; the worker captures both as job logs (ADR-0014 §8).
type Logger struct{}

func (Logger) Info(args ...any)  { fmt.Fprintln(os.Stdout, args...) }
func (Logger) Warn(args ...any)  { fmt.Fprintln(os.Stderr, args...) }
func (Logger) Error(args ...any) { fmt.Fprintln(os.Stderr, args...) }
func (Logger) Debug(args ...any) { fmt.Fprintln(os.Stdout, args...) }

// cpURL builds the workspace-scoped control-plane URL the job token is authorized for:
// {WF_BASE_URL}/api/w/{WF_WORKSPACE}{path} — the exact shape the TS/Python wrappers use
// (internal/executor api()). Variables/Resources/State call through here; Http is
// separate (it may target foreign origins, so it must NOT be workspace-scoped).
func cpURL(path string) (string, error) {
	base := strings.TrimRight(os.Getenv("WF_BASE_URL"), "/")
	if base == "" {
		return "", fmt.Errorf("WF_BASE_URL is empty")
	}
	return base + "/api/w/" + os.Getenv("WF_WORKSPACE") + path, nil
}

// cpDo issues a workspace-scoped, job-token-authed control-plane request. The CP is
// always the platform origin, so the token is always attached (unlike Http.Fetch). They
// read WF_TOKEN/WF_BASE_URL/WF_WORKSPACE from the env at call time; authors never see them.
func cpDo(method, path string, body io.Reader) (*http.Response, error) {
	u, err := cpURL(path)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(context.Background(), method, u, body)
	if err != nil {
		return nil, err
	}
	if tok := os.Getenv("WF_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return http.DefaultClient.Do(req)
}

// cpGetJSON does a CP GET and unmarshals a 2xx JSON body into v (a 0-length body is a
// no-op, leaving v at its zero value — e.g. state that was never set).
func cpGetJSON(path string, v any) error {
	resp, err := cpDo(http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("GET %s: %d %s", path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, v)
}

// Variables resolves secrets/variables from the control plane (ADR-0014 §9).
type Variables struct{}

// Get returns a variable's plaintext value. The endpoint wraps it as {"value": "..."}
// (mirrors the TS/Python wrapper's `(await r.json()).value`).
func (Variables) Get(path string) (string, error) {
	var out struct {
		Value string `json:"value"`
	}
	if err := cpGetJSON("/variables/get/p/"+path, &out); err != nil {
		return "", err
	}
	return out.Value, nil
}

// Resources resolves resources (jsonb) from the control plane; the body IS the resource.
type Resources struct{}

func (Resources) Get(path string) (any, error) {
	var v any
	if err := cpGetJSON("/resources/get/p/"+path, &v); err != nil {
		return nil, err
	}
	return v, nil
}

// Approval mints the approve/reject resume URLs for the approval step that immediately
// FOLLOWS the calling flow step (Model A, ADR-0053). The server computes the HMAC
// signatures; the SDK only carries the returned URLs, which the action delivers itself
// (email/Slack/...). Only a running flow-step job may mint.
type Approval struct{}

// ResumeURLs is the mint response: the approve/reject URLs, their shared resume slot, the
// upcoming approval step's index, and the URLs' expiry (unix seconds).
type ResumeURLs struct {
	Approve   string `json:"approve"`
	Reject    string `json:"reject"`
	ResumeID  int    `json:"resume_id"`
	StepIndex int    `json:"step_index"`
	ExpiresAt int64  `json:"expires_at"`
}

// GetResumeURLs mints the URLs for the upcoming approval (POST /flow/resume-urls). approver
// scopes the resume slot (one slot per approver); it is required for a multi-approval
// (requiredEvents>1) step and optional for a single-approval one. Mirrors the TS/Python
// ctx.approval.getResumeUrls.
func (Approval) GetResumeURLs(approver string) (ResumeURLs, error) {
	var out ResumeURLs
	path := "/flow/resume-urls"
	if approver != "" {
		path += "?approver=" + url.QueryEscape(approver)
	}
	resp, err := cpDo(http.MethodPost, path, nil)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return out, fmt.Errorf("approval.GetResumeURLs: %d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return out, err
	}
	return out, nil
}

// State is the per-action persisted state (ADR-0014 §10), keyed by WF_STATE_PATH which is
// passed as the ?path= query (mirrors the TS/Python wrapper, not a path segment).
type State struct{}

func (State) Get() (any, error) {
	var v any
	if err := cpGetJSON("/state?path="+url.QueryEscape(os.Getenv("WF_STATE_PATH")), &v); err != nil {
		return nil, err
	}
	return v, nil
}

func (State) Set(value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	resp, err := cpDo(http.MethodPost, "/state?path="+url.QueryEscape(os.Getenv("WF_STATE_PATH")), bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("set state: %d", resp.StatusCode)
	}
	return nil
}

// Http is the auth-preset HTTP client — the only outbound escape hatch (ADR-0014 §9). A
// relative target (leading "/") resolves against the platform base URL and carries the
// job token; an absolute URL is used as-is and the token is attached ONLY when it targets
// the base URL, so it can never leak to a foreign origin (mirrors the TS/Python wrapper's
// ctx.http.fetch). Pass opts to set the method, headers, or body on the request.
type Http struct{}

func (Http) Fetch(target string, opts ...func(*http.Request)) (*http.Response, error) {
	base := strings.TrimRight(os.Getenv("WF_BASE_URL"), "/")
	reqURL := target
	if strings.HasPrefix(target, "/") {
		reqURL = base + target
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	if base != "" && strings.HasPrefix(reqURL, base) {
		if tok := os.Getenv("WF_TOKEN"); tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	for _, o := range opts {
		o(req)
	}
	return http.DefaultClient.Do(req)
}

// RunMain is the wrapper entry: it builds ctx from WF_* env + input.json, calls the
// handler (recovering panics), writes result.json, and returns the process exit code
// (0 success / 1 failure). The generated wrapper does os.Exit(RunMain(Main)). The
// result/error/log protocol is the language-neutral one (ADR-0014 §6/§7).
func RunMain(main Handler) (code int) {
	ctx, err := newContext()
	if err != nil {
		return writeError(fmt.Errorf("build ctx: %w", err))
	}
	defer func() {
		if r := recover(); r != nil {
			code = writeErrorWithStack(fmt.Sprintf("%v", r), "panic", string(debug.Stack()))
		}
	}()
	res, err := main(ctx)
	if err != nil {
		return writeError(err)
	}
	b, merr := json.Marshal(res)
	if merr != nil {
		return writeError(fmt.Errorf("marshal result: %w", merr))
	}
	if res == nil {
		b = []byte("null")
	}
	if werr := os.WriteFile("result.json", b, 0o644); werr != nil {
		return writeError(werr)
	}
	return 0
}

// newContext builds the ctx from WF_* env + input.json (the transport, ADR-0014 §14).
func newContext() (*Context, error) {
	raw, err := os.ReadFile("input.json")
	if err != nil {
		return nil, err
	}
	var input any
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, fmt.Errorf("parse input.json: %w", err)
		}
	}
	c := &Context{
		Input:  input,
		App:    os.Getenv("WF_APP"),
		Action: os.Getenv("WF_ACTION"),
		Trigger: Trigger{
			Kind:         os.Getenv("WF_TRIGGER_KIND"),
			ScheduledFor: os.Getenv("WF_SCHEDULED_FOR"),
		},
		Job:   Job{ID: os.Getenv("WF_JOB_ID"), Workspace: os.Getenv("WF_WORKSPACE"), Tag: os.Getenv("WF_TAG")},
		Actor: Actor{Email: os.Getenv("WF_EMAIL"), Username: os.Getenv("WF_USERNAME"), PermissionedAs: os.Getenv("WF_PERMISSIONED_AS")},
	}
	// webhook raw body is the same value as ctx.Input (a JSON string passthrough).
	if c.Trigger.Kind == "webhook" {
		c.Trigger.Raw = string(raw)
	}
	// The step right after an approval (trigger kind "flow_resume") gets the approver's
	// value as ctx.Input; surface it by name as ctx.Flow.ResumeValue.
	if c.Trigger.Kind == "flow_resume" {
		c.Flow.ResumeValue = input
	}
	if hdrs := os.Getenv("WF_TRIGGER_HEADERS"); hdrs != "" {
		_ = json.Unmarshal([]byte(hdrs), &c.Trigger.Headers)
	}
	return c, nil
}

func writeError(err error) int {
	return writeErrorWithStack(err.Error(), fmt.Sprintf("%T", err), "")
}

func writeErrorWithStack(message, name, stack string) int {
	flat := map[string]string{"message": message, "name": name}
	if stack != "" {
		flat["stack"] = stack
	}
	b, _ := json.Marshal(flat)
	_ = os.WriteFile("result.json", b, 0o644)
	return 1
}
