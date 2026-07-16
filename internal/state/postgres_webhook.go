package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/imprun/windforce-lite/internal/contract"
	controlevent "github.com/imprun/windforce-lite/internal/event"
	"github.com/imprun/windforce-lite/internal/webhook"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const webhookSubscriptionColumns = `
id, workspace_id, name, endpoint_encrypted, signing_secret_encrypted,
event_types, app_keys, enabled, created_by, updated_by, created_at, updated_at, deleted_at`

const webhookDeliveryColumns = `
id, workspace_id, event_id, subscription_id, state, attempt, next_attempt_at,
lease_owner, lease_expires_at, response_status, latency_ms, error_summary,
created_at, updated_at, completed_at`

const webhookDeliveryQualifiedColumns = `
d.id, d.workspace_id, d.event_id, d.subscription_id, d.state, d.attempt, d.next_attempt_at,
d.lease_owner, d.lease_expires_at, d.response_status, d.latency_ms, d.error_summary,
d.created_at, d.updated_at, d.completed_at`

var _ webhook.Store = (*PostgresStore)(nil)

type postgresTxWorkspaceKeyProvider struct {
	tx pgx.Tx
}

func (p postgresTxWorkspaceKeyProvider) GetWorkspaceKeyVersioned(ctx context.Context, workspaceID string) (string, int32, error) {
	var key string
	var version int32
	err := p.tx.QueryRow(ctx, `
SELECT key, kek_version
FROM workspace_key
WHERE workspace_id = $1
`, contract.NormalizeWorkspace(workspaceID)).Scan(&key, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, nil
	}
	if err != nil {
		return "", 0, err
	}
	return key, version, nil
}

func (s *PostgresStore) ListSubscriptions(ctx context.Context, workspaceID string) ([]webhook.Subscription, error) {
	return s.listSubscriptions(ctx, workspaceID, false)
}

func (s *PostgresStore) ListSubscriptionsIncludingDeleted(ctx context.Context, workspaceID string) ([]webhook.Subscription, error) {
	return s.listSubscriptions(ctx, workspaceID, true)
}

func (s *PostgresStore) listSubscriptions(ctx context.Context, workspaceID string, includeDeleted bool) ([]webhook.Subscription, error) {
	rows, err := s.pool.Query(ctx, `
SELECT `+webhookSubscriptionColumns+`
FROM webhook_subscription
WHERE workspace_id = $1 AND ($2 OR deleted_at IS NULL)
ORDER BY name, id
`, contract.NormalizeWorkspace(workspaceID), includeDeleted)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]webhook.Subscription, 0)
	for rows.Next() {
		record, err := scanWebhookSubscription(rows)
		if err != nil {
			return nil, err
		}
		subscription, err := s.postgresSubscription(ctx, record)
		if err != nil {
			return nil, err
		}
		result = append(result, subscription)
	}
	return result, rows.Err()
}

