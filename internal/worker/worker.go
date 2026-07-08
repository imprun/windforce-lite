package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/imprun/windforce-lite/internal/contract"
	actionruntime "github.com/imprun/windforce-lite/internal/runtime"
	"github.com/imprun/windforce-lite/internal/state"
)

type Processor struct {
	Store    state.Store
	Runner   actionruntime.Runner
	WorkerID string
	LeaseTTL time.Duration
}

func (p *Processor) ProcessOne(ctx context.Context) (bool, error) {
	if p.Store == nil {
		return false, errors.New("state store is required")
	}
	workerID := p.WorkerID
	if workerID == "" {
		workerID = state.NewID("worker")
	}
	job, lease, err := p.Store.ClaimJob(ctx, workerID, p.LeaseTTL)
	if errors.Is(err, state.ErrNoQueuedJob) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	result, runErr := p.Runner.Run(ctx, actionruntime.RunRequest{
		Deployment: job.Payload.PinnedDeployment(),
		Action:     job.Payload.Action,
		Input:      job.Payload.Input,
		Env:        job.Payload.Env,
	})
	result.JobID = job.ID
	if runErr != nil {
		if result.Error == "" {
			result.Error = runErr.Error()
		}
		return true, p.Store.CompleteJobFailed(ctx, lease, result)
	}
	if result.ExitCode != 0 {
		return true, p.Store.CompleteJobFailed(ctx, lease, result)
	}

	task, ok, err := HumanTaskFromOutput(job.RunID, result.Output)
	if err != nil {
		result.Error = err.Error()
		return true, p.Store.CompleteJobFailed(ctx, lease, result)
	}
	if ok {
		return true, p.Store.CompleteJobWaitingHuman(ctx, lease, result, task)
	}
	return true, p.Store.CompleteJobSucceeded(ctx, lease, result)
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
