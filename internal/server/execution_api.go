package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/imprun/windforce-core/internal/contract"
	executionpkg "github.com/imprun/windforce-core/internal/execution"
	"github.com/imprun/windforce-core/internal/state"
)

type executionCreateRunRequest struct {
	App            string          `json:"app"`
	Action         string          `json:"action"`
	Input          json.RawMessage `json:"input"`
	Adapter        string          `json:"adapter,omitempty"`
	TriggerKind    string          `json:"trigger_kind,omitempty"`
	TriggerHeaders json.RawMessage `json:"trigger_headers,omitempty"`
	CorrelationID  string          `json:"correlation_id,omitempty"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
	Env            []string        `json:"env,omitempty"`
	ClientID       string          `json:"client_id,omitempty"`
}

type executionPinnedRelease struct {
	DeploymentID *string `json:"deployment_id,omitempty"`
	Commit       string  `json:"commit"`
	BundleDigest string  `json:"bundle_digest,omitempty"`
	RouteTag     string  `json:"route_tag"`
}

type executionRunView struct {
	RunID         string                 `json:"run_id"`
	JobID         string                 `json:"job_id,omitempty"`
	State         state.RunState         `json:"state"`
	App           string                 `json:"app"`
	Action        string                 `json:"action"`
	CorrelationID string                 `json:"correlation_id,omitempty"`
	PinnedRelease executionPinnedRelease `json:"pinned_release"`
	Replayed      bool                   `json:"replayed,omitempty"`
	CreatedAt     time.Time              `json:"created_at"`
	UpdatedAt     time.Time              `json:"updated_at"`
}

func (h *Handler) handleExecutionAPI(w http.ResponseWriter, r *http.Request) bool {
	parts := splitPath(r.URL.Path)
	if len(parts) == 3 && parts[0] == "execution" && parts[1] == "v1" && parts[2] == "openapi.json" && r.Method == http.MethodGet {
		h.handleExecutionOpenAPI(w, r)
		return true
	}
	if len(parts) < 5 || parts[0] != "execution" || parts[1] != "v1" || parts[2] != "workspaces" {
		return false
	}
	workspaceID := parts[3]
	if len(parts) == 5 && parts[4] == "runs" && r.Method == http.MethodPost {
		h.handleExecutionCreateRun(w, r, workspaceID)
		return true
	}
	if len(parts) == 6 && parts[4] == "runs" && r.Method == http.MethodGet {
		h.handleExecutionGetRun(w, r, workspaceID, parts[5])
		return true
	}
	if len(parts) == 7 && parts[4] == "runs" && parts[6] == "result" && r.Method == http.MethodGet {
		h.handleExecutionRunResult(w, r, workspaceID, parts[5])
		return true
	}
	if len(parts) == 7 && parts[4] == "runs" && parts[6] == "cancel" && r.Method == http.MethodPost {
		h.handleExecutionCancelRun(w, r, workspaceID, parts[5])
		return true
	}
	if len(parts) == 6 && parts[4] == "apps" && r.Method == http.MethodGet {
		h.handleExecutionDescribeApp(w, r, workspaceID, parts[5])
		return true
	}
	return false
}

func (h *Handler) handleExecutionCreateRun(w http.ResponseWriter, r *http.Request, workspaceID string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRunBodyBytes)
	defer r.Body.Close()
	var request executionCreateRunRequest
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&request); err != nil {
		writeExecutionError(w, http.StatusBadRequest, string(executionpkg.FaultInvalidRequest), "request body must be a JSON object")
		return
	}
	if strings.TrimSpace(r.Header.Get("Idempotency-Key")) != "" {
		request.IdempotencyKey = r.Header.Get("Idempotency-Key")
	}
	actor := requestActorSubject(r)
	admission, err := h.execution.CreateRun(r.Context(), executionpkg.CreateRunRequest{
		Workspace:      workspaceID,
		App:            request.App,
		Action:         request.Action,
		Input:          request.Input,
		Adapter:        request.Adapter,
		TriggerKind:    request.TriggerKind,
		TriggerHeaders: request.TriggerHeaders,
		CorrelationID:  request.CorrelationID,
		IdempotencyKey: request.IdempotencyKey,
		Env:            request.Env,
		ClientID:       request.ClientID,
		CreatedBy:      actor,
		PermissionedAs: actor,
	})
	if err != nil {
		writeExecutionFault(w, err)
		return
	}
	status := http.StatusCreated
	if admission.Replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, newExecutionRunView(admission.Run, admission.Job.ID, admission.Replayed))
}

func (h *Handler) handleExecutionGetRun(w http.ResponseWriter, r *http.Request, workspaceID string, runID string) {
	run, err := h.execution.GetRun(r.Context(), workspaceID, runID)
	if err != nil {
		writeExecutionFault(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newExecutionRunView(run, "", false))
}

func (h *Handler) handleExecutionRunResult(w http.ResponseWriter, r *http.Request, workspaceID string, runID string) {
	run, err := h.execution.GetRun(r.Context(), workspaceID, runID)
	if err != nil {
		writeExecutionFault(w, err)
		return
	}
	status := http.StatusOK
	if !state.TerminalRunState(run.State) {
		status = http.StatusAccepted
	}
	writeJSON(w, status, map[string]any{
		"run_id": run.ID,
		"state":  run.State,
		"output": run.Output,
		"result": run.Result,
		"error":  run.Error,
	})
}

func (h *Handler) handleExecutionCancelRun(w http.ResponseWriter, r *http.Request, workspaceID string, runID string) {
	var request struct {
		Reason string `json:"reason"`
	}
	if err := readOptionalJSON(r, &request); err != nil {
		writeExecutionError(w, http.StatusBadRequest, string(executionpkg.FaultInvalidRequest), "request body must be valid JSON")
		return
	}
	run, err := h.execution.CancelRun(r.Context(), workspaceID, runID, request.Reason)
	if err != nil {
		writeExecutionFault(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newExecutionRunView(run, "", false))
}

func (h *Handler) handleExecutionDescribeApp(w http.ResponseWriter, r *http.Request, workspaceID string, app string) {
	description, err := h.execution.DescribeApp(r.Context(), workspaceID, app)
	if err != nil {
		writeExecutionFault(w, err)
		return
	}
	writeJSON(w, http.StatusOK, description)
}

func newExecutionRunView(run state.Run, jobID string, replayed bool) executionRunView {
	action := run.Deployment.Actions[run.Action]
	return executionRunView{
		RunID:         run.ID,
		JobID:         jobID,
		State:         run.State,
		App:           run.App,
		Action:        run.Action,
		CorrelationID: run.CorrelationID,
		PinnedRelease: executionPinnedRelease{
			DeploymentID: run.Deployment.DeploymentID,
			Commit:       run.Deployment.Commit,
			BundleDigest: run.Deployment.BundleDigest,
			RouteTag:     contract.EffectiveRouteTagForAction(run.Deployment, action),
		},
		Replayed:  replayed,
		CreatedAt: run.CreatedAt,
		UpdatedAt: run.UpdatedAt,
	}
}

func writeExecutionFault(w http.ResponseWriter, err error) {
	status, kind := executionFaultStatus(err)
	message := err.Error()
	if errors.Is(err, state.ErrNotFound) && kind == executionpkg.FaultInternal {
		status = http.StatusNotFound
		kind = executionpkg.FaultAppNotFound
		message = "not found"
	}
	writeExecutionError(w, status, string(kind), message)
}

func writeLegacyExecutionFault(w http.ResponseWriter, err error) {
	status, _ := executionFaultStatus(err)
	writeError(w, status, err.Error())
}

func executionFaultStatus(err error) (int, executionpkg.FaultKind) {
	status := http.StatusInternalServerError
	kind := executionpkg.FaultKindOf(err)
	switch kind {
	case executionpkg.FaultUnavailable:
		status = http.StatusServiceUnavailable
	case executionpkg.FaultInvalidRequest:
		status = http.StatusBadRequest
	case executionpkg.FaultAppNotFound, executionpkg.FaultActionNotFound:
		status = http.StatusNotFound
	case executionpkg.FaultRoutingConflict, executionpkg.FaultConflict:
		status = http.StatusConflict
	}
	return status, kind
}

func writeExecutionError(w http.ResponseWriter, status int, code string, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}
