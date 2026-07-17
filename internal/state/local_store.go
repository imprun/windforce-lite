package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/imprun/windforce-core/internal/catalog"
	"github.com/imprun/windforce-core/internal/contract"
	controlevent "github.com/imprun/windforce-core/internal/event"
	"github.com/imprun/windforce-core/internal/webhook"
)

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
	return s.ClaimJobForWorker(ctx, workerID, nil, nil, leaseTTL)
}

func (s *LocalStore) ClaimJobForWorker(ctx context.Context, workerID string, tags []string, labels []string, leaseTTL time.Duration) (Job, Lease, error) {
	if workerID == "" {
		workerID = NewID("worker")
	}
	if leaseTTL <= 0 {
		leaseTTL = defaultLeaseTime
	}
	allowedTags := normalizeClaimTags(tags)
	offeredLabels := normalizeClaimTags(labels)
	var claimed Job
	var lease Lease
	err := s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		if err := s.requeueExpiredJobs(ctx, snapshot, now); err != nil {
			return err
		}

		ids := make([]string, 0, len(snapshot.Jobs))
		for id, job := range snapshot.Jobs {
			if job.State == JobQueued && claimAllowed(job, allowedTags, offeredLabels) {
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
	catalog.NormalizeSnapshot(&snapshot.ReleaseCatalog)
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
	if snapshot.Clients == nil {
		snapshot.Clients = snapshot.LegacyClients
		if snapshot.Clients == nil {
			snapshot.Clients = map[string]map[string]Client{}
		}
	}
	if snapshot.ClientAudits == nil {
		snapshot.ClientAudits = snapshot.LegacyClientAudits
		if snapshot.ClientAudits == nil {
			snapshot.ClientAudits = map[string][]ClientAudit{}
		}
	}
	if snapshot.InputConfigs == nil {
		snapshot.InputConfigs = map[string]map[string]InputConfig{}
	}
	if snapshot.InputConfigAudits == nil {
		snapshot.InputConfigAudits = map[string][]InputConfigAudit{}
	}
	if snapshot.WebhookSubscriptions == nil {
		snapshot.WebhookSubscriptions = map[string]WebhookSubscriptionRecord{}
	}
	if snapshot.ControlPlaneEvents == nil {
		snapshot.ControlPlaneEvents = map[string]controlevent.Envelope{}
	}
	if snapshot.WebhookDeliveries == nil {
		snapshot.WebhookDeliveries = map[string]webhook.Delivery{}
	}
	if snapshot.WebhookAudits == nil {
		snapshot.WebhookAudits = map[string][]webhook.Audit{}
	}
	snapshot.LegacyClients = nil
	snapshot.LegacyClientAudits = nil
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

func staleLock(path string, maxAge time.Duration) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) > maxAge
}

// PruneSettledJobs removes settled runs together with their jobs, logs,
// events, and human tasks: succeeded runs older than successOlderThan, and
// failed/canceled/expired runs older than failureOlderThan. Queued, running,
// and waiting-human runs are never touched. It returns the number of jobs
// removed.
func (s *LocalStore) PruneSettledJobs(ctx context.Context, successOlderThan time.Time, failureOlderThan time.Time) (int64, error) {
	var pruned int64
	err := s.update(ctx, func(snapshot *Snapshot, _ time.Time) error {
		expiredRuns := map[string]bool{}
		for id, run := range snapshot.Runs {
			if TerminalRunState(run.State) && run.UpdatedAt.Before(retentionCutoff(run.State, successOlderThan, failureOlderThan)) {
				expiredRuns[id] = true
			}
		}
		if len(expiredRuns) == 0 {
			return nil
		}
		for id, job := range snapshot.Jobs {
			if expiredRuns[job.RunID] {
				delete(snapshot.Jobs, id)
				delete(snapshot.JobLogs, id)
				pruned++
			}
		}
		for id, task := range snapshot.HumanTasks {
			if expiredRuns[task.RunID] {
				delete(snapshot.HumanTasks, id)
			}
		}
		events := snapshot.Events[:0]
		for _, event := range snapshot.Events {
			if !expiredRuns[event.RunID] {
				events = append(events, event)
			}
		}
		snapshot.Events = events
		for id := range expiredRuns {
			delete(snapshot.Runs, id)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return pruned, nil
}

// ExpireStuckJobs transitions runs that have been sitting in a non-terminal,
// non-waiting state (queued, running, resuming) without progress since
// stuckBefore into the expired/failure family, so the retention pruner can
// eventually reclaim them. Actively heartbeating jobs refresh UpdatedAt and
// are never considered stuck. It returns the number of runs expired.
func (s *LocalStore) ExpireStuckJobs(ctx context.Context, stuckBefore time.Time) (int64, error) {
	var expired int64
	err := s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		for id, run := range snapshot.Runs {
			if run.State != RunQueued && run.State != RunRunning && run.State != RunResuming {
				continue
			}
			latest := run.UpdatedAt
			for _, job := range snapshot.Jobs {
				if job.RunID == id && job.UpdatedAt.After(latest) {
					latest = job.UpdatedAt
				}
			}
			if !latest.Before(stuckBefore) {
				continue
			}
			run.State = RunExpired
			run.UpdatedAt = now
			snapshot.Runs[id] = run
			for jobID, job := range snapshot.Jobs {
				if job.RunID != id || job.State == JobSucceeded || job.State == JobFailed {
					continue
				}
				job.State = JobFailed
				reason := "expired by retention policy: no progress before " + stuckBefore.UTC().Format(time.RFC3339)
				job.CanceledReason = &reason
				job.UpdatedAt = now
				snapshot.Jobs[jobID] = job
			}
			expired++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return expired, nil
}

func (s *LocalStore) RegisterWorker(ctx context.Context, record WorkerRecord) error {
	return s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		if snapshot.Workers == nil {
			snapshot.Workers = map[string]WorkerRecord{}
		}
		if record.Slots <= 0 {
			record.Slots = 1
		}
		if record.StartedAt.IsZero() {
			record.StartedAt = now
		}
		record.LastHeartbeatAt = now
		snapshot.Workers[record.ID] = record
		return nil
	})
}

func (s *LocalStore) HeartbeatWorker(ctx context.Context, workerID string) error {
	return s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		record, ok := snapshot.Workers[workerID]
		if !ok {
			return fmt.Errorf("%w: worker %q", ErrNotFound, workerID)
		}
		record.LastHeartbeatAt = now
		snapshot.Workers[workerID] = record
		return nil
	})
}

func (s *LocalStore) DeregisterWorker(ctx context.Context, workerID string) error {
	return s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		delete(snapshot.Workers, workerID)
		return nil
	})
}

func (s *LocalStore) ListWorkers(ctx context.Context) ([]WorkerRecord, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]WorkerRecord, 0, len(snapshot.Workers))
	for _, record := range snapshot.Workers {
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