func (s *PostgresStore) GetSubscription(ctx context.Context, workspaceID string, subscriptionID string) (webhook.Subscription, error) {
	record, err := scanWebhookSubscription(s.pool.QueryRow(ctx, `
SELECT `+webhookSubscriptionColumns+`
FROM webhook_subscription
WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL
`, contract.NormalizeWorkspace(workspaceID), subscriptionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return webhook.Subscription{}, webhook.ErrNotFound
	}
	if err != nil {
		return webhook.Subscription{}, err
	}
	return s.postgresSubscription(ctx, record)
}

func (s *PostgresStore) CreateSubscription(ctx context.Context, subscription webhook.Subscription) (webhook.Subscription, error) {
	prepared, err := prepareNewSubscription(subscription, time.Now().UTC())
	if err != nil {
		return webhook.Subscription{}, err
	}
	err = s.withTx(ctx, func(tx pgx.Tx) error {
		provider := postgresTxWorkspaceKeyProvider{tx: tx}
		endpoint, secret, err := s.encryptPostgresSubscriptionWithProvider(ctx, provider, prepared)
		if err != nil {
			return err
		}
		eventTypes, _ := json.Marshal(prepared.EventTypes)
		appKeys, _ := json.Marshal(prepared.AppKeys)
		if _, err = tx.Exec(ctx, `
INSERT INTO webhook_subscription (
    id, workspace_id, name, endpoint_encrypted, signing_secret_encrypted,
    event_types, app_keys, enabled, created_by, updated_by, created_at, updated_at, deleted_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NULL)
`, prepared.ID, prepared.WorkspaceID, prepared.Name, endpoint, secret, eventTypes, appKeys, prepared.Enabled, prepared.CreatedBy, prepared.UpdatedBy, prepared.CreatedAt, prepared.UpdatedAt); err != nil {
			return webhookPostgresError(err)
		}
		audit := newWebhookAudit(prepared.WorkspaceID, prepared.ID, "", "webhook_subscription_created", webhookSubscriptionAuditDetail(prepared), prepared.CreatedBy, prepared.CreatedAt)
		return insertWebhookAudit(ctx, tx, audit)
	})
	return prepared, err
}

func (s *PostgresStore) UpdateSubscription(ctx context.Context, update webhook.Subscription) (webhook.Subscription, error) {
	var prepared webhook.Subscription
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		record, err := scanWebhookSubscription(tx.QueryRow(ctx, `
SELECT `+webhookSubscriptionColumns+`
FROM webhook_subscription
WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL
FOR UPDATE
`, contract.NormalizeWorkspace(update.WorkspaceID), update.ID))
		if errors.Is(err, pgx.ErrNoRows) {
			return webhook.ErrNotFound
		}
		if err != nil {
			return err
		}
		provider := postgresTxWorkspaceKeyProvider{tx: tx}
		existing, err := s.postgresSubscriptionWithProvider(ctx, provider, record)
		if err != nil {
			return err
		}
		prepared, err = prepareUpdatedSubscription(existing, update, time.Now().UTC())
		if err != nil {
			return err
		}
		endpoint, secret, err := s.encryptPostgresSubscriptionWithProvider(ctx, provider, prepared)
		if err != nil {
			return err
		}
		eventTypes, _ := json.Marshal(prepared.EventTypes)
		appKeys, _ := json.Marshal(prepared.AppKeys)
		_, err = tx.Exec(ctx, `
UPDATE webhook_subscription
SET name = $3,
    endpoint_encrypted = $4,
    signing_secret_encrypted = $5,
    event_types = $6,
    app_keys = $7,
    enabled = $8,
    updated_by = $9,
    updated_at = $10
WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL
`, prepared.WorkspaceID, prepared.ID, prepared.Name, endpoint, secret, eventTypes, appKeys, prepared.Enabled, prepared.UpdatedBy, prepared.UpdatedAt)
		if err != nil {
			return webhookPostgresError(err)
		}
		kind := "webhook_subscription_updated"
		if existing.Enabled && !prepared.Enabled {
			kind = "webhook_subscription_disabled"
		} else if !existing.Enabled && prepared.Enabled {
			kind = "webhook_subscription_enabled"
		}
		audit := newWebhookAudit(prepared.WorkspaceID, prepared.ID, "", kind, webhookSubscriptionUpdateAuditDetail(existing, prepared), prepared.UpdatedBy, prepared.UpdatedAt)
		return insertWebhookAudit(ctx, tx, audit)
	})
	return prepared, err
}

