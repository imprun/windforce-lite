package state

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/imprun/windforce-core/internal/catalog"
	"github.com/imprun/windforce-core/internal/contract"
	controlevent "github.com/imprun/windforce-core/internal/event"
	"github.com/imprun/windforce-core/internal/webhook"
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

// TerminalRunState reports whether a run has settled: it will never be
// claimed or resumed again, so its records are eligible for retention
// pruning.
func TerminalRunState(state RunState) bool {
	switch state {
	case RunSucceeded, RunFailed, RunCanceled, RunExpired:
		return true
	}
	return false
}

// retentionCutoff picks the per-outcome cutoff for a settled run: succeeded
// runs age out on the success TTL, everything else (failed, canceled,
// expired) on the failure TTL, which is typically longer for incident
// analysis (ADR 0007).
func retentionCutoff(state RunState, successOlderThan time.Time, failureOlderThan time.Time) time.Time {
	if state == RunSucceeded {
		return successOlderThan
	}
	return failureOlderThan
}

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
	ClientID       string              `json:"clientId,omitempty"`
	CreatedAt      time.Time           `json:"createdAt"`
	UpdatedAt      time.Time           `json:"updatedAt"`
	ExpiresAt      *time.Time          `json:"expiresAt,omitempty"`
}

type JobPayload struct {
	Workspace             string          `json:"workspace,omitempty"`
	GitSourceID           string          `json:"gitSourceId,omitempty"`
	Commit                string          `json:"commit,omitempty"`
	App                   string          `json:"app"`
	Action                string          `json:"action"`
	Version               string          `json:"version,omitempty"`
	Tag                   string          `json:"tag,omitempty"`
	DeploymentTag         string          `json:"deploymentTag,omitempty"`
	DeploymentTagOverride *string         `json:"deploymentTagOverride,omitempty"`
	Entrypoint            string          `json:"entrypoint,omitempty"`
	Runtime               string          `json:"runtime,omitempty"`
	ScriptLang            string          `json:"scriptLang,omitempty"`
	TimeoutS              int32           `json:"timeout,omitempty"`
	MaxConcurrent         *int32          `json:"maxConcurrent,omitempty"`
	RequiredCapabilities  []string        `json:"requiredCapabilities,omitempty"`
	RequiredLabels        []string        `json:"requiredLabels,omitempty"`
	DeploymentID          *string         `json:"deploymentId,omitempty"`
	BundleDigest          string          `json:"bundleDigest,omitempty"`
	BundleURI             string          `json:"bundleUri,omitempty"`
	ObjectURI             string          `json:"objectUri,omitempty"`
	TriggerKind           string          `json:"triggerKind,omitempty"`
	TriggerHeaders        json.RawMessage `json:"triggerHeaders,omitempty"`
	ActionSpec            contract.Action `json:"actionSpec,omitempty"`
	InputSchema           json.RawMessage `json:"inputSchema,omitempty"`
	OutputSchema          json.RawMessage `json:"outputSchema,omitempty"`
	Input                 json.RawMessage `json:"input,omitempty"`
	// Deployment is retained only to decode jobs created before compact pins.
	Deployment     *contract.Deployment `json:"deployment,omitempty"`
	CorrelationID  string               `json:"correlationId,omitempty"`
	Env            []string             `json:"env,omitempty"`
	CreatedBy      string               `json:"createdBy,omitempty"`
	PermissionedAs string               `json:"permissionedAs,omitempty"`
	ClientID       string               `json:"clientId,omitempty"`
	FlowRunID      string               `json:"flowRunId,omitempty"`
	FlowStepID     string               `json:"flowStepId,omitempty"`
	FlowKey        string               `json:"flowKey,omitempty"`
	FlowStepKey    string               `json:"flowStepKey,omitempty"`
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

type Client struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	ExternalKey string    `json:"external_key"`
	CreatedBy   string    `json:"created_by"`
	UpdatedBy   string    `json:"updated_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (client *Client) UnmarshalJSON(data []byte) error {
	type clientAlias Client
	if err := json.Unmarshal(data, (*clientAlias)(client)); err != nil {
		return err
	}
	if client.ExternalKey == "" {
		var legacy struct {
			ExternalKey string `json:"client_key"`
		}
		if err := json.Unmarshal(data, &legacy); err != nil {
			return err
		}
		client.ExternalKey = legacy.ExternalKey
	}
	return nil
}

type ClientAudit struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	ClientID    string    `json:"client_id"`
	Kind        string    `json:"kind"`
	Detail      string    `json:"detail,omitempty"`
	Actor       string    `json:"actor"`
	CreatedAt   time.Time `json:"created_at"`
}

type InputConfig struct {
	WorkspaceID string          `json:"workspace_id"`
	AppKey      string          `json:"app_key"`
	ActionKey   string          `json:"action_key"`
	ClientID    string          `json:"client_id,omitempty"`
	Config      json.RawMessage `json:"config"`
	LockedKeys  []string        `json:"locked_keys"`
	UpdatedBy   string          `json:"updated_by"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type InputConfigAudit struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	AppKey      string    `json:"app_key"`
	ActionKey   string    `json:"action_key"`
	ClientID    string    `json:"client_id,omitempty"`
	Kind        string    `json:"kind"`
	Detail      string    `json:"detail,omitempty"`
	Actor       string    `json:"actor"`
	CreatedAt   time.Time `json:"created_at"`
}

