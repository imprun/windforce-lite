// Package executor runs a single job via the Windforce ctx-first runtime
// contract. This is the lite direct-exec subset of vendor/windforce's executor:
// no sandbox/cgroup prep, but the per-job IO, wrapper ctx shape, result file,
// timeout cancellation, and log streaming follow the canonical implementation.
package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type RunParams struct {
	BunPath           string
	PythonPath        string
	ScriptLang        string
	BaseDir           string
	EntrypointAbsPath string
	Input             []byte
	Env               []string
	Timeout           time.Duration

	LogSink          func([]byte)
	LogFlushInterval time.Duration
	LogCapBytes      int
}

type Result struct {
	ExitCode   int
	Result     []byte
	Logs       string
	TimedOut   bool
	DurationMs int64
}

func (r Result) Success() bool {
	return r.ExitCode == 0 && !r.TimedOut
}

type langRuntime struct {
	label          string
	wrapperName    string
	wrapperContent func(entrypointAbsPath string) string
	argv           func(RunParams) []string
}

var ErrScriptLang = errors.New("unsupported script_lang")

func runtimeFor(lang string) (langRuntime, error) {
	switch lang {
	case "", "typescript":
		return langRuntime{
			label:          "bun",
			wrapperName:    "wrapper.ts",
			wrapperContent: wrapper,
			argv: func(p RunParams) []string {
				bun := p.BunPath
				if bun == "" {
					bun = "bun"
				}
				return []string{bun, "run", "wrapper.ts"}
			},
		}, nil
	case "python":
		return langRuntime{
			label:          "python",
			wrapperName:    "wrapper.py",
			wrapperContent: wrapperPy,
			argv: func(p RunParams) []string {
				return pythonArgv(p.PythonPath, "wrapper.py")
			},
		}, nil
	case "go":
		return langRuntime{
			label:          "go",
			wrapperName:    "",
			wrapperContent: nil,
			argv:           func(p RunParams) []string { return []string{p.EntrypointAbsPath} },
		}, nil
	default:
		return langRuntime{}, fmt.Errorf("%w: %q", ErrScriptLang, lang)
	}
}

func pythonArgv(pythonPath string, wrapperName string) []string {
	if pythonPath != "" {
		return []string{pythonPath, wrapperName}
	}
	if runtime.GOOS == "windows" {
		if py := defaultWindowsPythonPath(); py != "" {
			return []string{py, wrapperName}
		}
		return []string{"py", "-3", wrapperName}
	}
	return []string{"python3", wrapperName}
}

func defaultWindowsPythonPath() string {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		for _, name := range []string{"python.exe", "python3.exe"} {
			candidate := filepath.Join(dir, name)
			if isWindowsAppsAlias(candidate) {
				continue
			}
			info, err := os.Stat(candidate)
			if err == nil && !info.IsDir() {
				return candidate
			}
		}
	}
	return ""
}