func (s *PostgresStore) DeleteSubscription(ctx context.Context, workspaceID string, subscriptionID string, actor string) error {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	actor = firstNonEmpty(strings.TrimSpace(actor), "system")
	return s.withTx(ctx, func(tx pgx.Tx) error {
		now := time.Now().UTC()
		var name string
		err := tx.QueryRow(ctx, `
UPDATE webhook_subscription
SET enabled = false, updated_by = $3, updated_at = $4, deleted_at = $4
WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL
		RETURNING name
`, workspaceID, subscriptionID, actor, now).Scan(&name)
		if errors.Is(err, pgx.ErrNoRows) {
			return webhook.ErrNotFound
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
UPDATE webhook_delivery
SET state = $3, updated_at = $4, completed_at = $4
WHERE workspace_id = $1
  AND subscription_id = $2
  AND state IN ($5, $6)
`, workspaceID, subscriptionID, webhook.DeliveryCanceled, now, webhook.DeliveryPending, webhook.DeliveryRetrying)
		if err != nil {
			return err
		}
		audit := newWebhookAudit(workspaceID, subscriptionID, "", "webhook_subscription_deleted", "name="+name+"; subscription deleted", actor, now)
		return insertWebhookAudit(ctx, tx, audit)
	})
}

func (s *PostgresStore) ListDeliveries(ctx context.Context, workspaceID string, query webhook.DeliveryListQuery) ([]webhook.DeliveryDetail, error) {
	query, err := prepareWebhookDeliveryQuery(query)
	if err != nil {
		return nil, err
	}
	var cursor any
	if !query.CursorCreatedAt.IsZero() {
		cursor = query.CursorCreatedAt
	}
	rows, err := s.pool.Query(ctx, `
SELECT `+webhookDeliveryQualifiedColumns+`, e.body, s.name
FROM webhook_delivery d
JOIN control_plane_event e ON e.id = d.event_id AND e.workspace_id = d.workspace_id
JOIN webhook_subscription s ON s.id = d.subscription_id AND s.workspace_id = d.workspace_id
WHERE d.workspace_id = $1
  AND ($2 = '' OR d.subscription_id = $2)
  AND ($3 = '' OR d.state = $3)
  AND ($4::timestamptz IS NULL OR (d.created_at, d.id) < ($4, $5))
ORDER BY d.created_at DESC, d.id DESC
LIMIT $6
`, contract.NormalizeWorkspace(workspaceID), query.SubscriptionID, string(query.State), cursor, query.CursorID, query.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]webhook.DeliveryDetail, 0)
	for rows.Next() {
		detail, err := scanWebhookDeliveryDetail(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, detail)
	}
	return result, rows.Err()
}

func (s *PostgresStore) GetDelivery(ctx context.Context, workspaceID string, deliveryID string) (webhook.DeliveryDetail, error) {
	detail, err := scanWebhookDeliveryDetail(s.pool.QueryRow(ctx, `
SELECT `+webhookDeliveryQualifiedColumns+`, e.body, s.name
FROM webhook_delivery d
JOIN control_plane_event e ON e.id = d.event_id AND e.workspace_id = d.workspace_id
JOIN webhook_subscription s ON s.id = d.subscription_id AND s.workspace_id = d.workspace_id
WHERE d.workspace_id = $1 AND d.id = $2
`, contract.NormalizeWorkspace(workspaceID), strings.TrimSpace(deliveryID)))
	if errors.Is(err, pgx.ErrNoRows) {
		return webhook.DeliveryDetail{}, webhook.ErrNotFound
	}
	return detail, err
}

func (s *PostgresStore) CreateTestDelivery(ctx context.Context, workspaceID string, subscriptionID string, actor string) (webhook.DeliveryDetail, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	actor = firstNonEmpty(strings.TrimSpace(actor), "system")
	var detail webhook.DeliveryDetail
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		record, err := scanWebhookSubscription(tx.QueryRow(ctx, `
SELECT `+webhookSubscriptionColumns+`
FROM webhook_subscription
WHERE workspace_id = $1 AND id = $2 AND deleted_at IS NULL
FOR UPDATE
`, workspaceID, subscriptionID))
		if errors.Is(err, pgx.ErrNoRows) {
			return webhook.ErrNotFound
		}
		if err != nil {
			return err
		}
		if !record.Enabled {
			return webhook.ErrConflict
		}
		now := time.Now().UTC()
		event, err := controlevent.NewWebhookTest(NewID("evt"), now, controlevent.WebhookTestData{
			Workspace: workspaceID, SubscriptionID: subscriptionID, Actor: actor,
		})
		if err != nil {
			return err
		}
		eventJSON, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO control_plane_event (id, workspace_id, event_type, subject, body, created_at)
VALUES ($1, $2, $3, $4, $5, $6)
`, event.ID, workspaceID, event.Type, event.Subject, eventJSON, event.Time); err != nil {
			return err
		}
		delivery := newWebhookDelivery(event, workspaceID, subscriptionID, now)
		if _, err := tx.Exec(ctx, `
INSERT INTO webhook_delivery (
    id, workspace_id, event_id, subscription_id, state, attempt, next_attempt_at,
    created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, 0, $6, $6, $6)
`, delivery.ID, delivery.WorkspaceID, delivery.EventID, delivery.SubscriptionID, delivery.State, delivery.NextAttemptAt); err != nil {
			return err
		}
		audit := newWebhookAudit(workspaceID, subscriptionID, delivery.ID, "webhook_test_requested", "test delivery queued", actor, now)
		if err := insertWebhookAudit(ctx, tx, audit); err != nil {
			return err
		}
		detail = webhook.DeliveryDetail{Delivery: delivery, Event: event, SubscriptionName: record.Name}
		return nil
	})
	return detail, err
}

