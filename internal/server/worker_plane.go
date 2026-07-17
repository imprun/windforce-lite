package server

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/imprun/windforce-core/internal/contract"
	"github.com/imprun/windforce-core/internal/state"
)

// The remote worker plane (ADR 0010): the HTTP surface a worker outside the
// engine process uses to register, claim prepared jobs, heartbeat, complete,
// stream logs, and fetch execution artifacts. Input decryption and resolution
// never leave the engine — claims return prepared jobs.

type workerLeaseWire struct {
	JobID      string    `json:"job_id"`
	WorkerID   string    `json:"worker_id"`
	Attempt    int       `json:"attempt"`
	AcquiredAt time.Time `json:"acquired_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

func leaseToWire(lease state.Lease) workerLeaseWire {
	return workerLeaseWire{
		JobID:      lease.JobID,
		WorkerID:   lease.WorkerID,
		Attempt:    lease.Attempt,
		AcquiredAt: lease.AcquiredAt,
		ExpiresAt:  lease.ExpiresAt,
	}
}

func leaseFromWire(wire workerLeaseWire) state.Lease {
	return state.Lease{
		JobID:      wire.JobID,
		WorkerID:   wire.WorkerID,
		Attempt:    wire.Attempt,
		AcquiredAt: wire.AcquiredAt,
		ExpiresAt:  wire.ExpiresAt,
	}
}

func (h *Handler) workerPlaneAuthorized(r *http.Request) bool {
	token := h.workerToken
	if token == "" {
		token = h.adminToken
	}
	return authorized(r, token)
}

func (h *Handler) handleWorkerPlane(w http.ResponseWriter, r *http.Request) {
	if !h.workerPlaneAuthorized(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/worker/v1")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	switch {
	case len(parts) == 1 && parts[0] == "workers" && r.Method == http.MethodPost:
		h.workerPlaneRegister(w, r)
	case len(parts) == 3 && parts[0] == "workers" && parts[2] == "heartbeat" && r.Method == http.MethodPost:
		h.workerPlaneWorkerHeartbeat(w, r, parts[1])
	case len(parts) == 2 && parts[0] == "workers" && r.Method == http.MethodDelete:
		h.workerPlaneDeregister(w, r, parts[1])
	case len(parts) == 1 && parts[0] == "claims" && r.Method == http.MethodPost:
		h.workerPlaneClaim(w, r)
	case len(parts) == 3 && parts[0] == "jobs" && parts[2] == "heartbeat" && r.Method == http.MethodPost:
		h.workerPlaneJobHeartbeat(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "jobs" && parts[2] == "complete" && r.Method == http.MethodPost:
		h.workerPlaneComplete(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "jobs" && parts[2] == "logs" && r.Method == http.MethodPost:
		h.workerPlaneLogs(w, r, parts[1])
	case len(parts) == 2 && parts[0] == "artifacts" && r.Method == http.MethodGet:
		h.workerPlaneArtifact(w, r, parts[1])
	default:
		writeError(w, http.StatusNotFound, "unknown worker plane route")
	}
}

func (h *Handler) workerPlaneRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     string   `json:"id"`
		Group  string   `json:"group"`
		Tags   []string `json:"tags"`
		Labels []string `json:"labels"`
		Slots  int      `json:"slots"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	labels, err := contract.NormalizeLabels(req.Labels, true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		req.ID = state.NewID("worker")
	}
	if err := h.store.RegisterWorker(r.Context(), state.WorkerRecord{
		ID:     req.ID,
		Group:  req.Group,
		Tags:   req.Tags,
		Labels: labels,
		Slots:  req.Slots,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": req.ID})
}

func (h *Handler) workerPlaneWorkerHeartbeat(w http.ResponseWriter, r *http.Request, workerID string) {
	if err := h.store.HeartbeatWorker(r.Context(), workerID); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) workerPlaneDeregister(w http.ResponseWriter, r *http.Request, workerID string) {
	if err := h.store.DeregisterWorker(r.Context(), workerID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// workerPlaneClaim claims one job and prepares its input inside the engine
// (ADR 0010 §2). Preparation failures fail the job here and yield 204 so the
// worker simply polls again.
func (h *Handler) workerPlaneClaim(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkerID   string   `json:"worker_id"`
		Tags       []string `json:"tags"`
		Labels     []string `json:"labels"`
		LeaseTTLMs int64    `json:"lease_ttl_ms"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if strings.TrimSpace(req.WorkerID) == "" {
		writeError(w, http.StatusBadRequest, "worker_id is required")
		return
	}
	leaseTTL := time.Duration(req.LeaseTTLMs) * time.Millisecond
	if leaseTTL <= 0 {
		leaseTTL = 30 * time.Second
	}
	job, lease, err := h.store.ClaimJobForWorker(r.Context(), req.WorkerID, req.Tags, req.Labels, leaseTTL)
	if errors.Is(err, state.ErrNoQueuedJob) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	workspaceID := contract.NormalizeWorkspace(job.Payload.Workspace)
	prepared, err := h.store.DecryptInput(r.Context(), workspaceID, job.Payload.Input)
	if err == nil {
		prepared, err = h.store.ResolveInput(r.Context(), workspaceID, job.Payload.App, job.Payload.Action, job.Payload.ClientID, prepared)
	}
	if err != nil {
		result := contract.JobResult{
			JobID:  job.ID,
			App:    job.Payload.App,
			Action: job.Payload.Action,
			Stderr: fmt.Sprintf("input preparation failed: %v", err),
		}
		if completeErr := h.store.CompleteJobFailed(r.Context(), lease, result); completeErr != nil {
			writeError(w, http.StatusInternalServerError, completeErr.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	job.Payload.Input = prepared
	writeJSON(w, http.StatusOK, map[string]any{
		"job":   job,
		"lease": leaseToWire(lease),
	})
}

func (h *Handler) workerPlaneJobHeartbeat(w http.ResponseWriter, r *http.Request, jobID string) {
	var req struct {
		Lease      workerLeaseWire `json:"lease"`
		LeaseTTLMs int64           `json:"lease_ttl_ms"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Lease.JobID != jobID {
		writeError(w, http.StatusBadRequest, "lease does not match job")
		return
	}
	leaseTTL := time.Duration(req.LeaseTTLMs) * time.Millisecond
	if leaseTTL <= 0 {
		leaseTTL = 30 * time.Second
	}
	heartbeat, err := h.store.HeartbeatJob(r.Context(), leaseFromWire(req.Lease), leaseTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"still_owned":     heartbeat.StillOwned,
		"canceled_by":     heartbeat.CanceledBy,
		"canceled_reason": heartbeat.CanceledReason,
	})
}

func (h *Handler) workerPlaneComplete(w http.ResponseWriter, r *http.Request, jobID string) {
	var req struct {
		Lease     workerLeaseWire    `json:"lease"`
		Outcome   string             `json:"outcome"`
		Result    contract.JobResult `json:"result"`
		HumanTask *state.HumanTask   `json:"human_task,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Lease.JobID != jobID {
		writeError(w, http.StatusBadRequest, "lease does not match job")
		return
	}
	lease := leaseFromWire(req.Lease)
	var err error
	switch req.Outcome {
	case "succeeded":
		err = h.store.CompleteJobSucceeded(r.Context(), lease, req.Result)
	case "failed":
		err = h.store.CompleteJobFailed(r.Context(), lease, req.Result)
	case "waiting_human":
		if req.HumanTask == nil {
			writeError(w, http.StatusBadRequest, "human_task is required for waiting_human")
			return
		}
		err = h.store.CompleteJobWaitingHuman(r.Context(), lease, req.Result, *req.HumanTask)
	default:
		writeError(w, http.StatusBadRequest, "outcome must be succeeded, failed, or waiting_human")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) workerPlaneLogs(w http.ResponseWriter, r *http.Request, jobID string) {
	var req struct {
		Workspace string `json:"workspace"`
		Chunk     string `json:"chunk"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := h.store.AppendLogs(r.Context(), jobID, contract.NormalizeWorkspace(req.Workspace), req.Chunk); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// workerPlaneArtifact streams an execution bundle as a tar archive. Only
// digest-addressed execution bundles are served (ADR 0010 §3).
func (h *Handler) workerPlaneArtifact(w http.ResponseWriter, r *http.Request, digest string) {
	if h.artifactStore == nil {
		writeError(w, http.StatusServiceUnavailable, "artifact store is not configured")
		return
	}
	tempDir, err := os.MkdirTemp("", "wf-artifact-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer os.RemoveAll(tempDir)
	if _, err := h.artifactStore.FetchTo(r.Context(), tempDir, digest); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/x-tar")
	tw := tar.NewWriter(w)
	defer tw.Close()
	_ = filepath.WalkDir(tempDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		rel, err := filepath.Rel(tempDir, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(tw, file)
		return err
	})
}
