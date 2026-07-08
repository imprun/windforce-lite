package runner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"time"
)

// JSONSubprocessRequest describes one action subprocess execution.
type JSONSubprocessRequest struct {
	WorkDir    string
	Command    []string
	InputPath  string
	OutputPath string
	App        string
	Action     string
	Timeout    time.Duration
	Env        []string
}

// JSONSubprocessResult captures process output. Non-zero exit is represented
// here and is not returned as an infrastructure error.
type JSONSubprocessResult struct {
	ExitCode   int    `json:"exitCode"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	DurationMs int64  `json:"durationMs"`
}

// RunJSONSubprocess executes an action subprocess with file-based JSON IO.
func RunJSONSubprocess(ctx context.Context, req JSONSubprocessRequest) (JSONSubprocessResult, error) {
	if len(req.Command) == 0 {
		return JSONSubprocessResult{ExitCode: -1}, errors.New("command is required")
	}

	runCtx := ctx
	var cancel context.CancelFunc
	if req.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	started := time.Now()
	cmd := exec.CommandContext(runCtx, req.Command[0], req.Command[1:]...)
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}
	cmd.Env = append(os.Environ(),
		"WINDFORCE_INPUT_JSON="+req.InputPath,
		"WINDFORCE_OUTPUT_JSON="+req.OutputPath,
		"WINDFORCE_APP="+req.App,
		"WINDFORCE_ACTION="+req.Action,
	)
	cmd.Env = append(cmd.Env, req.Env...)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	} else if err != nil {
		exitCode = -1
	}

	res := JSONSubprocessResult{
		ExitCode:   exitCode,
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		DurationMs: time.Since(started).Milliseconds(),
	}

	if runCtx.Err() != nil {
		return res, runCtx.Err()
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return res, nil
	}
	return res, err
}
