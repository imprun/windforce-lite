package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/imprun/windforce-lite/internal/state"
	"github.com/imprun/windforce-lite/internal/webhook"
)

func TestCanonicalWebhookLifecycle(t *testing.T) {
	store := state.NewLocalStore(filepath.Join(t.TempDir(), "state.json"))
	store.SecretKey = "test-secret-key"
	server := httptest.NewServer(New(Config{
		Store:      store,
		Catalog:    store,
		EnableAPI:  true,
		AdminToken: "admin-token",
	}))
	defer server.Close()

	do := func(method string, path string, actor string, body string, wantStatus int, target any) []byte {
		t.Helper()
		request, err := http.NewRequest(method, server.URL+path, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer admin-token")
		if actor != "" {
			request.Header.Set("X-Windforce-Actor", actor)
		}
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var payload bytes.Buffer
		if _, err := payload.ReadFrom(response.Body); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != wantStatus {
			t.Fatalf("%s %s status = %d, want %d: %s", method, path, response.StatusCode, wantStatus, payload.String())
		}
		if target != nil && payload.Len() != 0 {
			if err := json.Unmarshal(payload.Bytes(), target); err != nil {
				t.Fatalf("decode %s %s: %v: %s", method, path, err, payload.String())
			}
		}
		return payload.Bytes()
	}

	unauthorized, err := http.Get(server.URL + "/api/w/ws-a/webhooks")
	if err != nil {
		t.Fatal(err)
	}
	unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.StatusCode)
	}

	const endpoint = "https://hooks.example.test/windforce/releases/private-path"
	const suppliedSecret = "supplied-signing-secret-12345"
	var created canonicalWebhookSubscriptionResponse
	createdBody := do(http.MethodPost, "/api/w/ws-a/webhooks", "alice@example.test", `{
  "name":"Operations releases",
  "endpoint":"`+endpoint+`",
  "signing_secret":"`+suppliedSecret+`",
  "app_keys":["APP-A"]
}`, http.StatusCreated, &created)
	if created.Subscription.ID == "" || created.Subscription.EndpointSummary != "https://hooks.example.test" || created.SigningSecret != suppliedSecret {
		t.Fatalf("created = %#v", created)
	}
	if bytes.Contains(createdBody, []byte("private-path")) || bytes.Count(createdBody, []byte(suppliedSecret)) != 1 {
		t.Fatalf("create response exposes endpoint path or repeats secret: %s", createdBody)
	}

	var subscriptions []canonicalWebhookSubscription
	listBody := do(http.MethodGet, "/api/w/ws-a/webhooks", "", "", http.StatusOK, &subscriptions)
	if len(subscriptions) != 1 || subscriptions[0].ID != created.Subscription.ID || !subscriptions[0].HasSigningSecret {
		t.Fatalf("subscriptions = %#v", subscriptions)
	}
	if bytes.Contains(listBody, []byte(suppliedSecret)) || bytes.Contains(listBody, []byte("private-path")) {
		t.Fatalf("list exposes secret endpoint data: %s", listBody)
	}
	do(http.MethodGet, "/api/w/ws-b/webhooks/"+created.Subscription.ID, "", "", http.StatusNotFound, nil)

	webhookPath := "/api/w/ws-a/webhooks/" + created.Subscription.ID
	var rotated canonicalWebhookSubscriptionResponse
	do(http.MethodPatch, webhookPath, "bob@example.test", `{"rotate_signing_secret":true}`, http.StatusOK, &rotated)
	if rotated.SigningSecret == "" || rotated.SigningSecret == suppliedSecret {
		t.Fatalf("rotated secret was not returned once: %#v", rotated)
	}
	getBody := do(http.MethodGet, webhookPath, "", "", http.StatusOK, nil)
	if bytes.Contains(getBody, []byte(rotated.SigningSecret)) || bytes.Contains(getBody, []byte(suppliedSecret)) {
		t.Fatalf("get exposes signing secret: %s", getBody)
	}

	var testDelivery webhook.DeliveryDetail
	do(http.MethodPost, webhookPath+"/test", "carol@example.test", "", http.StatusAccepted, &testDelivery)
	if testDelivery.Event.Type != "windforce.webhook.test" || testDelivery.Delivery.SubscriptionID != created.Subscription.ID {
		t.Fatalf("test delivery = %#v", testDelivery)
	}
	claimed, err := store.ClaimDelivery(context.Background(), "dispatcher-a", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	failure := "receiver returned 503"
	if err := store.CompleteDelivery(context.Background(), claimed.Lease, webhook.DeliveryResult{
		State: webhook.DeliveryFailed, ErrorSummary: &failure, CompletedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	var retried webhook.DeliveryDetail
	do(http.MethodPost, "/api/w/ws-a/webhook-deliveries/"+claimed.Delivery.ID+"/retry", "dave@example.test", "", http.StatusAccepted, &retried)
	if retried.Delivery.State != webhook.DeliveryRetrying {
		t.Fatalf("retried delivery = %#v", retried)
	}

	do(http.MethodPatch, webhookPath, "bob@example.test", `{"enabled":false}`, http.StatusOK, nil)
	do(http.MethodPost, webhookPath+"/test", "carol@example.test", "", http.StatusConflict, nil)
	do(http.MethodPatch, webhookPath, "bob@example.test", `{"enabled":true}`, http.StatusOK, nil)
	claimed, err = store.ClaimDelivery(context.Background(), "dispatcher-b", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteDelivery(context.Background(), claimed.Lease, webhook.DeliveryResult{
		State: webhook.DeliveryFailed, ErrorSummary: &failure, CompletedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	do(http.MethodPatch, webhookPath, "bob@example.test", `{"enabled":false}`, http.StatusOK, nil)
	do(http.MethodPost, "/api/w/ws-a/webhook-deliveries/"+claimed.Delivery.ID+"/retry", "dave@example.test", "", http.StatusConflict, nil)
	do(http.MethodPatch, webhookPath, "bob@example.test", `{"enabled":true}`, http.StatusOK, nil)

	do(http.MethodPost, webhookPath+"/test", "carol@example.test", "", http.StatusAccepted, nil)
	do(http.MethodPost, webhookPath+"/test", "carol@example.test", "", http.StatusAccepted, nil)
	var firstPage canonicalWebhookDeliveryPage
	do(http.MethodGet, webhookPath+"/deliveries?limit=1", "", "", http.StatusOK, &firstPage)
	if len(firstPage.Items) != 1 || firstPage.NextCursor == "" {
		t.Fatalf("first page = %#v", firstPage)
	}
	var secondPage canonicalWebhookDeliveryPage
	do(http.MethodGet, webhookPath+"/deliveries?limit=1&cursor="+url.QueryEscape(firstPage.NextCursor), "", "", http.StatusOK, &secondPage)
	if len(secondPage.Items) != 1 || secondPage.Items[0].Delivery.ID == firstPage.Items[0].Delivery.ID {
		t.Fatalf("second page = %#v", secondPage)
	}

	var auditEvents []canonicalAuditEvent
	auditBody := do(http.MethodGet, "/api/w/ws-a/audit-events?category=webhook&limit=100", "", "", http.StatusOK, &auditEvents)
	if len(auditEvents) < 8 || auditEvents[0].Category != "webhook" {
		t.Fatalf("webhook audit events = %#v", auditEvents)
	}
	if strings.Contains(string(auditBody), suppliedSecret) || strings.Contains(string(auditBody), rotated.SigningSecret) || strings.Contains(string(auditBody), "private-path") {
		t.Fatalf("audit exposes webhook secrets: %s", auditBody)
	}

	do(http.MethodDelete, webhookPath, "erin@example.test", "", http.StatusNoContent, nil)
	do(http.MethodGet, webhookPath, "", "", http.StatusNotFound, nil)
	var deleted []canonicalWebhookSubscription
	do(http.MethodGet, "/api/w/ws-a/webhooks?include_deleted=true", "", "", http.StatusOK, &deleted)
	if len(deleted) != 1 || deleted[0].DeletedAt == nil || deleted[0].Enabled {
		t.Fatalf("deleted subscriptions = %#v", deleted)
	}
}

func TestControlPlaneOpenAPIIncludesWebhookManagement(t *testing.T) {
	server := httptest.NewServer(New(Config{EnableAPI: true}))
	defer server.Close()
	response, err := http.Get(server.URL + "/api/w/default/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var payload bytes.Buffer
	if _, err := payload.ReadFrom(response.Body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", response.StatusCode, payload.String())
	}
	for _, required := range []string{
		`/api/w/{workspace}/webhooks`,
		`/api/w/{workspace}/webhooks/{webhookId}/deliveries`,
		`/api/w/{workspace}/webhook-deliveries/{deliveryId}/retry`,
		`WebhookSubscriptionMutation`,
		`WebhookDeliveryPage`,
		`Generated or rotated secret returned only in this response.`,
	} {
		if !strings.Contains(payload.String(), required) {
			t.Fatalf("OpenAPI is missing %q", required)
		}
	}
}

func TestCanonicalWebhookSubscriptionUsesEmptyJSONArrays(t *testing.T) {
	payload, err := json.Marshal(canonicalWebhookSubscriptionFrom(webhook.Subscription{}))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(payload, []byte(`"event_types":[]`)) || !bytes.Contains(payload, []byte(`"app_keys":[]`)) {
		t.Fatalf("empty webhook scopes must remain JSON arrays: %s", payload)
	}
}
