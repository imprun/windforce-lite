package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/imprun/windforce-lite/internal/contract"
	"github.com/imprun/windforce-lite/internal/state"
)

func (h *Handler) handleJobList(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "state store is not configured")
		return
	}
	query, limit, ok := parseJobListQuery(w, r, workspaceID)
	if !ok {
		return
	}
	query.Limit = limit + 1
	items, err := h.store.ListJobs(r.Context(), query)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	pagination := map[string]any{
		"limit":    limit,
		"count":    len(items),
		"has_more": hasMore,
	}
	if hasMore {
		last := items[len(items)-1]
		pagination["next_cursor"] = encodeJobCursor(last.CreatedAt, last.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "pagination": pagination})
}

func (h *Handler) handleJobSummary(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "state store is not configured")
		return
	}
	recent := 24 * time.Hour
	if raw := strings.TrimSpace(r.URL.Query().Get("recent_seconds")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 || value > 7*24*60*60 {
			writeError(w, http.StatusBadRequest, "recent_seconds must be between 1 and 604800")
			return
		}
		recent = time.Duration(value) * time.Second
	}
	summary, err := h.store.JobSummary(r.Context(), workspaceID, recent)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (h *Handler) handleJobRun(w http.ResponseWriter, r *http.Request, workspaceID string, app string, action string, wait bool) {
	timeout := time.Duration(0)
	if wait {
		var ok bool
		timeout, ok = parseRunWaitTimeout(w, r)
		if !ok {
			return
		}
	}
	job, ok := h.enqueueJobRun(w, r, workspaceID, app, action)
	if !ok {
		return
	}
	if !wait {
		writeJSON(w, http.StatusCreated, map[string]string{"job_id": job.ID})
		return
	}
	h.waitForJobResult(w, r, workspaceID, job.ID, timeout)
}

func (h *Handler) handleJobWebhook(w http.ResponseWriter, r *http.Request, workspaceID string, app string, action string) {
	input, ok := readWebhookRaw(w, r)
	if !ok {
		return
	}
	triggerHeaders := captureWebhookHeaders(r)
	job, ok := h.enqueueJob(w, r, workspaceID, app, action, "webhook", input, triggerHeaders)
	if !ok {
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"job_id": job.ID})
}

func (h *Handler) enqueueJobRun(w http.ResponseWriter, r *http.Request, workspaceID string, app string, action string) (state.Job, bool) {
	input, ok := readRunInput(w, r)
	if !ok {
		return state.Job{}, false
	}
	return h.enqueueJob(w, r, workspaceID, app, action, "api", input, nil)
}

func (h *Handler) enqueueJob(w http.ResponseWriter, r *http.Request, workspaceID string, app string, action string, triggerKind string, input json.RawMessage, triggerHeaders json.RawMessage) (state.Job, bool) {
	if h.store == nil || h.catalog == nil {
		writeError(w, http.StatusServiceUnavailable, "job API is not configured")
		return state.Job{}, false
	}
	if !validAppKey(app) || !validActionKey(action) {
		writeError(w, http.StatusBadRequest, "invalid app/action key")
		return state.Job{}, false
	}
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	deployment, err := h.lookupDeployment(r.Context(), workspaceID, app)
	if err != nil {
		writeError(w, http.StatusNotFound, "app not found: "+app)
		return state.Job{}, false
	}
	actionSpec, ok := deployment.Actions[action]
	if !ok {
		writeError(w, http.StatusNotFound, "action not found: "+app+"/"+action)
		return state.Job{}, false
	}
	effectiveCapabilities := contract.EffectiveCapabilities(deployment.RequiredCapabilities, actionSpec.Capabilities)
	capabilityTagConflict, err := contract.CapabilityTagConflict(deployment.Tag, deployment.TagOverride, actionSpec.Tag, actionSpec.TagOverride, effectiveCapabilities)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return state.Job{}, false
	}
	if capabilityTagConflict {
		writeError(w, http.StatusConflict, "required worker capability conflicts with explicit tag routing")
		return state.Job{}, false
	}
	run := state.NewRun("windforce", "", app, action, deployment, input)
	if actor := requestActorSubject(r); actor != "" {
		run.CreatedBy = actor
		run.PermissionedAs = actor
	}
	if correlationID := state.CleanID(r.Header.Get("X-Request-ID")); correlationID != "" {
		run.CorrelationID = correlationID
	}
	job := state.NewActionJob(run, input)
	job.Payload.TriggerKind = triggerKind
	job.Payload.TriggerHeaders = cloneRawMessage(triggerHeaders)
	schemaReader := h.newCanonicalSchemaReader(r.Context(), deployment)
	defer schemaReader.Close()
	inputSchema, err := schemaReader.Read(actionSpec.InputSchema, actionSpec.InputSchemaBody)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return state.Job{}, false
	}
	outputSchema, err := schemaReader.Read(actionSpec.OutputSchema, actionSpec.OutputSchemaBody)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return state.Job{}, false
	}
	job.Payload.InputSchema = inputSchema
	job.Payload.OutputSchema = outputSchema
	if err := h.store.CreateRunAndEnqueue(r.Context(), run, job); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, state.ErrConflict) {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return state.Job{}, false
	}
	return job, true
}

