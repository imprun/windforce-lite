package state

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/imprun/windforce-lite/internal/contract"
	wfcrypto "github.com/imprun/windforce-lite/internal/crypto"
)

type inputCryptoConfig struct {
	SecretKey         string
	SecretKeyPrevious string
}

type inputWorkspaceKeyProvider interface {
	GetWorkspaceKeyVersioned(ctx context.Context, workspaceID string) (string, int32, error)
}

func encryptInputAtRest(ctx context.Context, provider inputWorkspaceKeyProvider, config inputCryptoConfig, workspaceID string, input json.RawMessage) (json.RawMessage, error) {
	return encryptJSONAtRest(ctx, provider, config, workspaceID, input, "{}", "input")
}

func decryptInputAtRest(ctx context.Context, provider inputWorkspaceKeyProvider, config inputCryptoConfig, workspaceID string, input json.RawMessage) (json.RawMessage, error) {
	return decryptJSONAtRest(ctx, provider, config, workspaceID, input, "{}", "input")
}

func encryptResultAtRest(ctx context.Context, provider inputWorkspaceKeyProvider, config inputCryptoConfig, workspaceID string, result json.RawMessage) (json.RawMessage, error) {
	return encryptJSONAtRest(ctx, provider, config, workspaceID, result, "null", "result")
}

func decryptResultAtRest(ctx context.Context, provider inputWorkspaceKeyProvider, config inputCryptoConfig, workspaceID string, result json.RawMessage) (json.RawMessage, error) {
	return decryptJSONAtRest(ctx, provider, config, workspaceID, result, "null", "result")
}

func encryptJSONAtRest(ctx context.Context, provider inputWorkspaceKeyProvider, config inputCryptoConfig, workspaceID string, value json.RawMessage, defaultJSON string, label string) (json.RawMessage, error) {
	value = canonicalJSONValue(value, defaultJSON)
	if !json.Valid(value) {
		return nil, fmt.Errorf("%s is not valid JSON", label)
	}
	if strings.TrimSpace(config.SecretKey) == "" || wfcrypto.IsEnc(value) {
		return cloneRaw(value), nil
	}
	dek, err := resolveInputDEK(ctx, provider, config, workspaceID)
	if err != nil {
		return nil, err
	}
	encrypted, err := wfcrypto.WrapEnc(dek, value)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(encrypted), nil
}

func decryptJSONAtRest(ctx context.Context, provider inputWorkspaceKeyProvider, config inputCryptoConfig, workspaceID string, value json.RawMessage, defaultJSON string, label string) (json.RawMessage, error) {
	value = canonicalJSONValue(value, defaultJSON)
	if !wfcrypto.IsEnc(value) {
		return cloneRaw(value), nil
	}
	if strings.TrimSpace(config.SecretKey) == "" {
		return nil, fmt.Errorf("%s is encrypted but SECRET_KEY is not configured", label)
	}
	dek, err := resolveInputDEK(ctx, provider, config, workspaceID)
	if err != nil {
		return nil, err
	}
	plain, err := wfcrypto.UnwrapEnc(dek, value)
	if err != nil {
		return nil, err
	}
	if !json.Valid(plain) {
		return nil, fmt.Errorf("decrypted %s is not valid JSON", label)
	}
	return json.RawMessage(append([]byte(nil), plain...)), nil
}

func resolveInputDEK(ctx context.Context, provider inputWorkspaceKeyProvider, config inputCryptoConfig, workspaceID string) (string, error) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	if provider != nil {
		key, version, err := provider.GetWorkspaceKeyVersioned(ctx, workspaceID)
		if err != nil {
			return "", err
		}
		if key != "" {
			return wfcrypto.ResolveDEK(key, version, inputKEKs(config))
		}
	}
	return wfcrypto.DeriveWorkspaceKey(strings.TrimSpace(config.SecretKey), workspaceID), nil
}

func inputKEKs(config inputCryptoConfig) []string {
	keks := []string{wfcrypto.DeriveKEK(strings.TrimSpace(config.SecretKey))}
	if previous := strings.TrimSpace(config.SecretKeyPrevious); previous != "" {
		keks = append(keks, wfcrypto.DeriveKEK(previous))
	}
	return keks
}

func canonicalJSONInput(input json.RawMessage) json.RawMessage {
	return canonicalJSONValue(input, "{}")
}

func canonicalJSONValue(input json.RawMessage, defaultJSON string) json.RawMessage {
	if len(input) == 0 {
		return json.RawMessage(defaultJSON)
	}
	return cloneRaw(input)
}
