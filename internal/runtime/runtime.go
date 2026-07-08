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
	Deployment contract.Deployment
	Action     string
	Input      json.RawMessage
	InputPath  string
	OutputPath string
	Timeout    time.Duration
	Env        []string
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
	if len(action.Command) == 0 {
		return contract.JobResult{}, fmt.Errorf("action %q has no command", req.Action)
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

	timeout := req.Timeout
	if timeout == 0 && action.TimeoutMs > 0 {
		timeout = time.Duration(action.TimeoutMs) * time.Millisecond
	}

	execResult, err := runner.RunJSONSubprocess(ctx, runner.JSONSubprocessRequest{
		WorkDir:    sourceDir,
		Command:    action.Command,
		InputPath:  inputPath,
		OutputPath: outputPath,
		App:        req.Deployment.App,
		Action:     req.Action,
		Timeout:    timeout,
		Env:        req.Env,
	})

	jobResult := contract.JobResult{
		App:        req.Deployment.App,
		Action:     req.Action,
		ExitCode:   execResult.ExitCode,
		Stdout:     execResult.Stdout,
		Stderr:     execResult.Stderr,
		DurationMs: execResult.DurationMs,
	}
	if err != nil {
		jobResult.Error = err.Error()
		return jobResult, err
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
