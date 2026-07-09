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
	"time"

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
	if err := checkLockfile(sourceDir); err != nil {
		return contract.Deployment{}, err
	}
	if src.App != "" && src.App != app.App {
		return contract.Deployment{}, fmt.Errorf("source app %q does not match manifest app %q", src.App, app.App)
	}
	if err := materializeActionSchemas(sourceDir, &app); err != nil {
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
			return contract.Deployment{}, fmt.Errorf("materialize: %w", err)
		}
	}

	updatedAt := time.Now().UTC()
	var message *string
	if src.RepoURL != "" {
		if subject, err := source.CommitSubject(ctx, sourceDir); err == nil && strings.TrimSpace(subject) != "" {
			trimmed := strings.TrimSpace(subject)
			message = &trimmed
		}
	}
	deployment := contract.Deployment{
		Workspace:            workspace,
		GitSourceID:          gitSourceID,
		App:                  app.App,
		Tag:                  app.Tag,
		Entrypoint:           app.Entrypoint,
		Runtime:              app.Runtime,
		ScriptLang:           app.ScriptLang,
		TimeoutS:             app.TimeoutS,
		MaxConcurrent:        app.MaxConcurrent,
		RequiredCapabilities: app.Capabilities,
		Commit:               commit,
		Message:              message,
		Actions:              app.Actions,
		UpdatedAt:            &updatedAt,
	}
	for key, action := range deployment.Actions {
		action.UpdatedAt = &updatedAt
		deployment.Actions[key] = action
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

func materializeActionSchemas(root string, app *contract.App) error {
	if app.Actions == nil {
		return nil
	}
	for key, action := range app.Actions {
		inputSchema, err := readSchemaFile(root, action.InputSchema)
		if err != nil {
			return fmt.Errorf("action %s.%s input schema: %w", app.App, key, err)
		}
		outputSchema, err := readSchemaFile(root, action.OutputSchema)
		if err != nil {
			return fmt.Errorf("action %s.%s output schema: %w", app.App, key, err)
		}
		action.InputSchemaBody = inputSchema
		action.OutputSchemaBody = outputSchema
		app.Actions[key] = action
	}
	return nil
}

func readSchemaFile(root string, rel string) (json.RawMessage, error) {
	if rel == "" {
		return json.RawMessage([]byte("{}")), nil
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") || strings.Contains(rel, "..") {
		return nil, fmt.Errorf("schema path %q must be a relative path inside the app", rel)
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("manifest references schema %q but the file is missing", rel)
		}
		return nil, err
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("schema %q is not valid JSON", rel)
	}
	return json.RawMessage(append([]byte(nil), data...)), nil
}

func checkLockfile(root string) error {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var pkg struct {
		Dependencies         map[string]json.RawMessage `json:"dependencies"`
		DevDependencies      map[string]json.RawMessage `json:"devDependencies"`
		PeerDependencies     map[string]json.RawMessage `json:"peerDependencies"`
		OptionalDependencies map[string]json.RawMessage `json:"optionalDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return fmt.Errorf("parse package.json: %w", err)
	}
	if len(pkg.Dependencies)+len(pkg.DevDependencies)+len(pkg.PeerDependencies)+len(pkg.OptionalDependencies) == 0 {
		return nil
	}
	for _, lock := range []string{"bun.lock", "bun.lockb"} {
		if _, err := os.Stat(filepath.Join(root, lock)); err == nil {
			return nil
		}
	}
	return fmt.Errorf("package.json declares dependencies but no bun.lock (or bun.lockb) is committed at the source root — commit a lockfile so installs are reproducible (bun install --frozen-lockfile)")
}

func sourceDirForSubpath(root string, subpath string) (string, error) {
	if err := contract.ValidateSourceSubpath(subpath); err != nil {
		return "", err
	}
	if subpath == "" {
		return root, nil
	}
	sourceDir := filepath.Join(root, filepath.FromSlash(subpath))
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
