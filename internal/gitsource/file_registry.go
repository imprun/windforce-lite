package gitsource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/imprun/windforce-lite/internal/contract"
)

var ErrGitSourceNotFound = errors.New("git source not found")
var ErrGitSourceConflict = errors.New("git source already exists")

type Source struct {
	Workspace        string     `json:"workspace,omitempty"`
	ID               string     `json:"id"`
	RepoURL          string     `json:"repoUrl"`
	Branch           string     `json:"branch,omitempty"`
	Subpath          string     `json:"subpath,omitempty"`
	TokenEnv         string     `json:"tokenEnv,omitempty"`
	CreatedAt        *time.Time `json:"createdAt,omitempty"`
	LastSyncedCommit *string    `json:"lastSyncedCommit,omitempty"`
	LastSyncedAt     *time.Time `json:"lastSyncedAt,omitempty"`
}

type Patch struct {
	ID       *string
	RepoURL  *string
	Branch   *string
	Subpath  *string
	TokenEnv *string
}

type Snapshot struct {
	Sources map[string]Source `json:"sources"`
}

type FileRegistry struct {
	Path string
}

func NewFileRegistry(path string) *FileRegistry {
	return &FileRegistry{Path: path}
}

func (r *FileRegistry) Create(ctx context.Context, source Source) (Source, error) {
	if err := ctx.Err(); err != nil {
		return Source{}, err
	}
	source, err := normalizeSource(source)
	if err != nil {
		return Source{}, err
	}

	snapshot, err := r.Load(ctx)
	if err != nil {
		return Source{}, err
	}
	if snapshot.Sources == nil {
		snapshot.Sources = map[string]Source{}
	}
	sourceKey := key(source.Workspace, source.ID)
	if _, exists := snapshot.Sources[sourceKey]; exists {
		return Source{}, ErrGitSourceConflict
	}
	now := time.Now().UTC()
	if source.CreatedAt == nil {
		source.CreatedAt = &now
	}
	snapshot.Sources[sourceKey] = source
	if err := r.write(snapshot); err != nil {
		return Source{}, err
	}
	return source, nil
}

func (r *FileRegistry) Upsert(ctx context.Context, source Source) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	source, err := normalizeSource(source)
	if err != nil {
		return err
	}

	snapshot, err := r.Load(ctx)
	if err != nil {
		return err
	}
	if snapshot.Sources == nil {
		snapshot.Sources = map[string]Source{}
	}
	sourceKey := key(source.Workspace, source.ID)
	if existing, ok := snapshot.Sources[sourceKey]; ok {
		if source.CreatedAt == nil {
			source.CreatedAt = existing.CreatedAt
		}
		if source.LastSyncedCommit == nil {
			source.LastSyncedCommit = existing.LastSyncedCommit
		}
		if source.LastSyncedAt == nil {
			source.LastSyncedAt = existing.LastSyncedAt
		}
	}
	now := time.Now().UTC()
	if source.CreatedAt == nil {
		source.CreatedAt = &now
	}
	snapshot.Sources[sourceKey] = source
	return r.write(snapshot)
}

func (r *FileRegistry) Patch(ctx context.Context, workspace string, id string, patch Patch) (Source, error) {
	if err := ctx.Err(); err != nil {
		return Source{}, err
	}
	snapshot, err := r.Load(ctx)
	if err != nil {
		return Source{}, err
	}
	workspace = contract.NormalizeWorkspace(workspace)
	id = contract.NormalizeGitSourceID(id, "")
	oldKey := key(workspace, id)
	source, ok := snapshot.Sources[oldKey]
	if !ok {
		return Source{}, ErrGitSourceNotFound
	}
	if patch.ID != nil {
		source.ID = *patch.ID
	}
	if patch.RepoURL != nil {
		source.RepoURL = *patch.RepoURL
	}
	if patch.Branch != nil {
		source.Branch = *patch.Branch
	}
	if patch.Subpath != nil {
		source.Subpath = *patch.Subpath
	}
	if patch.TokenEnv != nil {
		source.TokenEnv = *patch.TokenEnv
	}
	source.Workspace = workspace
	source, err = normalizeSource(source)
	if err != nil {
		return Source{}, err
	}
	newKey := key(source.Workspace, source.ID)
	if newKey != oldKey {
		if _, exists := snapshot.Sources[newKey]; exists {
			return Source{}, ErrGitSourceConflict
		}
		delete(snapshot.Sources, oldKey)
	}
	snapshot.Sources[newKey] = source
	if err := r.write(snapshot); err != nil {
		return Source{}, err
	}
	return source, nil
}

func (r *FileRegistry) MarkSynced(ctx context.Context, workspace string, id string, commit string, syncedAt time.Time) (Source, error) {
	if err := ctx.Err(); err != nil {
		return Source{}, err
	}
	snapshot, err := r.Load(ctx)
	if err != nil {
		return Source{}, err
	}
	sourceKey := key(workspace, id)
	source, ok := snapshot.Sources[sourceKey]
	if !ok {
		return Source{}, ErrGitSourceNotFound
	}
	if syncedAt.IsZero() {
		syncedAt = time.Now().UTC()
	}
	source.LastSyncedCommit = &commit
	source.LastSyncedAt = &syncedAt
	snapshot.Sources[sourceKey] = source
	if err := r.write(snapshot); err != nil {
		return Source{}, err
	}
	return source, nil
}

func (r *FileRegistry) Delete(ctx context.Context, workspace string, id string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	snapshot, err := r.Load(ctx)
	if err != nil {
		return false, err
	}
	key := key(workspace, id)
	if _, ok := snapshot.Sources[key]; !ok {
		return false, nil
	}
	delete(snapshot.Sources, key)
	return true, r.write(snapshot)
}

func (r *FileRegistry) Get(ctx context.Context, workspace string, id string) (Source, error) {
	snapshot, err := r.Load(ctx)
	if err != nil {
		return Source{}, err
	}
	workspace = contract.NormalizeWorkspace(workspace)
	id = contract.NormalizeGitSourceID(id, "")
	source, ok := snapshot.Sources[key(workspace, id)]
	if !ok {
		return Source{}, ErrGitSourceNotFound
	}
	return source, nil
}

func (r *FileRegistry) Load(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	data, err := os.ReadFile(r.Path)
	if errors.Is(err, os.ErrNotExist) {
		return Snapshot{Sources: map[string]Source{}}, nil
	}
	if err != nil {
		return Snapshot{}, err
	}
	var snapshot Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return Snapshot{}, err
	}
	if snapshot.Sources == nil {
		snapshot.Sources = map[string]Source{}
	}
	return snapshot, nil
}

func (r *FileRegistry) write(snapshot Snapshot) error {
	if err := os.MkdirAll(filepath.Dir(r.Path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmpPath := r.Path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpPath, r.Path)
}

func key(workspace string, id string) string {
	return fmt.Sprintf("%s/%s", contract.NormalizeWorkspace(workspace), contract.NormalizeGitSourceID(id, ""))
}

func normalizeSource(source Source) (Source, error) {
	source.Workspace = contract.NormalizeWorkspace(source.Workspace)
	source.ID = contract.NormalizeGitSourceID(source.ID, "")
	if source.ID == contract.DefaultGitSourceID {
		return Source{}, errors.New("git source id is required")
	}
	if source.RepoURL == "" {
		return Source{}, errors.New("repo URL is required")
	}
	if source.Branch == "" {
		source.Branch = "main"
	}
	subpath, err := contract.NormalizeSourcePath(source.Subpath)
	if err != nil {
		return Source{}, err
	}
	source.Subpath = subpath
	return source, nil
}
