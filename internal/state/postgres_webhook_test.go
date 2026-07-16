package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	controlevent "github.com/imprun/windforce-lite/internal/event"
	"github.com/imprun/windforce-lite/internal/webhook"
)

func TestPostgresWebhookStoreContract(t *testing.T) {
	dsn := os.Getenv("WINDFORCE_LITE_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("WINDFORCE_LITE_POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	store := openIsolatedPostgresCatalogStore(t, dsn)
	store.ConfigureInputCrypto("postgres-test-secret-key", "")
	subscription := webhook.Subscription{
		WorkspaceID:   "workspace-a",
		Name:          "Operations",
		Endpoint:      "https://hooks.example.test/services/private?token=endpoint-secret",
		SigningSecret: "signing-secret-0123456789",
		EventTypes:    []string{controlevent.ReleasePublishedType},
		AppKeys:       []string{"checkout"},
		Enabled:       true,
		CreatedBy:     "operator@example.test",
	}
	created, err := store.CreateSubscription(ctx, subscription)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var endpointRaw []byte
	var secretRaw []byte
	if err := store.pool.QueryRow(ctx, `
SELECT endpoint_encrypted, signing_secret_encrypted
FROM webhook_subscription
WHERE id = $1
`, created.ID).Scan(&endpointRaw, &secretRaw); err != nil {
		t.Fatal(err)
	}
	for _, protected := range [][]byte{[]byte(subscription.Endpoint), []byte(subscription.SigningSecret), []byte("endpoint-secret")} {
		if bytes.Contains(endpointRaw, protected) || bytes.Contains(secretRaw, protected) {
			t.Fatalf("PostgreSQL contains plaintext %q: endpoint=%s secret=%s", protected, endpointRaw, secretRaw)
		}
	}
	loaded, err := store.GetSubscription(ctx, "workspace-a", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Endpoint != subscription.Endpoint || loaded.SigningSecret != subscription.SigningSecret {
		t.Fatalf("loaded subscription = %#v", loaded)
	}
	if _, err := store.CreateSubscription(ctx, subscription); !errors.Is(err, webhook.ErrConflict) {
		t.Fatalf("duplicate subscription error = %v", err)
	}
	actor := "operator@example.test"
	first := releaseCatalogDeployment("workspace-a", "source-a", "checkout", "commit-a")
	first.CreatedBy = &actor
	if _, err := store.PublishRelease(ctx, first, time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	second := releaseCatalogDeployment("workspace-a", "source-a", "checkout", "commit-b")
	second.CreatedBy = &actor
	if _, err := store.PublishRelease(ctx, second, time.Date(2026, 7, 16, 12, 5, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	var eventCount int
	var deliveryCount int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM control_plane_event`).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM webhook_delivery`).Scan(&deliveryCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 2 || deliveryCount != 2 {
		t.Fatalf("outbox counts = events:%d deliveries:%d", eventCount, deliveryCount)
	}
	var eventRaw []byte
	if err := store.pool.QueryRow(ctx, `SELECT body FROM control_plane_event ORDER BY created_at DESC LIMIT 1`).Scan(&eventRaw); err != nil {
		t.Fatal(err)
	}
	var latestEvent controlevent.Envelope
	if err := json.Unmarshal(eventRaw, &latestEvent); err != nil {
		t.Fatal(err)
	}
	data, err := controlevent.ReleasePublished(latestEvent)
	if err != nil {
		t.Fatal(err)
	}
	if data.PreviousReleaseID == nil || data.PreviousCommit == nil || *data.PreviousCommit != "commit-a" {
		t.Fatalf("previous release reference = %#v", data)
	}

	claimed, err := store.ClaimDelivery(ctx, "dispatcher-a", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.Subscription.SigningSecret != subscription.SigningSecret || claimed.Event.ID != claimed.Delivery.EventID {
		t.Fatalf("claimed delivery = %#v", claimed)
	}
	retryAt := time.Now().UTC().Add(-time.Second)
	errorSummary := "temporary failure"
	if err := store.CompleteDelivery(ctx, claimed.Lease, webhook.DeliveryResult{State: webhook.DeliveryRetrying, NextAttemptAt: retryAt, ErrorSummary: &errorSummary}); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := store.ClaimDelivery(ctx, "dispatcher-b", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if reclaimed.Delivery.ID != claimed.Delivery.ID || reclaimed.Delivery.Attempt != claimed.Delivery.Attempt+1 {
		t.Fatalf("reclaimed delivery = %#v, first = %#v", reclaimed.Delivery, claimed.Delivery)
	}
	completedAt := time.Now().UTC()
	status := 204
	if err := store.CompleteDelivery(ctx, reclaimed.Lease, webhook.DeliveryResult{State: webhook.DeliverySucceeded, ResponseStatus: &status, CompletedAt: completedAt}); err != nil {
		t.Fatal(err)
	}

	expiring, err := store.ClaimDelivery(ctx, "dispatcher-a", time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	recovered, err := store.ClaimDelivery(ctx, "dispatcher-b", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Delivery.ID != expiring.Delivery.ID || recovered.Delivery.Attempt != expiring.Delivery.Attempt+1 {
		t.Fatalf("recovered lease = %#v, expiring = %#v", recovered.Delivery, expiring.Delivery)
	}
	if err := store.CompleteDelivery(ctx, expiring.Lease, webhook.DeliveryResult{State: webhook.DeliveryFailed, CompletedAt: time.Now().UTC()}); !errors.Is(err, webhook.ErrInvalidLease) {
		t.Fatalf("stale lease completion error = %v", err)
	}
	if err := store.CompleteDelivery(ctx, recovered.Lease, webhook.DeliveryResult{State: webhook.DeliveryFailed, CompletedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := store.RetryDelivery(ctx, "workspace-a", recovered.Delivery.ID, actor); err != nil {
		t.Fatal(err)
	}
	manualRetry, err := store.ClaimDelivery(ctx, "dispatcher-c", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if manualRetry.Delivery.ID != recovered.Delivery.ID {
		t.Fatalf("manual retry delivery = %#v", manualRetry.Delivery)
	}
	testDelivery, err := store.CreateTestDelivery(ctx, "workspace-a", created.ID, "tester@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if testDelivery.Event.Type != controlevent.WebhookTestType || testDelivery.SubscriptionName != created.Name {
		t.Fatalf("test delivery = %#v", testDelivery)
	}
	deliveries, err := store.ListDeliveries(ctx, "workspace-a", webhook.DeliveryListQuery{SubscriptionID: created.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 3 || deliveries[0].Delivery.ID != testDelivery.Delivery.ID {
		t.Fatalf("deliveries = %#v", deliveries)
	}
	loadedDelivery, err := store.GetDelivery(ctx, "workspace-a", testDelivery.Delivery.ID)
	if err != nil || loadedDelivery.Event.ID != testDelivery.Event.ID {
		t.Fatalf("loaded delivery = %#v, err = %v", loadedDelivery, err)
	}
	if _, err := store.GetDelivery(ctx, "workspace-b", testDelivery.Delivery.ID); !errors.Is(err, webhook.ErrNotFound) {
		t.Fatalf("cross-workspace delivery error = %v", err)
	}
	if err := store.DeleteSubscription(ctx, "workspace-a", created.ID, actor); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteDelivery(ctx, manualRetry.Lease, webhook.DeliveryResult{State: webhook.DeliveryRetrying, NextAttemptAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	var finalState string
	if err := store.pool.QueryRow(ctx, `SELECT state FROM webhook_delivery WHERE id = $1`, manualRetry.Delivery.ID).Scan(&finalState); err != nil {
		t.Fatal(err)
	}
	if finalState != string(webhook.DeliveryCanceled) {
		t.Fatalf("deleted subscription in-flight state = %q, want canceled", finalState)
	}
	allSubscriptions, err := store.ListSubscriptionsIncludingDeleted(ctx, "workspace-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(allSubscriptions) != 1 || allSubscriptions[0].DeletedAt == nil {
		t.Fatalf("subscriptions including deleted = %#v", allSubscriptions)
	}
	audits, err := store.ListAudit(ctx, "workspace-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) < 4 || audits[0].Kind != "webhook_subscription_deleted" {
		t.Fatalf("webhook audit = %#v", audits)
	}
}

func TestPostgresWebhookTransactionsUseSingleConnection(t *testing.T) {
	dsn := os.Getenv("WINDFORCE_LITE_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("WINDFORCE_LITE_POSTGRES_TEST_DSN is not set")
	}
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	store := openIsolatedPostgresCatalogStore(t, dsn+separator+"pool_max_conns=1")
	store.ConfigureInputCrypto("postgres-test-secret-key", "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	created, err := store.CreateSubscription(ctx, webhook.Subscription{
		WorkspaceID:   "workspace-a",
		Name:          "Operations",
		Endpoint:      "https://hooks.example.test/releases",
		SigningSecret: "signing-secret-0123456789",
		EventTypes:    []string{controlevent.ReleasePublishedType},
		AppKeys:       []string{"checkout"},
		Enabled:       true,
		CreatedBy:     "operator@example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	created.Name = "Release operations"
	created.UpdatedBy = "operator@example.test"
	if _, err := store.UpdateSubscription(ctx, created); err != nil {
		t.Fatalf("update with one pool connection: %v", err)
	}
	if _, err := store.PublishRelease(ctx, releaseCatalogDeployment("workspace-a", "source-a", "checkout", "commit-a"), time.Now().UTC()); err != nil {
		t.Fatalf("publish with one pool connection: %v", err)
	}
	claimed, err := store.ClaimDelivery(ctx, "dispatcher-a", time.Minute)
	if err != nil {
		t.Fatalf("claim with one pool connection: %v", err)
	}
	if claimed.Subscription.Name != "Release operations" {
		t.Fatalf("claimed subscription name = %q", claimed.Subscription.Name)
	}
}

func TestPostgresWebhookRetentionPrunesOnlyTerminalDeliveries(t *testing.T) {
	dsn := os.Getenv("WINDFORCE_LITE_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("WINDFORCE_LITE_POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	store := openIsolatedPostgresCatalogStore(t, dsn)
	store.ConfigureInputCrypto("postgres-test-secret-key", "")
	if _, err := store.CreateSubscription(ctx, webhook.Subscription{
		WorkspaceID:   "workspace-a",
		Name:          "Retained subscription",
		Endpoint:      "https://hooks.example.test/releases",
		SigningSecret: "signing-secret-0123456789",
		EventTypes:    []string{controlevent.ReleasePublishedType},
		Enabled:       true,
		CreatedBy:     "operator@example.test",
	}); err != nil {
		t.Fatal(err)
	}
	removable, err := store.CreateSubscription(ctx, webhook.Subscription{
		WorkspaceID:   "workspace-a",
		Name:          "Deleted subscription",
		Endpoint:      "https://hooks.example.test/deleted",
		SigningSecret: "signing-secret-0123456789",
		EventTypes:    []string{controlevent.ReleasePublishedType},
		AppKeys:       []string{"never-matches"},
		Enabled:       true,
		CreatedBy:     "operator@example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 6; index++ {
		deployment := releaseCatalogDeployment("workspace-a", "source-a", "echo", fmt.Sprintf("commit-%d", index))
		if _, err := store.PublishRelease(ctx, deployment, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.DeleteSubscription(ctx, "workspace-a", removable.ID, "operator@example.test"); err != nil {
		t.Fatal(err)
	}

	rows, err := store.pool.Query(ctx, `SELECT id FROM webhook_delivery ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	deliveryIDs := make([]string, 0, 6)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		deliveryIDs = append(deliveryIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-100 * 24 * time.Hour)
	states := []webhook.DeliveryState{
		webhook.DeliverySucceeded,
		webhook.DeliveryFailed,
		webhook.DeliveryCanceled,
		webhook.DeliveryPending,
		webhook.DeliveryRetrying,
		webhook.DeliveryDelivering,
	}
	for index, deliveryID := range deliveryIDs {
		createdAt := old.Add(time.Duration(index) * time.Minute)
		var completedAt *time.Time
		if !isActiveWebhookDelivery(states[index]) {
			completedAt = cloneTime(&createdAt)
		}
		if _, err := store.pool.Exec(ctx, `
UPDATE webhook_delivery
SET state = $2, created_at = $3, updated_at = $3, completed_at = $4
WHERE id = $1
`, deliveryID, states[index], createdAt, completedAt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.pool.Exec(ctx, `UPDATE webhook_subscription SET deleted_at = $2 WHERE id = $1`, removable.ID, old); err != nil {
		t.Fatal(err)
	}

	stats, err := store.WebhookQueueStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantOldest := old.Add(3 * time.Minute)
	if stats.PendingCount != 3 || stats.OldestPending == nil || stats.OldestPending.Sub(wantOldest).Abs() > time.Microsecond {
		t.Fatalf("queue stats = %#v", stats)
	}
	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour)
	result, err := store.PruneWebhookData(ctx, webhook.RetentionPolicy{
		SucceededBefore:    cutoff,
		CanceledBefore:     cutoff,
		FailedBefore:       cutoff,
		SubscriptionBefore: cutoff,
		BatchSize:          20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != (webhook.RetentionResult{Deliveries: 3, Events: 3, Subscriptions: 1}) {
		t.Fatalf("retention result = %#v", result)
	}
	var deliveries int
	var events int
	if err := store.pool.QueryRow(ctx, `SELECT COUNT(*) FROM webhook_delivery`).Scan(&deliveries); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, `SELECT COUNT(*) FROM control_plane_event`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if deliveries != 3 || events != 3 {
		t.Fatalf("remaining counts = deliveries:%d events:%d", deliveries, events)
	}
}

func TestPostgresWebhookDeliveryUniqueConstraint(t *testing.T) {
	dsn := os.Getenv("WINDFORCE_LITE_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("WINDFORCE_LITE_POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	store := openIsolatedPostgresCatalogStore(t, dsn)
	store.ConfigureInputCrypto("postgres-test-secret-key", "")
	created, err := store.CreateSubscription(ctx, webhook.Subscription{
		WorkspaceID:   "workspace-a",
		Name:          "Operations",
		Endpoint:      "https://hooks.example.test/releases",
		SigningSecret: "signing-secret-0123456789",
		EventTypes:    []string{controlevent.ReleasePublishedType},
		Enabled:       true,
		CreatedBy:     "operator@example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishRelease(ctx, releaseCatalogDeployment("workspace-a", "source-a", "echo", "commit-a"), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var eventID string
	if err := store.pool.QueryRow(ctx, `SELECT id FROM control_plane_event LIMIT 1`).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	_, err = store.pool.Exec(ctx, `
INSERT INTO webhook_delivery (
    id, workspace_id, event_id, subscription_id, state, next_attempt_at, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, now(), now(), now())
`, "whd_duplicate", "workspace-a", eventID, created.ID, webhook.DeliveryPending)
	if err == nil {
		t.Fatal("duplicate event/subscription delivery insert succeeded")
	}
}

func TestPostgresReleaseOutboxMatchesHundredSubscriptions(t *testing.T) {
	dsn := os.Getenv("WINDFORCE_LITE_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("WINDFORCE_LITE_POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	store := openIsolatedPostgresCatalogStore(t, dsn)
	store.ConfigureInputCrypto("postgres-test-secret-key", "")
	for index := 0; index < 100; index++ {
		if _, err := store.CreateSubscription(ctx, webhook.Subscription{
			WorkspaceID:   "workspace-a",
			Name:          fmt.Sprintf("Subscriber %03d", index),
			Endpoint:      "https://hooks.example.test/releases",
			SigningSecret: "signing-secret-0123456789",
			EventTypes:    []string{controlevent.ReleasePublishedType},
			Enabled:       true,
			CreatedBy:     "operator@example.test",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.PublishRelease(ctx, releaseCatalogDeployment("workspace-a", "source-a", "echo", "commit-a"), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM webhook_delivery`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 100 {
		t.Fatalf("delivery count = %d, want 100", count)
	}
}

func TestPostgresWebhookConcurrentClaimHasSingleOwner(t *testing.T) {
	dsn := os.Getenv("WINDFORCE_LITE_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("WINDFORCE_LITE_POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	store := openIsolatedPostgresCatalogStore(t, dsn)
	store.ConfigureInputCrypto("postgres-test-secret-key", "")
	if _, err := store.CreateSubscription(ctx, webhook.Subscription{
		WorkspaceID:   "workspace-a",
		Name:          "Operations",
		Endpoint:      "https://hooks.example.test/releases",
		SigningSecret: "signing-secret-0123456789",
		EventTypes:    []string{controlevent.ReleasePublishedType},
		Enabled:       true,
		CreatedBy:     "operator@example.test",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishRelease(ctx, releaseCatalogDeployment("workspace-a", "source-a", "echo", "commit-a"), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	const contenders = 12
	start := make(chan struct{})
	results := make(chan error, contenders)
	claimed := make(chan *webhook.ClaimedDelivery, contenders)
	var wait sync.WaitGroup
	for index := 0; index < contenders; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			delivery, err := store.ClaimDelivery(ctx, fmt.Sprintf("dispatcher-%02d", index), time.Minute)
			if err == nil {
				claimed <- delivery
			}
			results <- err
		}(index)
	}
	close(start)
	wait.Wait()
	close(results)
	close(claimed)

	successes := 0
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, webhook.ErrNoPendingDelivery) {
			t.Fatalf("concurrent claim error = %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful claims = %d, want 1", successes)
	}
	owned := <-claimed
	if owned == nil || owned.Lease.WorkerID == "" {
		t.Fatalf("claimed delivery = %#v", owned)
	}
}

func TestPostgresWebhookConcurrentClaimsDoNotRequireGlobalDeliveryOrder(t *testing.T) {
	dsn := os.Getenv("WINDFORCE_LITE_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("WINDFORCE_LITE_POSTGRES_TEST_DSN is not set")
	}
	ctx := context.Background()
	store := openIsolatedPostgresCatalogStore(t, dsn)
	store.ConfigureInputCrypto("postgres-test-secret-key", "")
	if _, err := store.CreateSubscription(ctx, webhook.Subscription{
		WorkspaceID:   "workspace-a",
		Name:          "Operations",
		Endpoint:      "https://hooks.example.test/releases",
		SigningSecret: "signing-secret-0123456789",
		EventTypes:    []string{controlevent.ReleasePublishedType},
		Enabled:       true,
		CreatedBy:     "operator@example.test",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSubscription(ctx, webhook.Subscription{
		WorkspaceID:   "workspace-a",
		Name:          "Security",
		Endpoint:      "https://security-hooks.example.test/releases",
		SigningSecret: "signing-secret-9876543210",
		EventTypes:    []string{controlevent.ReleasePublishedType},
		Enabled:       true,
		CreatedBy:     "operator@example.test",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishRelease(ctx, releaseCatalogDeployment("workspace-a", "source-a", "echo", "commit-a"), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	claims := make(chan *webhook.ClaimedDelivery, 2)
	errorsFound := make(chan error, 2)
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			claimed, err := store.ClaimDelivery(ctx, fmt.Sprintf("dispatcher-%d", index), time.Minute)
			claims <- claimed
			errorsFound <- err
		}(index)
	}
	close(start)
	wait.Wait()
	close(claims)
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatalf("concurrent claim error = %v", err)
		}
	}
	ids := map[string]struct{}{}
	for claimed := range claims {
		if claimed == nil {
			t.Fatal("nil concurrent claim")
		}
		ids[claimed.Delivery.ID] = struct{}{}
	}
	if len(ids) != 2 {
		t.Fatalf("distinct claimed deliveries = %#v", ids)
	}
}
