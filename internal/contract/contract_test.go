package contract

import (
	"reflect"
	"testing"
)

func TestValidAppKeyMatchesCanonicalCatalogRules(t *testing.T) {
	valid := []string{"greet", "a1", "my_app"}
	invalid := []string{"", "a", " Greet", "greet ", "1greet", "my-app", "my.app", "with space"}
	for _, value := range valid {
		if !ValidAppKey(value) {
			t.Fatalf("ValidAppKey(%q) = false, want true", value)
		}
	}
	for _, value := range invalid {
		if ValidAppKey(value) {
			t.Fatalf("ValidAppKey(%q) = true, want false", value)
		}
	}
}

func TestValidActionKeyMatchesCanonicalCatalogRules(t *testing.T) {
	valid := []string{"hello", "a", "approval.sync", "a.b.c.d.e.f.g.h", "sync_now"}
	invalid := []string{"", " hello", "hello ", "Hello", "a..b", ".a", "a.", "hel-lo", "a.b.c.d.e.f.g.h.i"}
	for _, value := range valid {
		if !ValidActionKey(value) {
			t.Fatalf("ValidActionKey(%q) = false, want true", value)
		}
	}
	for _, value := range invalid {
		if ValidActionKey(value) {
			t.Fatalf("ValidActionKey(%q) = true, want false", value)
		}
	}
}

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

func TestCapabilityTagConflictMatchesCanonicalEnqueueRule(t *testing.T) {
	caps := []string{"browser"}
	appOverride := "app-blue"
	actionTag := "action-main"
	actionOverride := "action-fast"

	for _, test := range []struct {
		name           string
		appTag         string
		appOverride    *string
		actionTag      *string
		actionOverride *string
		want           bool
	}{
		{name: "default tag", appTag: DefaultRouteTag, want: false},
		{name: "empty tag", appTag: "", want: false},
		{name: "manifest app tag", appTag: "app-main", want: true},
		{name: "app override", appTag: DefaultRouteTag, appOverride: &appOverride, want: true},
		{name: "action tag", appTag: DefaultRouteTag, actionTag: &actionTag, want: true},
		{name: "action override", appTag: DefaultRouteTag, actionOverride: &actionOverride, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := CapabilityTagConflict(test.appTag, test.appOverride, test.actionTag, test.actionOverride, caps)
			if err != nil {
				t.Fatalf("CapabilityTagConflict returned error: %v", err)
			}
			if got != test.want {
				t.Fatalf("CapabilityTagConflict = %v, want %v", got, test.want)
			}
		})
	}

	got, err := CapabilityTagConflict("app-main", &appOverride, &actionTag, &actionOverride, nil)
	if err != nil {
		t.Fatalf("CapabilityTagConflict without caps returned error: %v", err)
	}
	if got {
		t.Fatalf("CapabilityTagConflict without capability route = true, want false")
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
