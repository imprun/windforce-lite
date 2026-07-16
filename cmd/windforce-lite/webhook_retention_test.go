package main

import (
	"context"
	"testing"
	"time"

	"github.com/imprun/windforce-lite/internal/webhook"
)

type webhookRetentionTestStore struct {
	webhook.Store
	results  []webhook.RetentionResult
	policies []webhook.RetentionPolicy
}

func (store *webhookRetentionTestStore) PruneWebhookData(_ context.Context, policy webhook.RetentionPolicy) (webhook.RetentionResult, error) {
	store.policies = append(store.policies, policy)
	if len(store.results) == 0 {
		return webhook.RetentionResult{}, nil
	}
	result := store.results[0]
	store.results = store.results[1:]
	return result, nil
}

func TestWebhookRetentionCycleUsesStateSpecificCutoffsAndBatches(t *testing.T) {
	store := &webhookRetentionTestStore{results: []webhook.RetentionResult{
		{Deliveries: 2, Events: 1},
		{Subscriptions: 1},
		{},
	}}
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	policy := webhookRetentionPolicy{
		Success:    30 * 24 * time.Hour,
		Failure:    90 * 24 * time.Hour,
		Interval:   time.Minute,
		BatchSize:  100,
		TimeBudget: time.Second,
	}
	result := runWebhookRetentionCycle(context.Background(), store, policy, now)
	if result != (webhook.RetentionResult{Deliveries: 2, Events: 1, Subscriptions: 1}) {
		t.Fatalf("result = %#v", result)
	}
	if len(store.policies) != 3 {
		t.Fatalf("prune calls = %d", len(store.policies))
	}
	retention := store.policies[0]
	if !retention.SucceededBefore.Equal(now.Add(-policy.Success)) || !retention.CanceledBefore.Equal(now.Add(-policy.Success)) {
		t.Fatalf("success cutoffs = %#v", retention)
	}
	if !retention.FailedBefore.Equal(now.Add(-policy.Failure)) || !retention.SubscriptionBefore.Equal(now.Add(-policy.Success)) {
		t.Fatalf("failure/subscription cutoffs = %#v", retention)
	}
	if retention.BatchSize != policy.BatchSize {
		t.Fatalf("batch size = %d", retention.BatchSize)
	}
}

func TestWebhookRetentionCycleZeroTTLKeepsStateForever(t *testing.T) {
	store := &webhookRetentionTestStore{}
	policy := webhookRetentionPolicy{Interval: time.Minute, BatchSize: 100, TimeBudget: time.Second}
	runWebhookRetentionCycle(context.Background(), store, policy, time.Now().UTC())
	if len(store.policies) != 1 {
		t.Fatalf("prune calls = %d", len(store.policies))
	}
	retention := store.policies[0]
	if !retention.SucceededBefore.IsZero() || !retention.CanceledBefore.IsZero() || !retention.FailedBefore.IsZero() || !retention.SubscriptionBefore.IsZero() {
		t.Fatalf("zero TTL cutoffs = %#v", retention)
	}
}
