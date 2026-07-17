package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/imprun/windforce-core/internal/bundle"
	"github.com/imprun/windforce-core/internal/contract"
	"github.com/imprun/windforce-core/internal/executor"
	"github.com/imprun/windforce-core/internal/runner"
	"github.com/imprun/windforce-core/internal/token"
)

type Runner struct {
	Store          bundle.Store
	CacheRoot      string
	BaseURL        string
	JobTokenSecret string
	APIToken       string
	BunPath        string
	PythonPath     string
	GoPath         string
	PrepareTimeout time.Duration
}

type RunRequest struct {
	JobID            string
	WorkspaceID      string
	Deployment       contract.Deployment
	Action           string
	Input            json.RawMessage
	TriggerKind      string
	TriggerHeaders   json.RawMessage
	Tag              string
	RunnablePath     string
	InputPath        string
	OutputPath       string
	Timeout          time.Duration
	CreatedBy        string
	PermissionedAs   string
	WorkerGroup      string
	EgressProxyAddr  string
	LogSink          func([]byte)
	LogFlushInterval time.Duration
	LogCapBytes      int
}

const actionAdapterProtocolVersion = "windforce.action-adapter/v1"

type actionAdapterRequest struct {
	Version        string                     `json:"version"`
	WorkDir        string                     `json:"workDir"`
	Command        []string                   `json:"command,omitempty"`
	InputPath      string                     `json:"inputPath"`
	OutputPath     string                     `json:"outputPath"`
	App            string                     `json:"app"`
	Action         string                     `json:"action"`
	Runtime        string                     `json:"runtime,omitempty"`
	Entrypoint     string                     `json:"entrypoint,omitempty"`
	TimeoutMs      int64                      `json:"timeoutMs,omitempty"`
	Env            []string                   `json:"env,omitempty"`
	TriggerHeaders json.RawMessage            `json:"triggerHeaders,omitempty"`
	ActionSpec     contract.Action            `json:"actionSpec"`
	Deployment     contract.Deployment        `json:"deployment"`
	Options        map[string]json.RawMessage `json:"options,omitempty"`
}

// Prepare materializes and prepares the exact source revision using the same
// runtime contract used immediately before execution.
func (r *Runner) Prepare(ctx context.Context, deployment contract.Deployment) (string, error) {
	if r.Store == nil {
		return "", errors.New("bundle store is required")
	}
	if deployment.App == "" {
		return "", errors.New("deployment app is required")
	}
	if deployment.Commit == "" {
		return "", errors.New("deployment commit is required")
	}
	return r.ensureSource(
		ctx,
		deployment.SourceWorkspace(),
		deployment.SourceGitSourceID(),
		deployment.Commit,
		firstNonEmpty(deployment.ScriptLang, "typescript"),
		deployment.Entrypoint,
	)
}

