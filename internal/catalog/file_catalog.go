package catalog

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/imprun/windforce-lite/internal/contract"
)

var ErrDeploymentNotFound = errors.New("deployment not found")
var ErrActionNotFound = errors.New("action not found")

type FileCatalog struct {
	Path string
}

type Snapshot struct {
	Deployments map[string]contract.Deployment `json:"deployments"`
	History     []DeploymentHistory            `json:"history,omitempty"`
	Audit       []AuditRecord                  `json:"audit,omitempty"`
}

// AuditRecord captures a non-release state change (repository settings,
// deletions, route tag overrides) for the audit trail. Releases live in
// DeploymentHistory.
type AuditRecord struct {
	ID          string    `json:"id"`
	Workspace   string    `json:"workspace"`
	GitSourceID string    `json:"gitSourceId"`
	App         string    `json:"app,omitempty"`
	Kind        string    `json:"kind"`
	Detail      string    `json:"detail,omitempty"`
	Actor       string    `json:"actor,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

type DeploymentHistory struct {
	ID           string              `json:"id"`
	Workspace    string              `json:"workspace"`
	GitSourceID  string              `json:"gitSourceId,omitempty"`
	App          string              `json:"app"`
	Commit       string              `json:"commit"`
	Entrypoint   string              `json:"entrypoint,omitempty"`
	Source       string              `json:"source"`
	Status       string              `json:"status"`
	DeploymentID *string             `json:"deploymentId,omitempty"`
	Message      *string             `json:"message,omitempty"`
	CreatedBy    *string             `json:"createdBy,omitempty"`
	ObjectURI    string              `json:"objectUri,omitempty"`
	Deployment   contract.Deployment `json:"deployment"`
	CreatedAt    time.Time           `json:"createdAt"`
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
	deployment = normalizeDeploymentDefaults(deployment)
	deployment = ensureDeploymentUpdatedAt(deployment, time.Now().UTC())
	snapshot.Deployments[deploymentKey(deployment.SourceWorkspace(), deployment.App)] = deployment
	snapshot.History = append(snapshot.History, newDeploymentHistory(deployment))
	return c.write(snapshot)
}

func (c *FileCatalog) AppendAudit(ctx context.Context, record AuditRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	snapshot, err := c.Load(ctx)
	if err != nil {
		return err
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	if record.ID == "" {
		record.ID = newAppVersionID(record.CreatedAt)
	}
	snapshot.Audit = append(snapshot.Audit, record)
	return c.write(snapshot)
}

func (c *FileCatalog) AuditTrail(ctx context.Context, workspace string, gitSourceID string) ([]AuditRecord, error) {
	snapshot, err := c.Load(ctx)
	if err != nil {
		return nil, err
	}
	records := make([]AuditRecord, 0)
	for _, record := range snapshot.Audit {
		if record.Workspace == workspace && record.GitSourceID == gitSourceID {
			records = append(records, record)
		}
	}
	return records, nil
}

func (c *FileCatalog) GetDeployment(ctx context.Context, app string) (contract.Deployment, error) {
	return c.GetDeploymentForWorkspace(ctx, contract.DefaultWorkspace, app)
}

func (c *FileCatalog) GetDeploymentForWorkspace(ctx context.Context, workspace string, app string) (contract.Deployment, error) {
	snapshot, err := c.Load(ctx)
	if err != nil {
		return contract.Deployment{}, err
	}
	deployment, ok := snapshot.Deployments[deploymentKey(workspace, app)]
	if !ok {
		return contract.Deployment{}, ErrDeploymentNotFound
	}
	return deployment, nil
}

func (c *FileCatalog) SetAppTagOverride(ctx context.Context, workspace string, app string, tagOverride *string) (contract.Deployment, error) {
	if err := ctx.Err(); err != nil {
		return contract.Deployment{}, err
	}
	snapshot, err := c.Load(ctx)
	if err != nil {
		return contract.Deployment{}, err
	}
	key := deploymentKey(workspace, app)
	deployment, ok := snapshot.Deployments[key]
	if !ok {
		return contract.Deployment{}, ErrDeploymentNotFound
	}
	deployment.TagOverride = cloneStringPtr(tagOverride)
	deployment.UpdatedAt = timePtr(time.Now().UTC())
	snapshot.Deployments[key] = deployment
	if err := c.write(snapshot); err != nil {
		return contract.Deployment{}, err
	}
	return deployment, nil
}

func (c *FileCatalog) SetActionTagOverride(ctx context.Context, workspace string, app string, actionKey string, tagOverride *string) (contract.Action, error) {
	if err := ctx.Err(); err != nil {
		return contract.Action{}, err
	}
	snapshot, err := c.Load(ctx)
	if err != nil {
		return contract.Action{}, err
	}
	key := deploymentKey(workspace, app)
	deployment, ok := snapshot.Deployments[key]
	if !ok {
		return contract.Action{}, ErrDeploymentNotFound
	}
	action, ok := deployment.Actions[actionKey]
	if !ok {
		return contract.Action{}, ErrActionNotFound
	}
	action.TagOverride = cloneStringPtr(tagOverride)
	action.UpdatedAt = timePtr(time.Now().UTC())
	deployment.Actions[actionKey] = action
	snapshot.Deployments[key] = deployment
	if err := c.write(snapshot); err != nil {
		return contract.Action{}, err
	}
	return action, nil
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
	snapshot.Deployments = normalizeDeploymentMap(snapshot.Deployments)
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

func ensureDeploymentUpdatedAt(deployment contract.Deployment, updatedAt time.Time) contract.Deployment {
	if deployment.UpdatedAt == nil {
		deployment.UpdatedAt = timePtr(updatedAt)
	}
	for key, action := range deployment.Actions {
		if action.UpdatedAt == nil {
			action.UpdatedAt = timePtr(updatedAt)
			deployment.Actions[key] = action
		}
	}
	return deployment
}

func normalizeDeploymentDefaults(deployment contract.Deployment) contract.Deployment {
	if deployment.Tag == "" {
		deployment.Tag = contract.DefaultRouteTag
	}
	if deployment.TimeoutS == 0 {
		deployment.TimeoutS = contract.DefaultTimeoutS
	}
	if deployment.ScriptLang == "" {
		deployment.ScriptLang = "typescript"
	}
	return deployment
}

func normalizeDeploymentMap(deployments map[string]contract.Deployment) map[string]contract.Deployment {
	normalized := make(map[string]contract.Deployment, len(deployments))
	for key, deployment := range deployments {
		deployment = normalizeDeploymentDefaults(deployment)
		normalizedKey := deploymentKey(deployment.SourceWorkspace(), deployment.App)
		if deployment.App == "" {
			normalizedKey = key
		}
		normalized[normalizedKey] = deployment
	}
	return normalized
}

func deploymentKey(workspace string, app string) string {
	return contract.NormalizeWorkspace(workspace) + "/" + app
}

func timePtr(value time.Time) *time.Time {
	return &value
}

func newDeploymentHistory(deployment contract.Deployment) DeploymentHistory {
	createdAt := time.Now().UTC()
	workspace := deployment.SourceWorkspace()
	gitSourceID := deployment.SourceGitSourceID()
	source := strings.TrimSpace(deployment.Source)
	if source == "" {
		source = "external_sync"
	}
	return DeploymentHistory{
		ID:           newAppVersionID(createdAt),
		Workspace:    workspace,
		GitSourceID:  gitSourceID,
		App:          deployment.App,
		Commit:       deployment.Commit,
		Entrypoint:   deployment.Entrypoint,
		Source:       source,
		Status:       "deployed",
		DeploymentID: cloneStringPtr(deployment.DeploymentID),
		Message:      deployment.Message,
		CreatedBy:    cloneStringPtr(deployment.CreatedBy),
		ObjectURI:    deployment.ObjectURI,
		Deployment:   deployment,
		CreatedAt:    createdAt,
	}
}

func newAppVersionID(createdAt time.Time) string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err == nil {
		b[6] = (b[6] & 0x0f) | 0x40
		b[8] = (b[8] & 0x3f) | 0x80
		return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
	}
	return fmt.Sprintf("%d", createdAt.UnixNano())
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
