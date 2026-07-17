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
	if len(app.Actions) == 0 {
		return contract.App{}, fmt.Errorf("%s declares no actions", FileName)
	}
	if app.Entrypoint != "" && (filepath.IsAbs(app.Entrypoint) || strings.HasPrefix(app.Entrypoint, "/") || strings.Contains(app.Entrypoint, "..")) {
		return contract.App{}, fmt.Errorf("app %s entrypoint %q must be a relative path inside the app", app.App, app.Entrypoint)
	}
	if app.ScriptLang == "" {
		app.ScriptLang = "typescript"
	}
	if app.TimeoutS == 0 {
		app.TimeoutS = contract.DefaultTimeoutS
	}
	if app.MaxConcurrent != nil && *app.MaxConcurrent <= 0 {
		return contract.App{}, fmt.Errorf("app %s maxConcurrent must be positive in %s", app.App, FileName)
	}
	caps, err := contract.NormalizeCapabilities(app.Capabilities)
	if err != nil {
		return contract.App{}, fmt.Errorf("app %s capabilities: %w", app.App, err)
	}
	app.Capabilities = caps
	if len(app.Capabilities) > 0 && app.Tag != "" {
		return contract.App{}, fmt.Errorf("app %s declares both tag and capabilities in %s", app.App, FileName)
	}

	for name, action := range app.Actions {
		if !contract.ValidActionKey(name) {
			return contract.App{}, fmt.Errorf("invalid action key %q in %s", name, FileName)
		}
		action.Action = name
		clearRuntimeOwnedActionManifestFields(&action)
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
		actionTag := action.Tag != nil && *action.Tag != ""
		if len(effectiveCaps) > 0 && (app.Tag != "" || actionTag) {
			return contract.App{}, fmt.Errorf("action %s.%s declares both tag and capabilities in %s", app.App, name, FileName)
		}
		applyAppDefaults(app, &action)
		if err := validateExecutableAction(app.App, name, action); err != nil {
			return contract.App{}, err
		}
		app.Actions[name] = action
	}
	if len(app.Capabilities) == 0 && app.Tag == "" {
		app.Tag = contract.DefaultRouteTag
	}
	return app, nil
}

func clearRuntimeOwnedActionManifestFields(action *contract.Action) {
	action.TagOverride = nil
	action.InputSchemaBody = nil
	action.OutputSchemaBody = nil
	action.OperatorSettingsSchemaBody = nil
	action.UpdatedAt = nil
}

func applyAppDefaults(app contract.App, action *contract.Action) {
	if action.Entrypoint == "" {
		action.Entrypoint = app.Entrypoint
	}
	if action.Runtime == "" {
		action.Runtime = app.ScriptLang
	}
	if action.TimeoutMs == 0 {
		if action.TimeoutS != nil {
			action.TimeoutMs = int64(*action.TimeoutS) * 1000
		} else if app.TimeoutS > 0 {
			action.TimeoutMs = int64(app.TimeoutS) * 1000
		}
	}
}

func validateExecutableAction(app string, actionName string, action contract.Action) error {
	if action.Adapter != nil {
		return fmt.Errorf("action %s.%s adapter is not supported in %s", app, actionName, FileName)
	}
	if len(action.Command) > 0 {
		return fmt.Errorf("action %s.%s command is not supported in %s", app, actionName, FileName)
	}
	if action.Entrypoint == "" {
		return fmt.Errorf("app %s has no entrypoint in %s", app, FileName)
	}
	if err := validateActionPath(app, actionName, "entrypoint", action.Entrypoint); err != nil {
		return err
	}
	return nil
}

func validateActionPath(app string, action string, field string, value string) error {
	if value == "" {
		return nil
	}
	owner := fmt.Sprintf("action %s.%s", app, action)
	if action == "" {
		owner = "app " + app
	}
	if filepath.IsAbs(value) || strings.HasPrefix(value, "/") || strings.Contains(value, "..") {
		return fmt.Errorf("%s %s path %q must be a relative path inside the app", owner, field, value)
	}
	return nil
}