func (s *PostgresStore) ClaimDelivery(ctx context.Context, workerID string, leaseTTL time.Duration) (*webhook.ClaimedDelivery, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return nil, webhook.ErrInvalidLease
	}
	if leaseTTL <= 0 {
		leaseTTL = 30 * time.Second
	}
	var claimed *webhook.ClaimedDelivery
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		now := time.Now().UTC()
		var deliveryID string
		err := tx.QueryRow(ctx, `
SELECT d.id
FROM webhook_delivery d
JOIN webhook_subscription s ON s.id = d.subscription_id AND s.workspace_id = d.workspace_id
WHERE s.enabled = true
  AND s.deleted_at IS NULL
  AND (
      (d.state IN ($1, $2) AND d.next_attempt_at <= $3)
      OR (d.state = $4 AND d.lease_expires_at <= $3)
  )
ORDER BY d.next_attempt_at, d.created_at, d.id
FOR UPDATE OF d, s SKIP LOCKED
LIMIT 1
`, webhook.DeliveryPending, webhook.DeliveryRetrying, now, webhook.DeliveryDelivering).Scan(&deliveryID)
		if errors.Is(err, pgx.ErrNoRows) {
			return webhook.ErrNoPendingDelivery
		}
		if err != nil {
			return err
		}
		delivery, err := scanWebhookDelivery(tx.QueryRow(ctx, `SELECT `+webhookDeliveryColumns+` FROM webhook_delivery WHERE id = $1`, deliveryID))
		if err != nil {
			return err
		}
		record, err := scanWebhookSubscription(tx.QueryRow(ctx, `SELECT `+webhookSubscriptionColumns+` FROM webhook_subscription WHERE id = $1`, delivery.SubscriptionID))
		if err != nil {
			return err
		}
		subscription, err := s.postgresSubscriptionWithProvider(ctx, postgresTxWorkspaceKeyProvider{tx: tx}, record)
		if err != nil {
			return err
		}
		var eventRaw []byte
		if err := tx.QueryRow(ctx, `SELECT body FROM control_plane_event WHERE id = $1`, delivery.EventID).Scan(&eventRaw); err != nil {
			return err
		}
		var controlEvent controlevent.Envelope
		if err := json.Unmarshal(eventRaw, &controlEvent); err != nil {
			return err
		}
		if err := controlevent.Validate(controlEvent); err != nil {
			return err
		}
		expiresAt := now.Add(leaseTTL).Truncate(time.Microsecond)
		delivery.State = webhook.DeliveryDelivering
		delivery.Attempt++
		delivery.LeaseOwner = &workerID
		delivery.LeaseExpiresAt = &expiresAt
		delivery.UpdatedAt = now
		if _, err := tx.Exec(ctx, `
UPDATE webhook_delivery
SET state = $2, attempt = $3, lease_owner = $4, lease_expires_at = $5, updated_at = $6
WHERE id = $1
`, delivery.ID, delivery.State, delivery.Attempt, workerID, expiresAt, now); err != nil {
			return err
		}
		lease := webhook.DeliveryLease{DeliveryID: delivery.ID, WorkerID: workerID, Attempt: delivery.Attempt, ExpiresAt: expiresAt}
		claimed = &webhook.ClaimedDelivery{Delivery: delivery, Event: controlEvent, Subscription: subscription, Lease: lease}
		return nil
	})
	return claimed, err
}

