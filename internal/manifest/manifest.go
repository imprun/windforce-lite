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

func validateActionPath(app string, action string, field string, value string) error {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" {
		return nil
	}
	if strings.Contains(value, "..") {
		return fmt.Errorf("action %s.%s %s path %q must be a relative path inside the app", app, action, field, value)
	}
	if _, err := contract.NormalizeSourcePath(value); err != nil {
		return fmt.Errorf("action %s.%s %s path: %w", app, action, field, err)
	}
	return nil
}
