// Package remoteworker is the /worker/v1 client (ADR 0010): a worker.Backend
// over HTTP plus a digest-addressed artifact fetcher, so a worker can run on
// any machine with only a URL and a bearer token.
package remoteworker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/imprun/windforce-core/internal/contract"
	"github.com/imprun/windforce-core/internal/executionbundle"
	"github.com/imprun/windforce-core/internal/state"
)

type Client struct {
	baseURL string
	token   string
	http    *http.Client

	mu        sync.Mutex
	jobTokens map[string]string
}

func New(baseURL, token string) *Client {
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		token:     token,
		http:      &http.Client{Timeout: 60 * time.Second},
		jobTokens: map[string]string{},
	}
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) (int, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode >= 400 {
		return resp.StatusCode, wireError(method, path, resp.StatusCode, payload)
	}
	if out != nil && len(payload) > 0 {
		if err := json.Unmarshal(payload, out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

// wireError rebuilds typed store errors from the worker plane's code field so
// callers' errors.Is checks (invalid lease, not found) work exactly as they
// do against a local store.
func wireError(method string, path string, status int, payload []byte) error {
	message := strings.TrimSpace(string(payload))
	var wire struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if json.Unmarshal(payload, &wire) == nil && wire.Error != "" {
		message = wire.Error
	}
	err := fmt.Errorf("worker plane %s %s: %d %s", method, path, status, message)
	switch wire.Code {
	case "invalid_lease":
		return fmt.Errorf("%v: %w", err, state.ErrInvalidLease)
	case "not_found":
		return fmt.Errorf("%v: %w", err, state.ErrNotFound)
	case "conflict":
		return fmt.Errorf("%v: %w", err, state.ErrConflict)
	}
	return err
}

func (c *Client) RegisterWorker(ctx context.Context, record state.WorkerRecord) error {
	_, err := c.do(ctx, http.MethodPost, "/worker/v1/workers", map[string]any{
		"id":     record.ID,
		"group":  record.Group,
		"tags":   record.Tags,
		"labels": record.Labels,
		"slots":  record.Slots,
	}, nil)
	return err
}

func (c *Client) HeartbeatWorker(ctx context.Context, workerID string) error {
	_, err := c.do(ctx, http.MethodPost, "/worker/v1/workers/"+workerID+"/heartbeat", struct{}{}, nil)
	return err
}

func (c *Client) DeregisterWorker(ctx context.Context, workerID string) error {
	_, err := c.do(ctx, http.MethodDelete, "/worker/v1/workers/"+workerID, nil, nil)
	return err
}

type leaseWire struct {
	JobID      string    `json:"job_id"`
	WorkerID   string    `json:"worker_id"`
	Attempt    int       `json:"attempt"`
	AcquiredAt time.Time `json:"acquired_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

func toLease(w leaseWire) state.Lease {
	return state.Lease{JobID: w.JobID, WorkerID: w.WorkerID, Attempt: w.Attempt, AcquiredAt: w.AcquiredAt, ExpiresAt: w.ExpiresAt}
}

func fromLease(l state.Lease) leaseWire {
	return leaseWire{JobID: l.JobID, WorkerID: l.WorkerID, Attempt: l.Attempt, AcquiredAt: l.AcquiredAt, ExpiresAt: l.ExpiresAt}
}

func (c *Client) ClaimJobForWorker(ctx context.Context, workerID string, tags []string, labels []string, leaseTTL time.Duration) (state.Job, state.Lease, error) {
	var out struct {
		Job      state.Job `json:"job"`
		Lease    leaseWire `json:"lease"`
		JobToken string    `json:"job_token"`
	}
	status, err := c.do(ctx, http.MethodPost, "/worker/v1/claims", map[string]any{
		"worker_id":    workerID,
		"tags":         tags,
		"labels":       labels,
		"lease_ttl_ms": leaseTTL.Milliseconds(),
	}, &out)
	if err != nil {
		return state.Job{}, state.Lease{}, err
	}
	if status == http.StatusNoContent {
		return state.Job{}, state.Lease{}, state.ErrNoQueuedJob
	}
	c.mu.Lock()
	c.jobTokens[out.Job.ID] = out.JobToken
	c.mu.Unlock()
	return out.Job, toLease(out.Lease), nil
}

// JobTokenFor hands the pre-minted SDK callback token to the runner.
func (c *Client) JobTokenFor(jobID string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.jobTokens[jobID]
}

// DecryptInput is an identity: claims arrive prepared (ADR 0010 §2).
func (c *Client) DecryptInput(ctx context.Context, workspaceID string, input json.RawMessage) (json.RawMessage, error) {
	return input, nil
}

// ResolveInput is an identity: claims arrive prepared (ADR 0010 §2).
func (c *Client) ResolveInput(ctx context.Context, workspaceID string, appKey string, actionKey string, clientID string, request json.RawMessage) (json.RawMessage, error) {
	return request, nil
}

func (c *Client) AppendLogs(ctx context.Context, jobID string, workspaceID string, chunk string) error {
	_, err := c.do(ctx, http.MethodPost, "/worker/v1/jobs/"+jobID+"/logs", map[string]any{
		"workspace": workspaceID,
		"chunk":     chunk,
	}, nil)
	return err
}

func (c *Client) HeartbeatJob(ctx context.Context, lease state.Lease, leaseTTL time.Duration) (state.HeartbeatResult, error) {
	var out struct {
		StillOwned     bool    `json:"still_owned"`
		CanceledBy     *string `json:"canceled_by"`
		CanceledReason *string `json:"canceled_reason"`
	}
	_, err := c.do(ctx, http.MethodPost, "/worker/v1/jobs/"+lease.JobID+"/heartbeat", map[string]any{
		"lease":        fromLease(lease),
		"lease_ttl_ms": leaseTTL.Milliseconds(),
	}, &out)
	if err != nil {
		return state.HeartbeatResult{}, err
	}
	return state.HeartbeatResult{StillOwned: out.StillOwned, CanceledBy: out.CanceledBy, CanceledReason: out.CanceledReason}, nil
}

func (c *Client) complete(ctx context.Context, lease state.Lease, outcome string, result contract.JobResult, task *state.HumanTask) error {
	body := map[string]any{
		"lease":   fromLease(lease),
		"outcome": outcome,
		"result":  result,
	}
	if task != nil {
		body["human_task"] = task
	}
	_, err := c.do(ctx, http.MethodPost, "/worker/v1/jobs/"+lease.JobID+"/complete", body, nil)
	c.mu.Lock()
	delete(c.jobTokens, lease.JobID)
	c.mu.Unlock()
	return err
}

func (c *Client) CompleteJobSucceeded(ctx context.Context, lease state.Lease, result contract.JobResult) error {
	return c.complete(ctx, lease, "succeeded", result, nil)
}

func (c *Client) CompleteJobFailed(ctx context.Context, lease state.Lease, result contract.JobResult) error {
	return c.complete(ctx, lease, "failed", result, nil)
}

func (c *Client) CompleteJobWaitingHuman(ctx context.Context, lease state.Lease, result contract.JobResult, task state.HumanTask) error {
	return c.complete(ctx, lease, "waiting_human", result, &task)
}

// ArtifactStore fetches digest-addressed execution bundles over the worker
// plane. Only FetchTo is meaningful worker-side.
type ArtifactStore struct {
	Client *Client
}

func (a ArtifactStore) Publish(ctx context.Context, sourceDir string) (executionbundle.Descriptor, error) {
	return executionbundle.Descriptor{}, fmt.Errorf("remote artifact store cannot publish")
}

func (a ArtifactStore) Exists(ctx context.Context, digest string) (bool, error) {
	return true, nil
}

func (a ArtifactStore) Verify(ctx context.Context, digest string) (executionbundle.Descriptor, error) {
	return executionbundle.Descriptor{Digest: digest}, nil
}

func (a ArtifactStore) FetchTo(ctx context.Context, destinationDir string, digest string) (executionbundle.Descriptor, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.Client.baseURL+"/worker/v1/artifacts/"+digest, nil)
	if err != nil {
		return executionbundle.Descriptor{}, err
	}
	if a.Client.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.Client.token)
	}
	resp, err := a.Client.http.Do(req)
	if err != nil {
		return executionbundle.Descriptor{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(resp.Body)
		return executionbundle.Descriptor{}, fmt.Errorf("fetch artifact %s: %d %s", digest, resp.StatusCode, strings.TrimSpace(string(payload)))
	}
	// Extract into a sibling temp dir, verify, then rename — a torn stream or
	// crash never leaves a partial tree at the destination for the ready
	// marker to bless.
	destinationDir, err = filepath.Abs(destinationDir)
	if err != nil {
		return executionbundle.Descriptor{}, err
	}
	if err := os.MkdirAll(filepath.Dir(destinationDir), 0o755); err != nil {
		return executionbundle.Descriptor{}, err
	}
	tempDir, err := os.MkdirTemp(filepath.Dir(destinationDir), ".fetch-")
	if err != nil {
		return executionbundle.Descriptor{}, err
	}
	defer os.RemoveAll(tempDir)
	if err := extractTar(tempDir, tar.NewReader(resp.Body)); err != nil {
		return executionbundle.Descriptor{}, fmt.Errorf("extract artifact %s: %w", digest, err)
	}
	// The digest hashes names, modes, symlink targets, and contents. Windows
	// cannot represent POSIX modes faithfully, so only POSIX hosts (the
	// deploy target) enforce the match.
	if runtime.GOOS != "windows" {
		actual, err := executionbundle.HashTree(ctx, tempDir)
		if err != nil {
			return executionbundle.Descriptor{}, err
		}
		if actual != digest {
			return executionbundle.Descriptor{}, fmt.Errorf("fetched artifact digest mismatch: got %s, want %s", actual, digest)
		}
	}
	if err := os.RemoveAll(destinationDir); err != nil {
		return executionbundle.Descriptor{}, err
	}
	if err := os.Rename(tempDir, destinationDir); err != nil {
		return executionbundle.Descriptor{}, err
	}
	return executionbundle.Descriptor{Digest: digest}, nil
}

func extractTar(root string, reader *tar.Reader) error {
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(filepath.FromSlash(strings.TrimSuffix(header.Name, "/")))
		if name == "." || name == "" || strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			return fmt.Errorf("artifact entry escapes destination: %q", header.Name)
		}
		target := filepath.Join(root, name)
		mode := os.FileMode(header.Mode) & 0o777
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			if err := os.Chmod(target, mode); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(file, reader); err != nil {
				file.Close()
				return err
			}
			if err := file.Close(); err != nil {
				return err
			}
			if err := os.Chmod(target, mode); err != nil {
				return err
			}
		case tar.TypeSymlink:
			// Bundles legitimately carry in-tree symlinks (e.g. node_modules
			// .bin); anything pointing outside the tree is rejected.
			if err := executionbundle.ValidateSymlink(root, target, header.Linkname); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(header.Linkname, target); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported artifact entry type %d: %q", header.Typeflag, header.Name)
		}
	}
}
