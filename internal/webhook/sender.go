package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type AttemptOutcome string

const (
	AttemptSucceeded AttemptOutcome = "success"
	AttemptRetry     AttemptOutcome = "retry"
	AttemptTerminal  AttemptOutcome = "terminal"
)

const defaultResponseBodyLimit int64 = 64 << 10

type AttemptResult struct {
	Outcome        AttemptOutcome
	ResponseStatus *int
	Latency        time.Duration
	RetryAt        *time.Time
	ErrorSummary   string
}

type SenderConfig struct {
	Policy            EgressPolicy
	RequestTimeout    time.Duration
	ResponseBodyLimit int64
	UserAgent         string
	Now               func() time.Time
}

type HTTPSender struct {
	config SenderConfig
}

func NewHTTPSender(config SenderConfig) *HTTPSender {
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = 10 * time.Second
	}
	if config.ResponseBodyLimit <= 0 {
		config.ResponseBodyLimit = defaultResponseBodyLimit
	}
	if strings.TrimSpace(config.UserAgent) == "" {
		config.UserAgent = "windforce-lite-webhook/dev"
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &HTTPSender{config: config}
}

func (sender *HTTPSender) Send(ctx context.Context, claimed *ClaimedDelivery) AttemptResult {
	started := time.Now()
	finish := func(result AttemptResult) AttemptResult {
		result.Latency = time.Since(started)
		return result
	}
	if claimed == nil || claimed.Subscription.SigningSecret == "" {
		return finish(AttemptResult{Outcome: AttemptTerminal, ErrorSummary: "signing_secret_missing"})
	}
	body, err := json.Marshal(claimed.Event)
	if err != nil {
		return finish(AttemptResult{Outcome: AttemptTerminal, ErrorSummary: "event_encoding_failed"})
	}
	attemptContext, cancel := context.WithTimeout(ctx, sender.config.RequestTimeout)
	defer cancel()
	endpoint, err := sender.config.Policy.ResolveEndpoint(attemptContext, claimed.Subscription.Endpoint)
	if err != nil {
		if errors.Is(err, ErrEgressPolicy) {
			return finish(AttemptResult{Outcome: AttemptTerminal, ErrorSummary: "egress_policy_rejected"})
		}
		return finish(AttemptResult{Outcome: AttemptRetry, ErrorSummary: "endpoint_resolution_failed"})
	}
	timestamp := TimestampValue(sender.config.Now())
	request, err := http.NewRequestWithContext(attemptContext, http.MethodPost, endpoint.URL.String(), bytes.NewReader(body))
	if err != nil {
		return finish(AttemptResult{Outcome: AttemptTerminal, ErrorSummary: "request_creation_failed"})
	}
	request.Header.Set("Content-Type", "application/cloudevents+json")
	request.Header.Set("User-Agent", sender.config.UserAgent)
	request.Header.Set(HeaderEventID, claimed.Event.ID)
	request.Header.Set(HeaderEventType, claimed.Event.Type)
	request.Header.Set(HeaderDelivery, claimed.Delivery.ID)
	request.Header.Set(HeaderTimestamp, timestamp)
	request.Header.Set(HeaderSignature, Sign(claimed.Subscription.SigningSecret, timestamp, body))

	transport := endpoint.NewTransport(sender.config.Policy)
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(attemptContext.Err(), context.DeadlineExceeded) {
			return finish(AttemptResult{Outcome: AttemptRetry, ErrorSummary: "request_timeout"})
		}
		return finish(AttemptResult{Outcome: AttemptRetry, ErrorSummary: "network_error"})
	}
	defer response.Body.Close()
	discardResponseBody(response.Body, sender.config.ResponseBodyLimit)
	status := response.StatusCode
	result := AttemptResult{ResponseStatus: &status}
	switch {
	case status >= 200 && status < 300:
		result.Outcome = AttemptSucceeded
	case retryableStatus(status):
		result.Outcome = AttemptRetry
		result.ErrorSummary = "http_" + strconv.Itoa(status)
		if retryAt, ok := ParseRetryAfter(response.Header.Get("Retry-After"), sender.config.Now()); ok {
			result.RetryAt = &retryAt
		}
	default:
		result.Outcome = AttemptTerminal
		result.ErrorSummary = "http_" + strconv.Itoa(status)
	}
	return finish(result)
}

func discardResponseBody(body io.Reader, limit int64) int64 {
	read, _ := io.Copy(io.Discard, io.LimitReader(body, limit))
	return read
}

func ParseRetryAfter(raw string, now time.Time) (time.Time, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, false
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		const maximumDurationSeconds = int64(^uint64(0)>>1) / int64(time.Second)
		if seconds < 0 || seconds > maximumDurationSeconds {
			return time.Time{}, false
		}
		return now.UTC().Add(time.Duration(seconds) * time.Second), true
	}
	parsed, err := http.ParseTime(value)
	if err != nil || !parsed.After(now) {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

func retryableStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == 425 || status == http.StatusTooManyRequests || (status >= 500 && status <= 599)
}
