package webhook

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var deliveryLatencyBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type metricKey struct {
	EventType string
	Value     string
}

type latencyHistogram struct {
	Buckets []uint64
	Count   uint64
	Sum     float64
}

type Metrics struct {
	mu         sync.RWMutex
	deliveries map[metricKey]uint64
	attempts   map[metricKey]uint64
	latency    map[string]*latencyHistogram
}

func NewMetrics() *Metrics {
	return &Metrics{
		deliveries: map[metricKey]uint64{},
		attempts:   map[metricKey]uint64{},
		latency:    map[string]*latencyHistogram{},
	}
}

func (metrics *Metrics) ObserveAttempt(eventType string, attemptOutcome AttemptOutcome, deliveryState DeliveryState, latency time.Duration) {
	if metrics == nil {
		return
	}
	eventType = normalizedMetricValue(eventType)
	outcome := normalizedMetricValue(string(attemptOutcome))
	state := normalizedMetricValue(string(deliveryState))
	seconds := latency.Seconds()
	if seconds < 0 {
		seconds = 0
	}

	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	metrics.attempts[metricKey{EventType: eventType, Value: outcome}]++
	metrics.deliveries[metricKey{EventType: eventType, Value: state}]++
	histogram := metrics.latency[eventType]
	if histogram == nil {
		histogram = &latencyHistogram{Buckets: make([]uint64, len(deliveryLatencyBuckets))}
		metrics.latency[eventType] = histogram
	}
	for index, bucket := range deliveryLatencyBuckets {
		if seconds <= bucket {
			histogram.Buckets[index]++
		}
	}
	histogram.Count++
	histogram.Sum += seconds
}

func (metrics *Metrics) Handler(statsProvider interface {
	WebhookQueueStats(ctx context.Context) (QueueStats, error)
}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stats, err := statsProvider.WebhookQueueStats(r.Context())
		if err != nil {
			http.Error(w, "webhook metrics unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = fmt.Fprint(w, metrics.render(stats, time.Now().UTC()))
	})
}

func (metrics *Metrics) render(stats QueueStats, now time.Time) string {
	metrics.mu.RLock()
	defer metrics.mu.RUnlock()
	var output strings.Builder
	output.WriteString("# HELP windforce_webhook_deliveries_total Delivery state transitions completed by this process.\n")
	output.WriteString("# TYPE windforce_webhook_deliveries_total counter\n")
	for _, key := range sortedMetricKeys(metrics.deliveries) {
		fmt.Fprintf(&output, "windforce_webhook_deliveries_total{event_type=%q,state=%q} %d\n", escapeMetricLabel(key.EventType), escapeMetricLabel(key.Value), metrics.deliveries[key])
	}
	output.WriteString("# HELP windforce_webhook_delivery_attempts_total Webhook delivery attempts by sender outcome.\n")
	output.WriteString("# TYPE windforce_webhook_delivery_attempts_total counter\n")
	for _, key := range sortedMetricKeys(metrics.attempts) {
		fmt.Fprintf(&output, "windforce_webhook_delivery_attempts_total{event_type=%q,outcome=%q} %d\n", escapeMetricLabel(key.EventType), escapeMetricLabel(key.Value), metrics.attempts[key])
	}
	output.WriteString("# HELP windforce_webhook_delivery_latency_seconds Webhook receiver request latency.\n")
	output.WriteString("# TYPE windforce_webhook_delivery_latency_seconds histogram\n")
	eventTypes := make([]string, 0, len(metrics.latency))
	for eventType := range metrics.latency {
		eventTypes = append(eventTypes, eventType)
	}
	sort.Strings(eventTypes)
	for _, eventType := range eventTypes {
		histogram := metrics.latency[eventType]
		for index, bucket := range deliveryLatencyBuckets {
			fmt.Fprintf(&output, "windforce_webhook_delivery_latency_seconds_bucket{event_type=%q,le=%q} %d\n", escapeMetricLabel(eventType), strconv.FormatFloat(bucket, 'g', -1, 64), histogram.Buckets[index])
		}
		fmt.Fprintf(&output, "windforce_webhook_delivery_latency_seconds_bucket{event_type=%q,le=%q} %d\n", escapeMetricLabel(eventType), "+Inf", histogram.Count)
		fmt.Fprintf(&output, "windforce_webhook_delivery_latency_seconds_sum{event_type=%q} %s\n", escapeMetricLabel(eventType), strconv.FormatFloat(histogram.Sum, 'g', -1, 64))
		fmt.Fprintf(&output, "windforce_webhook_delivery_latency_seconds_count{event_type=%q} %d\n", escapeMetricLabel(eventType), histogram.Count)
	}
	output.WriteString("# HELP windforce_webhook_pending_deliveries Non-terminal webhook deliveries waiting or leased.\n")
	output.WriteString("# TYPE windforce_webhook_pending_deliveries gauge\n")
	fmt.Fprintf(&output, "windforce_webhook_pending_deliveries %d\n", stats.PendingCount)
	output.WriteString("# HELP windforce_webhook_oldest_pending_seconds Age of the oldest non-terminal webhook delivery.\n")
	output.WriteString("# TYPE windforce_webhook_oldest_pending_seconds gauge\n")
	age := 0.0
	if stats.OldestPending != nil && now.After(*stats.OldestPending) {
		age = now.Sub(*stats.OldestPending).Seconds()
	}
	fmt.Fprintf(&output, "windforce_webhook_oldest_pending_seconds %s\n", strconv.FormatFloat(age, 'g', -1, 64))
	return output.String()
}

func sortedMetricKeys(values map[metricKey]uint64) []metricKey {
	keys := make([]metricKey, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool {
		if keys[left].EventType == keys[right].EventType {
			return keys[left].Value < keys[right].Value
		}
		return keys[left].EventType < keys[right].EventType
	})
	return keys
}

func normalizedMetricValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return value
}

func escapeMetricLabel(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	return strings.ReplaceAll(value, `"`, `\"`)
}
