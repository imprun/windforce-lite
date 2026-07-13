package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/imprun/windforce-lite/internal/contract"
	actionruntime "github.com/imprun/windforce-lite/internal/runtime"
	"github.com/imprun/windforce-lite/internal/state"
)

type Processor struct {
	Store             state.Store
	Runner            actionruntime.Runner
	WorkerID          string
	Group             string
	Tags              []string
	EgressProxyAddr   string
	LeaseTTL          time.Duration
	HeartbeatInterval time.Duration
	LogFlushInterval  time.Duration
	LogCapBytes       int
}

func (p *Processor) ProcessOne(ctx context.Context) (bool, error) {
	if p.Store == nil {
		return false, errors.New("state store is required")
	}
	workerID := p.WorkerID
	if workerID == "" {
		workerID = state.NewID("worker")
	}
	job, lease, err := p.Store.ClaimJobForTags(ctx, workerID, p.Tags, p.LeaseTTL)
	if errors.Is(err, state.ErrNoQueuedJob) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	startedAt := time.Now()
	outcome := "running"
	jobError := ""
	log.Printf("worker job started job=%s app=%s action=%s", job.ID, job.Payload.App, job.Payload.Action)
	defer func() {
		log.Printf("worker job finished job=%s app=%s action=%s outcome=%s duration=%s error=%q",
			job.ID, job.Payload.App, job.Payload.Action, outcome, time.Since(startedAt).Round(time.Millisecond), jobError)
	}()

	workspaceID := job.Payload.Workspace
	if workspaceID == "" {
		workspaceID = job.Payload.PinnedDeployment().SourceWorkspace()
	}
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopHeartbeat := p.startHeartbeat(lease, cancel)
	defer stopHeartbeat()
	input, err := p.Store.DecryptInput(runCtx, workspaceID, job.Payload.Input)
	if err != nil {
		outcome = "failed"
		jobError = "could not decrypt job input"
		result := contract.JobResult{
			JobID:    job.ID,
			App:      job.Payload.App,
			Action:   job.Payload.Action,
			Output:   actionruntime.ErrorResult("InputDecryptError", "could not decrypt job input"),
			ExitCode: -1,
			Error:    "could not decrypt job input",
		}
		return completeProcessed(p.Store.CompleteJobFailed(ctx, lease, result))
	}
	result, runErr := p.Runner.Run(runCtx, actionruntime.RunRequest{
		JobID:           job.ID,
		WorkspaceID:     workspaceID,
		Deployment:      job.Payload.PinnedDeployment(),
		Action:          job.Payload.Action,
		Input:           input,
		TriggerKind:     job.Payload.TriggerKind,
		TriggerHeaders:  job.Payload.TriggerHeaders,
		Tag:             job.Payload.Tag,
		CreatedBy:       job.Payload.CreatedBy,
		PermissionedAs:  job.Payload.PermissionedAs,
		WorkerGroup:     p.Group,
		EgressProxyAddr: p.EgressProxyAddr,
		LogSink: func(chunk []byte) {
			_ = p.Store.AppendLogs(context.Background(), job.ID, workspaceID, string(chunk))
		},
		LogFlushInterval: p.LogFlushInterval,
		LogCapBytes:      p.LogCapBytes,
	})
	result.JobID = job.ID
	result.Stdout = ""
	result.Stderr = ""
	if runErr != nil {
		if result.Error == "" {
			result.Error = runErr.Error()
		}
		if len(result.Output) == 0 {
			result.Output = namedErrorResult(runErr, result.Error)
		}
		outcome = "failed"
		jobError = result.Error
		return completeProcessed(p.Store.CompleteJobFailed(ctx, lease, result))
	}
	if result.ExitCode != 0 {
		if result.Error == "" {
			result.Error = fmt.Sprintf("action exited with code %d", result.ExitCode)
		}
		if len(result.Output) == 0 {
			result.Output = actionruntime.ErrorResult("ExecutionError", result.Error)
		}
		outcome = "failed"
		jobError = result.Error
		return completeProcessed(p.Store.CompleteJobFailed(ctx, lease, result))
	}

	task, ok, err := HumanTaskFromOutput(job.RunID, result.Output)
	if err != nil {
		result.Error = err.Error()
		outcome = "failed"
		jobError = result.Error
		return completeProcessed(p.Store.CompleteJobFailed(ctx, lease, result))
	}
	if ok {
		outcome = "waiting_human"
		return completeProcessed(p.Store.CompleteJobWaitingHuman(ctx, lease, result, task))
	}
	outcome = "succeeded"
	return completeProcessed(p.Store.CompleteJobSucceeded(ctx, lease, result))
}

