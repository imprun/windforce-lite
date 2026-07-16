package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	controlevent "github.com/imprun/windforce-lite/internal/event"
)

func TestHTTPSenderSignsStableCloudEventAndClassifiesResponse(t *testing.T) {
	now := time.Date(2026, 7, 16, 15, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	var bodies [][]byte
	var headers []http.Header
	requestCount := 0
	receiver := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
		}
		mu.Lock()
		bodies = append(bodies, body)
		headers = append(headers, request.Header.Clone())
		requestCount++
		current := requestCount
		mu.Unlock()
		if current == 1 {
			response.Header().Set("Retry-After", "120")
			response.WriteHeader(http.StatusTooManyRequests)
			_, _ = response.Write(make([]byte, 128<<10))
			return
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()

	claimed := testClaimedDelivery(t, receiver.URL)
	sender := NewHTTPSender(SenderConfig{
		Policy: EgressPolicy{AllowInsecureLoopback: true},
		Now:    func() time.Time { return now },
	})
	first := sender.Send(context.Background(), claimed)
	second := sender.Send(context.Background(), claimed)
	if first.Outcome != AttemptRetry || first.ResponseStatus == nil || *first.ResponseStatus != http.StatusTooManyRequests {
		t.Fatalf("first result = %#v", first)
	}
	if first.RetryAt == nil || !first.RetryAt.Equal(now.Add(120*time.Second)) {
		t.Fatalf("retry at = %v", first.RetryAt)
	}
	if second.Outcome != AttemptSucceeded || second.ResponseStatus == nil || *second.ResponseStatus != http.StatusNoContent {
		t.Fatalf("second result = %#v", second)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 || string(bodies[0]) != string(bodies[1]) {
		t.Fatalf("retry bodies differ: %q / %q", bodies[0], bodies[1])
	}
	for index, header := range headers {
		timestamp := header.Get(HeaderTimestamp)
		if timestamp != TimestampValue(now) {
			t.Fatalf("request %d timestamp = %q", index, timestamp)
		}
		if header.Get(HeaderEventID) != claimed.Event.ID || header.Get(HeaderEventType) != claimed.Event.Type || header.Get(HeaderDelivery) != claimed.Delivery.ID {
			t.Fatalf("request %d identity headers = %#v", index, header)
		}
		if !VerifySignature(claimed.Subscription.SigningSecret, timestamp, bodies[index], header.Get(HeaderSignature)) {
			t.Fatalf("request %d signature did not verify", index)
		}
		if header.Get("Content-Type") != "application/cloudevents+json" || header.Get("User-Agent") != "windforce-lite-webhook/dev" {
			t.Fatalf("request %d content headers = %#v", index, header)
		}
	}
}

func TestHTTPSenderDoesNotFollowRedirect(t *testing.T) {
	targetCalls := 0
	receiver := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/target" {
			targetCalls++
			response.WriteHeader(http.StatusNoContent)
			return
		}
		response.Header().Set("Location", "/target")
		response.WriteHeader(http.StatusFound)
	}))
	defer receiver.Close()
	claimed := testClaimedDelivery(t, receiver.URL+"/redirect")
	result := NewHTTPSender(SenderConfig{Policy: EgressPolicy{AllowInsecureLoopback: true}}).Send(context.Background(), claimed)
	if result.Outcome != AttemptTerminal || result.ResponseStatus == nil || *result.ResponseStatus != http.StatusFound {
		t.Fatalf("redirect result = %#v", result)
	}
	if targetCalls != 0 {
		t.Fatalf("redirect target calls = %d", targetCalls)
	}
}

func TestHTTPSenderTimeoutAndSecurityErrorsAreSanitized(t *testing.T) {
	receiver := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		time.Sleep(50 * time.Millisecond)
		response.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	claimed := testClaimedDelivery(t, receiver.URL+"/private?token=must-not-leak")
	timedOut := NewHTTPSender(SenderConfig{
		Policy:         EgressPolicy{AllowInsecureLoopback: true},
		RequestTimeout: 5 * time.Millisecond,
	}).Send(context.Background(), claimed)
	if timedOut.Outcome != AttemptRetry || timedOut.ErrorSummary != "request_timeout" {
		t.Fatalf("timeout result = %#v", timedOut)
	}
	rejected := NewHTTPSender(SenderConfig{Policy: EgressPolicy{}}).Send(context.Background(), claimed)
	if rejected.Outcome != AttemptTerminal || rejected.ErrorSummary != "egress_policy_rejected" {
		t.Fatalf("security result = %#v", rejected)
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 7, 16, 15, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		value string
		want  time.Time
		ok    bool
	}{
		{value: "15", want: now.Add(15 * time.Second), ok: true},
		{value: now.Add(time.Minute).Format(http.TimeFormat), want: now.Add(time.Minute), ok: true},
		{value: "-1"},
		{value: "9223372036854775807"},
		{value: "invalid"},
		{value: now.Add(-time.Minute).Format(http.TimeFormat)},
	} {
		got, ok := ParseRetryAfter(test.value, now)
		if ok != test.ok || (ok && !got.Equal(test.want)) {
			t.Fatalf("ParseRetryAfter(%q) = %v, %v", test.value, got, ok)
		}
	}
}

func TestRetryableStatus(t *testing.T) {
	for _, status := range []int{408, 425, 429, 500, 503, 599} {
		if !retryableStatus(status) {
			t.Errorf("status %d is not retryable", status)
		}
	}
	for _, status := range []int{200, 302, 400, 401, 404, 499, 600} {
		if retryableStatus(status) {
			t.Errorf("status %d is retryable", status)
		}
	}
}

func TestDiscardResponseBodyHonorsLimit(t *testing.T) {
	body := bytes.NewReader(make([]byte, 128<<10))
	if read := discardResponseBody(body, 64<<10); read != 64<<10 {
		t.Fatalf("read bytes = %d", read)
	}
	if body.Len() != 64<<10 {
		t.Fatalf("remaining bytes = %d", body.Len())
	}
}

func testClaimedDelivery(t *testing.T, endpoint string) *ClaimedDelivery {
	t.Helper()
	event, err := controlevent.NewReleasePublished("evt_01h00000000000000000000000", time.Date(2026, 7, 16, 14, 0, 0, 0, time.UTC), controlevent.ReleasePublishedData{
		Workspace: "default",
		AppKey:    "echo",
		ReleaseID: "release-a",
		Commit:    "commit-a",
		Actor:     "operator@example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(event)
	if err != nil || len(raw) == 0 {
		t.Fatalf("event marshal: %v", err)
	}
	return &ClaimedDelivery{
		Delivery: Delivery{ID: "whd_01h00000000000000000000000", EventID: event.ID, Attempt: 1},
		Event:    event,
		Subscription: Subscription{
			ID:            "whs_01h00000000000000000000000",
			Endpoint:      endpoint,
			SigningSecret: "0123456789abcdef0123456789abcdef",
		},
	}
}
