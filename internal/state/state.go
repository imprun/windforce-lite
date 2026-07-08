package state

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/imprun/windforce-lite/internal/contract"
)

type RunState string

const (
	RunQueued       RunState = "QUEUED"
	RunRunning      RunState = "RUNNING"
	RunWaitingHuman RunState = "WAITING_HUMAN"
	RunResuming     RunState = "RESUMING"
	RunSucceeded    RunState = "SUCCEEDED"
	RunFailed       RunState = "FAILED"
	RunCanceled     RunState = "CANCELED"
	RunExpired      RunState = "EXPIRED"
)

type JobState string

const (
	JobQueued    JobState = "queued"
	JobRunning   JobState = "running"
	JobSucceeded JobState = "succeeded"
	JobFailed    JobState = "failed"
)

type HumanTaskState string

const (
	HumanTaskPending   HumanTaskState = "pending"
	HumanTaskCompleted HumanTaskState = "completed"
	HumanTaskExpired   HumanTaskState = "expired"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrNoQueuedJob   = errors.New("no queued job")
	ErrConflict      = errors.New("conflict")
	ErrInvalidState  = errors.New("invalid state")
	ErrInvalidLease  = errors.New("invalid lease")
	ErrLockTimeout   = errors.New("state lock timeout")
	defaultLeaseTime = 30 * time.Second
)

type Run struct {
	ID         string              `json:"id"`
	Adapter    string              `json:"adapter,omitempty"`
	App        string              `json:"app"`
	Action     string              `json:"action"`
	State      RunState            `json:"state"`
	Deployment contract.Deployment `json:"deployment"`
	Input      json.RawMessage     `json:"input,omitempty"`
	Output     json.RawMessage     `json:"output,omitempty"`
	Result     *contract.JobResult `json:"result,omitempty"`
	Error      json.RawMessage     `json:"error,omitempty"`
	TaskID     string              `json:"taskId,omitempty"`
	Env        []string            `json:"env,omitempty"`
	CreatedAt  time.Time           `json:"createdAt"`
	UpdatedAt  time.Time           `json:"updatedAt"`
	ExpiresAt  *time.Time          `json:"expiresAt,omitempty"`
}

type JobPayload struct {
	Workspace   string              `json:"workspace,omitempty"`
	GitSourceID string              `json:"gitSourceId,omitempty"`
	Commit      string              `json:"commit,omitempty"`
	App         string              `json:"app"`
	Action      string              `json:"action"`
	ActionSpec  contract.Action     `json:"actionSpec,omitempty"`
	Input       json.RawMessage     `json:"input,omitempty"`
	Deployment  contract.Deployment `json:"deployment"`
	Env         []string            `json:"env,omitempty"`
}

type Job struct {
	ID             string     `json:"id"`
	RunID          string     `json:"runId"`
	State          JobState   `json:"state"`
	Kind           string     `json:"kind"`
	Payload        JobPayload `json:"payload"`
	Priority       int        `json:"priority"`
	Attempt        int        `json:"attempt"`
	LeaseOwner     string     `json:"leaseOwner,omitempty"`
	LeaseExpiresAt *time.Time `json:"leaseExpiresAt,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

type Lease struct {
	JobID      string
	WorkerID   string
	ExpiresAt  time.Time
	Attempt    int
	AcquiredAt time.Time
}

type HumanTask struct {
	ID          string          `json:"id"`
	RunID       string          `json:"runId"`
	State       HumanTaskState  `json:"state"`
	Title       string          `json:"title"`
	Description string          `json:"description,omitempty"`
	Schema      json.RawMessage `json:"schema,omitempty"`
	ResumeInput json.RawMessage `json:"resumeInput,omitempty"`
	CreatedAt   time.Time       `json:"createdAt"`
	CompletedAt *time.Time      `json:"completedAt,omitempty"`
	ExpiresAt   *time.Time      `json:"expiresAt,omitempty"`
}

type RunEvent struct {
	ID        int64           `json:"id"`
	RunID     string          `json:"runId,omitempty"`
	EventType string          `json:"eventType"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
}

