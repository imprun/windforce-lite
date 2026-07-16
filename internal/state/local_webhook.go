package state

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/imprun/windforce-lite/internal/contract"
	"github.com/imprun/windforce-lite/internal/webhook"
)

var _ webhook.Store = (*LocalStore)(nil)

func (s *LocalStore) ListSubscriptions(ctx context.Context, workspaceID string) ([]webhook.Subscription, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return nil, err
	}
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	result := make([]webhook.Subscription, 0)
	for _, record := range snapshot.WebhookSubscriptions {
		if contract.NormalizeWorkspace(record.WorkspaceID) != workspaceID || record.DeletedAt != nil {
			continue
		}
		subscription, err := s.localSubscription(ctx, record)
		if err != nil {
			return nil, err
		}
		result = append(result, subscription)
	}
	sort.Slice(result, func(i int, j int) bool {
		if result[i].Name == result[j].Name {
			return result[i].ID < result[j].ID
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func (s *LocalStore) GetSubscription(ctx context.Context, workspaceID string, subscriptionID string) (webhook.Subscription, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return webhook.Subscription{}, err
	}
	record, ok := snapshot.WebhookSubscriptions[webhookSubscriptionKey(workspaceID, subscriptionID)]
	if !ok || record.DeletedAt != nil {
		return webhook.Subscription{}, webhook.ErrNotFound
	}
	return s.localSubscription(ctx, record)
}

func (s *LocalStore) CreateSubscription(ctx context.Context, subscription webhook.Subscription) (webhook.Subscription, error) {
	prepared, err := prepareNewSubscription(subscription, time.Now().UTC())
	if err != nil {
		return webhook.Subscription{}, err
	}
	endpoint, secret, err := s.encryptLocalSubscription(ctx, prepared)
	if err != nil {
		return webhook.Subscription{}, err
	}
	err = s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		key := webhookSubscriptionKey(prepared.WorkspaceID, prepared.ID)
		if _, exists := snapshot.WebhookSubscriptions[key]; exists {
			return webhook.ErrConflict
		}
		for _, existing := range snapshot.WebhookSubscriptions {
			if existing.DeletedAt == nil && contract.NormalizeWorkspace(existing.WorkspaceID) == prepared.WorkspaceID && existing.Name == prepared.Name {
				return webhook.ErrConflict
			}
		}
		snapshot.WebhookSubscriptions[key] = subscriptionRecord(prepared, endpoint, secret)
		return nil
	})
	return prepared, err
}

func (s *LocalStore) UpdateSubscription(ctx context.Context, update webhook.Subscription) (webhook.Subscription, error) {
	var prepared webhook.Subscription
	err := s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		workspaceID := contract.NormalizeWorkspace(update.WorkspaceID)
		key := webhookSubscriptionKey(workspaceID, update.ID)
		current, ok := snapshot.WebhookSubscriptions[key]
		if !ok || current.DeletedAt != nil {
			return webhook.ErrNotFound
		}
		existing, err := s.localSubscription(ctx, current)
		if err != nil {
			return err
		}
		prepared, err = prepareUpdatedSubscription(existing, update, now)
		if err != nil {
			return err
		}
		for candidateKey, candidate := range snapshot.WebhookSubscriptions {
			if candidateKey != key && candidate.DeletedAt == nil && contract.NormalizeWorkspace(candidate.WorkspaceID) == prepared.WorkspaceID && candidate.Name == prepared.Name {
				return webhook.ErrConflict
			}
		}
		endpoint, secret, err := s.encryptLocalSubscription(ctx, prepared)
		if err != nil {
			return err
		}
		snapshot.WebhookSubscriptions[key] = subscriptionRecord(prepared, endpoint, secret)
		return nil
	})
	return prepared, err
}

func (s *LocalStore) DeleteSubscription(ctx context.Context, workspaceID string, subscriptionID string, actor string) error {
	return s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		key := webhookSubscriptionKey(workspaceID, subscriptionID)
		record, ok := snapshot.WebhookSubscriptions[key]
		if !ok || record.DeletedAt != nil {
			return webhook.ErrNotFound
		}
		record.Enabled = false
		record.UpdatedBy = firstNonEmpty(strings.TrimSpace(actor), "system")
		record.UpdatedAt = now
		record.DeletedAt = cloneTime(&now)
		snapshot.WebhookSubscriptions[key] = record
		for deliveryID, delivery := range snapshot.WebhookDeliveries {
			if contract.NormalizeWorkspace(delivery.WorkspaceID) != contract.NormalizeWorkspace(workspaceID) || delivery.SubscriptionID != subscriptionID || (delivery.State != webhook.DeliveryPending && delivery.State != webhook.DeliveryRetrying) {
				continue
			}
			delivery.State = webhook.DeliveryCanceled
			delivery.UpdatedAt = now
			delivery.CompletedAt = cloneTime(&now)
			snapshot.WebhookDeliveries[deliveryID] = delivery
		}
		return nil
	})
}