func (s *PostgresStore) CompleteDelivery(ctx context.Context, lease webhook.DeliveryLease, result webhook.DeliveryResult) error {
	if err := webhook.ValidateDeliveryResult(result); err != nil {
		return err
	}
	return s.withTx(ctx, func(tx pgx.Tx) error {
		delivery, err := scanWebhookDelivery(tx.QueryRow(ctx, `
SELECT `+webhookDeliveryColumns+`
FROM webhook_delivery
WHERE id = $1
FOR UPDATE
`, lease.DeliveryID))
		if errors.Is(err, pgx.ErrNoRows) {
			return webhook.ErrNotFound
		}
		if err != nil {
			return err
		}
		if err := validateDeliveryLease(delivery, lease); err != nil {
			return err
		}
		var deletedAt *time.Time
		if err := tx.QueryRow(ctx, `
SELECT deleted_at
FROM webhook_subscription
WHERE workspace_id = $1 AND id = $2
FOR SHARE
`, delivery.WorkspaceID, delivery.SubscriptionID).Scan(&deletedAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return webhook.ErrNotFound
			}
			return err
		}
		now := time.Now().UTC()
		effectiveState := result.State
		nextAttemptAt := result.NextAttemptAt
		var completedAt *time.Time
		if deletedAt != nil && effectiveState == webhook.DeliveryRetrying {
			effectiveState = webhook.DeliveryCanceled
			nextAttemptAt = now
			completedAt = cloneTime(&now)
		} else if effectiveState == webhook.DeliveryRetrying {
			completedAt = nil
		} else {
			nextAttemptAt = now
			completedAt = cloneTime(&result.CompletedAt)
		}
		_, err = tx.Exec(ctx, `
UPDATE webhook_delivery
SET state = $2,
    next_attempt_at = $3,
    lease_owner = NULL,
    lease_expires_at = NULL,
    response_status = $4,
    latency_ms = $5,
    error_summary = $6,
    updated_at = $7,
    completed_at = $8
WHERE id = $1
`, lease.DeliveryID, effectiveState, nextAttemptAt, result.ResponseStatus, result.LatencyMillis, result.ErrorSummary, now, completedAt)
		return err
	})
}

