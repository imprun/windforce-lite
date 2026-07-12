package server

import (
	"encoding/base64"
	"net/http/httptest"
	"testing"
)

func TestRequestActorSubjectUsesUTF8Header(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/w/default/git_sources/1/deploy", nil)
	req.Header.Set("X-Windforce-Actor-Utf8", base64.StdEncoding.EncodeToString([]byte("홍길동")))

	if got := requestActorSubject(req); got != "홍길동" {
		t.Fatalf("requestActorSubject() = %q, want %q", got, "홍길동")
	}
}

func TestRequestActorSubjectFallsBackToLegacyHeader(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/w/default/git_sources/1/deploy", nil)
	req.Header.Set("X-Windforce-Actor-Utf8", "not-base64")
	req.Header.Set("X-Windforce-Actor", "operator@example.test")

	if got := requestActorSubject(req); got != "operator@example.test" {
		t.Fatalf("requestActorSubject() = %q, want legacy actor", got)
	}
}
