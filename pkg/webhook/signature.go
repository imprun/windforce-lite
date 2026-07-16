package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	HeaderEventID   = "X-Windforce-Event"
	HeaderEventType = "X-Windforce-Event-Type"
	HeaderDelivery  = "X-Windforce-Delivery"
	HeaderTimestamp = "X-Windforce-Timestamp"
	HeaderSignature = "X-Windforce-Signature"
)

const DefaultTimestampTolerance = 5 * time.Minute

var ErrVerification = errors.New("windforce webhook verification failed")

type Verification struct {
	EventID    string
	EventType  string
	DeliveryID string
	Timestamp  time.Time
}

type Verifier struct {
	Secret             string
	TimestampTolerance time.Duration
	Now                func() time.Time
}

func TimestampValue(at time.Time) string {
	return strconv.FormatInt(at.UTC().Unix(), 10)
}

func Sign(secret string, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

func VerifySignature(secret string, timestamp string, body []byte, signature string) bool {
	provided := strings.TrimSpace(signature)
	if !strings.HasPrefix(provided, "v1=") {
		return false
	}
	return hmac.Equal([]byte(Sign(secret, timestamp, body)), []byte(provided))
}

func (verifier Verifier) Verify(header http.Header, body []byte) (Verification, error) {
	if strings.TrimSpace(verifier.Secret) == "" {
		return Verification{}, verificationError("secret is required")
	}
	eventID := strings.TrimSpace(header.Get(HeaderEventID))
	eventType := strings.TrimSpace(header.Get(HeaderEventType))
	deliveryID := strings.TrimSpace(header.Get(HeaderDelivery))
	timestampValue := strings.TrimSpace(header.Get(HeaderTimestamp))
	if eventID == "" || eventType == "" || deliveryID == "" || timestampValue == "" {
		return Verification{}, verificationError("required identity headers are missing")
	}
	seconds, err := strconv.ParseInt(timestampValue, 10, 64)
	if err != nil {
		return Verification{}, verificationError("timestamp is invalid")
	}
	timestamp := time.Unix(seconds, 0).UTC()
	now := time.Now().UTC()
	if verifier.Now != nil {
		now = verifier.Now().UTC()
	}
	tolerance := verifier.TimestampTolerance
	if tolerance <= 0 {
		tolerance = DefaultTimestampTolerance
	}
	if timestamp.Before(now.Add(-tolerance)) || timestamp.After(now.Add(tolerance)) {
		return Verification{}, verificationError("timestamp is outside the allowed window")
	}
	if !VerifySignature(verifier.Secret, timestampValue, body, header.Get(HeaderSignature)) {
		return Verification{}, verificationError("signature is invalid")
	}
	return Verification{EventID: eventID, EventType: eventType, DeliveryID: deliveryID, Timestamp: timestamp}, nil
}

func verificationError(message string) error {
	return fmt.Errorf("%w: %s", ErrVerification, message)
}