func (r *Runner) Run(ctx context.Context, req RunRequest) (contract.JobResult, error) {
	if r.Store == nil {
		return contract.JobResult{}, errors.New("bundle store is required")
	}
	if req.Deployment.App == "" {
		return contract.JobResult{}, errors.New("deployment app is required")
	}
	if req.Deployment.Commit == "" {
		return contract.JobResult{}, errors.New("deployment commit is required")
	}
	action, ok := req.Deployment.Actions[req.Action]
	if !ok {
		return contract.JobResult{}, fmt.Errorf("action %q not found in app %q", req.Action, req.Deployment.App)
	}
	adapterType := action.AdapterType()
	if adapterType == contract.ActionAdapterCommand && (action.Adapter == nil || len(action.Adapter.Command) == 0) {
		return contract.JobResult{}, fmt.Errorf("action %q has no adapter command", req.Action)
	}

	scriptLang := firstNonEmpty(req.Deployment.ScriptLang, "typescript")
	sourceDir, err := r.Prepare(ctx, req.Deployment)
	if err != nil {
		return contract.JobResult{}, err
	}
	if adapterType == contract.ActionAdapterJSONFile && len(action.Command) == 0 {
		return r.runEntrypoint(ctx, req, sourceDir, action)
	}

	jobDir, err := os.MkdirTemp("", "windforce-core-job-")
	if err != nil {
		return contract.JobResult{}, err
	}
	defer os.RemoveAll(jobDir)

	inputPath := req.InputPath
	if inputPath == "" {
		inputPath = filepath.Join(jobDir, "input.json")
		input := req.Input
		if len(input) == 0 {
			input = json.RawMessage("{}")
		}
		if !json.Valid(input) {
			return contract.JobResult{}, errors.New("input is not valid JSON")
		}
		input = append(append([]byte(nil), input...), '\n')
		if err := os.WriteFile(inputPath, input, 0o644); err != nil {
			return contract.JobResult{}, err
		}
	} else {
		inputPath, err = filepath.Abs(inputPath)
		if err != nil {
			return contract.JobResult{}, err
		}
	}
	outputPath := req.OutputPath
	if outputPath == "" {
		outputPath = filepath.Join(jobDir, "result.json")
	} else {
		outputPath, err = filepath.Abs(outputPath)
		if err != nil {
			return contract.JobResult{}, err
		}
		if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
			return contract.JobResult{}, err
		}
	}

	env := r.jobEnv(req, action)
	env = appendPreparedSourceEnv(env, sourceDir, scriptLang)
	if len(req.TriggerHeaders) > 0 {
		if !json.Valid(req.TriggerHeaders) {
			return contract.JobResult{}, errors.New("trigger headers are not valid JSON")
		}
		env = append(env, "WF_TRIGGER_HEADERS="+string(req.TriggerHeaders))
	}

	timeout := actionTimeout(req, action)

	var execResult runner.JSONSubprocessResult
	var execErr error
	switch adapterType {
	case contract.ActionAdapterJSONFile:
		execResult, execErr = runner.RunJSONSubprocess(ctx, runner.JSONSubprocessRequest{
			WorkDir:     sourceDir,
			Command:     action.Command,
			InputPath:   inputPath,
			OutputPath:  outputPath,
			App:         req.Deployment.App,
			Action:      req.Action,
			Timeout:     timeout,
			Env:         env,
			LogSink:     req.LogSink,
			LogCapBytes: req.LogCapBytes,
		})
	case contract.ActionAdapterCommand:
		adapterRequestPath := filepath.Join(jobDir, "adapter-request.json")
		adapterResultPath := filepath.Join(jobDir, "adapter-result.json")
		adapterEnv := append([]string(nil), action.Adapter.Env...)
		execResult, execErr = runner.RunActionAdapterSubprocess(ctx, runner.ActionAdapterSubprocessRequest{
			WorkDir:     sourceDir,
			Command:     action.Adapter.Command,
			RequestPath: adapterRequestPath,
			ResultPath:  adapterResultPath,
			Request: actionAdapterRequest{
				Version:        actionAdapterProtocolVersion,
				WorkDir:        sourceDir,
				Command:        append([]string(nil), action.Command...),
				InputPath:      inputPath,
				OutputPath:     outputPath,
				App:            req.Deployment.App,
				Action:         req.Action,
				Runtime:        action.Runtime,
				Entrypoint:     action.Entrypoint,
				TimeoutMs:      timeout.Milliseconds(),
				Env:            append([]string(nil), env...),
				TriggerHeaders: append(json.RawMessage(nil), req.TriggerHeaders...),
				ActionSpec:     action,
				Deployment:     req.Deployment,
				Options:        action.Adapter.Options,
			},
			App:         req.Deployment.App,
			Action:      req.Action,
			Timeout:     timeout,
			Env:         adapterEnv,
			LogSink:     req.LogSink,
			LogCapBytes: req.LogCapBytes,
		})
	default:
		return contract.JobResult{}, fmt.Errorf("unsupported action adapter %q", adapterType)
	}

	jobResult := contract.JobResult{
		App:        req.Deployment.App,
		Action:     req.Action,
		ExitCode:   execResult.ExitCode,
		Stdout:     execResult.Stdout,
		Stderr:     execResult.Stderr,
		DurationMs: execResult.DurationMs,
	}
	if execErr != nil {
		jobResult.Error = execErr.Error()
		return jobResult, execErr
	}

	output, readErr := os.ReadFile(outputPath)
	if readErr == nil && len(output) > 0 {
		if json.Valid(output) {
			jobResult.Output = json.RawMessage(output)
		} else {
			jobResult.Error = "output file is not valid JSON"
			return jobResult, errors.New(jobResult.Error)
		}
	}
	return jobResult, nil
}

