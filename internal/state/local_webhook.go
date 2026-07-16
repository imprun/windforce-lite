package state

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/imprun/windforce-lite/internal/contract"
	controlevent "github.com/imprun/windforce-lite/internal/event"
	"github.com/imprun/windforce-lite/internal/webhook"
)

var _ webhook.Store = (*LocalStore)(nil)

func (s *LocalStore) ListSubscriptions(ctx context.Context, workspaceID string) ([]webhook.Subscription, error) {
	return s.listSubscriptions(ctx, workspaceID, false)
}

func (s *LocalStore) ListSubscriptionsIncludingDeleted(ctx context.Context, workspaceID string) ([]webhook.Subscription, error) {
	return s.listSubscriptions(ctx, workspaceID, true)
}

func (s *LocalStore) listSubscriptions(ctx context.Context, workspaceID string, includeDeleted bool) ([]webhook.Subscription, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return nil, err
	}
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	result := make([]webhook.Subscription, 0)
	for _, record := range snapshot.WebhookSubscriptions {
		if contract.NormalizeWorkspace(record.WorkspaceID) != workspaceID || (!includeDeleted && record.DeletedAt != nil) {
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
		audit := newWebhookAudit(prepared.WorkspaceID, prepared.ID, "", "webhook_subscription_created", webhookSubscriptionAuditDetail(prepared), prepared.CreatedBy, now)
		snapshot.WebhookAudits[prepared.WorkspaceID] = append(snapshot.WebhookAudits[prepared.WorkspaceID], audit)
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
		kind := "webhook_subscription_updated"
		if existing.Enabled && !prepared.Enabled {
			kind = "webhook_subscription_disabled"
		} else if !existing.Enabled && prepared.Enabled {
			kind = "webhook_subscription_enabled"
		}
		audit := newWebhookAudit(prepared.WorkspaceID, prepared.ID, "", kind, webhookSubscriptionUpdateAuditDetail(existing, prepared), prepared.UpdatedBy, now)
		snapshot.WebhookAudits[prepared.WorkspaceID] = append(snapshot.WebhookAudits[prepared.WorkspaceID], audit)
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
		audit := newWebhookAudit(workspaceID, subscriptionID, "", "webhook_subscription_deleted", "name="+record.Name+"; subscription deleted", actor, now)
		workspaceID = contract.NormalizeWorkspace(workspaceID)
		snapshot.WebhookAudits[workspaceID] = append(snapshot.WebhookAudits[workspaceID], audit)
		return nil
	})
}

func (s *LocalStore) ListDeliveries(ctx context.Context, workspaceID string, query webhook.DeliveryListQuery) ([]webhook.DeliveryDetail, error) {
	query, err := prepareWebhookDeliveryQuery(query)
	if err != nil {
		return nil, err
	}
	snapshot, err := s.Load(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]webhook.DeliveryDetail, 0)
	for _, delivery := range snapshot.WebhookDeliveries {
		if !webhookDeliveryMatches(delivery, workspaceID, query) {
			continue
		}
		event, ok := snapshot.ControlPlaneEvents[delivery.EventID]
		if !ok {
			continue
		}
		name := ""
		if record, ok := snapshot.WebhookSubscriptions[webhookSubscriptionKey(delivery.WorkspaceID, delivery.SubscriptionID)]; ok {
			name = record.Name
		}
		result = append(result, webhook.DeliveryDetail{Delivery: delivery, Event: event, SubscriptionName: name})
	}
	sort.Slice(result, func(i int, j int) bool {
		if result[i].Delivery.CreatedAt.Equal(result[j].Delivery.CreatedAt) {
			return result[i].Delivery.ID > result[j].Delivery.ID
		}
		return result[i].Delivery.CreatedAt.After(result[j].Delivery.CreatedAt)
	})
	if len(result) > query.Limit {
		result = result[:query.Limit]
	}
	return result, nil
}