func (s *PostgresStore) RetryDelivery(ctx context.Context, workspaceID string, deliveryID string, actor string) error {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	actor = firstNonEmpty(strings.TrimSpace(actor), "system")
	return s.withTx(ctx, func(tx pgx.Tx) error {
		now := time.Now().UTC()
		var subscriptionID string
		err := tx.QueryRow(ctx, `
SELECT d.subscription_id
FROM webhook_delivery d
JOIN webhook_subscription s ON s.id = d.subscription_id AND s.workspace_id = d.workspace_id
WHERE d.workspace_id = $1 AND d.id = $2 AND d.state = $3
  AND s.enabled = true AND s.deleted_at IS NULL
FOR UPDATE OF d, s
`, workspaceID, deliveryID, webhook.DeliveryFailed).Scan(&subscriptionID)
		if errors.Is(err, pgx.ErrNoRows) {
			var found bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM webhook_delivery WHERE workspace_id = $1 AND id = $2)`, workspaceID, deliveryID).Scan(&found); err != nil {
				return err
			}
			if !found {
				return webhook.ErrNotFound
			}
			return webhook.ErrConflict
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
UPDATE webhook_delivery
SET state = $3,
    next_attempt_at = $4,
    lease_owner = NULL,
    lease_expires_at = NULL,
    completed_at = NULL,
    updated_at = $4
WHERE workspace_id = $1 AND id = $2 AND state = $5
`, workspaceID, deliveryID, webhook.DeliveryRetrying, now, webhook.DeliveryFailed); err != nil {
			return err
		}
		audit := newWebhookAudit(workspaceID, subscriptionID, deliveryID, "webhook_delivery_retried", "failed delivery queued for retry", actor, now)
		return insertWebhookAudit(ctx, tx, audit)
	})
}