func (h *Handler) handleJobStatus(w http.ResponseWriter, r *http.Request, workspaceID string, jobID string) {
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "state store is not configured")
		return
	}
	job, run, found, err := h.store.GetJob(r.Context(), workspaceID, jobID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	writeJSON(w, http.StatusOK, newJobStatus(workspaceID, job, run))
}

func (h *Handler) handleJobResult(w http.ResponseWriter, r *http.Request, workspaceID string, jobID string) {
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "state store is not configured")
		return
	}
	job, run, found, err := h.store.GetJob(r.Context(), workspaceID, jobID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	status, result, done := jobResult(job, run)
	if !done {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "pending"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": status, "result": result})
}

func (h *Handler) handleJobCancel(w http.ResponseWriter, r *http.Request, workspaceID string, jobID string) {
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "state store is not configured")
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	_ = readOptionalJSON(r, &request)
	result, err := h.store.CancelJob(r.Context(), workspaceID, jobID, requestActorSubject(r), request.Reason)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !result.Found {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) waitForJobResult(w http.ResponseWriter, r *http.Request, workspaceID string, jobID string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for {
		job, run, found, err := h.store.GetJob(r.Context(), workspaceID, jobID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !found {
			writeError(w, http.StatusNotFound, "job not found")
			return
		}
		status, result, done := jobResult(job, run)
		if done {
			writeJSON(w, http.StatusOK, map[string]any{"job_id": jobID, "status": status, "result": result})
			return
		}
		if !time.Now().Before(deadline) {
			writeJSON(w, http.StatusAccepted, map[string]string{"job_id": jobID, "status": "pending"})
			return
		}
		sleep := 50 * time.Millisecond
		if remaining := time.Until(deadline); remaining < sleep {
			sleep = remaining
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(sleep):
		}
	}
}

func (h *Handler) handleJobLogs(w http.ResponseWriter, r *http.Request, workspaceID string, jobID string) {
	if h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "state store is not configured")
		return
	}
	logs, exists, err := h.store.GetLogs(r.Context(), workspaceID, jobID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	tailBytes, err := parseTailBytes(r.URL.Query().Get("tail_bytes"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	data := []byte(logs)
	if tailBytes >= 0 && len(data) > tailBytes {
		data = data[len(data)-tailBytes:]
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (h *Handler) waitForRun(ctx context.Context, runID string) state.Run {
	run, err := h.store.GetRun(ctx, runID)
	if err != nil || h.wait <= 0 || state.IsSettledForTrigger(run) {
		return run
	}
	waitCtx, cancel := context.WithTimeout(ctx, h.wait)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-waitCtx.Done():
			run, _ = h.store.GetRun(context.Background(), runID)
			return run
		case <-ticker.C:
			current, err := h.store.GetRun(waitCtx, runID)
			if err == nil {
				run = current
				if state.IsSettledForTrigger(run) {
					return run
				}
			}
		}
	}
}

type jobStatusResponse struct {
	ID             string          `json:"id"`
	WorkspaceID    string          `json:"workspace_id"`
	State          string          `json:"state"`
	Status         *string         `json:"status,omitempty"`
	Worker         *string         `json:"worker,omitempty"`
	AppKey         *string         `json:"app_key,omitempty"`
	ActionKey      *string         `json:"action_key,omitempty"`
	TriggerKind    *string         `json:"trigger_kind,omitempty"`
	Kind           *string         `json:"kind,omitempty"`
	GitSourceID    *int64          `json:"git_source_id,omitempty"`
	CommitSha      *string         `json:"commit_sha,omitempty"`
	Entrypoint     *string         `json:"entrypoint,omitempty"`
	InputSchema    json.RawMessage `json:"input_schema,omitempty"`
	OutputSchema   json.RawMessage `json:"output_schema,omitempty"`
	Tag            string          `json:"tag,omitempty"`
	TimeoutS       int32           `json:"timeout_s,omitempty"`
	CreatedBy      string          `json:"created_by,omitempty"`
	PermissionedAs string          `json:"permissioned_as,omitempty"`
	Input          json.RawMessage `json:"input,omitempty"`
	CreatedAt      *time.Time      `json:"created_at,omitempty"`
	StartedAt      *time.Time      `json:"started_at,omitempty"`
	CompletedAt    *time.Time      `json:"completed_at,omitempty"`
	DurationMs     int64           `json:"duration_ms,omitempty"`
	CanceledBy     *string         `json:"canceled_by,omitempty"`
	CanceledReason *string         `json:"canceled_reason,omitempty"`
	FlowRunID      *string         `json:"flow_run_id,omitempty"`
	FlowKey        *string         `json:"flow_key,omitempty"`
	FlowStepKey    *string         `json:"flow_step_key,omitempty"`
}

func newJobStatus(workspaceID string, job state.Job, run state.Run) jobStatusResponse {
	stateValue := "queued"
	var statusValue *string
	var worker *string
	startedAt := job.StartedAt
	var completedAt *time.Time
	if job.LeaseOwner != "" {
		worker = stringPtr(job.LeaseOwner)
	}
	switch job.State {
	case state.JobRunning:
		stateValue = "running"
		if startedAt == nil {
			startedAt = &job.UpdatedAt
		}
	case state.JobSucceeded, state.JobFailed:
		stateValue = "completed"
		status := jobDetailStatus(job, run)
		statusValue = &status
		completedAt = &run.UpdatedAt
	}
	app := job.Payload.App
	action := job.Payload.Action
	kind := job.Kind
	commit := job.Payload.Commit
	tag := strings.TrimSpace(job.Payload.Tag)
	if tag == "" {
		tag = contract.EffectiveRouteTagForAction(job.Payload.Deployment, job.Payload.ActionSpec)
	}
	response := jobStatusResponse{
		ID:             job.ID,
		WorkspaceID:    contract.NormalizeWorkspace(workspaceID),
		State:          stateValue,
		Status:         statusValue,
		Worker:         worker,
		AppKey:         stringPtr(app),
		ActionKey:      stringPtr(action),
		TriggerKind:    stringPtr(jobStatusTriggerKind(job, run)),
		Kind:           stringPtr(kind),
		GitSourceID:    canonicalGitSourceIDPtr(job.Payload.GitSourceID),
		CommitSha:      stringPtr(commit),
		Entrypoint:     stringPtr(jobStatusEntrypoint(job)),
		InputSchema:    cloneRaw(job.Payload.InputSchema),
		OutputSchema:   cloneRaw(job.Payload.OutputSchema),
		Tag:            tag,
		TimeoutS:       timeoutSeconds(job.Payload.ActionSpec.TimeoutMs),
		CreatedBy:      firstNonEmpty(strings.TrimSpace(job.Payload.CreatedBy), strings.TrimSpace(run.CreatedBy)),
		PermissionedAs: firstNonEmpty(strings.TrimSpace(job.Payload.PermissionedAs), strings.TrimSpace(run.PermissionedAs), strings.TrimSpace(job.Payload.CreatedBy), strings.TrimSpace(run.CreatedBy)),
		Input:          cloneRaw(job.Payload.Input),
		CreatedAt:      &job.CreatedAt,
		StartedAt:      startedAt,
		CompletedAt:    completedAt,
		CanceledBy:     firstPresentStringPtr(job.CanceledBy, jobStatusCanceledBy(run)),
		CanceledReason: firstPresentStringPtr(job.CanceledReason, jobStatusCanceledReason(run)),
		FlowRunID:      stringPtr(job.Payload.FlowRunID),
		FlowKey:        stringPtr(job.Payload.FlowKey),
		FlowStepKey:    stringPtr(job.Payload.FlowStepKey),
	}
	if run.Result != nil {
		response.DurationMs = run.Result.DurationMs
	}
	return response
}

func jobStatusEntrypoint(job state.Job) string {
	if entrypoint := strings.TrimSpace(job.Payload.Deployment.Entrypoint); entrypoint != "" {
		return entrypoint
	}
	return strings.TrimSpace(job.Payload.ActionSpec.Entrypoint)
}

func jobStatusTriggerKind(job state.Job, run state.Run) string {
	if job.Payload.TriggerKind != "" {
		return job.Payload.TriggerKind
	}
	return run.Adapter
}

func jobStatusCanceledReason(run state.Run) *string {
	if run.State != state.RunCanceled || len(run.Error) == 0 {
		return nil
	}
	var payload struct {
		Message        string  `json:"message"`
		CanceledReason *string `json:"canceledReason"`
	}
	if json.Unmarshal(run.Error, &payload) == nil {
		if payload.CanceledReason != nil {
			return payload.CanceledReason
		}
		if strings.TrimSpace(payload.Message) != "" {
			return stringPtr(payload.Message)
		}
	}
	return nil
}

func jobStatusCanceledBy(run state.Run) *string {
	if run.State != state.RunCanceled || len(run.Error) == 0 {
		return nil
	}
	var payload struct {
		CanceledBy string `json:"canceledBy"`
	}
	if json.Unmarshal(run.Error, &payload) == nil {
		return stringPtr(strings.TrimSpace(payload.CanceledBy))
	}
	return nil
}

func timeoutSeconds(timeoutMs int64) int32 {
	if timeoutMs <= 0 {
		return 0
	}
	return int32((timeoutMs + 999) / 1000)
}

func jobResult(job state.Job, run state.Run) (string, json.RawMessage, bool) {
	if job.State == state.JobQueued || job.State == state.JobRunning {
		return "", nil, false
	}
	status := terminalJobStatus(job, run)
	switch status {
	case "success":
		return status, rawOrNull(run.Output), true
	case "canceled":
		message := runErrorMessage(run)
		if message == "" {
			message = "job canceled"
		}
		return status, mustRaw(map[string]string{"name": "Canceled", "message": message}), true
	default:
		if run.Result != nil && len(run.Result.Output) > 0 {
			return "failure", rawOrNull(run.Result.Output), true
		}
		message := runErrorMessage(run)
		if message == "" {
			message = "job failed"
		}
		return "failure", mustRaw(map[string]string{"name": "Error", "message": message}), true
	}
}

func terminalJobStatus(job state.Job, run state.Run) string {
	if run.State == state.RunCanceled {
		return "canceled"
	}
	if job.State == state.JobSucceeded || run.State == state.RunSucceeded || run.State == state.RunWaitingHuman {
		return "success"
	}
	return "failure"
}

func jobDetailStatus(job state.Job, run state.Run) string {
	return terminalJobStatus(job, run)
}

func runErrorMessage(run state.Run) string {
	if run.Result != nil && run.Result.Error != "" {
		return run.Result.Error
	}
	if len(run.Error) == 0 {
		return ""
	}
	var envelope struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(run.Error, &envelope) == nil {
		return envelope.Message
	}
	return string(run.Error)
}

func rawOrNull(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return json.RawMessage("null")
	}
	return cloneRaw(value)
}

func mustRaw(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage("null")
	}
	return data
}

func cloneRaw(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), value...)
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

