package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/imprun/windforce-lite/internal/contract"
)

const FileName = "windforce.json"

func Load(dir string) (contract.App, error) {
	path := filepath.Join(dir, FileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return contract.App{}, fmt.Errorf("no %s manifest at source root (subpath)", FileName)
		}
		return contract.App{}, err
	}
	return Parse(data)
}

func Parse(data []byte) (contract.App, error) {
	var parsed struct {
		contract.App
		Flows map[string]json.RawMessage `json:"flows"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return contract.App{}, fmt.Errorf("parse %s: %w", FileName, err)
	}
	app := parsed.App
	if !contract.ValidAppKey(app.App) {
		return contract.App{}, fmt.Errorf("invalid app key %q in %s", app.App, FileName)
	}
	if len(parsed.Flows) > 0 {
		return contract.App{}, fmt.Errorf("app %s declares flows in %s, but windforce-lite does not support flows", app.App, FileName)
	}
	app.Runtime = ""
	if strings.TrimSpace(app.Entrypoint) == "" {
		return contract.App{}, fmt.Errorf("app %s has no entrypoint in %s", app.App, FileName)
	}
	if len(app.Actions) == 0 {
		return contract.App{}, fmt.Errorf("%s declares no actions", FileName)
	}
	if err := validateActionPath(app.App, "", "entrypoint", app.Entrypoint); err != nil {
		return contract.App{}, err
	}
	if strings.TrimSpace(app.ScriptLang) == "" {
		app.ScriptLang = "typescript"
	}
	if app.MaxConcurrent != nil && *app.MaxConcurrent <= 0 {
		return contract.App{}, fmt.Errorf("app %s maxConcurrent must be positive in %s", app.App, FileName)
	}
	caps, err := contract.NormalizeCapabilities(app.Capabilities)
	if err != nil {
		return contract.App{}, fmt.Errorf("app %s capabilities: %w", app.App, err)
	}
	app.Capabilities = caps
	if len(app.Capabilities) > 0 && strings.TrimSpace(app.Tag) != "" {
		return contract.App{}, fmt.Errorf("app %s declares both tag and capabilities in %s", app.App, FileName)
	}

	for name, action := range app.Actions {
		if !contract.ValidActionKey(name) {
			return contract.App{}, fmt.Errorf("invalid action key %q in %s", name, FileName)
		}
		action.Action = name
		clearNonCanonicalActionManifestFields(&action)
		if action.Capabilities != nil {
			caps, err := contract.NormalizeCapabilities(*action.Capabilities)
			if err != nil {
				return contract.App{}, fmt.Errorf("action %s.%s capabilities: %w", app.App, name, err)
			}
			if caps == nil {
				caps = []string{}
			}
			action.Capabilities = &caps
		}
		effectiveCaps := contract.EffectiveCapabilities(app.Capabilities, action.Capabilities)
		actionTag := action.Tag != nil && strings.TrimSpace(*action.Tag) != ""
		if len(effectiveCaps) > 0 && (strings.TrimSpace(app.Tag) != "" || actionTag) {
			return contract.App{}, fmt.Errorf("action %s.%s declares both tag and capabilities in %s", app.App, name, FileName)
		}
		applyAppDefaults(app, &action)
		if err := validateActionPath(app.App, name, "input schema", action.InputSchema); err != nil {
			return contract.App{}, err
		}
		if err := validateActionPath(app.App, name, "output schema", action.OutputSchema); err != nil {
			return contract.App{}, err
		}
		app.Actions[name] = action
	}
	return app, nil
}

func clearNonCanonicalActionManifestFields(action *contract.Action) {
	action.TagOverride = nil
	action.Runtime = ""
	action.Entrypoint = ""
	action.Command = nil
	action.Adapter = nil
	action.InputSchemaBody = nil
	action.OutputSchemaBody = nil
	action.TimeoutMs = 0
	action.UpdatedAt = nil
}

func applyAppDefaults(app contract.App, action *contract.Action) {
	action.Entrypoint = app.Entrypoint
	action.Runtime = app.ScriptLang
	if action.TimeoutMs == 0 {
		if action.TimeoutS != nil {
			action.TimeoutMs = int64(*action.TimeoutS) * 1000
		} else if app.TimeoutS > 0 {
			action.TimeoutMs = int64(app.TimeoutS) * 1000
		}
	}
}

func validateActionPath(app string, action string, field string, value string) error {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" {
		return nil
	}
	owner := fmt.Sprintf("action %s.%s", app, action)
	if action == "" {
		owner = "app " + app
	}
	if strings.Contains(value, "..") {
		return fmt.Errorf("%s %s path %q must be a relative path inside the app", owner, field, value)
	}
	if _, err := contract.NormalizeSourcePath(value); err != nil {
		return fmt.Errorf("%s %s path: %w", owner, field, err)
	}
	return nil
}
