package server

import (
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/imprun/windforce-core/internal/contract"
	executionpkg "github.com/imprun/windforce-core/internal/execution"
	"github.com/imprun/windforce-core/internal/state"
)

const (
	defaultPublicAPIRPS   = 100
	publicJobIDHeader     = "X-WF-Job-Id"
	publicAPITriggerActor = "system:public-api"
)

type requestRateLimiter struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

func newRequestRateLimiter(rate float64, burst int) *requestRateLimiter {
	if rate <= 0 {
		rate = defaultPublicAPIRPS
	}
	if burst <= 0 {
		burst = int(math.Ceil(rate))
	}
	return &requestRateLimiter{rate: rate, burst: float64(burst), tokens: float64(burst)}
}

func (l *requestRateLimiter) Allow(now time.Time) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.last.IsZero() {
		l.last = now
	} else if now.After(l.last) {
		l.tokens = math.Min(l.burst, l.tokens+now.Sub(l.last).Seconds()*l.rate)
		l.last = now
	}
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}

func (h *Handler) handlePublicAPI(w http.ResponseWriter, r *http.Request) bool {
	parts := splitPath(r.URL.Path)
	if len(parts) < 3 || parts[0] != "api" || parts[1] != "v1" || parts[2] != "w" {
		return false
	}
	if r.Method != http.MethodPost || (len(parts) != 7 && len(parts) != 8) || parts[4] != "run" || (len(parts) == 8 && parts[7] != "wait") {
		writeError(w, http.StatusNotFound, "not found")
		return true
	}
	if h.store == nil || h.execution == nil {
		writeError(w, http.StatusServiceUnavailable, "public API is not configured")
		return true
	}
	workspaceID, app, action := parts[3], parts[5], parts[6]
	client, ok := h.authorizePublicClient(w, r, workspaceID)
	if !ok {
		return true
	}
	workspace, err := h.store.GetWorkspace(r.Context(), workspaceID)
	if err != nil {
		h.recordPublicTriggerAudit(r, workspaceID, client.ID, "trigger_rejected", app, action, "", false, "workspace unavailable")
		writeStateError(w, err)
		return true
	}
	if workspace.Status == state.WorkspaceArchived {
		h.recordPublicTriggerAudit(r, workspaceID, client.ID, "trigger_rejected", app, action, "", false, "workspace is archived")
		writeError(w, http.StatusConflict, "workspace is archived")
		return true
	}
	var timeout time.Duration
	if len(parts) == 8 {
		timeout, ok = parsePublicWaitTimeout(w, r)
		if !ok {
			h.recordPublicTriggerAudit(r, workspaceID, client.ID, "trigger_rejected", app, action, "", false, "invalid wait timeout")
			return true
		}
	}
	input, ok := readRunInput(w, r)
	if !ok {
		h.recordPublicTriggerAudit(r, workspaceID, client.ID, "trigger_rejected", app, action, "", false, "invalid request body")
		return true
	}
	actor := "client:" + client.ID
	admission, err := h.execution.CreateRun(r.Context(), executionpkg.CreateRunRequest{
		Workspace:      workspaceID,
		App:            app,
		Action:         action,
		Input:          input,
		Adapter:        "http",
		TriggerKind:    "http",
		CorrelationID:  r.Header.Get("X-Request-ID"),
		IdempotencyKey: r.Header.Get("Idempotency-Key"),
		ClientID:       client.ID,
		CreatedBy:      actor,
		PermissionedAs: actor,
	})
	if err != nil {
		h.recordPublicTriggerAudit(r, workspaceID, client.ID, "trigger_rejected", app, action, "", false, publicTriggerRejectionReason(err))
		writeLegacyExecutionFault(w, err)
		return true
	}
	jobID := admission.Job.ID
	if jobID == "" {
		h.recordPublicTriggerAudit(r, workspaceID, client.ID, "trigger_rejected", app, action, "", admission.Replayed, "admission did not return a job")
		writeError(w, http.StatusInternalServerError, "admission did not return a job")
		return true
	}
	h.recordPublicTriggerAudit(r, workspaceID, client.ID, "trigger_admitted", app, action, jobID, admission.Replayed, "")
	w.Header().Set(publicJobIDHeader, jobID)
	if len(parts) == 7 {
		writeJSON(w, http.StatusCreated, map[string]string{"job_id": jobID})
		return true
	}
	h.waitForPublicResult(w, r, workspaceID, jobID, timeout)
	return true
}