func (r *Runner) runEntrypoint(ctx context.Context, req RunRequest, sourceDir string, action contract.Action) (contract.JobResult, error) {
	entrypoint := strings.TrimSpace(req.Deployment.Entrypoint)
	if entrypoint == "" {
		return contract.JobResult{}, fmt.Errorf("app %q has no entrypoint", req.Deployment.App)
	}
	normalized, err := contract.NormalizeSourcePath(entrypoint)
	if err != nil {
		return contract.JobResult{}, err
	}
	scriptLang := firstNonEmpty(req.Deployment.ScriptLang, "typescript")
	if scriptLang == "go" {
		normalized = goBinaryRel()
	}
	entrypointPath := filepath.Join(sourceDir, filepath.FromSlash(normalized))
	if rel, relErr := filepath.Rel(sourceDir, entrypointPath); relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return contract.JobResult{}, fmt.Errorf("entrypoint %q escapes source root", entrypoint)
	}

	env := r.jobEnv(req, action)
	if len(req.TriggerHeaders) > 0 {
		if !json.Valid(req.TriggerHeaders) {
			return contract.JobResult{}, errors.New("trigger headers are not valid JSON")
		}
		env = append(env, "WF_TRIGGER_HEADERS="+string(req.TriggerHeaders))
	}

	input, err := requestInput(req)
	if err != nil {
		return contract.JobResult{}, err
	}
	env = appendPreparedSourceEnv(env, sourceDir, scriptLang)
	result, err := executor.Run(ctx, executor.RunParams{
		BunPath:           r.BunPath,
		PythonPath:        r.PythonPath,
		ScriptLang:        scriptLang,
		EntrypointAbsPath: entrypointPath,
		Input:             input,
		Env:               env,
		Timeout:           actionTimeout(req, action),
		LogSink:           req.LogSink,
		LogFlushInterval:  req.LogFlushInterval,
		LogCapBytes:       req.LogCapBytes,
	})
	if err == nil && req.OutputPath != "" {
		if writeErr := writeOutputFile(req.OutputPath, result.Result); writeErr != nil {
			err = writeErr
		}
	}
	jobResult := contract.JobResult{
		App:        req.Deployment.App,
		Action:     req.Action,
		Output:     cloneRaw(result.Result),
		ExitCode:   result.ExitCode,
		Stdout:     result.Logs,
		DurationMs: result.DurationMs,
	}
	if err != nil {
		jobResult.Error = err.Error()
		return jobResult, err
	}
	if !result.Success() {
		jobResult.Error = resultErrorMessage(result)
	}
	return jobResult, nil
}

func writeOutputFile(path string, output json.RawMessage) error {
	outputPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	if len(output) == 0 {
		output = json.RawMessage("null")
	}
	if !json.Valid(output) {
		return errors.New("output is not valid JSON")
	}
	return os.WriteFile(outputPath, append(append([]byte(nil), output...), '\n'), 0o644)
}

func (r *Runner) jobEnv(req RunRequest, action contract.Action) []string {
	workspace := contract.NormalizeWorkspace(firstNonEmpty(req.WorkspaceID, req.Deployment.SourceWorkspace()))
	triggerKind := firstNonEmpty(req.TriggerKind, "api")
	tag := firstNonEmpty(req.Tag, contract.EffectiveRouteTagForAction(req.Deployment, action))
	createdBy := firstNonEmpty(strings.TrimSpace(req.CreatedBy), "system")
	permissionedAs := firstNonEmpty(strings.TrimSpace(req.PermissionedAs), createdBy)
	workerGroup := firstNonEmpty(strings.TrimSpace(req.WorkerGroup), "default")
	egressProxyAddr := strings.TrimSpace(req.EgressProxyAddr)
	env := curatedHostEnv()
	add := func(key string, value string) {
		env = append(env, key+"="+value)
	}
	add("WF_JOB_ID", req.JobID)
	add("WF_WORKSPACE", workspace)
	add("WF_APP", req.Deployment.App)
	add("WF_ACTION", req.Action)
	add("WF_TAG", tag)
	add("WF_RUNNABLE_PATH", strings.TrimSpace(req.RunnablePath))
	add("WF_EMAIL", createdBy)
	add("WF_USERNAME", createdBy)
	add("WF_PERMISSIONED_AS", permissionedAs)
	add("WF_STATE_PATH", req.Deployment.App+"/"+req.Action)
	add("WF_TRIGGER_KIND", triggerKind)
	add("WF_WORKER_GROUP", workerGroup)
	if egressProxyAddr != "" {
		proxyURL := "http://job-" + req.JobID + "@" + egressProxyAddr
		add("WF_PROXY_URL", proxyURL)
		add("HTTP_PROXY", proxyURL)
		add("HTTPS_PROXY", proxyURL)
	}
	add("WF_BASE_URL", r.BaseURL)
	add("WF_TOKEN", r.jobToken(req, action, workspace, permissionedAs))
	return env
}