type Snapshot struct {
	Sequence   int64                `json:"sequence"`
	Runs       map[string]Run       `json:"runs"`
	Jobs       map[string]Job       `json:"jobs"`
	HumanTasks map[string]HumanTask `json:"humanTasks"`
	Events     []RunEvent           `json:"events"`
}

type Store interface {
	CreateRunAndEnqueue(ctx context.Context, run Run, job Job) error
	GetRun(ctx context.Context, runID string) (Run, error)
	GetHumanTask(ctx context.Context, taskID string) (HumanTask, error)
	ClaimJob(ctx context.Context, workerID string, leaseTTL time.Duration) (Job, Lease, error)
	CompleteJobSucceeded(ctx context.Context, lease Lease, result contract.JobResult) error
	CompleteJobFailed(ctx context.Context, lease Lease, result contract.JobResult) error
	CompleteJobWaitingHuman(ctx context.Context, lease Lease, result contract.JobResult, task HumanTask) error
	ResumeHumanTask(ctx context.Context, taskID string, resumeInput json.RawMessage) (Run, Job, error)
	ResumeRun(ctx context.Context, runID string, resumeInput json.RawMessage) (Run, Job, error)
	CancelRun(ctx context.Context, runID string, reason string) (Run, error)
	RetryRun(ctx context.Context, runID string) (Run, Job, error)
}

type LocalStore struct {
	Path string
}

func NewLocalStore(path string) *LocalStore {
	return &LocalStore{Path: path}
}

func NewID(prefix string) string {
	var data [12]byte
	if _, err := rand.Read(data[:]); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(data[:])
}

func NewRun(adapter string, id string, app string, action string, deployment contract.Deployment, input json.RawMessage) Run {
	if id == "" {
		id = NewID("run")
	}
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	now := time.Now().UTC()
	return Run{
		ID:         id,
		Adapter:    adapter,
		App:        app,
		Action:     action,
		State:      RunQueued,
		Deployment: deployment,
		Input:      cloneRaw(input),
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

func NewActionJob(run Run, input json.RawMessage) Job {
	if len(input) == 0 {
		input = run.Input
	}
	actionSpec := run.Deployment.Actions[run.Action]
	now := time.Now().UTC()
	return Job{
		ID:        NewID("job"),
		RunID:     run.ID,
		State:     JobQueued,
		Kind:      "action",
		Priority:  100,
		CreatedAt: now,
		UpdatedAt: now,
		Payload: JobPayload{
			Workspace:   run.Deployment.SourceWorkspace(),
			GitSourceID: run.Deployment.SourceGitSourceID(),
			Commit:      run.Deployment.Commit,
			App:         run.App,
			Action:      run.Action,
			ActionSpec:  actionSpec,
			Input:       cloneRaw(input),
			Deployment:  run.Deployment,
			Env:         append([]string(nil), run.Env...),
		},
	}
}

func (p JobPayload) PinnedDeployment() contract.Deployment {
	deployment := p.Deployment
	if deployment.Workspace == "" {
		deployment.Workspace = p.Workspace
	}
	if deployment.GitSourceID == "" {
		deployment.GitSourceID = p.GitSourceID
	}
	if deployment.Commit == "" {
		deployment.Commit = p.Commit
	}
	if deployment.App == "" {
		deployment.App = p.App
	}
	if deployment.Actions == nil && p.Action != "" {
		deployment.Actions = map[string]contract.Action{p.Action: p.ActionSpec}
	}
	return deployment
}

func (s *LocalStore) CreateRunAndEnqueue(ctx context.Context, run Run, job Job) error {
	return s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		if _, ok := snapshot.Runs[run.ID]; ok {
			return fmt.Errorf("%w: run %q already exists", ErrConflict, run.ID)
		}
		if _, ok := snapshot.Jobs[job.ID]; ok {
			return fmt.Errorf("%w: job %q already exists", ErrConflict, job.ID)
		}
		run.CreatedAt = nonZeroTime(run.CreatedAt, now)
		run.UpdatedAt = now
		job.CreatedAt = nonZeroTime(job.CreatedAt, now)
		job.UpdatedAt = now
		snapshot.Runs[run.ID] = run
		snapshot.Jobs[job.ID] = job
		appendEvent(snapshot, run.ID, "run_created", map[string]string{"app": run.App, "action": run.Action}, now)
		appendEvent(snapshot, run.ID, "job_enqueued", map[string]string{"jobId": job.ID}, now)
		return nil
	})
}

