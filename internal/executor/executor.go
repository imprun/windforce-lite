// Package executor runs one Windforce action using the ctx-first runtime
// contract: input.json and result.json live in a per-job directory, WF_* env
// carries platform context, and stdout/stderr are streamed as job logs.
package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
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
	LogSink           func([]byte)
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
	wrapperContent func(string) string
	argv           func(RunParams) []string
}

var ErrScriptLang = errors.New("unsupported script_lang")

func runtimeFor(lang string) (langRuntime, error) {
	switch strings.TrimSpace(lang) {
	case "", "typescript":
		return langRuntime{
			label:          "bun",
			wrapperName:    "wrapper.ts",
			wrapperContent: wrapperTS,
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
				python := p.PythonPath
				if python == "" {
					if goruntime.GOOS == "windows" {
						python = "python"
					} else {
						python = "python3"
					}
				}
				return []string{python, "wrapper.py"}
			},
		}, nil
	default:
		return langRuntime{}, fmt.Errorf("%w: %q", ErrScriptLang, lang)
	}
}

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
	if len(bytes.TrimSpace(input)) == 0 {
		input = []byte("{}")
	}
	if !json.Valid(input) {
		return Result{}, errors.New("input is not valid JSON")
	}

	jobDir, err := os.MkdirTemp(p.BaseDir, "job-")
	if err != nil {
		return Result{}, fmt.Errorf("mkdir job dir: %w", err)
	}
	defer os.RemoveAll(jobDir)

	if err := os.WriteFile(filepath.Join(jobDir, "input.json"), input, 0o644); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(filepath.Join(jobDir, rt.wrapperName), []byte(rt.wrapperContent(entrypoint)), 0o644); err != nil {
		return Result{}, err
	}

	runCtx := ctx
	var cancel context.CancelFunc
	if p.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, p.Timeout)
		defer cancel()
	}

	argv := rt.argv(p)
	started := time.Now()
	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	cmd.Dir = jobDir
	cmd.Env = append([]string(nil), p.Env...)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = logWriter(&stdout, p.LogSink)
	cmd.Stderr = logWriter(&stderr, p.LogSink)

	waitErr := cmd.Run()
	timedOut := errors.Is(runCtx.Err(), context.DeadlineExceeded)
	res := Result{
		Logs:       stdout.String() + stderr.String(),
		TimedOut:   timedOut,
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

func logWriter(buffer *bytes.Buffer, sink func([]byte)) io.Writer {
	if sink == nil {
		return buffer
	}
	return io.MultiWriter(buffer, logSinkWriter{sink: sink})
}

type logSinkWriter struct {
	sink func([]byte)
}

func (w logSinkWriter) Write(chunk []byte) (int, error) {
	if len(chunk) > 0 {
		w.sink(append([]byte(nil), chunk...))
	}
	return len(chunk), nil
}

func synthError(res Result, err error) []byte {
	message := "action failed"
	if res.TimedOut {
		message = "action timed out"
	} else if err != nil {
		message = err.Error()
	} else if res.ExitCode != 0 {
		message = fmt.Sprintf("action exited with code %d", res.ExitCode)
	}
	data, marshalErr := json.Marshal(map[string]any{
		"message":  message,
		"name":     "Error",
		"exitCode": res.ExitCode,
	})
	if marshalErr != nil {
		return []byte(`{"message":"action failed","name":"Error"}`)
	}
	return data
}

func wrapperTS(entrypointAbsPath string) string {
	return fmt.Sprintf(`import { readFileSync, writeFileSync } from "node:fs"

const env = (k) => process.env[k] ?? ""
let input = {}
try { input = JSON.parse(readFileSync("input.json", "utf8")) } catch { input = {} }

let headers = undefined
try { const h = env("WF_TRIGGER_HEADERS"); if (h) headers = JSON.parse(h) } catch { headers = undefined }

const ctx = {
  input,
  trigger: {
    kind: env("WF_TRIGGER_KIND"),
    raw: env("WF_TRIGGER_KIND") === "webhook" ? input : undefined,
    headers,
    scheduledFor: env("WF_SCHEDULED_FOR") || undefined,
  },
  app: env("WF_APP"),
  action: env("WF_ACTION"),
  job: { id: env("WF_JOB_ID"), workspace: env("WF_WORKSPACE"), tag: env("WF_TAG"), path: env("WF_RUNNABLE_PATH") || undefined },
  actor: { email: env("WF_EMAIL"), username: env("WF_USERNAME"), permissionedAs: env("WF_PERMISSIONED_AS") },
  logger: {
    info: (...a) => console.log(...a),
    warn: (...a) => console.error(...a),
    error: (...a) => console.error(...a),
    debug: (...a) => console.log(...a),
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
	return fmt.Sprintf(`import asyncio
import importlib.util
import json
import os
import sys
import traceback
from types import SimpleNamespace

def _env(k):
    return os.environ.get(k, "")

try:
    with open("input.json", "r", encoding="utf-8") as _f:
        _input = json.load(_f)
except Exception:
    _input = {}

try:
    _headers = json.loads(_env("WF_TRIGGER_HEADERS")) if _env("WF_TRIGGER_HEADERS") else None
except Exception:
    _headers = None

class _Logger:
    def info(self, *a): print(*a)
    def warn(self, *a): print(*a, file=sys.stderr)
    def error(self, *a): print(*a, file=sys.stderr)
    def debug(self, *a): print(*a)

_ctx = SimpleNamespace(
    input=_input,
    trigger=SimpleNamespace(
        kind=_env("WF_TRIGGER_KIND"),
        raw=_input if _env("WF_TRIGGER_KIND") == "webhook" else None,
        headers=_headers,
        scheduled_for=_env("WF_SCHEDULED_FOR") or None,
    ),
    app=_env("WF_APP"),
    action=_env("WF_ACTION"),
    job=SimpleNamespace(
        id=_env("WF_JOB_ID"),
        workspace=_env("WF_WORKSPACE"),
        tag=_env("WF_TAG"),
        path=_env("WF_RUNNABLE_PATH") or None,
    ),
    actor=SimpleNamespace(
        email=_env("WF_EMAIL"),
        username=_env("WF_USERNAME"),
        permissioned_as=_env("WF_PERMISSIONED_AS"),
    ),
    logger=_Logger(),
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
    result = _load_main(%q)(_ctx)
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
`, filepath.ToSlash(entrypointAbsPath))
}

func fileURL(scriptPath string) string {
	abs := scriptPath
	if a, err := filepath.Abs(scriptPath); err == nil {
		abs = a
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	return u.String()
}