func (r *Runner) jobToken(req RunRequest, action contract.Action, workspace string, permissionedAs string) string {
	secret := strings.TrimSpace(firstNonEmpty(r.JobTokenSecret, r.APIToken))
	if secret == "" || req.JobID == "" {
		return strings.TrimSpace(r.APIToken)
	}
	expiresAt := int64(0)
	if timeout := actionTimeout(req, action); timeout > 0 {
		expiresAt = time.Now().Add(timeout + 60*time.Second).Unix()
	} else {
		expiresAt = time.Now().Add(time.Hour).Unix()
	}
	return token.MintJob(secret, token.JobClaims{
		Workspace: workspace,
		JobID:     req.JobID,
		Subject:   permissionedAs,
		Exp:       expiresAt,
	})
}

func actionTimeout(req RunRequest, action contract.Action) time.Duration {
	if req.Timeout > 0 {
		return req.Timeout
	}
	if action.TimeoutS != nil && *action.TimeoutS > 0 {
		return time.Duration(*action.TimeoutS) * time.Second
	}
	if action.TimeoutMs > 0 {
		return time.Duration(action.TimeoutMs) * time.Millisecond
	}
	if req.Deployment.TimeoutS > 0 {
		return time.Duration(req.Deployment.TimeoutS) * time.Second
	}
	return 0
}

func requestInput(req RunRequest) (json.RawMessage, error) {
	if req.InputPath == "" {
		return cloneRaw(req.Input), nil
	}
	inputPath, err := filepath.Abs(req.InputPath)
	if err != nil {
		return nil, err
	}
	input, err := os.ReadFile(inputPath)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(input), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func resultErrorMessage(result executor.Result) string {
	if len(result.Result) > 0 {
		var body struct {
			Message string `json:"message"`
			Error   string `json:"error"`
		}
		if err := json.Unmarshal(result.Result, &body); err == nil {
			if body.Message != "" {
				return body.Message
			}
			if body.Error != "" {
				return body.Error
			}
		}
	}
	if result.TimedOut {
		return "action timed out"
	}
	if result.ExitCode != 0 {
		return fmt.Sprintf("action exited with code %d", result.ExitCode)
	}
	return "action failed"
}

var jobHostEnvAllow = map[string]bool{
	"PATH": true, "HOME": true, "TZ": true, "LANG": true, "LANGUAGE": true, "LC_ALL": true, "LC_CTYPE": true,
	"TMPDIR": true, "TMP": true, "TEMP": true,
	"SystemRoot": true, "COMSPEC": true, "PATHEXT": true,
	"PLAYWRIGHT_BROWSERS_PATH": true, "PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD": true,
}

var prepareHostEnvAllow = map[string]bool{
	"PIP_INDEX_URL":       true,
	"PIP_EXTRA_INDEX_URL": true,
	"PIP_TRUSTED_HOST":    true,
	"PIP_FIND_LINKS":      true,
	"PIP_NO_INDEX":        true,
	"PIP_CERT":            true,
	"PIP_CLIENT_CERT":     true,
}

func curatedHostEnv() []string {
	values := os.Environ()
	env := make([]string, 0, len(values))
	for _, value := range values {
		key := value
		if index := strings.IndexByte(value, '='); index >= 0 {
			key = value[:index]
		}
		if jobHostEnvAllow[key] {
			env = append(env, value)
		}
	}
	return env
}

func curatedPrepareEnv() []string {
	values := os.Environ()
	env := make([]string, 0, len(values))
	for _, value := range values {
		key := value
		if index := strings.IndexByte(value, '='); index >= 0 {
			key = value[:index]
		}
		if jobHostEnvAllow[key] || prepareHostEnvAllow[key] {
			env = append(env, value)
		}
	}
	return env
}

func safePath(value string) string {
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '.', r == '-', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
	}
	if builder.Len() == 0 {
		return "_"
	}
	return builder.String()
}

func cloneRaw(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), value...)
}
