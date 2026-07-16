package webhook

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type signatureFixture struct {
	Secret    string `json:"secret"`
	Timestamp string `json:"timestamp"`
	Body      string `json:"body"`
	Signature string `json:"signature"`
}

func TestSignatureFixtureAndRequestVerification(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "contracts", "webhooks", "v1", "signature.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture signatureFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	body := []byte(fixture.Body)
	if got := Sign(fixture.Secret, fixture.Timestamp, body); got != fixture.Signature {
		t.Fatalf("signature = %q, want %q", got, fixture.Signature)
	}
	timestampSeconds, err := time.Parse(time.RFC3339, "2026-07-16T10:05:00Z")
	if err != nil {
		t.Fatal(err)
	}
	header := http.Header{
		HeaderEventID:   []string{"evt_0123456789abcdef"},
		HeaderEventType: []string{"windforce.release.published"},
		HeaderDelivery:  []string{"whd_0123456789abcdef"},
		HeaderTimestamp: []string{fixture.Timestamp},
		HeaderSignature: []string{fixture.Signature},
	}
	verified, err := (Verifier{Secret: fixture.Secret, Now: func() time.Time { return timestampSeconds }}).Verify(header, body)
	if err != nil {
		t.Fatal(err)
	}
	if verified.EventID != "evt_0123456789abcdef" || verified.EventType != "windforce.release.published" || verified.DeliveryID != "whd_0123456789abcdef" {
		t.Fatalf("verification = %#v", verified)
	}

	tampered := append([]byte(nil), body...)
	tampered[len(tampered)-1] ^= 1
	if _, err := (Verifier{Secret: fixture.Secret, Now: func() time.Time { return timestampSeconds }}).Verify(header, tampered); !errors.Is(err, ErrVerification) {
		t.Fatalf("tampered verification error = %v", err)
	}
}

func TestVerifierRejectsStaleAndIncompleteHeaders(t *testing.T) {
	now := time.Date(2026, 7, 16, 10, 10, 1, 0, time.UTC)
	header := http.Header{
		HeaderEventID:   []string{"evt_example"},
		HeaderEventType: []string{"windforce.release.published"},
		HeaderDelivery:  []string{"whd_example"},
		HeaderTimestamp: []string{"1784196000"},
	}
	header.Set(HeaderSignature, Sign("example-secret", header.Get(HeaderTimestamp), []byte("{}")))
	if _, err := (Verifier{Secret: "example-secret", Now: func() time.Time { return now }}).Verify(header, []byte("{}")); !errors.Is(err, ErrVerification) {
		t.Fatalf("stale verification error = %v", err)
	}
	header.Del(HeaderDelivery)
	if _, err := (Verifier{Secret: "example-secret", Now: func() time.Time { return time.Unix(1784196000, 0) }}).Verify(header, []byte("{}")); !errors.Is(err, ErrVerification) {
		t.Fatalf("incomplete verification error = %v", err)
	}
}
