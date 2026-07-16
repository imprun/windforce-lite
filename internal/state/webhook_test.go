package state

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	controlevent "github.com/imprun/windforce-lite/internal/event"
	"github.com/imprun/windforce-lite/internal/webhook"
)

func TestLocalWebhookSubscriptionEncryptsSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := NewLocalStore(path)
	ctx := context.Background()
	subscription := webhook.Subscription{
		WorkspaceID:   "workspace-a",
		Name:          "Operations",
		Endpoint:      "https://hooks.example.test/services/private?token=endpoint-secret",
		SigningSecret: "signing-secret-0123456789",
		EventTypes:    []string{controlevent.ReleasePublishedType},
		Enabled:       true,
		CreatedBy:     "operator@example.test",
	}
	if _, err := store.CreateSubscription(ctx, subscription); err == nil {
		t.Fatal("CreateSubscription succeeded without SECRET_KEY")
	}
	store.ConfigureInputCrypto("local-test-secret-key", "")
	created, err := store.CreateSubscription(ctx, subscription)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, protected := range [][]byte{[]byte(subscription.Endpoint), []byte(subscription.SigningSecret), []byte("endpoint-secret")} {
		if bytes.Contains(raw, protected) {
			t.Fatalf("state contains plaintext %q: %s", protected, raw)
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
		t.Fatalf("duplicate name error = %v, want conflict", err)
	}
}

func TestWebhookOpaqueIDsUseStablePrefixes(t *testing.T) {
	for _, prefix := range []string{"evt", "whs", "whd"} {
		seen := map[string]struct{}{}
		for index := 0; index < 1000; index++ {
			id := NewID(prefix)
			if !strings.HasPrefix(id, prefix+"_") {
				t.Fatalf("id %q does not use prefix %q", id, prefix)
			}
			if _, exists := seen[id]; exists {
				t.Fatalf("duplicate id %q", id)
			}
			seen[id] = struct{}{}
		}
	}
}

