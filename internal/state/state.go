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
	"strconv"
	"strings"
	"time"

	"github.com/imprun/windforce-lite/internal/contract"
	wfcrypto "github.com/imprun/windforce-lite/internal/crypto"
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
	ID             string              `json:"id"`
	Adapter        string              `json:"adapter,omitempty"`
	App            string              `json:"app"`
	Action         string              `json:"action"`
	State          RunState            `json:"state"`
	Deployment     contract.Deployment `json:"deployment"`
	Input          json.RawMessage     `json:"input,omitempty"`
	Output         json.RawMessage     `json:"output,omitempty"`
	Result         *contract.JobResult `json:"result,omitempty"`
	Error          json.RawMessage     `json:"error,omitempty"`
	TaskID         string              `json:"taskId,omitempty"`
	CorrelationID  string              `json:"correlationId,omitempty"`
	Env            []string            `json:"env,omitempty"`
	CreatedBy      string              `json:"createdBy,omitempty"`
	PermissionedAs string              `json:"permissionedAs,omitempty"`
	CreatedAt      time.Time           `json:"createdAt"`
	UpdatedAt      time.Time           `json:"updatedAt"`
	ExpiresAt      *time.Time          `json:"expiresAt,omitempty"`
}

type JobPayload struct {
	Workspace      string              `json:"workspace,omitempty"`
	GitSourceID    string              `json:"gitSourceId,omitempty"`
	Commit         string              `json:"commit,omitempty"`
	App            string              `json:"app"`
	Action         string              `json:"action"`
	Tag            string              `json:"tag,omitempty"`
	TriggerKind    string              `json:"triggerKind,omitempty"`
	TriggerHeaders json.RawMessage     `json:"triggerHeaders,omitempty"`
	ActionSpec     contract.Action     `json:"actionSpec,omitempty"`
	InputSchema    json.RawMessage     `json:"inputSchema,omitempty"`
	OutputSchema   json.RawMessage     `json:"outputSchema,omitempty"`
	Input          json.RawMessage     `json:"input,omitempty"`
	Deployment     contract.Deployment `json:"deployment"`
	CorrelationID  string              `json:"correlationId,omitempty"`
	Env            []string            `json:"env,omitempty"`
	CreatedBy      string              `json:"createdBy,omitempty"`
	PermissionedAs string              `json:"permissionedAs,omitempty"`
	FlowRunID      string              `json:"flowRunId,omitempty"`
	FlowStepID     string              `json:"flowStepId,omitempty"`
	FlowKey        string              `json:"flowKey,omitempty"`
	FlowStepKey    string              `json:"flowStepKey,omitempty"`
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
	StartedAt      *time.Time `json:"startedAt,omitempty"`
	CanceledBy     *string    `json:"canceledBy,omitempty"`
	CanceledReason *string    `json:"canceledReason,omitempty"`
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

type HeartbeatResult struct {
	CanceledBy     *string
	CanceledReason *string
	StillOwned     bool
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

type CancelResult struct {
	Found            bool `json:"found"`
	CompletedNow     bool `json:"completed_now"`
	SoftCanceled     bool `json:"soft_canceled"`
	AlreadyCompleted bool `json:"already_completed"`
}

type JobListQuery struct {
	WorkspaceID     string
	Status          string
	AppKey          string
	ActionKey       string
	TriggerKind     string
	Limit           int
	CursorCreatedAt *time.Time
	CursorID        string
	Since           *time.Time
	Until           *time.Time
}

type RequeueAppSpec struct {
	WorkspaceID string
	AppKey      string
	ActionKey   *string
	ActionTags  map[string]string
}

type JobListItem struct {
	ID             string     `json:"id"`
	WorkspaceID    string     `json:"workspace_id"`
	AppKey         string     `json:"app_key"`
	ActionKey      string     `json:"action_key"`
	TriggerKind    string     `json:"trigger_kind"`
	Status         string     `json:"status"`
	Queued         bool       `json:"queued"`
	Running        bool       `json:"running"`
	Completed      bool       `json:"completed"`
	CreatedAt      time.Time  `json:"created_at"`
	StartedAt      *time.Time `json:"started_at"`
	CompletedAt    *time.Time `json:"completed_at"`
	DurationMs     int64      `json:"duration_ms"`
	Worker         *string    `json:"worker"`
	GitSourceID    *int64     `json:"git_source_id"`
	CommitSha      *string    `json:"commit_sha"`
	Entrypoint     string     `json:"entrypoint"`
	Tag            string     `json:"tag"`
	CreatedBy      string     `json:"created_by"`
	PermissionedAs string     `json:"permissioned_as"`
	CanceledBy     *string    `json:"canceled_by"`
	CanceledReason *string    `json:"canceled_reason"`
	FlowRunID      *string    `json:"flow_run_id,omitempty"`
	FlowStepID     *string    `json:"flow_step_id,omitempty"`
	ErrorSnippet   *string    `json:"error_snippet,omitempty"`
}

type JobSummaryCounts struct {
	QueuedCount          int `json:"queued_count"`
	RunningCount         int `json:"running_count"`
	CompletedCountRecent int `json:"completed_count_recent"`
	FailedCountRecent    int `json:"failed_count_recent"`
	CanceledCountRecent  int `json:"canceled_count_recent"`
}

type JobSummaryTagBreakdown struct {
	Tag string `json:"tag"`
	JobSummaryCounts
}

type JobSummaryAppBreakdown struct {
	AppKey string `json:"app_key"`
	JobSummaryCounts
}

type JobSummary struct {
	JobSummaryCounts
	OldestQueuedAt *time.Time               `json:"oldest_queued_at"`
	ByTag          []JobSummaryTagBreakdown `json:"by_tag"`
	ByApp          []JobSummaryAppBreakdown `json:"by_app"`
}

type Variable struct {
	AppKey      string `json:"app_key"`
	Path        string `json:"path"`
	Value       string `json:"value"`
	IsSecret    bool   `json:"is_secret"`
	Description string `json:"description"`
}

type Resource struct {
	Path         string          `json:"path"`
	Value        json.RawMessage `json:"value"`
	ResourceType string          `json:"resource_type"`
	Description  string          `json:"description"`
}

type RunEvent struct {
	ID        int64           `json:"id"`
	RunID     string          `json:"runId,omitempty"`
	EventType string          `json:"eventType"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
}

type JobLog struct {
	JobID       string    `json:"jobId"`
	WorkspaceID string    `json:"workspaceId"`
	Logs        string    `json:"logs"`
	CreatedAt   time.Time `json:"createdAt"`
}

type Snapshot struct {
	Sequence   int64                                 `json:"sequence"`
	Runs       map[string]Run                        `json:"runs"`
	Jobs       map[string]Job                        `json:"jobs"`
	HumanTasks map[string]HumanTask                  `json:"humanTasks"`
	Events     []RunEvent                            `json:"events"`
	JobLogs    map[string]JobLog                     `json:"jobLogs"`
	JobState   map[string]map[string]json.RawMessage `json:"jobState"`
	Variables  map[string]map[string]Variable        `json:"variables"`
	Resources  map[string]map[string]Resource        `json:"resources"`
}

type Store interface {
	CreateRunAndEnqueue(ctx context.Context, run Run, job Job) error
	GetRun(ctx context.Context, runID string) (Run, error)
	GetJob(ctx context.Context, workspaceID string, jobID string) (Job, Run, bool, error)
	ListJobs(ctx context.Context, query JobListQuery) ([]JobListItem, error)
	JobSummary(ctx context.Context, workspaceID string, recent time.Duration) (JobSummary, error)
	RequeueQueuedJobsForApp(ctx context.Context, spec RequeueAppSpec) (int64, error)
	GetHumanTask(ctx context.Context, taskID string) (HumanTask, error)
	AppendLogs(ctx context.Context, jobID string, workspaceID string, chunk string) error
	GetLogs(ctx context.Context, workspaceID string, jobID string) (string, bool, error)
	GetState(ctx context.Context, workspaceID string, statePath string) (json.RawMessage, bool, error)
	SetState(ctx context.Context, workspaceID string, statePath string, value json.RawMessage) error
	ListVariables(ctx context.Context, workspaceID string) ([]Variable, error)
	SetVariable(ctx context.Context, workspaceID string, appKey string, path string, value string, isSecret bool, description string) error
	GetVariable(ctx context.Context, workspaceID string, appKey string, path string) (Variable, bool, error)
	GetVariableExact(ctx context.Context, workspaceID string, appKey string, path string) (Variable, bool, error)
	DeleteVariable(ctx context.Context, workspaceID string, appKey string, path string) error
	SetResource(ctx context.Context, workspaceID string, path string, value json.RawMessage, resourceType string, description string) error
	GetResource(ctx context.Context, workspaceID string, path string) (Resource, bool, error)
	DecryptInput(ctx context.Context, workspaceID string, input json.RawMessage) (json.RawMessage, error)
	ClaimJob(ctx context.Context, workerID string, leaseTTL time.Duration) (Job, Lease, error)
	ClaimJobForTags(ctx context.Context, workerID string, tags []string, leaseTTL time.Duration) (Job, Lease, error)
	HeartbeatJob(ctx context.Context, lease Lease, leaseTTL time.Duration) (HeartbeatResult, error)
	CompleteJobSucceeded(ctx context.Context, lease Lease, result contract.JobResult) error
	CompleteJobFailed(ctx context.Context, lease Lease, result contract.JobResult) error
	CompleteJobWaitingHuman(ctx context.Context, lease Lease, result contract.JobResult, task HumanTask) error
	ResumeHumanTask(ctx context.Context, taskID string, resumeInput json.RawMessage) (Run, Job, error)
	ResumeRun(ctx context.Context, runID string, resumeInput json.RawMessage) (Run, Job, error)
	CancelJob(ctx context.Context, workspaceID string, jobID string, by string, reason string) (CancelResult, error)
	CancelRun(ctx context.Context, runID string, reason string) (Run, error)
	RetryRun(ctx context.Context, runID string) (Run, Job, error)
}

type LocalStore struct {
	Path              string
	SecretKey         string
	SecretKeyPrevious string
}

func NewLocalStore(path string) *LocalStore {
	return &LocalStore{Path: path}
}

func (s *LocalStore) ConfigureInputCrypto(secretKey string, previous string) {
	s.SecretKey = strings.TrimSpace(secretKey)
	s.SecretKeyPrevious = strings.TrimSpace(previous)
}

func (s *LocalStore) encryptInput(ctx context.Context, workspaceID string, input json.RawMessage) (json.RawMessage, error) {
	return encryptInputAtRest(ctx, nil, inputCryptoConfig{
		SecretKey:         s.SecretKey,
		SecretKeyPrevious: s.SecretKeyPrevious,
	}, workspaceID, input)
}

func (s *LocalStore) decryptInput(ctx context.Context, workspaceID string, input json.RawMessage) (json.RawMessage, error) {
	return decryptInputAtRest(ctx, nil, inputCryptoConfig{
		SecretKey:         s.SecretKey,
		SecretKeyPrevious: s.SecretKeyPrevious,
	}, workspaceID, input)
}

func (s *LocalStore) DecryptInput(ctx context.Context, workspaceID string, input json.RawMessage) (json.RawMessage, error) {
	return s.decryptInput(ctx, workspaceID, input)
}

func (s *LocalStore) encryptResult(ctx context.Context, workspaceID string, result json.RawMessage) (json.RawMessage, error) {
	return encryptResultAtRest(ctx, nil, inputCryptoConfig{
		SecretKey:         s.SecretKey,
		SecretKeyPrevious: s.SecretKeyPrevious,
	}, workspaceID, result)
}

func (s *LocalStore) decryptResult(ctx context.Context, workspaceID string, result json.RawMessage) (json.RawMessage, error) {
	return decryptResultAtRest(ctx, nil, inputCryptoConfig{
		SecretKey:         s.SecretKey,
		SecretKeyPrevious: s.SecretKeyPrevious,
	}, workspaceID, result)
}

func (s *LocalStore) encryptJobResult(ctx context.Context, workspaceID string, result contract.JobResult) (contract.JobResult, error) {
	output, err := s.encryptResult(ctx, workspaceID, result.Output)
	if err != nil {
		return contract.JobResult{}, err
	}
	result.Output = output
	return result, nil
}

func (s *LocalStore) encryptStoredRunResult(ctx context.Context, workspaceID string, snapshot *Snapshot, runID string) error {
	run, ok := snapshot.Runs[runID]
	if !ok {
		return nil
	}
	if len(run.Output) > 0 {
		output, err := s.encryptResult(ctx, workspaceID, run.Output)
		if err != nil {
			return err
		}
		run.Output = output
	}
	if run.Result != nil {
		result, err := s.encryptJobResult(ctx, workspaceID, *run.Result)
		if err != nil {
			return err
		}
		run.Result = cloneResult(result)
	}
	snapshot.Runs[runID] = run
	return nil
}

func (s *LocalStore) decryptRunResult(ctx context.Context, workspaceID string, run *Run) error {
	if len(run.Output) > 0 {
		output, err := s.decryptResult(ctx, workspaceID, run.Output)
		if err != nil {
			return err
		}
		run.Output = output
	}
	if run.Result != nil && len(run.Result.Output) > 0 {
		output, err := s.decryptResult(ctx, workspaceID, run.Result.Output)
		if err != nil {
			return err
		}
		result := *run.Result
		result.Output = output
		run.Result = &result
	}
	return nil
}

func clearRunResultOutput(run *Run) {
	run.Output = nil
	if run.Result == nil {
		return
	}
	result := *run.Result
	result.Output = nil
	run.Result = &result
}

func NewID(prefix string) string {
	if prefix == "run" || prefix == "job" || prefix == "human" {
		if id, ok := newCanonicalUUID(); ok {
			return id
		}
	}
	var data [12]byte
	if _, err := rand.Read(data[:]); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(data[:])
}

func newCanonicalUUID() (string, bool) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", false
	}
	data[6] = (data[6] & 0x0f) | 0x40
	data[8] = (data[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", data[0:4], data[4:6], data[6:8], data[8:10], data[10:16]), true
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
		ID:             id,
		Adapter:        adapter,
		App:            app,
		Action:         action,
		State:          RunQueued,
		Deployment:     deployment,
		Input:          cloneRaw(input),
		CreatedBy:      defaultActorSubject,
		PermissionedAs: defaultActorSubject,
		CreatedAt:      now,
		UpdatedAt:      now,
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
			Workspace:      run.Deployment.SourceWorkspace(),
			GitSourceID:    run.Deployment.SourceGitSourceID(),
			Commit:         run.Deployment.Commit,
			App:            run.App,
			Action:         run.Action,
			Tag:            contract.EffectiveRouteTagForAction(run.Deployment, actionSpec),
			TriggerKind:    run.Adapter,
			ActionSpec:     actionSpec,
			Input:          cloneRaw(input),
			Deployment:     run.Deployment,
			CorrelationID:  run.CorrelationID,
			Env:            append([]string(nil), run.Env...),
			CreatedBy:      actorCreatedBy(run),
			PermissionedAs: actorPermissionedAs(run),
		},
	}
}

const defaultActorSubject = "system"

func actorCreatedBy(run Run) string {
	if createdBy := strings.TrimSpace(run.CreatedBy); createdBy != "" {
		return createdBy
	}
	return defaultActorSubject
}

func actorPermissionedAs(run Run) string {
	if permissionedAs := strings.TrimSpace(run.PermissionedAs); permissionedAs != "" {
		return permissionedAs
	}
	return actorCreatedBy(run)
}

func cancelActorSubject(job Job, run Run, by string) string {
	return firstNonEmpty(
		strings.TrimSpace(by),
		strings.TrimSpace(run.PermissionedAs),
		strings.TrimSpace(run.CreatedBy),
		strings.TrimSpace(job.Payload.PermissionedAs),
		strings.TrimSpace(job.Payload.CreatedBy),
		defaultActorSubject,
	)
}

const (
	cancelBeforeExecutionMessage = "job canceled before execution"
	cancelDuringExecutionMessage = "job canceled"
	cancelWorkerLostMessage      = "job canceled; worker lost during execution"
)

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
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
		if job.Payload.CorrelationID == "" {
			job.Payload.CorrelationID = run.CorrelationID
		}
		workspaceID := normalizedJobWorkspace("", job)
		runInput, err := s.encryptInput(ctx, workspaceID, run.Input)
		if err != nil {
			return err
		}
		jobInput, err := s.encryptInput(ctx, workspaceID, job.Payload.Input)
		if err != nil {
			return err
		}
		run.Input = runInput
		job.Payload.Input = jobInput
		snapshot.Runs[run.ID] = run
		snapshot.Jobs[job.ID] = job
		runCreated := map[string]string{"app": run.App, "action": run.Action}
		if run.CorrelationID != "" {
			runCreated["correlationId"] = run.CorrelationID
		}
		appendEvent(snapshot, run.ID, "run_created", runCreated, now)
		jobEnqueued := map[string]string{"jobId": job.ID}
		if run.CorrelationID != "" {
			jobEnqueued["correlationId"] = run.CorrelationID
		}
		appendEvent(snapshot, run.ID, "job_enqueued", jobEnqueued, now)
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
	if input, err := s.decryptInput(ctx, run.Deployment.SourceWorkspace(), run.Input); err != nil {
		return Run{}, err
	} else {
		run.Input = input
	}
	if err := s.decryptRunResult(ctx, run.Deployment.SourceWorkspace(), &run); err != nil {
		return Run{}, err
	}
	return run, nil
}

func (s *LocalStore) GetJob(ctx context.Context, workspaceID string, jobID string) (Job, Run, bool, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return Job{}, Run{}, false, err
	}
	job, ok := snapshot.Jobs[jobID]
	if !ok || normalizedJobWorkspace("", job) != contract.NormalizeWorkspace(workspaceID) {
		return Job{}, Run{}, false, nil
	}
	run, ok := snapshot.Runs[job.RunID]
	if !ok {
		return Job{}, Run{}, false, fmt.Errorf("%w: run %q", ErrNotFound, job.RunID)
	}
	if input, err := s.decryptInput(ctx, normalizedJobWorkspace("", job), job.Payload.Input); err != nil {
		return Job{}, Run{}, false, err
	} else {
		job.Payload.Input = input
	}
	if input, err := s.decryptInput(ctx, run.Deployment.SourceWorkspace(), run.Input); err != nil {
		return Job{}, Run{}, false, err
	} else {
		run.Input = input
	}
	if err := s.decryptRunResult(ctx, normalizedJobWorkspace("", job), &run); err != nil {
		return Job{}, Run{}, false, err
	}
	return job, run, true, nil
}

func (s *LocalStore) ListJobs(ctx context.Context, query JobListQuery) ([]JobListItem, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return nil, err
	}
	records := make([]jobRunRecord, 0, len(snapshot.Jobs))
	for _, job := range snapshot.Jobs {
		run, ok := snapshot.Runs[job.RunID]
		if !ok {
			continue
		}
		if err := s.decryptRunResult(ctx, normalizedJobWorkspace("", job), &run); err != nil {
			clearRunResultOutput(&run)
		}
		records = append(records, jobRunRecord{Job: job, Run: run})
	}
	return listJobsFromRecords(records, query), nil
}

func (s *LocalStore) JobSummary(ctx context.Context, workspaceID string, recent time.Duration) (JobSummary, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return JobSummary{}, err
	}
	records := make([]jobRunRecord, 0, len(snapshot.Jobs))
	for _, job := range snapshot.Jobs {
		run, ok := snapshot.Runs[job.RunID]
		if !ok {
			continue
		}
		if err := s.decryptRunResult(ctx, normalizedJobWorkspace("", job), &run); err != nil {
			clearRunResultOutput(&run)
		}
		records = append(records, jobRunRecord{Job: job, Run: run})
	}
	return summarizeJobs(records, workspaceID, recent), nil
}

func (s *LocalStore) RequeueQueuedJobsForApp(ctx context.Context, spec RequeueAppSpec) (int64, error) {
	workspaceID := contract.NormalizeWorkspace(spec.WorkspaceID)
	var moved int64
	err := s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		for id, job := range snapshot.Jobs {
			if job.State != JobQueued || normalizedJobWorkspace("", job) != workspaceID || job.Payload.App != spec.AppKey {
				continue
			}
			if spec.ActionKey != nil && job.Payload.Action != *spec.ActionKey {
				continue
			}
			tag, ok := spec.ActionTags[job.Payload.Action]
			if !ok || strings.TrimSpace(tag) == "" {
				continue
			}
			tag = strings.TrimSpace(tag)
			if job.Payload.Tag == tag {
				continue
			}
			job.Payload.Tag = tag
			job.UpdatedAt = now
			snapshot.Jobs[id] = job
			moved++
		}
		return nil
	})
	return moved, err
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

func (s *LocalStore) AppendLogs(ctx context.Context, jobID string, workspaceID string, chunk string) error {
	if chunk == "" {
		return nil
	}
	return s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		job, ok := snapshot.Jobs[jobID]
		if !ok {
			return fmt.Errorf("%w: job %q", ErrNotFound, jobID)
		}
		workspaceID = normalizedJobWorkspace(workspaceID, job)
		log := snapshot.JobLogs[jobID]
		if log.JobID == "" {
			log.JobID = jobID
			log.WorkspaceID = workspaceID
			log.CreatedAt = now
		}
		if log.WorkspaceID == "" {
			log.WorkspaceID = workspaceID
		}
		log.Logs += chunk
		snapshot.JobLogs[jobID] = log
		return nil
	})
}

func (s *LocalStore) GetLogs(ctx context.Context, workspaceID string, jobID string) (string, bool, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return "", false, err
	}
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	if log, ok := snapshot.JobLogs[jobID]; ok && contract.NormalizeWorkspace(log.WorkspaceID) == workspaceID {
		return log.Logs, true, nil
	}
	job, ok := snapshot.Jobs[jobID]
	if !ok || normalizedJobWorkspace("", job) != workspaceID {
		return "", false, nil
	}
	return "", true, nil
}

func (s *LocalStore) GetState(ctx context.Context, workspaceID string, statePath string) (json.RawMessage, bool, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return nil, false, err
	}
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	if values, ok := snapshot.JobState[workspaceID]; ok {
		if value, ok := values[statePath]; ok {
			return cloneRaw(value), true, nil
		}
	}
	return json.RawMessage("null"), false, nil
}

func (s *LocalStore) SetState(ctx context.Context, workspaceID string, statePath string, value json.RawMessage) error {
	if len(value) == 0 {
		value = json.RawMessage("null")
	}
	if !json.Valid(value) {
		return errors.New("state value is not valid JSON")
	}
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	return s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		if snapshot.JobState[workspaceID] == nil {
			snapshot.JobState[workspaceID] = map[string]json.RawMessage{}
		}
		snapshot.JobState[workspaceID][statePath] = cloneRaw(value)
		return nil
	})
}

func (s *LocalStore) ListVariables(ctx context.Context, workspaceID string) ([]Variable, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return nil, err
	}
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	variables := make([]Variable, 0, len(snapshot.Variables[workspaceID]))
	for _, variable := range snapshot.Variables[workspaceID] {
		variables = append(variables, variable)
	}
	sort.Slice(variables, func(i, j int) bool {
		if variables[i].AppKey != variables[j].AppKey {
			return variables[i].AppKey < variables[j].AppKey
		}
		return variables[i].Path < variables[j].Path
	})
	return variables, nil
}

func (s *LocalStore) SetVariable(ctx context.Context, workspaceID string, appKey string, path string, value string, isSecret bool, description string) error {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	return s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		if snapshot.Variables[workspaceID] == nil {
			snapshot.Variables[workspaceID] = map[string]Variable{}
		}
		snapshot.Variables[workspaceID][variableKey(appKey, path)] = Variable{
			AppKey:      appKey,
			Path:        path,
			Value:       value,
			IsSecret:    isSecret,
			Description: description,
		}
		return nil
	})
}

func (s *LocalStore) GetVariable(ctx context.Context, workspaceID string, appKey string, path string) (Variable, bool, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return Variable{}, false, err
	}
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	variables := snapshot.Variables[workspaceID]
	if appKey != "" {
		if variable, ok := variables[variableKey(appKey, path)]; ok {
			return variable, true, nil
		}
	}
	variable, ok := variables[variableKey("", path)]
	return variable, ok, nil
}

func (s *LocalStore) GetVariableExact(ctx context.Context, workspaceID string, appKey string, path string) (Variable, bool, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return Variable{}, false, err
	}
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	variable, ok := snapshot.Variables[workspaceID][variableKey(appKey, path)]
	return variable, ok, nil
}

func (s *LocalStore) DeleteVariable(ctx context.Context, workspaceID string, appKey string, path string) error {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	return s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		delete(snapshot.Variables[workspaceID], variableKey(appKey, path))
		return nil
	})
}

func (s *LocalStore) SetResource(ctx context.Context, workspaceID string, path string, value json.RawMessage, resourceType string, description string) error {
	if len(value) == 0 {
		value = json.RawMessage("{}")
	}
	if !json.Valid(value) {
		return errors.New("resource value is not valid JSON")
	}
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	return s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		if snapshot.Resources[workspaceID] == nil {
			snapshot.Resources[workspaceID] = map[string]Resource{}
		}
		snapshot.Resources[workspaceID][path] = Resource{
			Path:         path,
			Value:        cloneRaw(value),
			ResourceType: resourceType,
			Description:  description,
		}
		return nil
	})
}

func (s *LocalStore) GetResource(ctx context.Context, workspaceID string, path string) (Resource, bool, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return Resource{}, false, err
	}
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	resource, ok := snapshot.Resources[workspaceID][path]
	resource.Value = cloneRaw(resource.Value)
	return resource, ok, nil
}

func (s *LocalStore) ClaimJob(ctx context.Context, workerID string, leaseTTL time.Duration) (Job, Lease, error) {
	return s.ClaimJobForTags(ctx, workerID, nil, leaseTTL)
}

func (s *LocalStore) ClaimJobForTags(ctx context.Context, workerID string, tags []string, leaseTTL time.Duration) (Job, Lease, error) {
	if workerID == "" {
		workerID = NewID("worker")
	}
	if leaseTTL <= 0 {
		leaseTTL = defaultLeaseTime
	}
	allowedTags := normalizeClaimTags(tags)
	var claimed Job
	var lease Lease
	err := s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		if err := s.requeueExpiredJobs(ctx, snapshot, now); err != nil {
			return err
		}

		ids := make([]string, 0, len(snapshot.Jobs))
		for id, job := range snapshot.Jobs {
			if job.State == JobQueued && claimTagAllowed(job, allowedTags) {
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

		var job Job
		for _, id := range ids {
			candidate := snapshot.Jobs[id]
			if maxConcurrentReached(snapshot, candidate) {
				continue
			}
			job = candidate
			break
		}
		if job.ID == "" {
			return ErrNoQueuedJob
		}
		expiresAt := now.Add(leaseTTL)
		job.State = JobRunning
		job.Attempt++
		job.LeaseOwner = workerID
		job.LeaseExpiresAt = &expiresAt
		job.StartedAt = &now
		job.UpdatedAt = now
		snapshot.Jobs[job.ID] = job

		run := snapshot.Runs[job.RunID]
		if run.State == RunQueued || run.State == RunResuming {
			run.State = RunRunning
			run.UpdatedAt = now
			snapshot.Runs[run.ID] = run
			appendEvent(snapshot, run.ID, "run_running", eventPayload(run.CorrelationID, map[string]any{"jobId": job.ID}), now)
		}
		appendEvent(snapshot, job.RunID, "job_claimed", eventPayload(job.Payload.CorrelationID, map[string]any{"jobId": job.ID, "workerId": workerID, "attempt": job.Attempt}), now)

		claimed = job
		lease = Lease{JobID: job.ID, WorkerID: workerID, ExpiresAt: expiresAt, Attempt: job.Attempt, AcquiredAt: now}
		return nil
	})
	if err != nil {
		return Job{}, Lease{}, err
	}
	return claimed, lease, nil
}

func (s *LocalStore) HeartbeatJob(ctx context.Context, lease Lease, leaseTTL time.Duration) (HeartbeatResult, error) {
	if leaseTTL <= 0 {
		leaseTTL = defaultLeaseTime
	}
	var result HeartbeatResult
	err := s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		job, ok := snapshot.Jobs[lease.JobID]
		if !ok || job.State != JobRunning || job.LeaseOwner != lease.WorkerID || job.Attempt != lease.Attempt {
			return nil
		}
		expiresAt := now.Add(leaseTTL)
		job.LeaseExpiresAt = &expiresAt
		job.UpdatedAt = now
		snapshot.Jobs[job.ID] = job
		result.StillOwned = true
		result.CanceledBy = cloneStringPtr(job.CanceledBy)
		result.CanceledReason = cloneStringPtr(job.CanceledReason)
		return nil
	})
	return result, err
}

func (s *LocalStore) CompleteJobSucceeded(ctx context.Context, lease Lease, result contract.JobResult) error {
	return s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		job, run, err := leasedJobAndRun(snapshot, lease, now)
		if err != nil {
			return err
		}
		if job.CanceledBy != nil {
			applyCanceledJob(snapshot, job, run, *job.CanceledBy, canceledReasonValue(job), cancelDuringExecutionMessage, now)
			return s.encryptStoredRunResult(ctx, normalizedJobWorkspace("", job), snapshot, run.ID)
		}
		storedResult, err := s.encryptJobResult(ctx, normalizedJobWorkspace("", job), result)
		if err != nil {
			return err
		}
		job.State = JobSucceeded
		job.LeaseExpiresAt = nil
		job.UpdatedAt = now
		run.State = RunSucceeded
		run.Output = cloneRaw(storedResult.Output)
		run.Result = cloneResult(storedResult)
		run.Error = nil
		run.TaskID = ""
		run.UpdatedAt = now
		snapshot.Jobs[job.ID] = job
		snapshot.Runs[run.ID] = run
		appendEvent(snapshot, run.ID, "run_succeeded", eventPayload(run.CorrelationID, map[string]any{"jobId": job.ID}), now)
		return nil
	})
}

func (s *LocalStore) CompleteJobFailed(ctx context.Context, lease Lease, result contract.JobResult) error {
	return s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		job, run, err := leasedJobAndRun(snapshot, lease, now)
		if err != nil {
			return err
		}
		if job.CanceledBy != nil {
			applyCanceledJob(snapshot, job, run, *job.CanceledBy, canceledReasonValue(job), cancelDuringExecutionMessage, now)
			return s.encryptStoredRunResult(ctx, normalizedJobWorkspace("", job), snapshot, run.ID)
		}
		if result.Error == "" && result.ExitCode != 0 {
			result.Error = fmt.Sprintf("action exited with code %d", result.ExitCode)
		}
		storedResult, err := s.encryptJobResult(ctx, normalizedJobWorkspace("", job), result)
		if err != nil {
			return err
		}
		job.State = JobFailed
		job.LeaseExpiresAt = nil
		job.UpdatedAt = now
		run.State = RunFailed
		run.Result = cloneResult(storedResult)
		run.Error = mustRaw(map[string]any{"message": result.Error, "exitCode": result.ExitCode})
		run.UpdatedAt = now
		snapshot.Jobs[job.ID] = job
		snapshot.Runs[run.ID] = run
		appendEvent(snapshot, run.ID, "run_failed", eventPayload(run.CorrelationID, map[string]any{"jobId": job.ID, "error": result.Error, "exitCode": result.ExitCode}), now)
		return nil
	})
}

func (s *LocalStore) CompleteJobWaitingHuman(ctx context.Context, lease Lease, result contract.JobResult, task HumanTask) error {
	return s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		job, run, err := leasedJobAndRun(snapshot, lease, now)
		if err != nil {
			return err
		}
		if job.CanceledBy != nil {
			applyCanceledJob(snapshot, job, run, *job.CanceledBy, canceledReasonValue(job), cancelDuringExecutionMessage, now)
			return s.encryptStoredRunResult(ctx, normalizedJobWorkspace("", job), snapshot, run.ID)
		}
		storedResult, err := s.encryptJobResult(ctx, normalizedJobWorkspace("", job), result)
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
		job.LeaseExpiresAt = nil
		job.UpdatedAt = now
		run.State = RunWaitingHuman
		run.Result = cloneResult(storedResult)
		run.Error = nil
		run.TaskID = task.ID
		run.UpdatedAt = now
		snapshot.Jobs[job.ID] = job
		snapshot.Runs[run.ID] = run
		snapshot.HumanTasks[task.ID] = task
		appendEvent(snapshot, run.ID, "human_task_created", eventPayload(run.CorrelationID, map[string]any{"jobId": job.ID, "taskId": task.ID}), now)
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
		plainRunInput, err := s.decryptInput(ctx, run.Deployment.SourceWorkspace(), run.Input)
		if err != nil {
			return err
		}
		jobInput := mergeResumeInput(plainRunInput, task.ID, resumeInput)
		job := NewActionJob(run, jobInput)
		job.CreatedAt = now
		job.UpdatedAt = now
		storedJobInput, err := s.encryptInput(ctx, normalizedJobWorkspace("", job), job.Payload.Input)
		if err != nil {
			return err
		}
		storedJob := job
		storedJob.Payload.Input = storedJobInput

		snapshot.HumanTasks[task.ID] = task
		snapshot.Runs[run.ID] = run
		snapshot.Jobs[storedJob.ID] = storedJob
		appendEvent(snapshot, run.ID, "human_task_resumed", eventPayload(run.CorrelationID, map[string]any{"taskId": task.ID, "jobId": job.ID}), now)
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

func (s *LocalStore) CancelJob(ctx context.Context, workspaceID string, jobID string, by string, reason string) (CancelResult, error) {
	var result CancelResult
	err := s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		job, ok := snapshot.Jobs[jobID]
		if !ok || normalizedJobWorkspace("", job) != contract.NormalizeWorkspace(workspaceID) {
			return nil
		}
		run, ok := snapshot.Runs[job.RunID]
		if !ok {
			return fmt.Errorf("%w: run %q", ErrNotFound, job.RunID)
		}
		result.Found = true
		if IsTerminal(run) || job.State == JobSucceeded || job.State == JobFailed {
			result.AlreadyCompleted = true
			return nil
		}
		canceledBy := cancelActorSubject(job, run, by)
		if job.State == JobRunning {
			job.CanceledBy = &canceledBy
			job.CanceledReason = &reason
			job.UpdatedAt = now
			snapshot.Jobs[job.ID] = job
			result.SoftCanceled = true
		} else {
			result.CompletedNow = true
			applyCanceledJob(snapshot, job, run, canceledBy, reason, cancelBeforeExecutionMessage, now)
			return s.encryptStoredRunResult(ctx, normalizedJobWorkspace("", job), snapshot, run.ID)
		}
		return nil
	})
	return result, err
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
		appendEvent(snapshot, runID, "run_canceled", eventPayload(run.CorrelationID, map[string]any{"reason": reason}), now)
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
		plainRunInput, err := s.decryptInput(ctx, run.Deployment.SourceWorkspace(), run.Input)
		if err != nil {
			return err
		}
		job := NewActionJob(run, plainRunInput)
		job.CreatedAt = now
		job.UpdatedAt = now
		storedJobInput, err := s.encryptInput(ctx, normalizedJobWorkspace("", job), job.Payload.Input)
		if err != nil {
			return err
		}
		storedJob := job
		storedJob.Payload.Input = storedJobInput
		snapshot.Runs[run.ID] = run
		snapshot.Jobs[storedJob.ID] = storedJob
		appendEvent(snapshot, run.ID, "run_retried", eventPayload(run.CorrelationID, map[string]any{"jobId": job.ID}), now)
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
	if snapshot.JobLogs == nil {
		snapshot.JobLogs = map[string]JobLog{}
	}
	if snapshot.JobState == nil {
		snapshot.JobState = map[string]map[string]json.RawMessage{}
	}
	if snapshot.Variables == nil {
		snapshot.Variables = map[string]map[string]Variable{}
	}
	if snapshot.Resources == nil {
		snapshot.Resources = map[string]map[string]Resource{}
	}
}

func variableKey(appKey string, path string) string {
	return appKey + "\x00" + path
}

func (s *LocalStore) requeueExpiredJobs(ctx context.Context, snapshot *Snapshot, now time.Time) error {
	for id, job := range snapshot.Jobs {
		if job.State != JobRunning || job.LeaseExpiresAt == nil || job.LeaseExpiresAt.After(now) {
			continue
		}
		if job.CanceledBy != nil {
			if run, ok := snapshot.Runs[job.RunID]; ok {
				applyCanceledJob(snapshot, job, run, *job.CanceledBy, canceledReasonValue(job), cancelWorkerLostMessage, now)
				if err := s.encryptStoredRunResult(ctx, normalizedJobWorkspace("", job), snapshot, run.ID); err != nil {
					return err
				}
			}
			continue
		}
		job.State = JobQueued
		job.LeaseOwner = ""
		job.LeaseExpiresAt = nil
		job.StartedAt = nil
		job.UpdatedAt = now
		snapshot.Jobs[id] = job
		appendEvent(snapshot, job.RunID, "job_lease_expired", eventPayload(job.Payload.CorrelationID, map[string]any{"jobId": job.ID}), now)
	}
	return nil
}

func leasedJobAndRun(snapshot *Snapshot, lease Lease, now time.Time) (Job, Run, error) {
	job, ok := snapshot.Jobs[lease.JobID]
	if !ok {
		return Job{}, Run{}, fmt.Errorf("%w: job %q", ErrNotFound, lease.JobID)
	}
	if job.State != JobRunning || job.LeaseOwner != lease.WorkerID || job.Attempt != lease.Attempt {
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

func applyCanceledJob(snapshot *Snapshot, job Job, run Run, by string, reason string, message string, now time.Time) {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "job canceled"
	}
	job.State = JobFailed
	job.LeaseExpiresAt = nil
	job.CanceledBy = &by
	job.CanceledReason = &reason
	job.UpdatedAt = now
	run.State = RunCanceled
	run.Result = canceledJobResult(job, run, message)
	run.Error = mustRaw(map[string]string{
		"message":        message,
		"canceledBy":     by,
		"canceledReason": reason,
	})
	run.UpdatedAt = now
	snapshot.Jobs[job.ID] = job
	snapshot.Runs[run.ID] = run
	appendEvent(snapshot, run.ID, "run_canceled", eventPayload(run.CorrelationID, map[string]any{"jobId": job.ID, "by": by, "reason": reason}), now)
}

func canceledJobResult(job Job, run Run, message string) *contract.JobResult {
	return &contract.JobResult{
		JobID:    job.ID,
		App:      run.App,
		Action:   run.Action,
		Output:   mustRaw(map[string]string{"name": "Canceled", "message": message}),
		ExitCode: -1,
		Error:    message,
	}
}

type jobRunRecord struct {
	Job Job
	Run Run
}

func listJobsFromRecords(records []jobRunRecord, query JobListQuery) []JobListItem {
	query.WorkspaceID = contract.NormalizeWorkspace(query.WorkspaceID)
	if query.Status == "" {
		query.Status = "all"
	}
	items := make([]JobListItem, 0, len(records))
	for _, record := range records {
		job := record.Job
		run := record.Run
		if normalizedJobWorkspace("", job) != query.WorkspaceID {
			continue
		}
		if query.AppKey != "" && job.Payload.App != query.AppKey {
			continue
		}
		if query.ActionKey != "" && job.Payload.Action != query.ActionKey {
			continue
		}
		if query.TriggerKind != "" && jobTriggerKind(job) != query.TriggerKind {
			continue
		}
		if query.Since != nil && job.CreatedAt.Before(*query.Since) {
			continue
		}
		if query.Until != nil && job.CreatedAt.After(*query.Until) {
			continue
		}
		item := newJobListItem(query.WorkspaceID, job, run)
		if !jobStatusMatches(query.Status, item.Status) {
			continue
		}
		if query.CursorCreatedAt != nil {
			if job.CreatedAt.After(*query.CursorCreatedAt) || job.CreatedAt.Equal(*query.CursorCreatedAt) && job.ID >= query.CursorID {
				continue
			}
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i int, j int) bool {
		if !items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].CreatedAt.After(items[j].CreatedAt)
		}
		return items[i].ID > items[j].ID
	})
	if query.Limit > 0 && len(items) > query.Limit {
		items = items[:query.Limit]
	}
	return items
}

func summarizeJobs(records []jobRunRecord, workspaceID string, recent time.Duration) JobSummary {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	if recent <= 0 {
		recent = 24 * time.Hour
	}
	cutoff := time.Now().UTC().Add(-recent)
	summary := JobSummary{
		ByTag: []JobSummaryTagBreakdown{},
		ByApp: []JobSummaryAppBreakdown{},
	}
	byTag := map[string]*JobSummaryCounts{}
	byApp := map[string]*JobSummaryCounts{}
	for _, record := range records {
		job := record.Job
		run := record.Run
		if normalizedJobWorkspace("", job) != workspaceID {
			continue
		}
		status := jobTerminalStatus(job, run)
		tag := jobTag(job)
		app := job.Payload.App
		tagCounts := byTag[tag]
		if tagCounts == nil {
			tagCounts = &JobSummaryCounts{}
			byTag[tag] = tagCounts
		}
		appCounts := byApp[app]
		if appCounts == nil {
			appCounts = &JobSummaryCounts{}
			byApp[app] = appCounts
		}
		countJobSummary(&summary.JobSummaryCounts, job, run, status, cutoff)
		countJobSummary(tagCounts, job, run, status, cutoff)
		countJobSummary(appCounts, job, run, status, cutoff)
		if job.State == JobQueued && (summary.OldestQueuedAt == nil || job.CreatedAt.Before(*summary.OldestQueuedAt)) {
			value := job.CreatedAt
			summary.OldestQueuedAt = &value
		}
	}
	tags := make([]string, 0, len(byTag))
	for tag := range byTag {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	for _, tag := range tags {
		counts := *byTag[tag]
		if includeJobSummaryBreakdown(counts) {
			summary.ByTag = append(summary.ByTag, JobSummaryTagBreakdown{Tag: tag, JobSummaryCounts: counts})
		}
	}
	apps := make([]string, 0, len(byApp))
	for app := range byApp {
		apps = append(apps, app)
	}
	sort.Strings(apps)
	for _, app := range apps {
		counts := *byApp[app]
		if includeJobSummaryBreakdown(counts) {
			summary.ByApp = append(summary.ByApp, JobSummaryAppBreakdown{AppKey: app, JobSummaryCounts: counts})
		}
	}
	return summary
}

func includeJobSummaryBreakdown(counts JobSummaryCounts) bool {
	return counts.QueuedCount > 0 || counts.RunningCount > 0 || counts.CompletedCountRecent > 0
}

func countJobSummary(counts *JobSummaryCounts, job Job, run Run, status string, cutoff time.Time) {
	switch job.State {
	case JobQueued:
		counts.QueuedCount++
	case JobRunning:
		counts.RunningCount++
	}
	if job.State == JobSucceeded || job.State == JobFailed || IsTerminal(run) {
		completedAt := run.UpdatedAt
		if completedAt.IsZero() {
			completedAt = job.UpdatedAt
		}
		if !completedAt.Before(cutoff) {
			counts.CompletedCountRecent++
			switch status {
			case "failure":
				counts.FailedCountRecent++
			case "canceled":
				counts.CanceledCountRecent++
			}
		}
	}
}

func newJobListItem(workspaceID string, job Job, run Run) JobListItem {
	status := jobTerminalStatus(job, run)
	var startedAt *time.Time
	var completedAt *time.Time
	var worker *string
	startedAt = job.StartedAt
	if job.LeaseOwner != "" {
		worker = stringPtr(job.LeaseOwner)
	}
	if job.State == JobRunning {
		if startedAt == nil {
			startedAt = &job.UpdatedAt
		}
	}
	if job.State == JobSucceeded || job.State == JobFailed || IsTerminal(run) {
		completedAt = &run.UpdatedAt
	}
	var durationMs int64
	if run.Result != nil {
		durationMs = run.Result.DurationMs
	}
	return JobListItem{
		ID:             job.ID,
		WorkspaceID:    contract.NormalizeWorkspace(workspaceID),
		AppKey:         job.Payload.App,
		ActionKey:      job.Payload.Action,
		TriggerKind:    jobTriggerKind(job),
		Status:         status,
		Queued:         job.State == JobQueued,
		Running:        job.State == JobRunning,
		Completed:      job.State == JobSucceeded || job.State == JobFailed || IsTerminal(run),
		CreatedAt:      job.CreatedAt,
		StartedAt:      startedAt,
		CompletedAt:    completedAt,
		DurationMs:     durationMs,
		Worker:         worker,
		GitSourceID:    numericStringPtr(job.Payload.GitSourceID),
		CommitSha:      stringPtr(job.Payload.Commit),
		Entrypoint:     jobEntrypoint(job),
		Tag:            jobTag(job),
		CreatedBy:      firstNonEmpty(strings.TrimSpace(job.Payload.CreatedBy), strings.TrimSpace(run.CreatedBy), defaultActorSubject),
		PermissionedAs: firstNonEmpty(strings.TrimSpace(job.Payload.PermissionedAs), strings.TrimSpace(run.PermissionedAs), strings.TrimSpace(job.Payload.CreatedBy), strings.TrimSpace(run.CreatedBy), defaultActorSubject),
		CanceledBy:     firstPresentStringPtr(job.CanceledBy, canceledBy(run)),
		CanceledReason: firstPresentStringPtr(job.CanceledReason, canceledReason(run)),
		FlowRunID:      stringPtr(job.Payload.FlowRunID),
		FlowStepID:     stringPtr(job.Payload.FlowStepID),
		ErrorSnippet:   failureSnippet(status, run),
	}
}

func jobEntrypoint(job Job) string {
	if entrypoint := strings.TrimSpace(job.Payload.Deployment.Entrypoint); entrypoint != "" {
		return entrypoint
	}
	return strings.TrimSpace(job.Payload.ActionSpec.Entrypoint)
}

func jobStatusMatches(filter string, status string) bool {
	switch filter {
	case "", "all":
		return true
	case "completed":
		return status == "success" || status == "failure" || status == "canceled"
	case "success":
		return status == "success"
	case "failure":
		return status == "failure"
	default:
		return status == filter
	}
}

func jobTerminalStatus(job Job, run Run) string {
	if run.State == RunCanceled {
		return "canceled"
	}
	if job.State == JobQueued {
		return "queued"
	}
	if job.State == JobRunning {
		return "running"
	}
	if job.State == JobSucceeded || run.State == RunSucceeded || run.State == RunWaitingHuman {
		return "success"
	}
	return "failure"
}

func jobTriggerKind(job Job) string {
	if job.Payload.TriggerKind != "" {
		return job.Payload.TriggerKind
	}
	if job.Payload.CorrelationID != "" {
		return "api"
	}
	return "api"
}

func jobTag(job Job) string {
	if strings.TrimSpace(job.Payload.Tag) != "" {
		return strings.TrimSpace(job.Payload.Tag)
	}
	return contract.EffectiveRouteTagForAction(job.Payload.Deployment, job.Payload.ActionSpec)
}

func jobAppKey(job Job) string {
	if app := strings.TrimSpace(job.Payload.App); app != "" {
		return app
	}
	return strings.TrimSpace(job.Payload.Deployment.App)
}

func jobMaxConcurrent(job Job) (int, bool) {
	if job.Payload.Deployment.MaxConcurrent == nil || *job.Payload.Deployment.MaxConcurrent <= 0 {
		return 0, false
	}
	return int(*job.Payload.Deployment.MaxConcurrent), true
}

func maxConcurrentReached(snapshot *Snapshot, candidate Job) bool {
	limit, ok := jobMaxConcurrent(candidate)
	if !ok {
		return false
	}
	workspaceID := normalizedJobWorkspace("", candidate)
	appKey := jobAppKey(candidate)
	if appKey == "" {
		return false
	}
	running := 0
	for _, job := range snapshot.Jobs {
		if job.State != JobRunning {
			continue
		}
		if normalizedJobWorkspace("", job) == workspaceID && jobAppKey(job) == appKey {
			running++
		}
	}
	return running >= limit
}

func normalizeClaimTags(tags []string) map[string]struct{} {
	normalized := map[string]struct{}{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		normalized[tag] = struct{}{}
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func claimTagAllowed(job Job, tags map[string]struct{}) bool {
	if len(tags) == 0 {
		return true
	}
	_, ok := tags[jobTag(job)]
	return ok
}

func canceledReason(run Run) *string {
	if run.State != RunCanceled || len(run.Error) == 0 {
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
		if payload.Message != "" {
			return stringPtr(payload.Message)
		}
	}
	return nil
}

func canceledReasonValue(job Job) string {
	if job.CanceledReason == nil {
		return ""
	}
	return *job.CanceledReason
}

func canceledBy(run Run) *string {
	if run.State != RunCanceled || len(run.Error) == 0 {
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

func firstPresentStringPtr(values ...*string) *string {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

const errorSnippetMax = 200

func failureSnippet(status string, run Run) *string {
	if status != "failure" {
		return nil
	}
	if run.Result != nil {
		if len(run.Result.Output) > 0 {
			if snippet := errorSnippet(run.Result.Output); snippet != nil {
				return snippet
			}
		}
		if run.Result.Error != "" {
			return errorSnippet([]byte(run.Result.Error))
		}
	}
	if len(run.Error) > 0 {
		return errorSnippet(run.Error)
	}
	return nil
}

func errorSnippet(plain []byte) *string {
	var workerError struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	}
	text := ""
	if err := json.Unmarshal(plain, &workerError); err == nil && workerError.Message != "" {
		if workerError.Name != "" {
			text = workerError.Name + ": " + workerError.Message
		} else {
			text = workerError.Message
		}
	} else {
		text = string(plain)
	}
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return nil
	}
	runes := []rune(text)
	if len(runes) > errorSnippetMax {
		text = string(runes[:errorSnippetMax]) + "\u2026"
	}
	return stringPtr(text)
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func numericStringPtr(value string) *int64 {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed <= 0 {
		return nil
	}
	return &parsed
}

func eventPayload(correlationID string, values map[string]any) map[string]any {
	if values == nil {
		values = map[string]any{}
	}
	if correlationID != "" {
		values["correlationId"] = correlationID
	}
	return values
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

func normalizedJobWorkspace(workspaceID string, job Job) string {
	if workspaceID == "" {
		workspaceID = job.Payload.Workspace
	}
	if workspaceID == "" {
		workspaceID = job.Payload.Deployment.SourceWorkspace()
	}
	return contract.NormalizeWorkspace(workspaceID)
}

type inputCryptoConfig struct {
	SecretKey         string
	SecretKeyPrevious string
}

type inputWorkspaceKeyProvider interface {
	GetWorkspaceKeyVersioned(ctx context.Context, workspaceID string) (string, int32, error)
}

func encryptInputAtRest(ctx context.Context, provider inputWorkspaceKeyProvider, config inputCryptoConfig, workspaceID string, input json.RawMessage) (json.RawMessage, error) {
	return encryptJSONAtRest(ctx, provider, config, workspaceID, input, "{}", "input")
}

func decryptInputAtRest(ctx context.Context, provider inputWorkspaceKeyProvider, config inputCryptoConfig, workspaceID string, input json.RawMessage) (json.RawMessage, error) {
	return decryptJSONAtRest(ctx, provider, config, workspaceID, input, "{}", "input")
}

func encryptResultAtRest(ctx context.Context, provider inputWorkspaceKeyProvider, config inputCryptoConfig, workspaceID string, result json.RawMessage) (json.RawMessage, error) {
	return encryptJSONAtRest(ctx, provider, config, workspaceID, result, "null", "result")
}

func decryptResultAtRest(ctx context.Context, provider inputWorkspaceKeyProvider, config inputCryptoConfig, workspaceID string, result json.RawMessage) (json.RawMessage, error) {
	return decryptJSONAtRest(ctx, provider, config, workspaceID, result, "null", "result")
}

func encryptJSONAtRest(ctx context.Context, provider inputWorkspaceKeyProvider, config inputCryptoConfig, workspaceID string, value json.RawMessage, defaultJSON string, label string) (json.RawMessage, error) {
	value = canonicalJSONValue(value, defaultJSON)
	if !json.Valid(value) {
		return nil, fmt.Errorf("%s is not valid JSON", label)
	}
	if strings.TrimSpace(config.SecretKey) == "" || wfcrypto.IsEnc(value) {
		return cloneRaw(value), nil
	}
	dek, err := resolveInputDEK(ctx, provider, config, workspaceID)
	if err != nil {
		return nil, err
	}
	encrypted, err := wfcrypto.WrapEnc(dek, value)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(encrypted), nil
}

func decryptJSONAtRest(ctx context.Context, provider inputWorkspaceKeyProvider, config inputCryptoConfig, workspaceID string, value json.RawMessage, defaultJSON string, label string) (json.RawMessage, error) {
	value = canonicalJSONValue(value, defaultJSON)
	if !wfcrypto.IsEnc(value) {
		return cloneRaw(value), nil
	}
	if strings.TrimSpace(config.SecretKey) == "" {
		return nil, fmt.Errorf("%s is encrypted but SECRET_KEY is not configured", label)
	}
	dek, err := resolveInputDEK(ctx, provider, config, workspaceID)
	if err != nil {
		return nil, err
	}
	plain, err := wfcrypto.UnwrapEnc(dek, value)
	if err != nil {
		return nil, err
	}
	if !json.Valid(plain) {
		return nil, fmt.Errorf("decrypted %s is not valid JSON", label)
	}
	return json.RawMessage(append([]byte(nil), plain...)), nil
}

func resolveInputDEK(ctx context.Context, provider inputWorkspaceKeyProvider, config inputCryptoConfig, workspaceID string) (string, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	if provider != nil {
		key, version, err := provider.GetWorkspaceKeyVersioned(ctx, workspaceID)
		if err != nil {
			return "", err
		}
		if key != "" {
			return wfcrypto.ResolveDEK(key, version, inputKEKs(config))
		}
	}
	return wfcrypto.DeriveWorkspaceKey(strings.TrimSpace(config.SecretKey), workspaceID), nil
}

func inputKEKs(config inputCryptoConfig) []string {
	keks := []string{wfcrypto.DeriveKEK(strings.TrimSpace(config.SecretKey))}
	if previous := strings.TrimSpace(config.SecretKeyPrevious); previous != "" {
		keks = append(keks, wfcrypto.DeriveKEK(previous))
	}
	return keks
}

func canonicalJSONInput(input json.RawMessage) json.RawMessage {
	return canonicalJSONValue(input, "{}")
}

func canonicalJSONValue(input json.RawMessage, defaultJSON string) json.RawMessage {
	if len(input) == 0 {
		return json.RawMessage(defaultJSON)
	}
	return cloneRaw(input)
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
