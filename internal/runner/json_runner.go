package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
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
	LogSink    func([]byte)
}

// JSONSubprocessResult captures process output. Non-zero exit is represented
// here and is not returned as an infrastructure error.
type JSONSubprocessResult struct {
	ExitCode   int    `json:"exitCode"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	DurationMs int64  `json:"durationMs"`
}

// ActionAdapterSubprocessRequest describes an adapter subprocess execution.
// The adapter receives a Windforce action-adapter request file and must write a
// JSONSubprocessResult-compatible result file. The adapter is responsible for
// translating that request into the concrete script contract it owns.
type ActionAdapterSubprocessRequest struct {
	WorkDir     string
	Command     []string
	RequestPath string
	ResultPath  string
	Request     any
	App         string
	Action      string
	Timeout     time.Duration
	Env         []string
	LogSink     func([]byte)
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
	cmd.Env = append([]string(nil), req.Env...)
	cmd.Env = append(cmd.Env,
		"WF_INPUT_JSON="+req.InputPath,
		"WF_RESULT_JSON="+req.OutputPath,
		"WF_APP="+req.App,
		"WF_ACTION="+req.Action,
	)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = logWriter(&stdout, req.LogSink)
	cmd.Stderr = logWriter(&stderr, req.LogSink)

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

func RunActionAdapterSubprocess(ctx context.Context, req ActionAdapterSubprocessRequest) (JSONSubprocessResult, error) {
	if len(req.Command) == 0 {
		return JSONSubprocessResult{ExitCode: -1}, errors.New("adapter command is required")
	}
	if req.RequestPath == "" {
		return JSONSubprocessResult{ExitCode: -1}, errors.New("adapter request path is required")
	}
	if req.ResultPath == "" {
		return JSONSubprocessResult{ExitCode: -1}, errors.New("adapter result path is required")
	}

	requestPayload := req.Request
	if requestPayload == nil {
		requestPayload = map[string]any{}
	}
	requestBytes, err := json.Marshal(requestPayload)
	if err != nil {
		return JSONSubprocessResult{ExitCode: -1}, fmt.Errorf("marshal adapter request: %w", err)
	}
	requestBytes = append(requestBytes, '\n')
	if err := os.WriteFile(req.RequestPath, requestBytes, 0o644); err != nil {
		return JSONSubprocessResult{ExitCode: -1}, err
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
	cmd.Env = append([]string(nil), req.Env...)
	cmd.Env = append(cmd.Env,
		"WF_ADAPTER_REQUEST_JSON="+req.RequestPath,
		"WF_ADAPTER_RESULT_JSON="+req.ResultPath,
		"WF_APP="+req.App,
		"WF_ACTION="+req.Action,
	)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = logWriter(&stdout, req.LogSink)
	cmd.Stderr = logWriter(&stderr, req.LogSink)

	err = cmd.Run()
	processResult := JSONSubprocessResult{
		ExitCode:   0,
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		DurationMs: time.Since(started).Milliseconds(),
	}
	if cmd.ProcessState != nil {
		processResult.ExitCode = cmd.ProcessState.ExitCode()
	} else if err != nil {
		processResult.ExitCode = -1
	}
	if runCtx.Err() != nil {
		return processResult, runCtx.Err()
	}
	if err != nil {
		return processResult, err
	}

	resultBytes, err := os.ReadFile(req.ResultPath)
	if err != nil {
		return processResult, fmt.Errorf("read adapter result: %w", err)
	}
	var adapterResult JSONSubprocessResult
	if err := json.Unmarshal(resultBytes, &adapterResult); err != nil {
		return processResult, fmt.Errorf("decode adapter result: %w", err)
	}
	if adapterResult.DurationMs == 0 {
		adapterResult.DurationMs = processResult.DurationMs
	}
	if adapterResult.Stdout != "" && req.LogSink != nil {
		req.LogSink([]byte(adapterResult.Stdout))
	}
	if adapterResult.Stderr != "" && req.LogSink != nil {
		req.LogSink([]byte(adapterResult.Stderr))
	}
	adapterResult.Stdout = joinLogText(processResult.Stdout, adapterResult.Stdout)
	adapterResult.Stderr = joinLogText(processResult.Stderr, adapterResult.Stderr)
	return adapterResult, nil
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

func joinLogText(left string, right string) string {
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}
	if strings.HasSuffix(left, "\n") || strings.HasSuffix(left, "\r") {
		return left + right
	}
	return left + "\n" + right
}
