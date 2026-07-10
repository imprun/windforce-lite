package token

import (
	"testing"
	"time"
)

func TestJobTokenRoundTripAndExpiry(t *testing.T) {
	raw := MintJob("secret-a", JobClaims{
		Workspace: "ws-a",
		JobID:     "job-a",
		Subject:   "runner@example.test",
		Exp:       time.Now().Add(time.Minute).Unix(),
	})
	if !IsJobToken(raw) {
		t.Fatalf("token prefix = %q", raw)
	}
	claims, ok := VerifyJob("secret-a", raw)
	if !ok {
		t.Fatalf("VerifyJob rejected minted token")
	}
	if claims.Workspace != "ws-a" || claims.JobID != "job-a" || claims.Subject != "runner@example.test" {
		t.Fatalf("claims = %#v", claims)
	}
	if _, ok := VerifyJob("secret-b", raw); ok {
		t.Fatalf("VerifyJob accepted wrong secret")
	}
	expired := MintJob("secret-a", JobClaims{
		Workspace: "ws-a",
		JobID:     "job-a",
		Subject:   "runner@example.test",
		Exp:       time.Now().Add(-time.Minute).Unix(),
	})
	if _, ok := VerifyJob("secret-a", expired); ok {
		t.Fatalf("VerifyJob accepted expired token")
	}
}
