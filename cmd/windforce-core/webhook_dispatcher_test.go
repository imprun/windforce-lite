package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/imprun/windforce-core/internal/contract"
	controlevent "github.com/imprun/windforce-core/internal/event"
	"github.com/imprun/windforce-core/internal/state"
	"github.com/imprun/windforce-core/internal/webhook"
)

func TestRunWebhookDispatcherOnceDeliversPersistedEvent(t *testing.T) {
	received := make(chan struct{}, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get(webhook.HeaderSignature) == "" {
			t.Error("missing webhook signature")
		}
		received <- struct{}{}
		response.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()

	secretKey := "webhook-dispatcher-cli-test-secret"
	t.Setenv("SECRET_KEY", secretKey)
	t.Setenv("WINDFORCE_LITE_WEBHOOK_ALLOW_INSECURE_LOOPBACK", "true")
	statePath := filepath.Join(t.TempDir(), "state.json")
	store := state.NewLocalStore(statePath)
	store.ConfigureInputCrypto(secretKey, "")
	ctx := context.Background()
	if _, err := store.CreateSubscription(ctx, webhook.Subscription{
		WorkspaceID:   "default",
		Name:          "CLI receiver",
		Endpoint:      receiver.URL,
		SigningSecret: "0123456789abcdef0123456789abcdef",
		EventTypes:    []string{controlevent.ReleasePublishedType},
		Enabled:       true,
		CreatedBy:     "test",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishRelease(ctx, contract.Deployment{
		Workspace:   "default",
		GitSourceID: "source-a",
		App:         "echo",
		Commit:      "commit-a",
		Entrypoint:  "main.py",
		ObjectURI:   "bundle://default/source-a/commit-a",
		Actions:     map[string]contract.Action{"run": {Action: "run"}},
	}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	exitCode := runWebhookDispatcher([]string{"--state", statePath, "--once"})
	if exitCode != 0 {
		t.Fatalf("exit code = %d", exitCode)
	}
	select {
	case <-received:
	case <-time.After(time.Second):
		t.Fatal("webhook receiver was not called")
	}
}

func TestNewWebhookDispatcherRejectsUnsafeTiming(t *testing.T) {
	flags := flagSetForWebhookTest(t, 5*time.Second, 5*time.Second)
	if _, err := newWebhookDispatcher(state.NewLocalStore(filepath.Join(t.TempDir(), "state.json")), flags, nil); err == nil {
		t.Fatal("request timeout equal to lease was accepted")
	}
}

func flagSetForWebhookTest(t *testing.T, requestTimeout time.Duration, leaseTTL time.Duration) webhookDispatcherFlags {
	t.Helper()
	dispatchInterval := time.Second
	maxAttempts := 8
	allowedHosts := ""
	allowedCIDRs := ""
	allowedInsecureHTTPHosts := ""
	allowLoopback := false
	workerID := "dispatcher-test"
	return webhookDispatcherFlags{
		dispatchInterval:         &dispatchInterval,
		requestTimeout:           &requestTimeout,
		leaseTTL:                 &leaseTTL,
		maxAttempts:              &maxAttempts,
		allowedHosts:             &allowedHosts,
		allowedCIDRs:             &allowedCIDRs,
		allowedInsecureHTTPHosts: &allowedInsecureHTTPHosts,
		allowInsecureLoopback:    &allowLoopback,
		workerID:                 &workerID,
	}
}
