package contract

import "testing"

func TestEffectiveRouteTagPrecedence(t *testing.T) {
	appOverride := "app-blue"
	actionTag := "action-main"
	actionOverride := "action-fast"

	if got := EffectiveRouteTag("app-main", &appOverride, &actionTag, &actionOverride); got != "action-fast" {
		t.Fatalf("action override tag = %q, want action-fast", got)
	}
	if got := EffectiveRouteTag("app-main", &appOverride, &actionTag, nil); got != "action-main" {
		t.Fatalf("action manifest tag = %q, want action-main", got)
	}
	if got := EffectiveRouteTag("app-main", &appOverride, nil, nil); got != "app-blue" {
		t.Fatalf("app override tag = %q, want app-blue", got)
	}
	if got := EffectiveRouteTag("app-main", nil, nil, nil); got != "app-main" {
		t.Fatalf("app manifest tag = %q, want app-main", got)
	}
	if got := EffectiveRouteTag("", nil, nil, nil); got != DefaultRouteTag {
		t.Fatalf("default tag = %q, want %q", got, DefaultRouteTag)
	}
}
