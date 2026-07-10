package token

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

const jobPrefix = "wfjob_"

// IsJobToken reports whether a presented token is a stateless job token.
func IsJobToken(tok string) bool { return strings.HasPrefix(tok, jobPrefix) }

// JobClaims is the principal carried by a job token.
type JobClaims struct {
	Workspace string `json:"ws"`
	JobID     string `json:"job"`
	Subject   string `json:"sub"`
	Exp       int64  `json:"exp"`
}

// MintJob produces a stateless job token signed with the instance secret.
func MintJob(secret string, c JobClaims) string {
	payload, _ := json.Marshal(c)
	p := base64.RawURLEncoding.EncodeToString(payload)
	return jobPrefix + p + "." + sign(secret, p)
}

// VerifyJob validates a job token's signature and expiry.
func VerifyJob(secret string, tok string) (*JobClaims, bool) {
	if secret == "" || !strings.HasPrefix(tok, jobPrefix) {
		return nil, false
	}
	body := strings.TrimPrefix(tok, jobPrefix)
	dot := strings.LastIndexByte(body, '.')
	if dot < 0 {
		return nil, false
	}
	p, mac := body[:dot], body[dot+1:]
	if subtle.ConstantTimeCompare([]byte(mac), []byte(sign(secret, p))) != 1 {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return nil, false
	}
	var c JobClaims
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, false
	}
	if c.Exp != 0 && time.Now().Unix() > c.Exp {
		return nil, false
	}
	return &c, true
}

// VerifyJobAny validates a job token against any of the candidate secrets.
func VerifyJobAny(secrets []string, tok string) (*JobClaims, bool) {
	for _, secret := range secrets {
		if c, ok := VerifyJob(secret, tok); ok {
			return c, true
		}
	}
	return nil, false
}

func sign(secret string, msg string) string {
	m := hmac.New(sha256.New, []byte(secret))
	_, _ = m.Write([]byte(msg))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