func (record *ClientAudit) UnmarshalJSON(data []byte) error {
	type auditAlias ClientAudit
	if err := json.Unmarshal(data, (*auditAlias)(record)); err != nil {
		return err
	}
	if record.ClientID == "" {
		var legacy struct {
			ClientID string `json:"api_client_id"`
		}
		if err := json.Unmarshal(data, &legacy); err != nil {
			return err
		}
		record.ClientID = legacy.ClientID
	}
	return nil
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
	Sequence             int64                                 `json:"sequence"`
	Runs                 map[string]Run                        `json:"runs"`
	Jobs                 map[string]Job                        `json:"jobs"`
	HumanTasks           map[string]HumanTask                  `json:"humanTasks"`
	Events               []RunEvent                            `json:"events"`
	JobLogs              map[string]JobLog                     `json:"jobLogs"`
	JobState             map[string]map[string]json.RawMessage `json:"jobState"`
	Variables            map[string]map[string]Variable        `json:"variables"`
	Resources            map[string]map[string]Resource        `json:"resources"`
	Clients              map[string]map[string]Client          `json:"clients"`
	ClientAudits         map[string][]ClientAudit              `json:"clientAudits"`
	InputConfigs         map[string]map[string]InputConfig     `json:"inputConfigs"`
	InputConfigAudits    map[string][]InputConfigAudit         `json:"inputConfigAudits"`
	LegacyClients        map[string]map[string]Client          `json:"apiClients,omitempty"`
	LegacyClientAudits   map[string][]ClientAudit              `json:"apiClientAudits,omitempty"`
	ReleaseCatalog       catalog.Snapshot                      `json:"releaseCatalog"`
	WebhookSubscriptions map[string]WebhookSubscriptionRecord  `json:"webhookSubscriptions"`
	ControlPlaneEvents   map[string]controlevent.Envelope      `json:"controlPlaneEvents"`
	WebhookDeliveries    map[string]webhook.Delivery           `json:"webhookDeliveries"`
	WebhookAudits        map[string][]webhook.Audit            `json:"webhookAudits"`
	Workers              map[string]WorkerRecord               `json:"workers,omitempty"`
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
	ListClients(ctx context.Context, workspaceID string) ([]Client, error)
	GetClient(ctx context.Context, workspaceID string, id string) (Client, error)
	GetClientByExternalKey(ctx context.Context, workspaceID string, externalKey string) (Client, error)
	CreateClient(ctx context.Context, workspaceID string, name string, externalKey string, actor string) (Client, error)
	UpdateClient(ctx context.Context, workspaceID string, id string, name string, externalKey string, actor string) (Client, error)
	DeleteClient(ctx context.Context, workspaceID string, id string, actor string) error
	ListClientAudit(ctx context.Context, workspaceID string, id string) ([]ClientAudit, error)
	ListInputConfigsForApp(ctx context.Context, workspaceID string, appKey string) ([]InputConfig, error)
	ListInputConfigsForClient(ctx context.Context, workspaceID string, clientID string) ([]InputConfig, error)
	SetInputConfig(ctx context.Context, config InputConfig, actor string) (InputConfig, error)
	DeleteInputConfig(ctx context.Context, workspaceID string, appKey string, actionKey string, clientID string, actor string) error
	ListInputConfigAudit(ctx context.Context, workspaceID string, appKey string, clientID string) ([]InputConfigAudit, error)
	ResolveInput(ctx context.Context, workspaceID string, appKey string, actionKey string, clientID string, request json.RawMessage) (json.RawMessage, error)
	DecryptInput(ctx context.Context, workspaceID string, input json.RawMessage) (json.RawMessage, error)
	ClaimJob(ctx context.Context, workerID string, leaseTTL time.Duration) (Job, Lease, error)
	ClaimJobForWorker(ctx context.Context, workerID string, tags []string, labels []string, leaseTTL time.Duration) (Job, Lease, error)
	RegisterWorker(ctx context.Context, record WorkerRecord) error
	HeartbeatWorker(ctx context.Context, workerID string) error
	DeregisterWorker(ctx context.Context, workerID string) error
	ListWorkers(ctx context.Context) ([]WorkerRecord, error)
	HeartbeatJob(ctx context.Context, lease Lease, leaseTTL time.Duration) (HeartbeatResult, error)
	CompleteJobSucceeded(ctx context.Context, lease Lease, result contract.JobResult) error
	CompleteJobFailed(ctx context.Context, lease Lease, result contract.JobResult) error
	CompleteJobWaitingHuman(ctx context.Context, lease Lease, result contract.JobResult, task HumanTask) error
	ResumeHumanTask(ctx context.Context, taskID string, resumeInput json.RawMessage) (Run, Job, error)
	ResumeRun(ctx context.Context, runID string, resumeInput json.RawMessage) (Run, Job, error)
	CancelJob(ctx context.Context, workspaceID string, jobID string, by string, reason string) (CancelResult, error)
	PruneSettledJobs(ctx context.Context, successOlderThan time.Time, failureOlderThan time.Time) (int64, error)
	ExpireStuckJobs(ctx context.Context, stuckBefore time.Time) (int64, error)
	CancelRun(ctx context.Context, runID string, reason string) (Run, error)
	RetryRun(ctx context.Context, runID string) (Run, Job, error)
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
		Deployment:     contract.PinExecutionDeployment(deployment, action),
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
	inputSchema := cloneRaw(actionSpec.InputSchemaBody)
	outputSchema := cloneRaw(actionSpec.OutputSchemaBody)
	actionSpec.InputSchemaBody = nil
	actionSpec.OutputSchemaBody = nil
	actionSpec.OperatorSettingsSchemaBody = nil
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
			Workspace:             run.Deployment.SourceWorkspace(),
			GitSourceID:           run.Deployment.SourceGitSourceID(),
			Commit:                run.Deployment.Commit,
			App:                   run.App,
			Action:                run.Action,
			Version:               run.Deployment.Version,
			Tag:                   contract.EffectiveRouteTagForAction(run.Deployment, actionSpec),
			DeploymentTag:         run.Deployment.Tag,
			DeploymentTagOverride: cloneStringPointer(run.Deployment.TagOverride),
			Entrypoint:            run.Deployment.Entrypoint,
			Runtime:               run.Deployment.Runtime,
			ScriptLang:            run.Deployment.ScriptLang,
			TimeoutS:              run.Deployment.TimeoutS,
			MaxConcurrent:         cloneInt32Pointer(run.Deployment.MaxConcurrent),
			RequiredCapabilities:  append([]string(nil), run.Deployment.RequiredCapabilities...),
			RequiredLabels:        contract.EffectiveRequiredLabels(run.Deployment, actionSpec),
			DeploymentID:          cloneStringPointer(run.Deployment.DeploymentID),
			BundleDigest:          run.Deployment.BundleDigest,
			BundleURI:             run.Deployment.BundleURI,
			ObjectURI:             run.Deployment.ObjectURI,
			TriggerKind:           run.Adapter,
			ActionSpec:            actionSpec,
			InputSchema:           inputSchema,
			OutputSchema:          outputSchema,
			Input:                 cloneRaw(input),
			CorrelationID:         run.CorrelationID,
			Env:                   append([]string(nil), run.Env...),
			CreatedBy:             actorCreatedBy(run),
			PermissionedAs:        actorPermissionedAs(run),
			ClientID:              run.ClientID,
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
	deployment := contract.Deployment{}
	if p.Deployment != nil {
		deployment = *p.Deployment
	}
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
	if deployment.Version == "" {
		deployment.Version = p.Version
	}
	if deployment.Tag == "" {
		deployment.Tag = p.DeploymentTag
	}
	if deployment.TagOverride == nil {
		deployment.TagOverride = cloneStringPointer(p.DeploymentTagOverride)
	}
	if deployment.Entrypoint == "" {
		deployment.Entrypoint = p.Entrypoint
	}
	if deployment.Runtime == "" {
		deployment.Runtime = p.Runtime
	}
	if deployment.ScriptLang == "" {
		deployment.ScriptLang = p.ScriptLang
	}
	if deployment.TimeoutS == 0 {
		deployment.TimeoutS = p.TimeoutS
	}
	if deployment.MaxConcurrent == nil {
		deployment.MaxConcurrent = cloneInt32Pointer(p.MaxConcurrent)
	}
	if len(deployment.RequiredCapabilities) == 0 {
		deployment.RequiredCapabilities = append([]string(nil), p.RequiredCapabilities...)
	}
	if len(deployment.RequiredLabels) == 0 {
		deployment.RequiredLabels = append([]string(nil), p.RequiredLabels...)
	}
	if deployment.DeploymentID == nil {
		deployment.DeploymentID = cloneStringPointer(p.DeploymentID)
	}
	if deployment.BundleDigest == "" {
		deployment.BundleDigest = p.BundleDigest
	}
	if deployment.BundleURI == "" {
		deployment.BundleURI = p.BundleURI
	}
	if deployment.ObjectURI == "" {
		deployment.ObjectURI = p.ObjectURI
	}
	if deployment.Actions == nil && p.Action != "" {
		deployment.Actions = map[string]contract.Action{p.Action: p.ActionSpec}
	}
	return deployment
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneInt32Pointer(value *int32) *int32 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func nonZeroTime(value time.Time, fallback time.Time) time.Time {
	if value.IsZero() {
		return fallback
	}
	return value
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