func (s *LocalStore) ClaimDelivery(ctx context.Context, workerID string, leaseTTL time.Duration) (*webhook.ClaimedDelivery, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return nil, webhook.ErrInvalidLease
	}
	if leaseTTL <= 0 {
		leaseTTL = 30 * time.Second
	}
	var claimed *webhook.ClaimedDelivery
	err := s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		var selected webhook.Delivery
		var selectedRecord WebhookSubscriptionRecord
		found := false
		for _, delivery := range snapshot.WebhookDeliveries {
			record, ok := snapshot.WebhookSubscriptions[webhookSubscriptionKey(delivery.WorkspaceID, delivery.SubscriptionID)]
			if !ok || !deliveryEligible(delivery, record, now) {
				continue
			}
			if !found || delivery.NextAttemptAt.Before(selected.NextAttemptAt) || (delivery.NextAttemptAt.Equal(selected.NextAttemptAt) && delivery.CreatedAt.Before(selected.CreatedAt)) {
				selected = delivery
				selectedRecord = record
				found = true
			}
		}
		if !found {
			return webhook.ErrNoPendingDelivery
		}
		subscription, err := s.localSubscription(ctx, selectedRecord)
		if err != nil {
			return err
		}
		event, ok := snapshot.ControlPlaneEvents[selected.EventID]
		if !ok {
			return webhook.ErrNotFound
		}
		expiresAt := now.Add(leaseTTL)
		selected.State = webhook.DeliveryDelivering
		selected.Attempt++
		selected.LeaseOwner = &workerID
		selected.LeaseExpiresAt = &expiresAt
		selected.UpdatedAt = now
		snapshot.WebhookDeliveries[selected.ID] = selected
		lease := webhook.DeliveryLease{DeliveryID: selected.ID, WorkerID: workerID, Attempt: selected.Attempt, ExpiresAt: expiresAt}
		claimed = &webhook.ClaimedDelivery{Delivery: selected, Event: event, Subscription: subscription, Lease: lease}
		return nil
	})
	return claimed, err
}

func (s *LocalStore) CompleteDelivery(ctx context.Context, lease webhook.DeliveryLease, result webhook.DeliveryResult) error {
	if err := webhook.ValidateDeliveryResult(result); err != nil {
		return err
	}
	return s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		delivery, ok := snapshot.WebhookDeliveries[lease.DeliveryID]
		if !ok {
			return webhook.ErrNotFound
		}
		if err := validateDeliveryLease(delivery, lease); err != nil {
			return err
		}
		effectiveState := result.State
		completedAt := cloneTime(&result.CompletedAt)
		record, exists := snapshot.WebhookSubscriptions[webhookSubscriptionKey(delivery.WorkspaceID, delivery.SubscriptionID)]
		if !exists {
			return webhook.ErrNotFound
		}
		if record.DeletedAt != nil && effectiveState == webhook.DeliveryRetrying {
			effectiveState = webhook.DeliveryCanceled
			completedAt = cloneTime(&now)
		}
		delivery.State = effectiveState
		delivery.NextAttemptAt = result.NextAttemptAt
		delivery.ResponseStatus = result.ResponseStatus
		delivery.LatencyMillis = result.LatencyMillis
		delivery.ErrorSummary = result.ErrorSummary
		delivery.LeaseOwner = nil
		delivery.LeaseExpiresAt = nil
		delivery.UpdatedAt = now
		if effectiveState == webhook.DeliveryRetrying {
			delivery.CompletedAt = nil
		} else {
			delivery.CompletedAt = completedAt
		}
		snapshot.WebhookDeliveries[delivery.ID] = delivery
		return nil
	})
}

func (s *LocalStore) RetryDelivery(ctx context.Context, workspaceID string, deliveryID string, actor string) error {
	return s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		delivery, ok := snapshot.WebhookDeliveries[deliveryID]
		if !ok || contract.NormalizeWorkspace(delivery.WorkspaceID) != contract.NormalizeWorkspace(workspaceID) {
			return webhook.ErrNotFound
		}
		if delivery.State != webhook.DeliveryFailed {
			return webhook.ErrConflict
		}
		delivery.State = webhook.DeliveryRetrying
		delivery.NextAttemptAt = now
		delivery.LeaseOwner = nil
		delivery.LeaseExpiresAt = nil
		delivery.CompletedAt = nil
		delivery.UpdatedAt = now
		snapshot.WebhookDeliveries[delivery.ID] = delivery
		return nil
	})
}

func (s *LocalStore) localSubscription(ctx context.Context, record WebhookSubscriptionRecord) (webhook.Subscription, error) {
	config := inputCryptoConfig{SecretKey: s.SecretKey, SecretKeyPrevious: s.SecretKeyPrevious}
	endpoint, err := decryptWebhookString(ctx, nil, config, record.WorkspaceID, record.EndpointEncrypted, "webhook endpoint")
	if err != nil {
		return webhook.Subscription{}, err
	}
	secret, err := decryptWebhookString(ctx, nil, config, record.WorkspaceID, record.SigningSecretEncrypted, "webhook signing secret")
	if err != nil {
		return webhook.Subscription{}, err
	}
	return subscriptionFromRecord(record, endpoint, secret), nil
}

func (s *LocalStore) encryptLocalSubscription(ctx context.Context, subscription webhook.Subscription) (json.RawMessage, json.RawMessage, error) {
	config := inputCryptoConfig{SecretKey: s.SecretKey, SecretKeyPrevious: s.SecretKeyPrevious}
	endpoint, err := encryptWebhookString(ctx, nil, config, subscription.WorkspaceID, subscription.Endpoint, "webhook endpoint")
	if err != nil {
		return nil, nil, err
	}
	secret, err := encryptWebhookString(ctx, nil, config, subscription.WorkspaceID, subscription.SigningSecret, "webhook signing secret")
	if err != nil {
		return nil, nil, err
	}
	return endpoint, secret, nil
}
