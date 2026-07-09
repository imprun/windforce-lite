package manifest

import (
	"encoding/json"
	"errors"
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
		return contract.App{}, err
	}
	return Parse(data)
}

func Parse(data []byte) (contract.App, error) {
	var app contract.App
	if err := json.Unmarshal(data, &app); err != nil {
		return contract.App{}, err
	}
	if app.App == "" {
		return contract.App{}, errors.New("app is required")
	}
	if !contract.ValidAppKey(app.App) {
		return contract.App{}, fmt.Errorf("invalid app key %q in %s", app.App, FileName)
	}
	if len(app.Actions) == 0 {
		return contract.App{}, errors.New("at least one action is required")
	}
	if err := validateActionPath(app.App, "", "entrypoint", app.Entrypoint); err != nil {
		return contract.App{}, err
	}
	if app.MaxConcurrent != nil && *app.MaxConcurrent <= 0 {
		return contract.App{}, fmt.Errorf("app %s maxConcurrent must be positive in %s", app.App, FileName)
	}

	for name, action := range app.Actions {
		if !contract.ValidActionKey(name) {
			return contract.App{}, fmt.Errorf("invalid action key %q in %s", name, FileName)
		}
		if action.Action == "" {
			action.Action = name
		}
		if action.Action != name {
			return contract.App{}, fmt.Errorf("action %q has mismatched action field %q", name, action.Action)
		}
		applyAppDefaults(app, &action)
		if err := validateActionPath(app.App, name, "entrypoint", action.Entrypoint); err != nil {
			return contract.App{}, err
		}
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

func applyAppDefaults(app contract.App, action *contract.Action) {
	if action.Entrypoint == "" {
		action.Entrypoint = app.Entrypoint
	}
	if action.Runtime == "" {
		action.Runtime = firstNonEmpty(app.Runtime, app.ScriptLang)
	}
	if action.TimeoutMs == 0 {
		if action.TimeoutS != nil {
			action.TimeoutMs = int64(*action.TimeoutS) * 1000
		} else if app.TimeoutS > 0 {
			action.TimeoutMs = int64(app.TimeoutS) * 1000
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
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
