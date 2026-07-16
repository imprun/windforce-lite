package webhook

import (
	"time"

	webhookcontract "github.com/imprun/windforce-lite/pkg/webhook"
)

const (
	HeaderEventID   = webhookcontract.HeaderEventID
	HeaderEventType = webhookcontract.HeaderEventType
	HeaderDelivery  = webhookcontract.HeaderDelivery
	HeaderTimestamp = webhookcontract.HeaderTimestamp
	HeaderSignature = webhookcontract.HeaderSignature
)

func TimestampValue(at time.Time) string {
	return webhookcontract.TimestampValue(at)
}

func Sign(secret string, timestamp string, body []byte) string {
	return webhookcontract.Sign(secret, timestamp, body)
}

func VerifySignature(secret string, timestamp string, body []byte, signature string) bool {
	return webhookcontract.VerifySignature(secret, timestamp, body, signature)
}
