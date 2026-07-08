package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
	if len(app.Actions) == 0 {
		return contract.App{}, errors.New("at least one action is required")
	}

	for name, action := range app.Actions {
		if name == "" {
			return contract.App{}, errors.New("action key is required")
		}
		if action.Action == "" {
			action.Action = name
		}
		if action.Action != name {
			return contract.App{}, fmt.Errorf("action %q has mismatched action field %q", name, action.Action)
		}
		app.Actions[name] = action
	}
	return app, nil
}
