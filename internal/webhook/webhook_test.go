package webhook

import (
	"errors"
	"testing"

	controlevent "github.com/imprun/windforce-lite/internal/event"
)

func TestValidateAndMatchSubscription(t *testing.T) {
	subscription := Subscription{
		Name:          "  Operations  ",
		Endpoint:      "https://hooks.example.test/services/private?token=secret",
		SigningSecret: "0123456789abcdef",
		EventTypes:    []string{controlevent.ReleasePublishedType, controlevent.ReleasePublishedType},
		AppKeys:       []string{"checkout", "checkout", "orders"},
		Enabled:       true,
	}
	NormalizeFilters(&subscription)
	if err := ValidateSubscription(subscription); err != nil {
		t.Fatal(err)
	}
	if len(subscription.EventTypes) != 1 || len(subscription.AppKeys) != 2 {
		t.Fatalf("normalized filters = %#v", subscription)
	}
	if !Matches(subscription, controlevent.ReleasePublishedType, "checkout") || Matches(subscription, controlevent.ReleasePublishedType, "unknown") {
		t.Fatalf("subscription matching failed: %#v", subscription)
	}
	if got := EndpointSummary(subscription.Endpoint); got != "https://hooks.example.test" {
		t.Fatalf("endpoint summary = %q", got)
	}
}

func TestValidateSubscriptionRejectsUnsafeValues(t *testing.T) {
	tests := []Subscription{
		{Name: "test", Endpoint: "http://hooks.example.test", SigningSecret: "0123456789abcdef", EventTypes: []string{controlevent.ReleasePublishedType}},
		{Name: "test", Endpoint: "https://user:pass@hooks.example.test", SigningSecret: "0123456789abcdef", EventTypes: []string{controlevent.ReleasePublishedType}},
		{Name: "test", Endpoint: "https://hooks.example.test", SigningSecret: "short", EventTypes: []string{controlevent.ReleasePublishedType}},
		{Name: "test", Endpoint: "https://hooks.example.test", SigningSecret: "0123456789abcdef", EventTypes: []string{"unknown"}},
	}
	for _, test := range tests {
		if err := ValidateSubscription(test); !errors.Is(err, ErrInvalid) {
			t.Fatalf("ValidateSubscription(%#v) error = %v", test, err)
		}
	}
}