func TestLocalReleasePublicationCreatesMatchingWebhookOutbox(t *testing.T) {
	store := NewLocalStore(filepath.Join(t.TempDir(), "state.json"))
	store.ConfigureInputCrypto("local-test-secret-key", "")
	ctx := context.Background()
	matching, err := store.CreateSubscription(ctx, webhook.Subscription{
		WorkspaceID:   "workspace-a",
		Name:          "Checkout releases",
		Endpoint:      "https://hooks.example.test/checkout",
		SigningSecret: "signing-secret-0123456789",
		EventTypes:    []string{controlevent.ReleasePublishedType},
		AppKeys:       []string{"checkout"},
		Enabled:       true,
		CreatedBy:     "operator@example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSubscription(ctx, webhook.Subscription{
		WorkspaceID:   "workspace-a",
		Name:          "Other releases",
		Endpoint:      "https://hooks.example.test/other",
		SigningSecret: "signing-secret-0123456789",
		EventTypes:    []string{controlevent.ReleasePublishedType},
		AppKeys:       []string{"other"},
		Enabled:       true,
		CreatedBy:     "operator@example.test",
	}); err != nil {
		t.Fatal(err)
	}

	first := releaseCatalogDeployment("workspace-a", "source-a", "checkout", "commit-a")
	actor := "operator@example.test"
	first.CreatedBy = &actor
	if _, err := store.PublishRelease(ctx, first, time.Date(2026, 7, 16, 11, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	second := releaseCatalogDeployment("workspace-a", "source-a", "checkout", "commit-b")
	second.CreatedBy = &actor
	if _, err := store.PublishRelease(ctx, second, time.Date(2026, 7, 16, 11, 5, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}

	snapshot, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.ControlPlaneEvents) != 2 || len(snapshot.WebhookDeliveries) != 2 {
		t.Fatalf("outbox counts = events:%d deliveries:%d", len(snapshot.ControlPlaneEvents), len(snapshot.WebhookDeliveries))
	}
	latestHistory := snapshot.ReleaseCatalog.History[len(snapshot.ReleaseCatalog.History)-1]
	latestEvent := snapshot.ControlPlaneEvents[findEventForRelease(t, snapshot, latestHistory.ID)]
	data, err := controlevent.ReleasePublished(latestEvent)
	if err != nil {
		t.Fatal(err)
	}
	if data.PreviousReleaseID == nil || *data.PreviousReleaseID != snapshot.ReleaseCatalog.History[0].ID || data.PreviousCommit == nil || *data.PreviousCommit != "commit-a" {
		t.Fatalf("release event previous reference = %#v", data)
	}

	claimed, err := store.ClaimDelivery(ctx, "dispatcher-a", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.Subscription.ID != matching.ID || claimed.Subscription.SigningSecret == "" || claimed.Event.ID != claimed.Delivery.EventID {
		t.Fatalf("claimed delivery = %#v", claimed)
	}
	retryAt := time.Now().UTC().Add(-time.Second)
	message := "temporary failure"
	if err := store.CompleteDelivery(ctx, claimed.Lease, webhook.DeliveryResult{State: webhook.DeliveryRetrying, NextAttemptAt: retryAt, ErrorSummary: &message}); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := store.ClaimDelivery(ctx, "dispatcher-b", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if reclaimed.Delivery.Attempt < 1 {
		t.Fatalf("reclaimed delivery attempt = %d", reclaimed.Delivery.Attempt)
	}
	completedAt := time.Now().UTC()
	status := 204
	if err := store.CompleteDelivery(ctx, reclaimed.Lease, webhook.DeliveryResult{State: webhook.DeliverySucceeded, ResponseStatus: &status, CompletedAt: completedAt}); err != nil {
		t.Fatal(err)
	}
	inFlight, err := store.ClaimDelivery(ctx, "dispatcher-a", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSubscription(ctx, "workspace-a", matching.ID, "operator@example.test"); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteDelivery(ctx, inFlight.Lease, webhook.DeliveryResult{State: webhook.DeliveryRetrying, NextAttemptAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	snapshot, err = store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.WebhookDeliveries[inFlight.Delivery.ID].State; got != webhook.DeliveryCanceled {
		t.Fatalf("deleted subscription in-flight state = %q, want canceled", got)
	}
}

func TestLocalExpiredDeliveryLeaseIsReclaimed(t *testing.T) {
	store := NewLocalStore(filepath.Join(t.TempDir(), "state.json"))
	store.ConfigureInputCrypto("local-test-secret-key", "")
	ctx := context.Background()
	if _, err := store.CreateSubscription(ctx, webhook.Subscription{
		WorkspaceID:   "workspace-a",
		Name:          "All releases",
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
	first, err := store.ClaimDelivery(ctx, "dispatcher-a", time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	second, err := store.ClaimDelivery(ctx, "dispatcher-b", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if second.Delivery.ID != first.Delivery.ID || second.Delivery.Attempt != first.Delivery.Attempt+1 {
		t.Fatalf("reclaimed delivery = %#v, first = %#v", second.Delivery, first.Delivery)
	}
	completedAt := time.Now().UTC()
	if err := store.CompleteDelivery(ctx, first.Lease, webhook.DeliveryResult{State: webhook.DeliveryFailed, CompletedAt: completedAt}); !errors.Is(err, webhook.ErrInvalidLease) {
		t.Fatalf("stale lease completion error = %v", err)
	}
}

func findEventForRelease(t *testing.T, snapshot Snapshot, releaseID string) string {
	t.Helper()
	for id, value := range snapshot.ControlPlaneEvents {
		data, err := controlevent.ReleasePublished(value)
		if err != nil {
			t.Fatal(err)
		}
		if data.ReleaseID == releaseID {
			return id
		}
	}
	t.Fatalf("event for release %s not found", releaseID)
	return ""
}
