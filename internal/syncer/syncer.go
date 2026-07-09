package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/imprun/windforce-lite/internal/bundle"
	"github.com/imprun/windforce-lite/internal/contract"
	"github.com/imprun/windforce-lite/internal/manifest"
	"github.com/imprun/windforce-lite/internal/source"
)

type Catalog interface {
	UpsertDeployment(ctx context.Context, deployment contract.Deployment) error
}

type Source struct {
	Workspace   string
	GitSourceID string
	App         string
	RepoURL     string
	Branch      string
	Commit      string
	Subpath     string
	Token       string
	LocalDir    string
}

type Syncer struct {
	Store     bundle.Store
	Catalog   Catalog
	CloneRoot string
}

func (s *Syncer) Sync(ctx context.Context, src Source) (contract.Deployment, error) {
	if s.Store == nil {
		return contract.Deployment{}, errors.New("bundle store is required")
	}

	commit := src.Commit
	var err error
	if commit == "" {
		if src.LocalDir != "" {
			commit, err = source.TreeDigest(ctx, src.LocalDir)
		} else {
			if src.RepoURL == "" {
				return contract.Deployment{}, errors.New("repo URL or local source is required")
			}
			commit, err = source.ResolveBranchCommit(ctx, src.RepoURL, src.Branch, src.Token)
		}
		if err != nil {
			return contract.Deployment{}, err
		}
	}

	sourceDir, cleanup, err := s.prepareSource(ctx, src, commit)
	if err != nil {
		return contract.Deployment{}, err
	}
	defer cleanup()

	app, err := manifest.Load(sourceDir)
	if err != nil {
		return contract.Deployment{}, err
	}
	if src.App != "" && src.App != app.App {
		return contract.Deployment{}, fmt.Errorf("source app %q does not match manifest app %q", src.App, app.App)
	}
	if err := validateActionSchemas(sourceDir, app); err != nil {
		return contract.Deployment{}, err
	}

	workspace := contract.NormalizeWorkspace(src.Workspace)
	gitSourceID := contract.NormalizeGitSourceID(src.GitSourceID, app.App)
	exists, err := s.Store.Exists(ctx, workspace, gitSourceID, commit)
	if err != nil {
		return contract.Deployment{}, err
	}
	if !exists {
		if err := s.Store.Materialize(ctx, workspace, gitSourceID, commit, sourceDir); err != nil {
			return contract.Deployment{}, err
		}
	}

	deployment := contract.Deployment{
		Workspace:   workspace,
		GitSourceID: gitSourceID,
		App:         app.App,
		Tag:         app.Tag,
		Commit:      commit,
		Actions:     app.Actions,
	}
	deployment.ObjectURI = deployment.SourceObjectURI()

	// Catalog is updated only after the source bundle is fully materialized.
	if s.Catalog != nil {
		if err := s.Catalog.UpsertDeployment(ctx, deployment); err != nil {
			return contract.Deployment{}, err
		}
	}
	return deployment, nil
}

func (s *Syncer) prepareSource(ctx context.Context, src Source, commit string) (string, func(), error) {
	if src.LocalDir != "" {
		sourceDir, err := sourceDirForSubpath(src.LocalDir, src.Subpath)
		if err != nil {
			return "", nil, err
		}
		return sourceDir, func() {}, nil
	}
	if src.RepoURL == "" {
		return "", nil, errors.New("repo URL is required")
	}

	cloneRoot := s.CloneRoot
	if cloneRoot == "" {
		cloneRoot = os.TempDir()
	}
	if err := os.MkdirAll(cloneRoot, 0o755); err != nil {
		return "", nil, err
	}
	cloneDir, err := os.MkdirTemp(cloneRoot, "windforce-lite-clone-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() {
		_ = os.RemoveAll(cloneDir)
	}

	repoDir := filepath.Join(cloneDir, "source")
	cloned := false
	if src.Subpath != "" {
		if err := source.CloneCommitSparse(ctx, src.RepoURL, src.Branch, commit, repoDir, src.Subpath, src.Token); err != nil {
			log.Printf("syncer: sparse clone %s@%s fell back to full clone: %v", src.GitSourceID, commit, err)
			_ = os.RemoveAll(repoDir)
		} else {
			cloned = true
		}
	}
	if !cloned {
		if err := source.CloneCommit(ctx, src.RepoURL, src.Branch, commit, repoDir, src.Token); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	sourceDir, err := sourceDirForSubpath(repoDir, src.Subpath)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return sourceDir, cleanup, nil
}

func validateActionSchemas(root string, app contract.App) error {
	for key, action := range app.Actions {
		if err := validateSchemaFile(root, action.InputSchema); err != nil {
			return fmt.Errorf("action %s.%s input schema: %w", app.App, key, err)
		}
		if err := validateSchemaFile(root, action.OutputSchema); err != nil {
			return fmt.Errorf("action %s.%s output schema: %w", app.App, key, err)
		}
	}
	return nil
}

func validateSchemaFile(root string, rel string) error {
	rel = strings.TrimSpace(strings.ReplaceAll(rel, "\\", "/"))
	if rel == "" {
		return nil
	}
	if strings.Contains(rel, "..") {
		return fmt.Errorf("schema path %q must be a relative path inside the app", rel)
	}
	normalized, err := contract.NormalizeSourcePath(rel)
	if err != nil {
		return fmt.Errorf("schema path %q must be a relative path inside the app", rel)
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(normalized)))
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("manifest references schema %q but the file is missing", rel)
		}
		return err
	}
	if !json.Valid(data) {
		return fmt.Errorf("schema %q is not valid JSON", rel)
	}
	return nil
}

func sourceDirForSubpath(root string, subpath string) (string, error) {
	normalized, err := contract.NormalizeSourcePath(subpath)
	if err != nil {
		return "", err
	}
	if normalized == "" {
		return root, nil
	}
	sourceDir := filepath.Join(root, filepath.FromSlash(normalized))
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	sourceAbs, err := filepath.Abs(sourceDir)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(rootAbs, sourceAbs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("source subpath %q escapes git source root", subpath)
	}
	return sourceDir, nil
}
