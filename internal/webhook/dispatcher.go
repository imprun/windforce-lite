package webhook

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultDispatcherLeaseTTL    = 30 * time.Second
	defaultDispatcherMaxAttempts = 8
	defaultBackoffBase           = 5 * time.Second
	defaultBackoffMax            = 24 * time.Hour
)

type AttemptSender interface {
	Send(ctx context.Context, delivery *ClaimedDelivery) AttemptResult
}

type Dispatcher struct {
	Store       Store
	Sender      AttemptSender
	WorkerID    string
	LeaseTTL    time.Duration
	MaxAttempts int
	BackoffBase time.Duration
	BackoffMax  time.Duration
	Now         func() time.Time
	Logger      *slog.Logger
	Metrics     *Metrics
}

func (dispatcher *Dispatcher) ProcessOne(ctx context.Context) (bool, error) {
	if dispatcher.Store == nil || dispatcher.Sender == nil {
		return false, errors.New("webhook dispatcher requires store and sender")
	}
	dispatcher.applyDefaults()
	claimed, err := dispatcher.Store.ClaimDelivery(ctx, dispatcher.WorkerID, dispatcher.LeaseTTL)
	if errors.Is(err, ErrNoPendingDelivery) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	attempt := dispatcher.Sender.Send(ctx, claimed)
	result := dispatcher.deliveryResult(claimed, attempt)
	if err := dispatcher.Store.CompleteDelivery(ctx, claimed.Lease, result); err != nil {
		return true, err
	}
	dispatcher.Metrics.ObserveAttempt(claimed.Event.Type, attempt.Outcome, result.State, attempt.Latency)
	dispatcher.logAttempt(claimed, attempt, result)
	return true, nil
}

func (dispatcher *Dispatcher) RunLoop(ctx context.Context, pollInterval time.Duration) error {
	if pollInterval <= 0 {
		pollInterval = 500 * time.Millisecond
	}
	for {
		processed, err := dispatcher.ProcessOne(ctx)
		if err != nil && ctx.Err() == nil {
			dispatcher.logger().Error("webhook dispatcher iteration failed", "error", err)
		}
		if ctx.Err() != nil {
			return nil
		}
		if processed && err == nil {
			continue
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}

func (dispatcher *Dispatcher) deliveryResult(claimed *ClaimedDelivery, attempt AttemptResult) DeliveryResult {
	now := dispatcher.Now().UTC()
	latencyMillis := attempt.Latency.Milliseconds()
	result := DeliveryResult{
		ResponseStatus: attempt.ResponseStatus,
		LatencyMillis:  &latencyMillis,
	}
	if attempt.ErrorSummary != "" {
		summary := attempt.ErrorSummary
		result.ErrorSummary = &summary
	}
	switch attempt.Outcome {
	case AttemptSucceeded:
		result.State = DeliverySucceeded
		result.CompletedAt = now
	case AttemptRetry:
		if claimed.Delivery.Attempt >= dispatcher.MaxAttempts {
			result.State = DeliveryFailed
			result.CompletedAt = now
			summary := "max_attempts_exceeded"
			result.ErrorSummary = &summary
			break
		}
		result.State = DeliveryRetrying
		delay := RetryDelay(dispatcher.BackoffBase, dispatcher.BackoffMax, claimed.Delivery.Attempt, claimed.Delivery.ID)
		if attempt.RetryAt != nil {
			retryDelay := attempt.RetryAt.Sub(now)
			if retryDelay > delay {
				delay = retryDelay
			}
		}
		if delay > dispatcher.BackoffMax {
			delay = dispatcher.BackoffMax
		}
		if delay < 0 {
			delay = 0
		}
		result.NextAttemptAt = now.Add(delay)
	default:
		result.State = DeliveryFailed
		result.CompletedAt = now
	}
	return result
}

func RetryDelay(base time.Duration, maximum time.Duration, attempt int, seed string) time.Duration {
	if base <= 0 {
		base = defaultBackoffBase
	}
	if maximum <= 0 {
		maximum = defaultBackoffMax
	}
	if base > maximum {
		base = maximum
	}
	if attempt < 1 {
		attempt = 1
	}
	delay := base
	for count := 1; count < attempt && delay < maximum; count++ {
		if delay > maximum/2 {
			delay = maximum
			break
		}
		delay *= 2
	}
	if delay > maximum {
		delay = maximum
	}
	half := delay / 2
	if half == 0 {
		return delay
	}
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(seed))
	_, _ = hash.Write([]byte("/" + strconv.Itoa(attempt)))
	jitter := time.Duration(hash.Sum64() % uint64(half+1))
	return half + jitter
}

func (dispatcher *Dispatcher) applyDefaults() {
	if strings.TrimSpace(dispatcher.WorkerID) == "" {
		hostname, _ := os.Hostname()
		dispatcher.WorkerID = fmt.Sprintf("webhook-%s-%d", firstNonEmptyValue(hostname, "dispatcher"), os.Getpid())
	}
	if dispatcher.LeaseTTL <= 0 {
		dispatcher.LeaseTTL = defaultDispatcherLeaseTTL
	}
	if dispatcher.MaxAttempts <= 0 {
		dispatcher.MaxAttempts = defaultDispatcherMaxAttempts
	}
	if dispatcher.BackoffBase <= 0 {
		dispatcher.BackoffBase = defaultBackoffBase
	}
	if dispatcher.BackoffMax <= 0 {
		dispatcher.BackoffMax = defaultBackoffMax
	}
	if dispatcher.Now == nil {
		dispatcher.Now = time.Now
	}
}

func (dispatcher *Dispatcher) logAttempt(claimed *ClaimedDelivery, attempt AttemptResult, result DeliveryResult) {
	attributes := []any{
		"delivery_id", claimed.Delivery.ID,
		"event_type", claimed.Event.Type,
		"attempt", claimed.Delivery.Attempt,
		"duration_ms", attempt.Latency.Milliseconds(),
		"outcome", result.State,
	}
	if attempt.ResponseStatus != nil {
		attributes = append(attributes, "response_status", *attempt.ResponseStatus)
	}
	dispatcher.logger().Info("webhook delivery attempted", attributes...)
}

func (dispatcher *Dispatcher) logger() *slog.Logger {
	if dispatcher.Logger != nil {
		return dispatcher.Logger
	}
	return slog.Default()
}

func firstNonEmptyValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