func (s *LocalStore) GetRun(ctx context.Context, runID string) (Run, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return Run{}, err
	}
	run, ok := snapshot.Runs[runID]
	if !ok {
		return Run{}, fmt.Errorf("%w: run %q", ErrNotFound, runID)
	}
	return run, nil
}

func (s *LocalStore) GetHumanTask(ctx context.Context, taskID string) (HumanTask, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return HumanTask{}, err
	}
	task, ok := snapshot.HumanTasks[taskID]
	if !ok {
		return HumanTask{}, fmt.Errorf("%w: human task %q", ErrNotFound, taskID)
	}
	return task, nil
}

func (s *LocalStore) ClaimJob(ctx context.Context, workerID string, leaseTTL time.Duration) (Job, Lease, error) {
	if workerID == "" {
		workerID = NewID("worker")
	}
	if leaseTTL <= 0 {
		leaseTTL = defaultLeaseTime
	}
	var claimed Job
	var lease Lease
	err := s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		requeueExpiredJobs(snapshot, now)

		ids := make([]string, 0, len(snapshot.Jobs))
		for id, job := range snapshot.Jobs {
			if job.State == JobQueued {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			return ErrNoQueuedJob
		}
		sort.Slice(ids, func(i int, j int) bool {
			left := snapshot.Jobs[ids[i]]
			right := snapshot.Jobs[ids[j]]
			if left.Priority != right.Priority {
				return left.Priority < right.Priority
			}
			if !left.CreatedAt.Equal(right.CreatedAt) {
				return left.CreatedAt.Before(right.CreatedAt)
			}
			return left.ID < right.ID
		})

		job := snapshot.Jobs[ids[0]]
		expiresAt := now.Add(leaseTTL)
		job.State = JobRunning
		job.Attempt++
		job.LeaseOwner = workerID
		job.LeaseExpiresAt = &expiresAt
		job.UpdatedAt = now
		snapshot.Jobs[job.ID] = job

		run := snapshot.Runs[job.RunID]
		if run.State == RunQueued || run.State == RunResuming {
			run.State = RunRunning
			run.UpdatedAt = now
			snapshot.Runs[run.ID] = run
			appendEvent(snapshot, run.ID, "run_running", map[string]string{"jobId": job.ID}, now)
		}
		appendEvent(snapshot, job.RunID, "job_claimed", map[string]any{"jobId": job.ID, "workerId": workerID, "attempt": job.Attempt}, now)

		claimed = job
		lease = Lease{JobID: job.ID, WorkerID: workerID, ExpiresAt: expiresAt, Attempt: job.Attempt, AcquiredAt: now}
		return nil
	})
	if err != nil {
		return Job{}, Lease{}, err
	}
	return claimed, lease, nil
}

func (s *LocalStore) CompleteJobSucceeded(ctx context.Context, lease Lease, result contract.JobResult) error {
	return s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		job, run, err := leasedJobAndRun(snapshot, lease, now)
		if err != nil {
			return err
		}
		job.State = JobSucceeded
		job.LeaseOwner = ""
		job.LeaseExpiresAt = nil
		job.UpdatedAt = now
		run.State = RunSucceeded
		run.Output = cloneRaw(result.Output)
		run.Result = cloneResult(result)
		run.Error = nil
		run.TaskID = ""
		run.UpdatedAt = now
		snapshot.Jobs[job.ID] = job
		snapshot.Runs[run.ID] = run
		appendEvent(snapshot, run.ID, "run_succeeded", map[string]string{"jobId": job.ID}, now)
		return nil
	})
}

