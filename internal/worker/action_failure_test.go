package worker

import (
	"encoding/json"
	"testing"

	"github.com/imprun/windforce-core/internal/contract"
)

func TestActionDeclaredFailureSupportsWindforceEnvelope(t *testing.T) {
	message, failed := actionDeclaredFailure(contract.JobResult{
		Output: json.RawMessage(`{"$windforce":{"type":"action_failure","code":"TargetRejected","message":"target rejected"}}`),
	})
	if !failed || message != "TargetRejected" {
		t.Fatalf("failure = %v, message = %q", failed, message)
	}
}

func TestActionDeclaredFailureSupportsResultFailCompatibility(t *testing.T) {
	message, failed := actionDeclaredFailure(contract.JobResult{
		Output: json.RawMessage(`{"RESULT":"FAIL","ECODE":"ERR_TEST","EMSG":"failed"}`),
	})
	if !failed || message != "ERR_TEST" {
		t.Fatalf("failure = %v, message = %q", failed, message)
	}
}
