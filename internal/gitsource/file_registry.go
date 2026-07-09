package gitsource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/imprun/windforce-lite/internal/contract"
)

var ErrGitSourceNotFound = errors.New("git source not found")
var ErrGitSourceConflict = errors.New("git source already exists")

type Source struct {
	Workspace        string     `json:"workspace,omitempty"`
	ID               string     `json:"id"`
	Name             string     `json:"name,omitempty"`
	RepoURL          string     `json:"repoUrl"`
	Branch           string     `json:"branch,omitempty"`
	Subpath          string     `json:"subpath,omitempty"`
	TokenEnv         string     `json:"tokenEnv,omitempty"`
	Kind             string     `json:"kind,omitempty"`
	CreatedAt        *time.Time `json:"createdAt,omitempty"`
	LastSyncedCommit *string    `json:"lastSyncedCommit,omitempty"`
	LastSyncedAt     *time.Time `json:"lastSyncedAt,omitempty"`
}

type Patch struct {
	Name     *string
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
	if hasSourceName(snapshot, source.Workspace, source.Name, "") {
		return Source{}, ErrGitSourceConflict
	}
	if source.ID == "" {
		source.ID = strconv.FormatInt(nextSourceID(snapshot), 10)
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
	if source.ID == "" {
		if existing, ok := findSourceByName(snapshot, source.Workspace, source.Name); ok {
			source.ID = existing.ID
		} else {
			source.ID = strconv.FormatInt(nextSourceID(snapshot), 10)
		}
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
	oldKey, source, ok := resolveSource(snapshot, workspace, id)
	if !ok {
		return Source{}, ErrGitSourceNotFound
	}
	previousRepoURL := source.RepoURL
	previousBranch := source.Branch
	previousSubpath := source.Subpath
	namePatch := patch.Name
	if namePatch == nil {
		namePatch = patch.ID
	}
	if namePatch != nil {
		source.Name = *namePatch
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
	if source.RepoURL != previousRepoURL || source.Branch != previousBranch || source.Subpath != previousSubpath {
		source.LastSyncedCommit = nil
		source.LastSyncedAt = nil
	}
	if hasSourceName(snapshot, source.Workspace, source.Name, oldKey) {
		return Source{}, ErrGitSourceConflict
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
	workspace = contract.NormalizeWorkspace(workspace)
	sourceKey, source, ok := resolveSource(snapshot, workspace, id)
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
	workspace = contract.NormalizeWorkspace(workspace)
	sourceKey, _, ok := resolveSource(snapshot, workspace, id)
	if !ok {
		return false, nil
	}
	delete(snapshot.Sources, sourceKey)
	return true, r.write(snapshot)
}

func (r *FileRegistry) Get(ctx context.Context, workspace string, id string) (Source, error) {
	snapshot, err := r.Load(ctx)
	if err != nil {
		return Source{}, err
	}
	workspace = contract.NormalizeWorkspace(workspace)
	_, source, ok := resolveSource(snapshot, workspace, id)
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
	snapshot.Sources = normalizeSnapshot(snapshot.Sources)
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
	return fmt.Sprintf("%s/%s", contract.NormalizeWorkspace(workspace), strings.TrimSpace(id))
}

func normalizeSource(source Source) (Source, error) {
	source.Workspace = contract.NormalizeWorkspace(source.Workspace)
	rawID := strings.TrimSpace(source.ID)
	source.ID = normalizeNumericID(rawID)
	source.Name = strings.TrimSpace(source.Name)
	if source.Name == "" && source.ID != "" {
		source.Name = source.ID
	}
	if source.Name == "" {
		source.Name = rawID
	}
	if source.Name == "" || source.Name == contract.DefaultGitSourceID {
		return Source{}, errors.New("git source name is required")
	}
	if source.RepoURL == "" {
		return Source{}, errors.New("repo URL is required")
	}
	if source.Branch == "" {
		source.Branch = "main"
	}
	if source.Kind == "" {
		source.Kind = "external"
	}
	if source.Kind != "external" && source.Kind != "managed" {
		return Source{}, errors.New("git source kind must be external or managed")
	}
	if err := contract.ValidateSourceSubpath(source.Subpath); err != nil {
		return Source{}, err
	}
	return source, nil
}

func normalizeSnapshot(sources map[string]Source) map[string]Source {
	prepared := make([]Source, 0, len(sources))
	used := map[string]bool{}
	nextID := int64(1)
	for _, source := range sources {
		source.Workspace = contract.NormalizeWorkspace(source.Workspace)
		source.Name = strings.TrimSpace(source.Name)
		source.ID = strings.TrimSpace(source.ID)
		if source.Name == "" && !isPositiveInteger(source.ID) {
			source.Name = source.ID
			source.ID = ""
		}
		if source.Name == "" {
			source.Name = source.ID
		}
		if id, ok := parsePositiveID(source.ID); ok {
			used[key(source.Workspace, source.ID)] = true
			if id >= nextID {
				nextID = id + 1
			}
		} else {
			source.ID = ""
		}
		if source.Kind == "" {
			source.Kind = "external"
		}
		if source.Branch == "" {
			source.Branch = "main"
		}
		prepared = append(prepared, source)
	}
	normalized := map[string]Source{}
	for _, source := range prepared {
		if source.ID != "" {
			normalized[key(source.Workspace, source.ID)] = source
			continue
		}
		for {
			id := strconv.FormatInt(nextID, 10)
			nextID++
			sourceKey := key(source.Workspace, id)
			if used[sourceKey] {
				continue
			}
			source.ID = id
			used[sourceKey] = true
			normalized[sourceKey] = source
			break
		}
	}
	return normalized
}

func resolveSource(snapshot Snapshot, workspace string, ref string) (string, Source, bool) {
	workspace = contract.NormalizeWorkspace(workspace)
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", Source{}, false
	}
	if isPositiveInteger(ref) {
		sourceKey := key(workspace, ref)
		source, ok := snapshot.Sources[sourceKey]
		return sourceKey, source, ok
	}
	if source, ok := findSourceByName(snapshot, workspace, ref); ok {
		return key(source.Workspace, source.ID), source, true
	}
	return "", Source{}, false
}

func findSourceByName(snapshot Snapshot, workspace string, name string) (Source, bool) {
	workspace = contract.NormalizeWorkspace(workspace)
	name = strings.TrimSpace(name)
	for _, source := range snapshot.Sources {
		if contract.NormalizeWorkspace(source.Workspace) == workspace && source.Name == name {
			return source, true
		}
	}
	return Source{}, false
}

func hasSourceName(snapshot Snapshot, workspace string, name string, exceptKey string) bool {
	workspace = contract.NormalizeWorkspace(workspace)
	name = strings.TrimSpace(name)
	for sourceKey, source := range snapshot.Sources {
		if sourceKey == exceptKey {
			continue
		}
		if contract.NormalizeWorkspace(source.Workspace) == workspace && source.Name == name {
			return true
		}
	}
	return false
}

func nextSourceID(snapshot Snapshot) int64 {
	next := int64(1)
	for _, source := range snapshot.Sources {
		if id, ok := parsePositiveID(source.ID); ok && id >= next {
			next = id + 1
		}
	}
	return next
}

func normalizeNumericID(value string) string {
	value = strings.TrimSpace(value)
	if isPositiveInteger(value) {
		return value
	}
	return ""
}

func isPositiveInteger(value string) bool {
	_, ok := parsePositiveID(value)
	return ok
}

func parsePositiveID(value string) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	return id, err == nil && id > 0
}
