package gitsource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/imprun/windforce-lite/internal/contract"
)

var ErrGitSourceNotFound = errors.New("git source not found")

type Source struct {
	Workspace string `json:"workspace,omitempty"`
	ID        string `json:"id"`
	RepoURL   string `json:"repoUrl"`
	Branch    string `json:"branch,omitempty"`
	Subpath   string `json:"subpath,omitempty"`
	TokenEnv  string `json:"tokenEnv,omitempty"`
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

func (r *FileRegistry) Upsert(ctx context.Context, source Source) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	source.Workspace = contract.NormalizeWorkspace(source.Workspace)
	source.ID = contract.NormalizeGitSourceID(source.ID, "")
	if source.ID == contract.DefaultGitSourceID {
		return errors.New("git source id is required")
	}
	if source.RepoURL == "" {
		return errors.New("repo URL is required")
	}
	if source.Branch == "" {
		source.Branch = "main"
	}
	subpath, err := contract.NormalizeSourcePath(source.Subpath)
	if err != nil {
		return err
	}
	source.Subpath = subpath

	snapshot, err := r.Load(ctx)
	if err != nil {
		return err
	}
	if snapshot.Sources == nil {
		snapshot.Sources = map[string]Source{}
	}
	snapshot.Sources[key(source.Workspace, source.ID)] = source
	return r.write(snapshot)
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