func (s *LocalStore) CompleteJobFailed(ctx context.Context, lease Lease, result contract.JobResult) error {
	return s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		job, run, err := leasedJobAndRun(snapshot, lease, now)
		if err != nil {
			return err
		}
		if result.Error == "" && result.ExitCode != 0 {
			result.Error = fmt.Sprintf("action exited with code %d", result.ExitCode)
		}
		job.State = JobFailed
		job.LeaseOwner = ""
		job.LeaseExpiresAt = nil
		job.UpdatedAt = now
		run.State = RunFailed
		run.Result = cloneResult(result)
		run.Error = mustRaw(map[string]any{"message": result.Error, "exitCode": result.ExitCode})
		run.UpdatedAt = now
		snapshot.Jobs[job.ID] = job
		snapshot.Runs[run.ID] = run
		appendEvent(snapshot, run.ID, "run_failed", map[string]any{"jobId": job.ID, "error": result.Error, "exitCode": result.ExitCode}, now)
		return nil
	})
}

func (s *LocalStore) CompleteJobWaitingHuman(ctx context.Context, lease Lease, result contract.JobResult, task HumanTask) error {
	return s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		job, run, err := leasedJobAndRun(snapshot, lease, now)
		if err != nil {
			return err
		}
		if task.ID == "" {
			task.ID = NewID("human")
		}
		if task.RunID == "" {
			task.RunID = run.ID
		}
		if task.State == "" {
			task.State = HumanTaskPending
		}
		task.CreatedAt = nonZeroTime(task.CreatedAt, now)
		if _, exists := snapshot.HumanTasks[task.ID]; exists {
			return fmt.Errorf("%w: human task %q already exists", ErrConflict, task.ID)
		}
		job.State = JobSucceeded
		job.LeaseOwner = ""
		job.LeaseExpiresAt = nil
		job.UpdatedAt = now
		run.State = RunWaitingHuman
		run.Result = cloneResult(result)
		run.Error = nil
		run.TaskID = task.ID
		run.UpdatedAt = now
		snapshot.Jobs[job.ID] = job
		snapshot.Runs[run.ID] = run
		snapshot.HumanTasks[task.ID] = task
		appendEvent(snapshot, run.ID, "human_task_created", map[string]string{"jobId": job.ID, "taskId": task.ID}, now)
		return nil
	})
}

func (s *LocalStore) ResumeHumanTask(ctx context.Context, taskID string, resumeInput json.RawMessage) (Run, Job, error) {
	var resumedRun Run
	var enqueuedJob Job
	err := s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		task, ok := snapshot.HumanTasks[taskID]
		if !ok {
			return fmt.Errorf("%w: human task %q", ErrNotFound, taskID)
		}
		if task.State != HumanTaskPending {
			return fmt.Errorf("%w: human task %q is %s", ErrInvalidState, taskID, task.State)
		}
		run, ok := snapshot.Runs[task.RunID]
		if !ok {
			return fmt.Errorf("%w: run %q", ErrNotFound, task.RunID)
		}
		if run.State != RunWaitingHuman {
			return fmt.Errorf("%w: run %q is %s", ErrInvalidState, run.ID, run.State)
		}
		if len(resumeInput) == 0 {
			resumeInput = json.RawMessage("{}")
		}
		if !json.Valid(resumeInput) {
			return errors.New("resume input is not valid JSON")
		}

		task.State = HumanTaskCompleted
		task.ResumeInput = cloneRaw(resumeInput)
		task.CompletedAt = &now
		run.State = RunResuming
		run.TaskID = ""
		run.UpdatedAt = now
		jobInput := mergeResumeInput(run.Input, task.ID, resumeInput)
		job := NewActionJob(run, jobInput)
		job.CreatedAt = now
		job.UpdatedAt = now

		snapshot.HumanTasks[task.ID] = task
		snapshot.Runs[run.ID] = run
		snapshot.Jobs[job.ID] = job
		appendEvent(snapshot, run.ID, "human_task_resumed", map[string]string{"taskId": task.ID, "jobId": job.ID}, now)
		resumedRun = run
		enqueuedJob = job
		return nil
	})
	return resumedRun, enqueuedJob, err
}

