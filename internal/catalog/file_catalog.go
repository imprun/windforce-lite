package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/imprun/windforce-lite/internal/contract"
)

var ErrDeploymentNotFound = errors.New("deployment not found")

type FileCatalog struct {
	Path string
}

type Snapshot struct {
	Deployments map[string]contract.Deployment `json:"deployments"`
}

func NewFileCatalog(path string) *FileCatalog {
	return &FileCatalog{Path: path}
}

func (c *FileCatalog) UpsertDeployment(ctx context.Context, deployment contract.Deployment) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	snapshot, err := c.Load(ctx)
	if err != nil {
		return err
	}
	if snapshot.Deployments == nil {
		snapshot.Deployments = map[string]contract.Deployment{}
	}
	snapshot.Deployments[deployment.App] = deployment
	return c.write(snapshot)
}

func (c *FileCatalog) GetDeployment(ctx context.Context, app string) (contract.Deployment, error) {
	snapshot, err := c.Load(ctx)
	if err != nil {
		return contract.Deployment{}, err
	}
	deployment, ok := snapshot.Deployments[app]
	if !ok {
		return contract.Deployment{}, ErrDeploymentNotFound
	}
	return deployment, nil
}

func (c *FileCatalog) Load(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	data, err := os.ReadFile(c.Path)
	if errors.Is(err, os.ErrNotExist) {
		return Snapshot{Deployments: map[string]contract.Deployment{}}, nil
	}
	if err != nil {
		return Snapshot{}, err
	}
	var snapshot Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return Snapshot{}, err
	}
	if snapshot.Deployments == nil {
		snapshot.Deployments = map[string]contract.Deployment{}
	}
	return snapshot, nil
}

func (c *FileCatalog) write(snapshot Snapshot) error {
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmpPath := c.Path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, c.Path)
}