func (s *LocalStore) GetDelivery(ctx context.Context, workspaceID string, deliveryID string) (webhook.DeliveryDetail, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return webhook.DeliveryDetail{}, err
	}
	delivery, ok := snapshot.WebhookDeliveries[strings.TrimSpace(deliveryID)]
	if !ok || contract.NormalizeWorkspace(delivery.WorkspaceID) != contract.NormalizeWorkspace(workspaceID) {
		return webhook.DeliveryDetail{}, webhook.ErrNotFound
	}
	event, ok := snapshot.ControlPlaneEvents[delivery.EventID]
	if !ok {
		return webhook.DeliveryDetail{}, webhook.ErrNotFound
	}
	name := ""
	if record, ok := snapshot.WebhookSubscriptions[webhookSubscriptionKey(delivery.WorkspaceID, delivery.SubscriptionID)]; ok {
		name = record.Name
	}
	return webhook.DeliveryDetail{Delivery: delivery, Event: event, SubscriptionName: name}, nil
}

func (s *LocalStore) CreateTestDelivery(ctx context.Context, workspaceID string, subscriptionID string, actor string) (webhook.DeliveryDetail, error) {
	var detail webhook.DeliveryDetail
	err := s.update(ctx, func(snapshot *Snapshot, now time.Time) error {
		workspaceID = contract.NormalizeWorkspace(workspaceID)
		record, ok := snapshot.WebhookSubscriptions[webhookSubscriptionKey(workspaceID, subscriptionID)]
		if !ok || record.DeletedAt != nil {
			return webhook.ErrNotFound
		}
		if !record.Enabled {
			return webhook.ErrConflict
		}
		event, err := controlevent.NewWebhookTest(NewID("evt"), now, controlevent.WebhookTestData{
			Workspace: workspaceID, SubscriptionID: subscriptionID, Actor: actor,
		})
		if err != nil {
			return err
		}
		delivery := newWebhookDelivery(event, workspaceID, subscriptionID, now)
		snapshot.ControlPlaneEvents[event.ID] = event
		snapshot.WebhookDeliveries[delivery.ID] = delivery
		audit := newWebhookAudit(workspaceID, subscriptionID, delivery.ID, "webhook_test_requested", "test delivery queued", actor, now)
		snapshot.WebhookAudits[workspaceID] = append(snapshot.WebhookAudits[workspaceID], audit)
		detail = webhook.DeliveryDetail{Delivery: delivery, Event: event, SubscriptionName: record.Name}
		return nil
	})
	return detail, err
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
		record, ok := snapshot.WebhookSubscriptions[webhookSubscriptionKey(delivery.WorkspaceID, delivery.SubscriptionID)]
		if !ok || record.DeletedAt != nil || !record.Enabled {
			return webhook.ErrConflict
		}
		delivery.State = webhook.DeliveryRetrying
		delivery.NextAttemptAt = now
		delivery.LeaseOwner = nil
		delivery.LeaseExpiresAt = nil
		delivery.CompletedAt = nil
		delivery.UpdatedAt = now
		snapshot.WebhookDeliveries[delivery.ID] = delivery
		audit := newWebhookAudit(workspaceID, delivery.SubscriptionID, delivery.ID, "webhook_delivery_retried", "failed delivery queued for retry", actor, now)
		workspaceID = contract.NormalizeWorkspace(workspaceID)
		snapshot.WebhookAudits[workspaceID] = append(snapshot.WebhookAudits[workspaceID], audit)
		return nil
	})
}

func (s *LocalStore) ListAudit(ctx context.Context, workspaceID string) ([]webhook.Audit, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return nil, err
	}
	audits := append([]webhook.Audit(nil), snapshot.WebhookAudits[contract.NormalizeWorkspace(workspaceID)]...)
	sort.Slice(audits, func(i int, j int) bool {
		if audits[i].CreatedAt.Equal(audits[j].CreatedAt) {
			return audits[i].ID > audits[j].ID
		}
		return audits[i].CreatedAt.After(audits[j].CreatedAt)
	})
	return audits, nil
}

func (s *LocalStore) WebhookQueueStats(ctx context.Context) (webhook.QueueStats, error) {
	snapshot, err := s.Load(ctx)
	if err != nil {
		return webhook.QueueStats{}, err
	}
	var stats webhook.QueueStats
	for _, delivery := range snapshot.WebhookDeliveries {
		if !isActiveWebhookDelivery(delivery.State) {
			continue
		}
		stats.PendingCount++
		if stats.OldestPending == nil || delivery.CreatedAt.Before(*stats.OldestPending) {
			stats.OldestPending = cloneTime(&delivery.CreatedAt)
		}
	}
	return stats, nil
}