func (s *LocalStore) ResumeRun(ctx context.Context, runID string, resumeInput json.RawMessage) (Run, Job, error) {
	run, err := s.GetRun(ctx, runID)
	if err != nil {
		return Run{}, Job{}, err
	}
	if run.TaskID == "" {
		return Run{}, Job{}, fmt.Errorf("%w: run %q has no pending human task", ErrInvalidState, runID)
	}
	return s.ResumeHumanTask(ctx, run.TaskID, resumeInput)
}

func (s *LocalStore) CancelRun(ctx context.Context, runID string, reason string) (Run, error) {
	var canceled Run
	err := s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		run, ok := snapshot.Runs[runID]
		if !ok {
			return fmt.Errorf("%w: run %q", ErrNotFound, runID)
		}
		if IsTerminal(run) {
			return fmt.Errorf("%w: run %q is %s", ErrInvalidState, runID, run.State)
		}
		run.State = RunCanceled
		run.Error = mustRaw(map[string]string{"message": reason})
		run.UpdatedAt = now
		for id, job := range snapshot.Jobs {
			if job.RunID != runID || (job.State != JobQueued && job.State != JobRunning) {
				continue
			}
			job.State = JobFailed
			job.LeaseOwner = ""
			job.LeaseExpiresAt = nil
			job.UpdatedAt = now
			snapshot.Jobs[id] = job
		}
		for id, task := range snapshot.HumanTasks {
			if task.RunID != runID || task.State != HumanTaskPending {
				continue
			}
			task.State = HumanTaskExpired
			snapshot.HumanTasks[id] = task
		}
		snapshot.Runs[runID] = run
		appendEvent(snapshot, runID, "run_canceled", map[string]string{"reason": reason}, now)
		canceled = run
		return nil
	})
	return canceled, err
}

func (s *LocalStore) RetryRun(ctx context.Context, runID string) (Run, Job, error) {
	var retried Run
	var enqueued Job
	err := s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		run, ok := snapshot.Runs[runID]
		if !ok {
			return fmt.Errorf("%w: run %q", ErrNotFound, runID)
		}
		switch run.State {
		case RunFailed, RunCanceled, RunExpired:
		default:
			return fmt.Errorf("%w: run %q is %s", ErrInvalidState, runID, run.State)
		}
		run.State = RunQueued
		run.Output = nil
		run.Result = nil
		run.Error = nil
		run.TaskID = ""
		run.UpdatedAt = now
		job := NewActionJob(run, run.Input)
		job.CreatedAt = now
		job.UpdatedAt = now
		snapshot.Runs[run.ID] = run
		snapshot.Jobs[job.ID] = job
		appendEvent(snapshot, run.ID, "run_retried", map[string]string{"jobId": job.ID}, now)
		retried = run
		enqueued = job
		return nil
	})
	return retried, enqueued, err
}

func (s *LocalStore) Load(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return newSnapshot(), nil
	}
	if err != nil {
		return Snapshot{}, err
	}
	var snapshot Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return Snapshot{}, err
	}
	ensureSnapshot(&snapshot)
	return snapshot, nil
}

func (s *LocalStore) update(ctx context.Context, fn func(*Snapshot, time.Time) error) error {
	if s.Path == "" {
		return errors.New("state path is required")
	}
	return s.withLock(ctx, func() error {
		snapshot, err := s.Load(ctx)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		if err := fn(&snapshot, now); err != nil {
			return err
		}
		return s.write(snapshot)
	})
}

