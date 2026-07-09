package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRunJSONSubprocessSuccess(t *testing.T) {
	res, err := RunJSONSubprocess(context.Background(), JSONSubprocessRequest{
		Command:    []string{os.Args[0], "-test.run=TestHelperProcess", "--"},
		InputPath:  "input.json",
		OutputPath: "output.json",
		App:        "test-app",
		Action:     "test-action",
		Env:        []string{"WINDFORCE_LITE_HELPER=success"},
	})
	if err != nil {
		t.Fatalf("RunJSONSubprocess returned error: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", res.ExitCode)
	}
	if res.Stdout != "test-app:test-action:input.json:output.json\n" {
		t.Fatalf("stdout = %q", res.Stdout)
	}
}

func TestRunJSONSubprocessNonZeroExitIsResult(t *testing.T) {
	res, err := RunJSONSubprocess(context.Background(), JSONSubprocessRequest{
		Command: []string{os.Args[0], "-test.run=TestHelperProcess", "--"},
		Env:     []string{"WINDFORCE_LITE_HELPER=fail"},
	})
	if err != nil {
		t.Fatalf("RunJSONSubprocess returned harness error: %v", err)
	}
	if res.ExitCode != 7 {
		t.Fatalf("exit code = %d, want 7", res.ExitCode)
	}
}

func TestRunActionAdapterSubprocessSuccess(t *testing.T) {
	tempDir := t.TempDir()
	outputPath := filepath.Join(tempDir, "output.json")
	resultPath := filepath.Join(tempDir, "adapter-result.json")

	res, err := RunActionAdapterSubprocess(context.Background(), ActionAdapterSubprocessRequest{
		Command:     []string{os.Args[0], "-test.run=TestHelperProcess", "--"},
		RequestPath: filepath.Join(tempDir, "adapter-request.json"),
		ResultPath:  resultPath,
		Request: map[string]any{
			"version":    "windforce.action-adapter/v1",
			"command":    []string{"solution", "script"},
			"inputPath":  "input.json",
			"outputPath": outputPath,
		},
		App:    "test-app",
		Action: "test-action",
		Env:    []string{"WINDFORCE_LITE_HELPER=adapter"},
	})
	if err != nil {
		t.Fatalf("RunActionAdapterSubprocess returned error: %v", err)
	}
	if res.ExitCode != 0 || res.Stdout != "script stdout" {
		t.Fatalf("adapter result = %#v", res)
	}
	output, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("ReadFile output returned error: %v", err)
	}
	if string(output) != `{"adapter":"ok"}` {
		t.Fatalf("output = %s", output)
	}
}

func TestHelperProcess(t *testing.T) {
	switch os.Getenv("WINDFORCE_LITE_HELPER") {
	case "":
		return
	case "success":
		fmt.Printf("%s:%s:%s:%s\n",
			os.Getenv("WINDFORCE_APP"),
			os.Getenv("WINDFORCE_ACTION"),
			os.Getenv("WINDFORCE_INPUT_JSON"),
			os.Getenv("WINDFORCE_OUTPUT_JSON"),
		)
		os.Exit(0)
	case "fail":
		os.Exit(7)
	case "adapter":
		var request struct {
			Command    []string `json:"command"`
			OutputPath string   `json:"outputPath"`
		}
		requestPath := os.Getenv("WINDFORCE_ADAPTER_REQUEST_JSON")
		requestBytes, err := os.ReadFile(requestPath)
		if err != nil {
			os.Exit(3)
		}
		if err := json.Unmarshal(requestBytes, &request); err != nil {
			os.Exit(3)
		}
		if len(request.Command) != 2 || request.Command[0] != "solution" || request.Command[1] != "script" {
			os.Exit(4)
		}
		if err := os.WriteFile(request.OutputPath, []byte(`{"adapter":"ok"}`), 0o644); err != nil {
			os.Exit(5)
		}
		resultBytes, err := json.Marshal(JSONSubprocessResult{ExitCode: 0, Stdout: "script stdout"})
		if err != nil {
			os.Exit(6)
		}
		if err := os.WriteFile(os.Getenv("WINDFORCE_ADAPTER_RESULT_JSON"), resultBytes, 0o644); err != nil {
			os.Exit(6)
		}
		os.Exit(0)
	default:
		os.Exit(2)
	}
}