func (s *PostgresStore) ListAudit(ctx context.Context, workspaceID string) ([]webhook.Audit, error) {
	rows, err := s.pool.Query(ctx, `
SELECT id, workspace_id, subscription_id, delivery_id, kind, detail, actor, created_at
FROM webhook_audit
WHERE workspace_id = $1
ORDER BY created_at DESC, id DESC
`, contract.NormalizeWorkspace(workspaceID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	audits := make([]webhook.Audit, 0)
	for rows.Next() {
		var audit webhook.Audit
		if err := rows.Scan(&audit.ID, &audit.WorkspaceID, &audit.SubscriptionID, &audit.DeliveryID, &audit.Kind, &audit.Detail, &audit.Actor, &audit.CreatedAt); err != nil {
			return nil, err
		}
		audits = append(audits, audit)
	}
	return audits, rows.Err()
}

func (s *PostgresStore) WebhookQueueStats(ctx context.Context) (webhook.QueueStats, error) {
	var stats webhook.QueueStats
	err := s.pool.QueryRow(ctx, `
SELECT COUNT(*), MIN(created_at)
FROM webhook_delivery
WHERE state IN ($1, $2, $3)
`, webhook.DeliveryPending, webhook.DeliveryRetrying, webhook.DeliveryDelivering).Scan(&stats.PendingCount, &stats.OldestPending)
	return stats, err
}

func (s *PostgresStore) PruneWebhookData(ctx context.Context, policy webhook.RetentionPolicy) (webhook.RetentionResult, error) {
	policy.BatchSize = normalizedWebhookRetentionBatchSize(policy.BatchSize)
	var result webhook.RetentionResult
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
WITH candidates AS (
    SELECT id
    FROM webhook_delivery
    WHERE (
        state = $2
        AND $5::timestamptz IS NOT NULL
        AND COALESCE(completed_at, updated_at) < $5
    ) OR (
        state = $3
        AND $6::timestamptz IS NOT NULL
        AND COALESCE(completed_at, updated_at) < $6
    ) OR (
        state = $4
        AND $7::timestamptz IS NOT NULL
        AND COALESCE(completed_at, updated_at) < $7
    )
    ORDER BY COALESCE(completed_at, updated_at), id
    FOR UPDATE SKIP LOCKED
    LIMIT $1
), deleted AS (
    DELETE FROM webhook_delivery
    WHERE id IN (SELECT id FROM candidates)
    RETURNING id
)
SELECT COUNT(*) FROM deleted
`, policy.BatchSize,
			webhook.DeliverySucceeded,
			webhook.DeliveryCanceled,
			webhook.DeliveryFailed,
			optionalWebhookRetentionCutoff(policy.SucceededBefore),
			optionalWebhookRetentionCutoff(policy.CanceledBefore),
			optionalWebhookRetentionCutoff(policy.FailedBefore),
		).Scan(&result.Deliveries); err != nil {
			return err
		}

		remaining := int64(policy.BatchSize) - result.Deliveries
		if remaining > 0 {
			if err := tx.QueryRow(ctx, `
WITH candidates AS (
    SELECT event.id
    FROM control_plane_event event
    WHERE NOT EXISTS (
        SELECT 1 FROM webhook_delivery delivery WHERE delivery.event_id = event.id
    )
    ORDER BY event.created_at, event.id
    FOR UPDATE SKIP LOCKED
    LIMIT $1
), deleted AS (
    DELETE FROM control_plane_event
    WHERE id IN (SELECT id FROM candidates)
    RETURNING id
)
SELECT COUNT(*) FROM deleted
`, remaining).Scan(&result.Events); err != nil {
				return err
			}
			remaining -= result.Events
		}

		if remaining > 0 && !policy.SubscriptionBefore.IsZero() {
			if err := tx.QueryRow(ctx, `
WITH candidates AS (
    SELECT subscription.id
    FROM webhook_subscription subscription
    WHERE subscription.deleted_at < $2
      AND NOT EXISTS (
          SELECT 1
          FROM webhook_delivery delivery
          WHERE delivery.workspace_id = subscription.workspace_id
            AND delivery.subscription_id = subscription.id
      )
    ORDER BY subscription.deleted_at, subscription.id
    FOR UPDATE SKIP LOCKED
    LIMIT $1
), deleted AS (
    DELETE FROM webhook_subscription
    WHERE id IN (SELECT id FROM candidates)
    RETURNING id
)
SELECT COUNT(*) FROM deleted
`, remaining, policy.SubscriptionBefore).Scan(&result.Subscriptions); err != nil {
				return err
			}
		}
		return nil
	})
	return result, err
}

func optionalWebhookRetentionCutoff(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func (s *PostgresStore) postgresSubscription(ctx context.Context, record WebhookSubscriptionRecord) (webhook.Subscription, error) {
	return s.postgresSubscriptionWithProvider(ctx, s, record)
}

func (s *PostgresStore) postgresSubscriptionWithProvider(ctx context.Context, provider inputWorkspaceKeyProvider, record WebhookSubscriptionRecord) (webhook.Subscription, error) {
	config := inputCryptoConfig{SecretKey: s.SecretKey, SecretKeyPrevious: s.SecretKeyPrevious}
	endpoint, err := decryptWebhookString(ctx, provider, config, record.WorkspaceID, record.EndpointEncrypted, "webhook endpoint")
	if err != nil {
		return webhook.Subscription{}, err
	}
	secret, err := decryptWebhookString(ctx, provider, config, record.WorkspaceID, record.SigningSecretEncrypted, "webhook signing secret")
	if err != nil {
		return webhook.Subscription{}, err
	}
	return subscriptionFromRecord(record, endpoint, secret), nil
}

func (s *PostgresStore) encryptPostgresSubscription(ctx context.Context, subscription webhook.Subscription) (json.RawMessage, json.RawMessage, error) {
	return s.encryptPostgresSubscriptionWithProvider(ctx, s, subscription)
}

func (s *PostgresStore) encryptPostgresSubscriptionWithProvider(ctx context.Context, provider inputWorkspaceKeyProvider, subscription webhook.Subscription) (json.RawMessage, json.RawMessage, error) {
	config := inputCryptoConfig{SecretKey: s.SecretKey, SecretKeyPrevious: s.SecretKeyPrevious}
	endpoint, err := encryptWebhookString(ctx, provider, config, subscription.WorkspaceID, subscription.Endpoint, "webhook endpoint")
	if err != nil {
		return nil, nil, err
	}
	secret, err := encryptWebhookString(ctx, provider, config, subscription.WorkspaceID, subscription.SigningSecret, "webhook signing secret")
	if err != nil {
		return nil, nil, err
	}
	return endpoint, secret, nil
}

func scanWebhookSubscription(row rowScanner) (WebhookSubscriptionRecord, error) {
	var record WebhookSubscriptionRecord
	var eventTypes []byte
	var appKeys []byte
	err := row.Scan(
		&record.ID,
		&record.WorkspaceID,
		&record.Name,
		&record.EndpointEncrypted,
		&record.SigningSecretEncrypted,
		&eventTypes,
		&appKeys,
		&record.Enabled,
		&record.CreatedBy,
		&record.UpdatedBy,
		&record.CreatedAt,
		&record.UpdatedAt,
		&record.DeletedAt,
	)
	if err != nil {
		return WebhookSubscriptionRecord{}, err
	}
	if err := json.Unmarshal(eventTypes, &record.EventTypes); err != nil {
		return WebhookSubscriptionRecord{}, err
	}
	if err := json.Unmarshal(appKeys, &record.AppKeys); err != nil {
		return WebhookSubscriptionRecord{}, err
	}
	sort.Strings(record.EventTypes)
	sort.Strings(record.AppKeys)
	return record, nil
}

func scanWebhookDelivery(row rowScanner) (webhook.Delivery, error) {
	var delivery webhook.Delivery
	var state string
	err := row.Scan(
		&delivery.ID,
		&delivery.WorkspaceID,
		&delivery.EventID,
		&delivery.SubscriptionID,
		&state,
		&delivery.Attempt,
		&delivery.NextAttemptAt,
		&delivery.LeaseOwner,
		&delivery.LeaseExpiresAt,
		&delivery.ResponseStatus,
		&delivery.LatencyMillis,
		&delivery.ErrorSummary,
		&delivery.CreatedAt,
		&delivery.UpdatedAt,
		&delivery.CompletedAt,
	)
	delivery.State = webhook.DeliveryState(state)
	return delivery, err
}

func scanWebhookDeliveryDetail(row rowScanner) (webhook.DeliveryDetail, error) {
	var detail webhook.DeliveryDetail
	var state string
	var eventRaw []byte
	err := row.Scan(
		&detail.Delivery.ID,
		&detail.Delivery.WorkspaceID,
		&detail.Delivery.EventID,
		&detail.Delivery.SubscriptionID,
		&state,
		&detail.Delivery.Attempt,
		&detail.Delivery.NextAttemptAt,
		&detail.Delivery.LeaseOwner,
		&detail.Delivery.LeaseExpiresAt,
		&detail.Delivery.ResponseStatus,
		&detail.Delivery.LatencyMillis,
		&detail.Delivery.ErrorSummary,
		&detail.Delivery.CreatedAt,
		&detail.Delivery.UpdatedAt,
		&detail.Delivery.CompletedAt,
		&eventRaw,
		&detail.SubscriptionName,
	)
	if err != nil {
		return webhook.DeliveryDetail{}, err
	}
	detail.Delivery.State = webhook.DeliveryState(state)
	if err := json.Unmarshal(eventRaw, &detail.Event); err != nil {
		return webhook.DeliveryDetail{}, err
	}
	if err := controlevent.Validate(detail.Event); err != nil {
		return webhook.DeliveryDetail{}, err
	}
	return detail, nil
}

func insertWebhookAudit(ctx context.Context, tx pgx.Tx, audit webhook.Audit) error {
	_, err := tx.Exec(ctx, `
INSERT INTO webhook_audit (
    id, workspace_id, subscription_id, delivery_id, kind, detail, actor, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
`, audit.ID, audit.WorkspaceID, audit.SubscriptionID, audit.DeliveryID, audit.Kind, audit.Detail, audit.Actor, audit.CreatedAt)
	return err
}

func webhookPostgresError(err error) error {
	if err == nil {
		return nil
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return fmt.Errorf("%w: webhook subscription name or delivery already exists", webhook.ErrConflict)
	}
	return err
}