func (s *LocalStore) withLock(ctx context.Context, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	lockPath := s.Path + ".lock"
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		file, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
			_ = file.Close()
			defer os.Remove(lockPath)
			return fn()
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		if staleLock(lockPath, 2*time.Minute) {
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return ErrLockTimeout
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (s *LocalStore) write(snapshot Snapshot) error {
	ensureSnapshot(&snapshot)
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmpPath := s.Path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.Path)
}

func newSnapshot() Snapshot {
	snapshot := Snapshot{}
	ensureSnapshot(&snapshot)
	return snapshot
}

func ensureSnapshot(snapshot *Snapshot) {
	if snapshot.Runs == nil {
		snapshot.Runs = map[string]Run{}
	}
	if snapshot.Jobs == nil {
		snapshot.Jobs = map[string]Job{}
	}
	if snapshot.HumanTasks == nil {
		snapshot.HumanTasks = map[string]HumanTask{}
	}
	if snapshot.Events == nil {
		snapshot.Events = []RunEvent{}
	}
}

func requeueExpiredJobs(snapshot *Snapshot, now time.Time) {
	for id, job := range snapshot.Jobs {
		if job.State != JobRunning || job.LeaseExpiresAt == nil || job.LeaseExpiresAt.After(now) {
			continue
		}
		job.State = JobQueued
		job.LeaseOwner = ""
		job.LeaseExpiresAt = nil
		job.UpdatedAt = now
		snapshot.Jobs[id] = job
		appendEvent(snapshot, job.RunID, "job_lease_expired", map[string]string{"jobId": job.ID}, now)
	}
}

func leasedJobAndRun(snapshot *Snapshot, lease Lease, now time.Time) (Job, Run, error) {
	job, ok := snapshot.Jobs[lease.JobID]
	if !ok {
		return Job{}, Run{}, fmt.Errorf("%w: job %q", ErrNotFound, lease.JobID)
	}
	if job.State != JobRunning || job.LeaseOwner != lease.WorkerID {
		return Job{}, Run{}, fmt.Errorf("%w: job %q", ErrInvalidLease, lease.JobID)
	}
	if job.LeaseExpiresAt != nil && job.LeaseExpiresAt.Before(now) {
		return Job{}, Run{}, fmt.Errorf("%w: job %q expired", ErrInvalidLease, lease.JobID)
	}
	run, ok := snapshot.Runs[job.RunID]
	if !ok {
		return Job{}, Run{}, fmt.Errorf("%w: run %q", ErrNotFound, job.RunID)
	}
	return job, run, nil
}

func appendEvent(snapshot *Snapshot, runID string, eventType string, payload any, now time.Time) {
	snapshot.Sequence++
	snapshot.Events = append(snapshot.Events, RunEvent{
		ID:        snapshot.Sequence,
		RunID:     runID,
		EventType: eventType,
		Payload:   mustRaw(payload),
		CreatedAt: now,
	})
}

func mergeResumeInput(original json.RawMessage, taskID string, resumeInput json.RawMessage) json.RawMessage {
	if len(original) == 0 || !json.Valid(original) {
		original = json.RawMessage("{}")
	}
	if len(resumeInput) == 0 || !json.Valid(resumeInput) {
		resumeInput = json.RawMessage("{}")
	}
	resume := mustRaw(map[string]any{
		"humanTaskID": taskID,
		"input":       json.RawMessage(resumeInput),
	})

	var object map[string]json.RawMessage
	if json.Unmarshal(original, &object) == nil && object != nil {
		object["$resume"] = resume
		data, _ := json.Marshal(object)
		return data
	}
	data, _ := json.Marshal(map[string]json.RawMessage{
		"$input":  original,
		"$resume": resume,
	})
	return data
}

func mustRaw(value any) json.RawMessage {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{"error":"event payload marshal failed"}`)
	}
	return data
}

func cloneRaw(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), value...)
}

func cloneResult(result contract.JobResult) *contract.JobResult {
	cloned := result
	cloned.Output = cloneRaw(result.Output)
	return &cloned
}

func nonZeroTime(value time.Time, fallback time.Time) time.Time {
	if value.IsZero() {
		return fallback
	}
	return value
}

func staleLock(path string, maxAge time.Duration) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) > maxAge
}

func IsTerminal(run Run) bool {
	switch run.State {
	case RunSucceeded, RunFailed, RunCanceled, RunExpired:
		return true
	default:
		return false
	}
}

func IsSettledForTrigger(run Run) bool {
	return IsTerminal(run) || run.State == RunWaitingHuman
}

func CleanID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '.', r == '-', r == '_', r == ':':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
	}
	return builder.String()
}
