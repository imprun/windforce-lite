package crypto

import "encoding/json"

const (
	encMarkerKey = "__wf_enc"
	encVersion   = 1
)

type encEnvelope struct {
	Version int    `json:"__wf_enc"`
	CT      string `json:"ct"`
}

// IsEnc reports whether b is a self-identifying encrypted JSON envelope.
func IsEnc(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	var probe map[string]json.RawMessage
	if json.Unmarshal(b, &probe) != nil {
		return false
	}
	_, ok := probe[encMarkerKey]
	return ok
}

// WrapEnc encrypts a JSON value under dek while keeping the stored value valid JSON.
func WrapEnc(dek string, plaintextJSON []byte) ([]byte, error) {
	ct, err := Encrypt(dek, string(plaintextJSON))
	if err != nil {
		return nil, err
	}
	return json.Marshal(encEnvelope{Version: encVersion, CT: ct})
}

// UnwrapEnc returns plaintext for encrypted envelopes and passes plaintext through.
func UnwrapEnc(dek string, b []byte) ([]byte, error) {
	if !IsEnc(b) {
		return b, nil
	}
	var env encEnvelope
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, err
	}
	plain, err := Decrypt(dek, env.CT)
	if err != nil {
		return nil, err
	}
	return []byte(plain), nil
}
