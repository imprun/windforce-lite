package contract

import (
	"reflect"
	"testing"
)

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

func TestCapabilitiesReduceRouteTag(t *testing.T) {
	caps, err := NormalizeCapabilities([]string{"browser", "browser"})
	if err != nil {
		t.Fatalf("NormalizeCapabilities returned error: %v", err)
	}
	if !reflect.DeepEqual(caps, []string{"browser"}) {
		t.Fatalf("capabilities = %#v, want [browser]", caps)
	}

	deployment := Deployment{Tag: "default", RequiredCapabilities: caps}
	action := Action{}
	if got := EffectiveRouteTagForAction(deployment, action); got != "browser" {
		t.Fatalf("capability route tag = %q, want browser", got)
	}

	clear := []string{}
	action.Capabilities = &clear
	if got := EffectiveRouteTagForAction(deployment, action); got != "default" {
		t.Fatalf("cleared action capability route tag = %q, want default", got)
	}
}

func TestNormalizeCapabilitiesRejectsUnsupported(t *testing.T) {
	if _, err := NormalizeCapabilities([]string{"gpu"}); err == nil {
		t.Fatalf("expected unsupported capability error")
	}
}

func TestValidateSourceSubpathPreservesCanonicalRawPath(t *testing.T) {
	if err := ValidateSourceSubpath("apps/echo"); err != nil {
		t.Fatalf("ValidateSourceSubpath returned error: %v", err)
	}
}

func TestValidateSourceSubpathRejectsEscapingPaths(t *testing.T) {
	for _, subpath := range []string{"/apps/echo", "../apps/echo", "apps/../echo"} {
		if err := ValidateSourceSubpath(subpath); err == nil {
			t.Fatalf("ValidateSourceSubpath(%q) unexpectedly passed", subpath)
		}
	}
}
