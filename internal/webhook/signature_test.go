package webhook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type signatureFixture struct {
	Secret    string `json:"secret"`
	Timestamp string `json:"timestamp"`
	Body      string `json:"body"`
	Signature string `json:"signature"`
}

func TestSignatureGoldenFixture(t *testing.T) {
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
	if !VerifySignature(fixture.Secret, fixture.Timestamp, body, fixture.Signature) {
		t.Fatal("fixture signature did not verify")
	}
	tampered := append([]byte(nil), body...)
	tampered[len(tampered)-1] ^= 1
	if VerifySignature(fixture.Secret, fixture.Timestamp, tampered, fixture.Signature) {
		t.Fatal("tampered body verified")
	}
}
