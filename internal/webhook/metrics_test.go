package webhook

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type metricsStatsProvider struct {
	stats QueueStats
	err   error
}

func (provider metricsStatsProvider) WebhookQueueStats(context.Context) (QueueStats, error) {
	return provider.stats, provider.err
}

func TestWebhookMetricsExposeBoundedLabelsAndQueueHealth(t *testing.T) {
	metrics := NewMetrics()
	metrics.ObserveAttempt("windforce.release.published.v1", AttemptRetry, DeliveryRetrying, 125*time.Millisecond)
	oldest := time.Now().UTC().Add(-2 * time.Minute)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metrics.Handler(metricsStatsProvider{stats: QueueStats{PendingCount: 3, OldestPending: &oldest}}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, expected := range []string{
		`windforce_webhook_deliveries_total{event_type="windforce.release.published.v1",state="retrying"} 1`,
		`windforce_webhook_delivery_attempts_total{event_type="windforce.release.published.v1",outcome="retry"} 1`,
		`windforce_webhook_delivery_latency_seconds_count{event_type="windforce.release.published.v1"} 1`,
		"windforce_webhook_pending_deliveries 3",
		"windforce_webhook_oldest_pending_seconds ",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("metrics missing %q:\n%s", expected, body)
		}
	}
	for _, forbidden := range []string{"endpoint=", "subscription_id=", "delivery_id=", "app=", "secret", "hooks.example.test"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("metrics contains forbidden value %q:\n%s", forbidden, body)
		}
	}
}

func TestWebhookMetricsReturnUnavailableWhenQueueStatsFail(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	NewMetrics().Handler(metricsStatsProvider{err: context.Canceled}).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
}
