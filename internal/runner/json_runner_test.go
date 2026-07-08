package runner

import (
	"context"
	"fmt"
	"os"
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
	default:
		os.Exit(2)
	}
}
