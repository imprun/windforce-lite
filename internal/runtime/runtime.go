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

	"github.com/imprun/windforce-lite/internal/bundle"
	"github.com/imprun/windforce-lite/internal/contract"
	"github.com/imprun/windforce-lite/internal/runner"
)

type Runner struct {
	Store     bundle.Store
	CacheRoot string
}

type RunRequest struct {
	Deployment     contract.Deployment
	Action         string
	Input          json.RawMessage
	TriggerHeaders json.RawMessage
	InputPath      string
	OutputPath     string
	Timeout        time.Duration
	Env            []string
	LogSink        func([]byte)
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
	workspace := req.Deployment.SourceWorkspace()
	gitSourceID := req.Deployment.SourceGitSourceID()

	action, ok := req.Deployment.Actions[req.Action]
	if !ok {
		return contract.JobResult{}, fmt.Errorf("action %q not found in app %q", req.Action, req.Deployment.App)
	}
	adapterType := action.AdapterType()
	if adapterType == contract.ActionAdapterJSONFile && len(action.Command) == 0 {
		return contract.JobResult{}, fmt.Errorf("action %q has no command", req.Action)
	}
	if adapterType == contract.ActionAdapterCommand && (action.Adapter == nil || len(action.Adapter.Command) == 0) {
		return contract.JobResult{}, fmt.Errorf("action %q has no adapter command", req.Action)
	}

	cacheRoot := r.CacheRoot
	if cacheRoot == "" {
		cacheRoot = filepath.Join(os.TempDir(), "windforce-lite-cache")
	}
	sourceDir := filepath.Join(cacheRoot, "src", safePath(workspace), safePath(gitSourceID), safePath(req.Deployment.Commit))
	if err := r.Store.FetchTo(ctx, sourceDir, workspace, gitSourceID, req.Deployment.Commit); err != nil {
		return contract.JobResult{}, err
	}

	jobDir, err := os.MkdirTemp("", "windforce-lite-job-")
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
		outputPath = filepath.Join(jobDir, "output.json")
	} else {
		outputPath, err = filepath.Abs(outputPath)
		if err != nil {
			return contract.JobResult{}, err
		}
		if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
			return contract.JobResult{}, err
		}
	}

	env := append([]string(nil), req.Env...)
	if len(req.TriggerHeaders) > 0 {
		if !json.Valid(req.TriggerHeaders) {
			return contract.JobResult{}, errors.New("trigger headers are not valid JSON")
		}
		triggerHeadersPath := filepath.Join(jobDir, "trigger.headers.json")
		headers := append(append([]byte(nil), req.TriggerHeaders...), '\n')
		if err := os.WriteFile(triggerHeadersPath, headers, 0o644); err != nil {
			return contract.JobResult{}, err
		}
		env = append(env, "WINDFORCE_TRIGGER_HEADERS_JSON="+triggerHeadersPath)
	}

	timeout := req.Timeout
	if timeout == 0 && action.TimeoutMs > 0 {
		timeout = time.Duration(action.TimeoutMs) * time.Millisecond
	}

	var execResult runner.JSONSubprocessResult
	var execErr error
	switch adapterType {
	case contract.ActionAdapterJSONFile:
		execResult, execErr = runner.RunJSONSubprocess(ctx, runner.JSONSubprocessRequest{
			WorkDir:    sourceDir,
			Command:    action.Command,
			InputPath:  inputPath,
			OutputPath: outputPath,
			App:        req.Deployment.App,
			Action:     req.Action,
			Timeout:    timeout,
			Env:        env,
			LogSink:    req.LogSink,
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
			App:     req.Deployment.App,
			Action:  req.Action,
			Timeout: timeout,
			Env:     adapterEnv,
			LogSink: req.LogSink,
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
