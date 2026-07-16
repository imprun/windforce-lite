package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/imprun/windforce-lite/internal/webhook"
)

type webhookRetentionPolicy struct {
	Success    time.Duration
	Failure    time.Duration
	Interval   time.Duration
	BatchSize  int
	TimeBudget time.Duration
}

func (policy webhookRetentionPolicy) Enabled() bool {
	return policy.Success > 0 || policy.Failure > 0
}

func webhookRetentionFromFlags(flags webhookDispatcherFlags) (webhookRetentionPolicy, error) {
	policy := webhookRetentionPolicy{
		Success:    *flags.successRetention,
		Failure:    *flags.failureRetention,
		Interval:   *flags.retentionInterval,
		BatchSize:  *flags.retentionBatchSize,
		TimeBudget: *flags.retentionTimeBudget,
	}
	if policy.Success < 0 || policy.Failure < 0 {
		return webhookRetentionPolicy{}, fmt.Errorf("retention durations must be non-negative")
	}
	if policy.Interval <= 0 {
		return webhookRetentionPolicy{}, fmt.Errorf("retention interval must be positive")
	}
	if policy.BatchSize <= 0 {
		return webhookRetentionPolicy{}, fmt.Errorf("retention batch size must be positive")
	}
	if policy.TimeBudget <= 0 {
		return webhookRetentionPolicy{}, fmt.Errorf("retention time budget must be positive")
	}
	return policy, nil
}

func runWebhookRetentionLoop(ctx context.Context, store webhook.Store, policy webhookRetentionPolicy) {
	runWebhookRetentionCycle(ctx, store, policy, time.Now().UTC())
	ticker := time.NewTicker(policy.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			runWebhookRetentionCycle(ctx, store, policy, now.UTC())
		}
	}
}

func runWebhookRetentionCycle(ctx context.Context, store webhook.Store, policy webhookRetentionPolicy, now time.Time) webhook.RetentionResult {
	cycleCtx, cancel := context.WithTimeout(ctx, policy.TimeBudget)
	defer cancel()
	cutoff := func(ttl time.Duration) time.Time {
		if ttl <= 0 {
			return time.Time{}
		}
		return now.Add(-ttl)
	}
	retention := webhook.RetentionPolicy{
		SucceededBefore:    cutoff(policy.Success),
		CanceledBefore:     cutoff(policy.Success),
		FailedBefore:       cutoff(policy.Failure),
		SubscriptionBefore: cutoff(policy.Success),
		BatchSize:          policy.BatchSize,
	}
	var total webhook.RetentionResult
	for {
		result, err := store.PruneWebhookData(cycleCtx, retention)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				slog.Warn("webhook retention time budget exhausted", "deliveries", total.Deliveries, "events", total.Events, "subscriptions", total.Subscriptions)
			} else if !errors.Is(err, context.Canceled) {
				slog.Error("webhook retention failed", "error", err)
			}
			return total
		}
		total.Deliveries += result.Deliveries
		total.Events += result.Events
		total.Subscriptions += result.Subscriptions
		if result.Empty() {
			break
		}
	}
	if !total.Empty() {
		slog.Info("webhook retention completed", "deliveries", total.Deliveries, "events", total.Events, "subscriptions", total.Subscriptions)
	}
	return total
}
