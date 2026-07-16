package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/imprun/windforce-lite/internal/contract"
	controlevent "github.com/imprun/windforce-lite/internal/event"
	"github.com/imprun/windforce-lite/internal/state"
	internalwebhook "github.com/imprun/windforce-lite/internal/webhook"
	webhookcontract "github.com/imprun/windforce-lite/pkg/webhook"
)

const receiverTestSecret = "0123456789abcdef0123456789abcdef"

func TestReleasePublicationReachesVerifiedIdempotentReceiver(t *testing.T) {
	receiver := newReceiver(receiverTestSecret, 5*time.Minute, 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := httptest.NewServer(receiver)
	defer server.Close()
	store := receiverTestStore(t, server.URL+"/webhook")
	note := "Publish verified checkout release"
	actor := "operator@example.test"
	if _, err := store.PublishRelease(context.Background(), receiverTestDeployment("commit-current", actor, note), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	dispatcher := receiverTestDispatcher(store)
	processed, err := dispatcher.ProcessOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("ProcessOne() = %v, %v", processed, err)
	}

	events := receiver.eventsSnapshot()
	if len(events) != 1 {
		t.Fatalf("accepted events = %#v", events)
	}
	accepted := events[0]
	if accepted.EventType != controlevent.ReleasePublishedType || accepted.AppKey != "checkout" || accepted.Commit != "commit-current" || accepted.Actor != actor || accepted.Note == nil || *accepted.Note != note {
		t.Fatalf("accepted release = %#v", accepted)
	}
	snapshot, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.WebhookDeliveries) != 1 {
		t.Fatalf("deliveries = %#v", snapshot.WebhookDeliveries)
	}
	for _, delivery := range snapshot.WebhookDeliveries {
		if delivery.State != internalwebhook.DeliverySucceeded {
			t.Fatalf("delivery state = %q", delivery.State)
		}
	}

	var event controlevent.Envelope
	for _, candidate := range snapshot.ControlPlaneEvents {
		event = candidate
	}
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	request := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/cloudevents+json")
	request.Header.Set(webhookcontract.HeaderEventID, event.ID)
	request.Header.Set(webhookcontract.HeaderEventType, event.Type)
	request.Header.Set(webhookcontract.HeaderDelivery, "whd_replay")
	request.Header.Set(webhookcontract.HeaderTimestamp, webhookcontract.TimestampValue(now))
	request.Header.Set(webhookcontract.HeaderSignature, webhookcontract.Sign(receiverTestSecret, request.Header.Get(webhookcontract.HeaderTimestamp), body))
	response := httptest.NewRecorder()
	receiver.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("X-Windforce-Duplicate") != "true" {
		t.Fatalf("duplicate response = %d %#v %s", response.Code, response.Header(), response.Body.String())
	}
	if len(receiver.eventsSnapshot()) != 1 {
		t.Fatalf("duplicate produced another accepted event: %#v", receiver.eventsSnapshot())
	}
}

func TestReceiverRejectsUnsignedOrMismatchedRequests(t *testing.T) {
	receiver := newReceiver(receiverTestSecret, 5*time.Minute, 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader([]byte(`{}`)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	receiver.ServeHTTP(response, request)
	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("content type status = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader([]byte(`{}`)))
	request.Header.Set("Content-Type", "application/cloudevents+json")
	response = httptest.NewRecorder()
	receiver.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned status = %d", response.Code)
	}
}

func TestRetryableReceiverFailureLeavesDeliveryRetryingThenSucceeds(t *testing.T) {
	receiver := newReceiver(receiverTestSecret, 5*time.Minute, 1, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := httptest.NewServer(receiver)
	defer server.Close()
	store := receiverTestStore(t, server.URL+"/webhook")
	if _, err := store.PublishRelease(context.Background(), receiverTestDeployment("commit-retry", "operator@example.test", "Retry receiver"), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	dispatcher := receiverTestDispatcher(store)
	processed, err := dispatcher.ProcessOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("first ProcessOne() = %v, %v", processed, err)
	}
	snapshot, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, delivery := range snapshot.WebhookDeliveries {
		if delivery.State != internalwebhook.DeliveryRetrying {
			t.Fatalf("delivery state after 503 = %q", delivery.State)
		}
	}
	if len(receiver.eventsSnapshot()) != 0 {
		t.Fatalf("failed request was accepted: %#v", receiver.eventsSnapshot())
	}
	time.Sleep(5 * time.Millisecond)
	processed, err = dispatcher.ProcessOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("second ProcessOne() = %v, %v", processed, err)
	}
	if len(receiver.eventsSnapshot()) != 1 {
		t.Fatalf("retry was not accepted: %#v", receiver.eventsSnapshot())
	}
}

func receiverTestStore(t *testing.T, endpoint string) *state.LocalStore {
	t.Helper()
	store := state.NewLocalStore(filepath.Join(t.TempDir(), "state.json"))
	store.ConfigureInputCrypto("receiver-test-encryption-key", "")
	if _, err := store.CreateSubscription(context.Background(), internalwebhook.Subscription{
		WorkspaceID:   "default",
		Name:          "Generic connector receiver",
		Endpoint:      endpoint,
		SigningSecret: receiverTestSecret,
		EventTypes:    []string{controlevent.ReleasePublishedType},
		Enabled:       true,
		CreatedBy:     "test",
	}); err != nil {
		t.Fatal(err)
	}
	return store
}

func receiverTestDeployment(commit string, actor string, note string) contract.Deployment {
	return contract.Deployment{
		Workspace:   "default",
		GitSourceID: "source-checkout",
		App:         "checkout",
		Commit:      commit,
		Entrypoint:  "main.py",
		ObjectURI:   "bundle://default/source-checkout/" + commit,
		Actions:     map[string]contract.Action{"run": {Action: "run"}},
		CreatedBy:   &actor,
		Message:     &note,
	}
}

func receiverTestDispatcher(store *state.LocalStore) *internalwebhook.Dispatcher {
	return &internalwebhook.Dispatcher{
		Store: store,
		Sender: internalwebhook.NewHTTPSender(internalwebhook.SenderConfig{
			Policy:         internalwebhook.EgressPolicy{AllowInsecureLoopback: true},
			RequestTimeout: time.Second,
		}),
		WorkerID:    "connector-contract-test",
		LeaseTTL:    2 * time.Second,
		MaxAttempts: 3,
		BackoffBase: time.Millisecond,
		BackoffMax:  time.Millisecond,
	}
}
