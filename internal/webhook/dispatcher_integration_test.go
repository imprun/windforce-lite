package webhook_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/imprun/windforce-lite/internal/contract"
	controlevent "github.com/imprun/windforce-lite/internal/event"
	"github.com/imprun/windforce-lite/internal/state"
	"github.com/imprun/windforce-lite/internal/webhook"
)

func TestDispatcherResumesPersistedDeliveryAfterRestart(t *testing.T) {
	type receivedHeaders struct {
		eventID   string
		eventType string
	}
	received := make(chan receivedHeaders, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		received <- receivedHeaders{
			eventID:   request.Header.Get(webhook.HeaderEventID),
			eventType: request.Header.Get(webhook.HeaderEventType),
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()

	ctx := context.Background()
	statePath := filepath.Join(t.TempDir(), "state.json")
	producerStore := state.NewLocalStore(statePath)
	producerStore.ConfigureInputCrypto("dispatcher-integration-secret", "")
	if _, err := producerStore.CreateSubscription(ctx, webhook.Subscription{
		WorkspaceID:   "default",
		Name:          "local receiver",
		Endpoint:      receiver.URL,
		SigningSecret: "0123456789abcdef0123456789abcdef",
		EventTypes:    []string{controlevent.ReleasePublishedType},
		AppKeys:       []string{"echo"},
		Enabled:       true,
		CreatedBy:     "test",
	}); err != nil {
		t.Fatal(err)
	}
	actor := "operator@example.test"
	if _, err := producerStore.PublishRelease(ctx, contract.Deployment{
		Workspace:   "default",
		GitSourceID: "source-a",
		App:         "echo",
		Commit:      "commit-a",
		Entrypoint:  "main.py",
		ObjectURI:   "bundle://default/source-a/commit-a",
		Actions:     map[string]contract.Action{"run": {Action: "run"}},
		CreatedBy:   &actor,
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	restartedStore := state.NewLocalStore(statePath)
	restartedStore.ConfigureInputCrypto("dispatcher-integration-secret", "")
	dispatcher := webhook.Dispatcher{
		Store:    restartedStore,
		Sender:   webhook.NewHTTPSender(webhook.SenderConfig{Policy: webhook.EgressPolicy{AllowInsecureLoopback: true}}),
		WorkerID: "dispatcher-after-restart",
	}
	processed, err := dispatcher.ProcessOne(ctx)
	if err != nil || !processed {
		t.Fatalf("ProcessOne() = %v, %v", processed, err)
	}
	select {
	case headers := <-received:
		if headers.eventID == "" || headers.eventType != controlevent.ReleasePublishedType {
			t.Fatalf("receiver headers = %#v", headers)
		}
	case <-time.After(time.Second):
		t.Fatal("receiver was not called")
	}
	processed, err = dispatcher.ProcessOne(ctx)
	if err != nil || processed {
		t.Fatalf("second ProcessOne() = %v, %v", processed, err)
	}
	snapshot, err := restartedStore.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.WebhookDeliveries) != 1 {
		t.Fatalf("deliveries = %#v", snapshot.WebhookDeliveries)
	}
	for _, delivery := range snapshot.WebhookDeliveries {
		if delivery.State != webhook.DeliverySucceeded || delivery.Attempt != 1 {
			t.Fatalf("persisted delivery = %#v", delivery)
		}
	}
}
