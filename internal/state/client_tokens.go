package state

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
)

func HashClientToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func ClientTokenMatches(client Client, value string) bool {
	if client.TokenHash == "" || strings.TrimSpace(value) == "" {
		return false
	}
	want, err := hex.DecodeString(client.TokenHash)
	if err != nil {
		return false
	}
	got, err := hex.DecodeString(HashClientToken(value))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(want, got) == 1
}