func parsePublicWaitTimeout(w http.ResponseWriter, r *http.Request) (time.Duration, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("timeout"))
	if raw == "" {
		return defaultRunWaitTimeout, true
	}
	timeout, err := time.ParseDuration(raw)
	if err != nil || timeout < 0 {
		writeError(w, http.StatusBadRequest, "timeout must be a non-negative duration")
		return 0, false
	}
	if timeout > maxRunWaitTimeout {
		timeout = maxRunWaitTimeout
	}
	return timeout, true
}

func (h *Handler) authorizePublicClient(w http.ResponseWriter, r *http.Request, workspaceID string) (state.Client, bool) {
	value := bearer(r)
	if !strings.HasPrefix(value, contract.ClientTokenPrefix) {
		h.recordPublicAuthFailure(r, workspaceID)
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return state.Client{}, false
	}
	client, err := h.store.GetClientByTokenHash(r.Context(), workspaceID, state.HashClientToken(value))
	if errors.Is(err, state.ErrNotFound) {
		h.recordPublicAuthFailure(r, workspaceID)
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return state.Client{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not authenticate client")
		return state.Client{}, false
	}
	return client, true
}

func (h *Handler) recordPublicAuthFailure(r *http.Request, workspaceID string) {
	if h.store == nil {
		return
	}
	if err := h.store.AppendClientAudit(r.Context(), workspaceID, "", "trigger_auth_failed", "invalid client token", publicAPITriggerActor); err != nil {
		log.Printf("public API auth audit failed workspace=%s error=%q", contract.NormalizeWorkspace(workspaceID), err)
	}
}

func (h *Handler) recordPublicTriggerAudit(r *http.Request, workspaceID string, clientID string, kind string, app string, action string, jobID string, replayed bool, reason string) {
	if h.store == nil {
		return
	}
	detail, err := json.Marshal(map[string]any{
		"action":   strings.TrimSpace(action),
		"app":      strings.TrimSpace(app),
		"job_id":   strings.TrimSpace(jobID),
		"reason":   strings.TrimSpace(reason),
		"replayed": replayed,
	})
	if err != nil {
		log.Printf("public API trigger audit encoding failed workspace=%s client=%s error=%q", contract.NormalizeWorkspace(workspaceID), clientID, err)
		return
	}
	if err := h.store.AppendClientAudit(r.Context(), workspaceID, clientID, kind, string(detail), publicAPITriggerActor); err != nil {
		log.Printf("public API trigger audit failed workspace=%s client=%s kind=%s error=%q", contract.NormalizeWorkspace(workspaceID), clientID, kind, err)
	}
}

func publicTriggerRejectionReason(err error) string {
	var locked *state.LockedKeysError
	if errors.As(err, &locked) {
		return "locked input keys"
	}
	var fault *executionpkg.Fault
	if !errors.As(err, &fault) {
		return "admission failed"
	}
	if fault.Kind == executionpkg.FaultInvalidRequest {
		switch {
		case strings.HasPrefix(fault.Message, "input does not match action schema"):
			return "input does not match action schema"
		default:
			return "invalid request"
		}
	}
	return string(fault.Kind)
}

func (h *Handler) waitForPublicResult(w http.ResponseWriter, r *http.Request, workspaceID string, jobID string, timeout time.Duration) {
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
		_, result, done := jobResult(job, run)
		if done {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(result)
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
