package webhook

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	controlevent "github.com/imprun/windforce-lite/internal/event"
)

type DeliveryState string

const (
	DeliveryPending    DeliveryState = "pending"
	DeliveryDelivering DeliveryState = "delivering"
	DeliveryRetrying   DeliveryState = "retrying"
	DeliverySucceeded  DeliveryState = "succeeded"
	DeliveryFailed     DeliveryState = "failed"
	DeliveryCanceled   DeliveryState = "canceled"
)

var (
	ErrNotFound          = errors.New("webhook resource not found")
	ErrConflict          = errors.New("webhook resource conflict")
	ErrInvalid           = errors.New("invalid webhook resource")
	ErrNoPendingDelivery = errors.New("no pending webhook delivery")
	ErrInvalidLease      = errors.New("invalid webhook delivery lease")
)

type Subscription struct {
	ID            string     `json:"id"`
	WorkspaceID   string     `json:"workspace_id"`
	Name          string     `json:"name"`
	Endpoint      string     `json:"-"`
	SigningSecret string     `json:"-"`
	EventTypes    []string   `json:"event_types"`
	AppKeys       []string   `json:"app_keys"`
	Enabled       bool       `json:"enabled"`
	CreatedBy     string     `json:"created_by"`
	UpdatedBy     string     `json:"updated_by"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	DeletedAt     *time.Time `json:"deleted_at,omitempty"`
}

type Delivery struct {
	ID             string        `json:"id"`
	WorkspaceID    string        `json:"workspace_id"`
	EventID        string        `json:"event_id"`
	SubscriptionID string        `json:"subscription_id"`
	State          DeliveryState `json:"state"`
	Attempt        int           `json:"attempt"`
	NextAttemptAt  time.Time     `json:"next_attempt_at"`
	LeaseOwner     *string       `json:"lease_owner,omitempty"`
	LeaseExpiresAt *time.Time    `json:"lease_expires_at,omitempty"`
	ResponseStatus *int          `json:"response_status,omitempty"`
	LatencyMillis  *int64        `json:"latency_ms,omitempty"`
	ErrorSummary   *string       `json:"error_summary,omitempty"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
	CompletedAt    *time.Time    `json:"completed_at,omitempty"`
}

type DeliveryLease struct {
	DeliveryID string
	WorkerID   string
	Attempt    int
	ExpiresAt  time.Time
}

type ClaimedDelivery struct {
	Delivery     Delivery
	Event        controlevent.Envelope
	Subscription Subscription
	Lease        DeliveryLease
}

type DeliveryResult struct {
	State          DeliveryState
	NextAttemptAt  time.Time
	ResponseStatus *int
	LatencyMillis  *int64
	ErrorSummary   *string
	CompletedAt    time.Time
}

type Store interface {
	ListSubscriptions(ctx context.Context, workspaceID string) ([]Subscription, error)
	GetSubscription(ctx context.Context, workspaceID string, subscriptionID string) (Subscription, error)
	CreateSubscription(ctx context.Context, subscription Subscription) (Subscription, error)
	UpdateSubscription(ctx context.Context, subscription Subscription) (Subscription, error)
	DeleteSubscription(ctx context.Context, workspaceID string, subscriptionID string, actor string) error
	ClaimDelivery(ctx context.Context, workerID string, leaseTTL time.Duration) (*ClaimedDelivery, error)
	CompleteDelivery(ctx context.Context, lease DeliveryLease, result DeliveryResult) error
	RetryDelivery(ctx context.Context, workspaceID string, deliveryID string, actor string) error
}

func ValidateSubscription(subscription Subscription) error {
	if strings.TrimSpace(subscription.Name) == "" {
		return invalid("name is required")
	}
	if _, err := ValidateEndpoint(subscription.Endpoint, false); err != nil {
		return err
	}
	if len(subscription.SigningSecret) < 16 {
		return invalid("signing secret must be at least 16 characters")
	}
	if len(subscription.EventTypes) == 0 {
		return invalid("at least one event type is required")
	}
	for _, eventType := range subscription.EventTypes {
		if eventType != controlevent.ReleasePublishedType {
			return invalid("unsupported event type %q", eventType)
		}
	}
	return nil
}

func NormalizeFilters(subscription *Subscription) {
	subscription.Name = strings.TrimSpace(subscription.Name)
	subscription.EventTypes = uniqueSorted(subscription.EventTypes)
	subscription.AppKeys = uniqueSorted(subscription.AppKeys)
}

func Matches(subscription Subscription, eventType string, appKey string) bool {
	if !subscription.Enabled || subscription.DeletedAt != nil {
		return false
	}
	if !contains(subscription.EventTypes, eventType) {
		return false
	}
	return len(subscription.AppKeys) == 0 || contains(subscription.AppKeys, appKey)
}

func ValidateEndpoint(raw string, allowHTTP bool) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, invalid("endpoint: %v", err)
	}
	if parsed.Scheme != "https" && !(allowHTTP && parsed.Scheme == "http") {
		return nil, invalid("endpoint must use HTTPS")
	}
	if parsed.Hostname() == "" {
		return nil, invalid("endpoint host is required")
	}
	if parsed.User != nil {
		return nil, invalid("endpoint credentials are not allowed")
	}
	if parsed.Fragment != "" {
		return nil, invalid("endpoint fragment is not allowed")
	}
	return parsed, nil
}

func EndpointSummary(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "invalid endpoint"
	}
	return parsed.Scheme + "://" + parsed.Host
}

func ValidateDeliveryResult(result DeliveryResult) error {
	switch result.State {
	case DeliverySucceeded, DeliveryFailed, DeliveryCanceled:
		if result.CompletedAt.IsZero() {
			return invalid("completed_at is required for terminal delivery state")
		}
	case DeliveryRetrying:
		if result.NextAttemptAt.IsZero() {
			return invalid("next_attempt_at is required for retrying delivery")
		}
	default:
		return invalid("unsupported completion state %q", result.State)
	}
	return nil
}

func uniqueSorted(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}
