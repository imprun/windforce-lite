package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/imprun/windforce-lite/internal/contract"
)

var ErrDeploymentNotFound = errors.New("deployment not found")

type FileCatalog struct {
	Path string
}

type Snapshot struct {
	Deployments map[string]contract.Deployment `json:"deployments"`
	History     []DeploymentHistory            `json:"history,omitempty"`
}

type DeploymentHistory struct {
	ID          string              `json:"id"`
	Workspace   string              `json:"workspace"`
	GitSourceID string              `json:"gitSourceId,omitempty"`
	App         string              `json:"app"`
	Commit      string              `json:"commit"`
	Entrypoint  string              `json:"entrypoint,omitempty"`
	Source      string              `json:"source"`
	Status      string              `json:"status"`
	ObjectURI   string              `json:"objectUri,omitempty"`
	Deployment  contract.Deployment `json:"deployment"`
	CreatedAt   time.Time           `json:"createdAt"`
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
	snapshot.History = append(snapshot.History, newDeploymentHistory(deployment))
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
	if snapshot.History == nil {
		snapshot.History = []DeploymentHistory{}
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

func newDeploymentHistory(deployment contract.Deployment) DeploymentHistory {
	createdAt := time.Now().UTC()
	workspace := deployment.SourceWorkspace()
	gitSourceID := deployment.SourceGitSourceID()
	return DeploymentHistory{
		ID:          fmt.Sprintf("%s/%s/%s/%d", workspace, deployment.App, deployment.Commit, createdAt.UnixNano()),
		Workspace:   workspace,
		GitSourceID: gitSourceID,
		App:         deployment.App,
		Commit:      deployment.Commit,
		Entrypoint:  firstEntrypoint(deployment),
		Source:      "external_sync",
		Status:      "deployed",
		ObjectURI:   deployment.ObjectURI,
		Deployment:  deployment,
		CreatedAt:   createdAt,
	}
}

func firstEntrypoint(deployment contract.Deployment) string {
	keys := make([]string, 0, len(deployment.Actions))
	for key := range deployment.Actions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if deployment.Actions[key].Entrypoint != "" {
			return deployment.Actions[key].Entrypoint
		}
	}
	return ""
}