func (p *Processor) startHeartbeat(lease state.Lease, cancel context.CancelFunc) func() {
	interval := p.effectiveHeartbeatInterval()
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				heartbeat, err := p.Store.HeartbeatJob(context.Background(), lease, p.LeaseTTL)
				if err != nil {
					log.Printf("worker heartbeat job %s: %v", lease.JobID, err)
					continue
				}
				if !heartbeat.StillOwned {
					cancel()
					return
				}
				if heartbeat.CanceledBy != nil {
					cancel()
					return
				}
			}
		}
	}()
	return func() {
		close(done)
	}
}

func (p *Processor) effectiveHeartbeatInterval() time.Duration {
	if p.HeartbeatInterval > 0 {
		return p.HeartbeatInterval
	}
	if p.LeaseTTL > 0 {
		interval := p.LeaseTTL / 3
		if interval < 10*time.Millisecond {
			return 10 * time.Millisecond
		}
		if interval > 10*time.Second {
			return 10 * time.Second
		}
		return interval
	}
	return 10 * time.Second
}

func completeProcessed(err error) (bool, error) {
	if errors.Is(err, state.ErrInvalidLease) {
		return true, nil
	}
	return true, err
}

func namedErrorResult(err error, message string) json.RawMessage {
	name := "ExecutionError"
	if runtimeName, ok := actionruntime.ErrorName(err); ok {
		name = runtimeName
	}
	return actionruntime.ErrorResult(name, message)
}

func (p *Processor) RunLoop(ctx context.Context, pollInterval time.Duration) error {
	if pollInterval <= 0 {
		pollInterval = 500 * time.Millisecond
	}
	for {
		processed, err := p.ProcessOne(ctx)
		if err != nil {
			return err
		}
		if processed {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

func HumanTaskFromOutput(runID string, output json.RawMessage) (state.HumanTask, bool, error) {
	if len(output) == 0 {
		return state.HumanTask{}, false, nil
	}
	var envelope struct {
		Windforce *struct {
			Type        string          `json:"type"`
			Title       string          `json:"title"`
			Description string          `json:"description"`
			Fields      json.RawMessage `json:"fields"`
			TimeoutMs   int64           `json:"timeoutMs"`
		} `json:"$windforce"`
	}
	if err := json.Unmarshal(output, &envelope); err != nil {
		return state.HumanTask{}, false, err
	}
	if envelope.Windforce == nil {
		return state.HumanTask{}, false, nil
	}
	if envelope.Windforce.Type != "human_task" {
		return state.HumanTask{}, false, fmt.Errorf("unsupported $windforce type %q", envelope.Windforce.Type)
	}
	title := envelope.Windforce.Title
	if title == "" {
		title = "Human task"
	}
	var expiresAt *time.Time
	if envelope.Windforce.TimeoutMs > 0 {
		value := time.Now().UTC().Add(time.Duration(envelope.Windforce.TimeoutMs) * time.Millisecond)
		expiresAt = &value
	}
	return state.HumanTask{
		ID:          state.NewID("human"),
		RunID:       runID,
		State:       state.HumanTaskPending,
		Title:       title,
		Description: envelope.Windforce.Description,
		Schema:      fieldsSchema(envelope.Windforce.Fields),
		ExpiresAt:   expiresAt,
	}, true, nil
}

func fieldsSchema(fields json.RawMessage) json.RawMessage {
	if len(fields) == 0 {
		return nil
	}
	data, err := json.Marshal(map[string]json.RawMessage{"fields": fields})
	if err != nil {
		return nil
	}
	return data
}

func ResultError(result contract.JobResult) string {
	if result.Error != "" {
		return result.Error
	}
	if result.ExitCode != 0 {
		return fmt.Sprintf("action exited with code %d", result.ExitCode)
	}
	return ""
}
