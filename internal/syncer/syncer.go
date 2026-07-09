package syncer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

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
		return src.LocalDir, func() {}, nil
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

	sourceDir := filepath.Join(cloneDir, "source")
	if err := source.CloneCommit(ctx, src.RepoURL, src.Branch, commit, sourceDir, src.Token); err != nil {
		cleanup()
		return "", nil, err
	}
	return sourceDir, cleanup, nil
}
