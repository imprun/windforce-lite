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
	"github.com/imprun/windforce-core/internal/token"
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

// workerPlaneMaxBody caps worker plane request bodies (job results and log
// chunks are the largest payloads; the public API is capped similarly).
const workerPlaneMaxBody = 10 << 20

// workerPlaneMaxLeaseTTL caps client-supplied lease TTLs so a buggy worker
// cannot park a claimed job beyond the reaper's reach.
const workerPlaneMaxLeaseTTL = 15 * time.Minute

// writeStoreError maps typed store errors onto the wire (code field) so
// remote workers keep the same recovery semantics as local ones — an invalid
// lease or missing record must not surface as an opaque 500.
func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrInvalidLease):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error(), "code": "invalid_lease"})
	case errors.Is(err, state.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error(), "code": "not_found"})
	case errors.Is(err, state.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error(), "code": "conflict"})
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func clampLeaseTTL(ms int64) time.Duration {
	leaseTTL := time.Duration(ms) * time.Millisecond
	if leaseTTL <= 0 {
		return 30 * time.Second
	}
	if leaseTTL > workerPlaneMaxLeaseTTL {
		return workerPlaneMaxLeaseTTL
	}
	return leaseTTL
}

func (h *Handler) handleWorkerPlane(w http.ResponseWriter, r *http.Request) {
	if !h.workerPlaneAuthorized(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, workerPlaneMaxBody)
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
		writeStoreError(w, err)
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
	leaseTTL := clampLeaseTTL(req.LeaseTTLMs)
	job, lease, err := h.store.ClaimJobForWorker(r.Context(), req.WorkerID, req.Tags, req.Labels, leaseTTL)
	if errors.Is(err, state.ErrNoQueuedJob) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		writeStoreError(w, err)
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
	jobToken := ""
	if secret := strings.TrimSpace(h.jobTokenSecret); secret != "" {
		ttl := jobTokenTTL(job)
		jobToken = token.MintJob(secret, token.JobClaims{
			Workspace: workspaceID,
			JobID:     job.ID,
			Subject:   job.Payload.PermissionedAs,
			Exp:       time.Now().Add(ttl).Unix(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"job":       job,
		"lease":     leaseToWire(lease),
		"job_token": jobToken,
	})
}

// jobTokenTTL mirrors the local worker's mint path (runtime actionTimeout
// precedence: pinned action timeout over the app-level pin) so remote SDK
// callbacks stay valid for the whole run, not just the app default.
func jobTokenTTL(job state.Job) time.Duration {
	timeout := time.Duration(job.Payload.TimeoutS) * time.Second
	deployment := job.Payload.PinnedDeployment()
	if action, ok := deployment.Actions[job.Payload.Action]; ok {
		if action.TimeoutS != nil && *action.TimeoutS > 0 {
			timeout = time.Duration(*action.TimeoutS) * time.Second
		} else if action.TimeoutMs > 0 {
			timeout = time.Duration(action.TimeoutMs) * time.Millisecond
		}
	}
	if timeout <= 0 {
		return time.Hour
	}
	return timeout + time.Minute
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
	heartbeat, err := h.store.HeartbeatJob(r.Context(), leaseFromWire(req.Lease), clampLeaseTTL(req.LeaseTTLMs))
	if err != nil {
		writeStoreError(w, err)
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
		writeStoreError(w, err)
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
		writeStoreError(w, err)
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
	walkErr := filepath.WalkDir(tempDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(tempDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		// Symlinks (bundles carry validated in-tree links, e.g. node_modules
		// .bin) become link headers; directories carry their modes — both
		// participate in the bundle digest the client re-verifies.
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			if link, err = os.Readlink(path); err != nil {
				return err
			}
		}
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		if d.IsDir() {
			header.Name += "/"
		}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(tw, file)
		return err
	})
	if walkErr != nil {
		// The 200 header is already on the wire — abort the connection so the
		// client sees a broken stream, never a well-formed partial archive.
		panic(http.ErrAbortHandler)
	}
	_ = tw.Close()
}