const maxTailBytes = 1048576
const (
	defaultRunWaitTimeout = 30 * time.Second
	maxRunWaitTimeout     = 30 * time.Second
)

func parseTailBytes(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return -1, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, errors.New("tail_bytes must be a non-negative integer")
	}
	if value > maxTailBytes {
		return 0, errors.New("tail_bytes exceeds server limit")
	}
	return int(value), nil
}

func parseRunWaitTimeout(w http.ResponseWriter, r *http.Request) (time.Duration, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("timeout_ms"))
	if raw == "" {
		return defaultRunWaitTimeout, true
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		writeError(w, http.StatusBadRequest, "timeout_ms must be a non-negative integer")
		return 0, false
	}
	timeout := time.Duration(value) * time.Millisecond
	if timeout > maxRunWaitTimeout {
		timeout = maxRunWaitTimeout
	}
	return timeout, true
}

const (
	defaultJobListLimit = 50
	maxJobListLimit     = 500
)

func parseJobListQuery(w http.ResponseWriter, r *http.Request, workspaceID string) (state.JobListQuery, int, bool) {
	query := r.URL.Query()
	status := strings.TrimSpace(query.Get("status"))
	if status == "" {
		status = "all"
	}
	if !validJobStatusFilter(status) {
		writeError(w, http.StatusBadRequest, "invalid status filter")
		return state.JobListQuery{}, 0, false
	}
	order := strings.TrimSpace(query.Get("order"))
	if order != "" && order != "created_at_desc" {
		writeError(w, http.StatusBadRequest, "unsupported order")
		return state.JobListQuery{}, 0, false
	}
	limit := defaultJobListLimit
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 || value > maxJobListLimit {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 500")
			return state.JobListQuery{}, 0, false
		}
		limit = value
	}
	var cursorCreatedAt *time.Time
	cursorID := ""
	if raw := strings.TrimSpace(query.Get("cursor")); raw != "" {
		createdAt, id, err := decodeJobCursor(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid cursor")
			return state.JobListQuery{}, 0, false
		}
		cursorCreatedAt = &createdAt
		cursorID = id
	}
	since, ok := parseOptionalTime(w, query.Get("since"), "since")
	if !ok {
		return state.JobListQuery{}, 0, false
	}
	until, ok := parseOptionalTime(w, query.Get("until"), "until")
	if !ok {
		return state.JobListQuery{}, 0, false
	}
	return state.JobListQuery{
		WorkspaceID:     contract.NormalizeWorkspace(workspaceID),
		Status:          status,
		AppKey:          strings.TrimSpace(query.Get("app")),
		ActionKey:       strings.TrimSpace(query.Get("action")),
		TriggerKind:     strings.TrimSpace(query.Get("trigger_kind")),
		Limit:           limit,
		CursorCreatedAt: cursorCreatedAt,
		CursorID:        cursorID,
		Since:           since,
		Until:           until,
	}, limit, true
}

func validJobStatusFilter(status string) bool {
	switch status {
	case "queued", "running", "success", "failure", "completed", "canceled", "all":
		return true
	default:
		return false
	}
}

func parseOptionalTime(w http.ResponseWriter, raw string, name string) (*time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, true
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, name+" must be RFC3339")
		return nil, false
	}
	return &value, true
}

func encodeJobCursor(createdAt time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(createdAt.UTC().Format(time.RFC3339Nano) + "|" + id))
}

func decodeJobCursor(raw string) (time.Time, string, error) {
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return time.Time{}, "", err
	}
	createdRaw, id, ok := strings.Cut(string(data), "|")
	if !ok {
		return time.Time{}, "", fmt.Errorf("malformed cursor")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdRaw)
	if err != nil {
		return time.Time{}, "", err
	}
	if id == "" || !isCanonicalUUID(id) {
		return time.Time{}, "", fmt.Errorf("malformed cursor")
	}
	return createdAt, id, nil
}

func isCanonicalUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, r := range value {
		switch index {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				return false
			}
		}
	}
	return true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstPresentStringPtr(values ...*string) *string {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