func (s *LocalStore) PruneWebhookData(ctx context.Context, policy webhook.RetentionPolicy) (webhook.RetentionResult, error) {
	policy.BatchSize = normalizedWebhookRetentionBatchSize(policy.BatchSize)
	var result webhook.RetentionResult
	err := s.update(ctx, func(snapshot *Snapshot, _ time.Time) error {
		remaining := policy.BatchSize
		candidates := make([]webhook.Delivery, 0)
		for _, delivery := range snapshot.WebhookDeliveries {
			if webhookDeliveryExpired(delivery, policy) {
				candidates = append(candidates, delivery)
			}
		}
		sort.Slice(candidates, func(left int, right int) bool {
			leftTime := webhookDeliveryRetentionTime(candidates[left])
			rightTime := webhookDeliveryRetentionTime(candidates[right])
			if leftTime.Equal(rightTime) {
				return candidates[left].ID < candidates[right].ID
			}
			return leftTime.Before(rightTime)
		})
		for _, delivery := range candidates {
			if remaining == 0 {
				break
			}
			delete(snapshot.WebhookDeliveries, delivery.ID)
			result.Deliveries++
			remaining--
		}

		if remaining > 0 {
			referencedEvents := make(map[string]struct{}, len(snapshot.WebhookDeliveries))
			for _, delivery := range snapshot.WebhookDeliveries {
				referencedEvents[delivery.EventID] = struct{}{}
			}
			eventIDs := make([]string, 0)
			for eventID := range snapshot.ControlPlaneEvents {
				if _, referenced := referencedEvents[eventID]; !referenced {
					eventIDs = append(eventIDs, eventID)
				}
			}
			sort.Strings(eventIDs)
			for _, eventID := range eventIDs {
				if remaining == 0 {
					break
				}
				delete(snapshot.ControlPlaneEvents, eventID)
				result.Events++
				remaining--
			}
		}

		if remaining > 0 && !policy.SubscriptionBefore.IsZero() {
			referencedSubscriptions := make(map[string]struct{}, len(snapshot.WebhookDeliveries))
			for _, delivery := range snapshot.WebhookDeliveries {
				referencedSubscriptions[webhookSubscriptionKey(delivery.WorkspaceID, delivery.SubscriptionID)] = struct{}{}
			}
			subscriptionKeys := make([]string, 0)
			for key, subscription := range snapshot.WebhookSubscriptions {
				if subscription.DeletedAt == nil || !subscription.DeletedAt.Before(policy.SubscriptionBefore) {
					continue
				}
				if _, referenced := referencedSubscriptions[key]; !referenced {
					subscriptionKeys = append(subscriptionKeys, key)
				}
			}
			sort.Strings(subscriptionKeys)
			for _, key := range subscriptionKeys {
				if remaining == 0 {
					break
				}
				delete(snapshot.WebhookSubscriptions, key)
				result.Subscriptions++
				remaining--
			}
		}
		return nil
	})
	return result, err
}

func isActiveWebhookDelivery(state webhook.DeliveryState) bool {
	return state == webhook.DeliveryPending || state == webhook.DeliveryRetrying || state == webhook.DeliveryDelivering
}

func webhookDeliveryExpired(delivery webhook.Delivery, policy webhook.RetentionPolicy) bool {
	cutoff := time.Time{}
	switch delivery.State {
	case webhook.DeliverySucceeded:
		cutoff = policy.SucceededBefore
	case webhook.DeliveryCanceled:
		cutoff = policy.CanceledBefore
	case webhook.DeliveryFailed:
		cutoff = policy.FailedBefore
	default:
		return false
	}
	return !cutoff.IsZero() && webhookDeliveryRetentionTime(delivery).Before(cutoff)
}

func webhookDeliveryRetentionTime(delivery webhook.Delivery) time.Time {
	if delivery.CompletedAt != nil {
		return *delivery.CompletedAt
	}
	return delivery.UpdatedAt
}

func normalizedWebhookRetentionBatchSize(batchSize int) int {
	if batchSize <= 0 {
		return 1000
	}
	return batchSize
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