func isWindowsAppsAlias(path string) bool {
	clean := strings.ToLower(filepath.Clean(path))
	return strings.Contains(clean, `\microsoft\windowsapps\`)
}

// Run executes the job and returns its result. An error is returned only for
// harness failures; a non-zero script exit is represented in Result.
func Run(ctx context.Context, p RunParams) (Result, error) {
	rt, err := runtimeFor(p.ScriptLang)
	if err != nil {
		return Result{}, err
	}
	entrypoint, err := filepath.Abs(p.EntrypointAbsPath)
	if err != nil {
		return Result{}, err
	}
	if entrypoint == "" {
		return Result{}, errors.New("entrypoint is required")
	}
	input := p.Input
	if len(input) == 0 {
		input = []byte("{}")
	}

	jobDir, err := os.MkdirTemp(p.BaseDir, "job-")
	if err != nil {
		return Result{}, fmt.Errorf("mkdir job dir: %w", err)
	}
	defer os.RemoveAll(jobDir)

	if err := os.WriteFile(filepath.Join(jobDir, "input.json"), input, 0o644); err != nil {
		return Result{}, err
	}
	if rt.wrapperContent != nil {
		if err := os.WriteFile(filepath.Join(jobDir, rt.wrapperName), []byte(rt.wrapperContent(entrypoint)), 0o644); err != nil {
			return Result{}, err
		}
	}

	cctx := ctx
	var cancel context.CancelFunc
	if p.Timeout > 0 {
		cctx, cancel = context.WithTimeout(ctx, p.Timeout)
		defer cancel()
	}

	argv := rt.argv(p)
	started := time.Now()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = p.Env
	cmd.Dir = jobDir
	setProcAttr(cmd)

	ls := &logStreamer{cap: p.LogCapBytes, sink: p.LogSink}
	cmd.Stdout = ls
	cmd.Stderr = ls

	err = cmd.Start()
	if err != nil {
		return Result{}, fmt.Errorf("start %s: %w", rt.label, err)
	}

	done := make(chan struct{})
	var timedOut atomic.Bool
	go func() {
		select {
		case <-cctx.Done():
			if errors.Is(cctx.Err(), context.DeadlineExceeded) {
				timedOut.Store(true)
			}
			killGroup(cmd)
		case <-done:
		}
	}()

	flushInterval := p.LogFlushInterval
	if flushInterval <= 0 {
		flushInterval = 2 * time.Second
	}
	flushStop := make(chan struct{})
	flushGone := make(chan struct{})
	go func() {
		defer close(flushGone)
		ticker := time.NewTicker(flushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-flushStop:
				return
			case <-ticker.C:
				ls.flush()
			}
		}
	}()

	waitErr := cmd.Wait()
	close(done)
	close(flushStop)
	<-flushGone
	ls.flush()
	ls.finalize()

	res := Result{
		Logs:       ls.accumulated(),
		TimedOut:   timedOut.Load(),
		DurationMs: time.Since(started).Milliseconds(),
	}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	} else if waitErr != nil {
		res.ExitCode = -1
	}

	data, readErr := os.ReadFile(filepath.Join(jobDir, "result.json"))
	switch {
	case readErr == nil && json.Valid(data):
		res.Result = data
	case res.Success():
		res.Result = []byte("null")
	default:
		res.Result = synthError(res, waitErr)
	}
	return res, nil
}

// wrapper is the canonical TypeScript ctx wrapper with the platform helpers
// exposed by vendor/windforce. Lite may not implement every control-plane
// endpoint yet, but scripts see the same ctx shape.
func wrapper(entrypointAbsPath string) string {
	return fmt.Sprintf(`import { readFileSync, writeFileSync } from "node:fs"

const env = (k) => process.env[k] ?? ""
const WS = env("WF_WORKSPACE")
const APP = env("WF_APP")
const BASE = env("WF_BASE_URL")
const TOKEN = env("WF_TOKEN")
const KIND = env("WF_TRIGGER_KIND")
const STATE_PATH = env("WF_STATE_PATH")

let input = {}
try { input = JSON.parse(readFileSync("input.json", "utf8")) } catch { input = {} }

let headers = undefined
try { const h = env("WF_TRIGGER_HEADERS"); if (h) headers = JSON.parse(h) } catch { headers = undefined }

async function api(method, path, body) {
  const reqHeaders = { Authorization: "Bearer " + TOKEN, "Content-Type": "application/json" }
  return fetch(BASE + "/api/w/" + WS + path, {
    method,
    headers: reqHeaders,
    body: body === undefined ? undefined : JSON.stringify(body),
  })
}

const ctx = {
  input,
  trigger: {
    kind: KIND,
    raw: KIND === "webhook" ? input : undefined,
    headers,
    scheduledFor: env("WF_SCHEDULED_FOR") || undefined,
  },
  app: APP,
  action: env("WF_ACTION"),
  job: { id: env("WF_JOB_ID"), workspace: WS, tag: env("WF_TAG"), path: env("WF_RUNNABLE_PATH") || undefined },
  actor: { email: env("WF_EMAIL"), username: env("WF_USERNAME"), permissionedAs: env("WF_PERMISSIONED_AS") },
  logger: {
    info: (...a) => console.log(...a),
    warn: (...a) => console.error(...a),
    error: (...a) => console.error(...a),
    debug: (...a) => console.log(...a),
  },
  variables: {
    async get(p) {
      const r = await api("GET", "/variables/get/p/" + p)
      if (!r.ok) throw new Error("variables.get(" + p + ") failed: " + r.status)
      return (await r.json()).value
    },
  },
  resources: {
    async get(p) {
      const r = await api("GET", "/resources/get/p/" + p)
      if (!r.ok) throw new Error("resources.get(" + p + ") failed: " + r.status)
      return r.json()
    },
  },
  state: {
    async get() {
      const r = await api("GET", "/state?path=" + encodeURIComponent(STATE_PATH))
      if (!r.ok) throw new Error("state.get failed: " + r.status)
      return r.json()
    },
    async set(v) {
      const r = await api("POST", "/state?path=" + encodeURIComponent(STATE_PATH), v)
      if (!r.ok) throw new Error("state.set failed: " + r.status)
    },
  },
  http: {
    fetch(inp, init) {
      let url = inp
      if (typeof inp === "string" && inp.startsWith("/")) url = BASE + inp
      const headers = { ...(init && init.headers) }
      if (typeof url === "string" && url.startsWith(BASE)) headers["Authorization"] = "Bearer " + TOKEN
      return fetch(url, { ...init, headers })
    },
  },
  approval: {
    async getResumeUrls(approver) {
      const q = approver ? "?approver=" + encodeURIComponent(approver) : ""
      const r = await api("POST", "/flow/resume-urls" + q)
      if (!r.ok) throw new Error("approval.getResumeUrls failed: " + r.status)
      return r.json()
    },
  },
  flow: {
    resumeValue: KIND === "flow_resume" ? input : undefined,
  },
}

try {
  const Main = await import(%q)
  if (typeof Main.main !== "function") throw new Error("main function is missing")
  const result = await Main.main(ctx)
  writeFileSync("result.json", JSON.stringify(result === undefined ? null : result))
  process.exit(0)
} catch (e) {
  const err = { message: String((e && e.message) != null ? e.message : e), name: (e && e.name) || "Error", stack: e && e.stack }
  writeFileSync("result.json", JSON.stringify(err))
  console.error(e)
  process.exit(1)
}
`, fileURL(entrypointAbsPath))
}

func wrapperPy(entrypointAbsPath string) string {
	abs := entrypointAbsPath
	if a, err := filepath.Abs(entrypointAbsPath); err == nil {
		abs = a
	}
	return fmt.Sprintf(`import asyncio
import importlib.util
import json
import os
import sys
import traceback
import urllib.error
import urllib.parse
import urllib.request
from types import SimpleNamespace


def _env(k):
    return os.environ.get(k, "")


_WS = _env("WF_WORKSPACE")
_APP = _env("WF_APP")
_BASE = _env("WF_BASE_URL")
_TOKEN = _env("WF_TOKEN")
_KIND = _env("WF_TRIGGER_KIND")
_STATE_PATH = _env("WF_STATE_PATH")

_vendor = _env("WF_PY_VENDOR")
if _vendor and _vendor not in sys.path:
    sys.path.insert(0, _vendor)

try:
    with open("input.json", "r", encoding="utf-8") as _f:
        _input = json.load(_f)
except Exception:
    _input = {}

_headers = None
try:
    _h = _env("WF_TRIGGER_HEADERS")
    if _h:
        _headers = json.loads(_h)
except Exception:
    _headers = None


def _call(method, url, headers, body):
    data = None
    if body is not None:
        data = json.dumps(body).encode("utf-8")
    req = urllib.request.Request(url, data=data, method=method, headers=headers)
    try:
        with urllib.request.urlopen(req) as resp:
            return resp.status, resp.read()
    except urllib.error.HTTPError as e:
        return e.code, e.read()


def _api(method, path, body=None):
    url = _BASE + "/api/w/" + _WS + path
    headers = {"Authorization": "Bearer " + _TOKEN, "Content-Type": "application/json"}
    return _call(method, url, headers, body)


class _Variables:
    async def get(self, path):
        status, raw = await asyncio.to_thread(_api, "GET", "/variables/get/p/" + path)
        if status < 200 or status >= 300:
            raise RuntimeError("variables.get(" + path + ") failed: " + str(status))
        return json.loads(raw).get("value")


class _Resources:
    async def get(self, path):
        status, raw = await asyncio.to_thread(_api, "GET", "/resources/get/p/" + path)
        if status < 200 or status >= 300:
            raise RuntimeError("resources.get(" + path + ") failed: " + str(status))
        return json.loads(raw)


class _State:
    async def get(self):
        q = "/state?path=" + urllib.parse.quote(_STATE_PATH, safe="")
        status, raw = await asyncio.to_thread(_api, "GET", q)
        if status < 200 or status >= 300:
            raise RuntimeError("state.get failed: " + str(status))
        return json.loads(raw)

    async def set(self, value):
        q = "/state?path=" + urllib.parse.quote(_STATE_PATH, safe="")
        status, _raw = await asyncio.to_thread(_api, "POST", q, value)
        if status < 200 or status >= 300:
            raise RuntimeError("state.set failed: " + str(status))


class _Response:
    def __init__(self, status, body):
        self.status = status
        self.ok = 200 <= status < 300
        self._body = body

    async def json(self):
        return json.loads(self._body)

    async def text(self):
        return self._body.decode("utf-8")


class _Http:
    async def fetch(self, url, **options):
        target = url
        if isinstance(url, str) and url.startswith("/"):
            target = _BASE + url
        headers = dict(options.get("headers") or {})
        if isinstance(target, str) and target.startswith(_BASE):
            headers["Authorization"] = "Bearer " + _TOKEN
        method = options.get("method", "GET")
        body = options.get("body")
        status, raw = await asyncio.to_thread(_call, method, target, headers, body)
        return _Response(status, raw)


class _Approval:
    async def get_resume_urls(self, approver=None):
        q = ""
        if approver:
            q = "?approver=" + urllib.parse.quote(approver, safe="")
        status, raw = await asyncio.to_thread(_api, "POST", "/flow/resume-urls" + q)
        if status < 200 or status >= 300:
            raise RuntimeError("approval.get_resume_urls failed: " + str(status))
        return json.loads(raw)


class _Logger:
    def info(self, *a):
        print(*a)

    def warn(self, *a):
        print(*a, file=sys.stderr)

    def error(self, *a):
        print(*a, file=sys.stderr)

    def debug(self, *a):
        print(*a)


_ctx = SimpleNamespace(
    input=_input,
    trigger=SimpleNamespace(
        kind=_KIND,
        raw=_input if _KIND == "webhook" else None,
        headers=_headers,
        scheduled_for=_env("WF_SCHEDULED_FOR") or None,
    ),
    app=_APP,
    action=_env("WF_ACTION"),
    job=SimpleNamespace(
        id=_env("WF_JOB_ID"),
        workspace=_WS,
        tag=_env("WF_TAG"),
        path=_env("WF_RUNNABLE_PATH") or None,
    ),
    actor=SimpleNamespace(
        email=_env("WF_EMAIL"),
        username=_env("WF_USERNAME"),
        permissioned_as=_env("WF_PERMISSIONED_AS"),
    ),
    logger=_Logger(),
    variables=_Variables(),
    resources=_Resources(),
    state=_State(),
    http=_Http(),
    approval=_Approval(),
    flow=SimpleNamespace(resume_value=(_input if _KIND == "flow_resume" else None)),
)


def _load_main(entry_path):
    entry_dir = os.path.dirname(entry_path)
    if entry_dir and entry_dir not in sys.path:
        sys.path.insert(0, entry_dir)
    spec = importlib.util.spec_from_file_location("__windforce_entry__", entry_path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    main = getattr(module, "main", None)
    if not callable(main):
        raise RuntimeError("main function is missing")
    return main


async def _run():
    main = _load_main(%q)
    result = main(_ctx)
    if asyncio.iscoroutine(result) or hasattr(result, "__await__"):
        result = await result
    return result


try:
    _result = asyncio.run(_run())
    _out = json.dumps(_result)
    _status = 0
except BaseException as _e:
    _out = json.dumps({
        "message": str(_e),
        "name": type(_e).__name__,
        "stack": "".join(traceback.format_exception(type(_e), _e, _e.__traceback__)),
    })
    _status = 1
    traceback.print_exc()

with open("result.json", "w", encoding="utf-8") as _f:
    _f.write(_out)
sys.exit(_status)
`, filepath.ToSlash(abs))
}

func fileURL(scriptPath string) string {
	abs := scriptPath
	if a, err := filepath.Abs(scriptPath); err == nil {
		abs = a
	}
	p := filepath.ToSlash(abs)
	if runtime.GOOS == "windows" {
		return "file:///" + p
	}
	return "file://" + p
}

func synthError(r Result, waitErr error) []byte {
	msg := "job failed"
	if r.TimedOut {
		msg = "job timed out"
	} else if waitErr != nil {
		msg = waitErr.Error()
	}
	data, _ := json.Marshal(map[string]string{"message": msg, "name": "ExecutionError"})
	return data
}

type logStreamer struct {
	mu        sync.Mutex
	pending   bytes.Buffer
	acc       bytes.Buffer
	total     int
	cap       int
	truncated bool
	sink      func([]byte)
}

func (l *logStreamer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(p)
	if l.cap > 0 {
		if l.total >= l.cap {
			l.truncated = true
			return n, nil
		}
		if l.total+len(p) > l.cap {
			p = p[:l.cap-l.total]
			l.truncated = true
		}
	}
	l.pending.Write(p)
	l.total += len(p)
	return n, nil
}

func (l *logStreamer) flush() {
	l.mu.Lock()
	if l.pending.Len() == 0 {
		l.mu.Unlock()
		return
	}
	chunk := make([]byte, l.pending.Len())
	copy(chunk, l.pending.Bytes())
	l.pending.Reset()
	sink := l.sink
	if sink == nil {
		l.acc.Write(chunk)
	}
	l.mu.Unlock()
	if sink != nil {
		sink(chunk)
	}
}

func (l *logStreamer) finalize() {
	l.mu.Lock()
	truncated := l.truncated
	sink := l.sink
	if truncated && sink == nil {
		l.acc.WriteString("\n[log truncated: job exceeded log size cap]\n")
	}
	l.mu.Unlock()
	if truncated && sink != nil {
		sink([]byte("\n[log truncated: job exceeded log size cap]\n"))
	}
}

func (l *logStreamer) accumulated() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.acc.String()
}
