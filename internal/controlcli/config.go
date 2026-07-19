package controlcli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultAPIURL    = "http://127.0.0.1:18091"
	defaultWorkspace = "default"
	defaultTokenEnv  = "WINDFORCE_CORE_API_TOKEN"
)

type Profile struct {
	APIURL    string `json:"api_url,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	Actor     string `json:"actor,omitempty"`
	TokenEnv  string `json:"token_env,omitempty"`
}

type ConfigFile struct {
	CurrentProfile string             `json:"current_profile,omitempty"`
	Profiles       map[string]Profile `json:"profiles,omitempty"`
}

type resolvedConfig struct {
	ProfileName string
	Profile
	Token string
}

func configPath() (string, error) {
	if path := strings.TrimSpace(os.Getenv("WINDFORCE_CONFIG")); path != "" {
		return path, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(dir, "windforce", "config.json"), nil
}

func loadConfig(path string) (ConfigFile, error) {
	config := ConfigFile{Profiles: map[string]Profile{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return config, nil
	}
	if err != nil {
		return config, fmt.Errorf("read config: %w", err)
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return config, fmt.Errorf("decode config: %w", err)
	}
	if config.Profiles == nil {
		config.Profiles = map[string]Profile{}
	}
	return config, nil
}

func saveConfig(path string, config ConfigFile) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func resolveProfile(config ConfigFile, selected string, overrides Profile) (resolvedConfig, error) {
	name := firstNonEmpty(selected, os.Getenv("WINDFORCE_PROFILE"), config.CurrentProfile)
	profile := Profile{}
	if name != "" {
		var ok bool
		profile, ok = config.Profiles[name]
		if !ok {
			return resolvedConfig{}, fmt.Errorf("profile %q does not exist", name)
		}
	}
	profile.APIURL = firstNonEmpty(overrides.APIURL, os.Getenv("WINDFORCE_CORE_API_URL"), os.Getenv("WINDFORCE_LITE_API_URL"), profile.APIURL, defaultAPIURL)
	profile.Workspace = firstNonEmpty(overrides.Workspace, os.Getenv("WINDFORCE_CORE_WORKSPACE"), os.Getenv("WINDFORCE_LITE_WORKSPACE"), profile.Workspace, defaultWorkspace)
	profile.Actor = firstNonEmpty(overrides.Actor, os.Getenv("WINDFORCE_CORE_ACTOR"), os.Getenv("WINDFORCE_LITE_ACTOR"), profile.Actor)
	profile.TokenEnv = firstNonEmpty(overrides.TokenEnv, os.Getenv("WINDFORCE_CORE_TOKEN_ENV"), profile.TokenEnv, defaultTokenEnv)
	token := ""
	if profile.TokenEnv != "" {
		token = strings.TrimSpace(os.Getenv(profile.TokenEnv))
	}
	return resolvedConfig{ProfileName: name, Profile: profile, Token: token}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
