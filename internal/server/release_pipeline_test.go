package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/imprun/windforce-core/internal/bundle"
	"github.com/imprun/windforce-core/internal/catalog"
	"github.com/imprun/windforce-core/internal/contract"
	"github.com/imprun/windforce-core/internal/gitsource"
	"github.com/imprun/windforce-core/internal/state"
	"github.com/imprun/windforce-core/internal/syncer"
)

type executionBundleManagerStub struct {
	build    func(context.Context, contract.Deployment) (contract.Deployment, error)
	validate func(context.Context, contract.Deployment) error
}

func (s executionBundleManagerStub) BuildExecutionBundle(ctx context.Context, deployment contract.Deployment) (contract.Deployment, error) {
	return s.build(ctx, deployment)
}

func (s executionBundleManagerStub) ValidateExecutionBundle(ctx context.Context, deployment contract.Deployment) error {
	if s.validate == nil {
		return nil
	}
	return s.validate(ctx, deployment)
}

func readyExecutionBundleManager() ExecutionBundleManager {
	return executionBundleManagerStub{build: func(_ context.Context, deployment contract.Deployment) (contract.Deployment, error) {
		deployment.BundleDigest = "sha256:test-bundle"
		deployment.BundleURI = "execution-bundle://sha256/test-bundle"
		return deployment, nil
	}}
}

func TestGitSourceSyncStoresSourceWithoutRuntimePreparation(t *testing.T) {
	tempDir := t.TempDir()
	repoDir := createTestGitSourceRepo(t, tempDir, "repo", "")
	bundleStore := bundle.NewLocalStore(filepath.Join(tempDir, "store"))
	releaseCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	registry := gitsource.NewFileRegistry(filepath.Join(tempDir, "git-sources.json"))
	if err := registry.Upsert(context.Background(), gitsource.Source{
		Workspace: "ws-a",
		Name:      "source-a",
		RepoURL:   filepath.ToSlash(repoDir),
		Branch:    "main",
	}); err != nil {
		t.Fatal(err)
	}

	buildCalled := false
	preparer := executionBundleManagerStub{build: func(_ context.Context, deployment contract.Deployment) (contract.Deployment, error) {
		buildCalled = true
		return contract.Deployment{}, errors.New("dependency version is unavailable")
	}}
	server := httptest.NewServer(New(Config{
		Store:            state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		Catalog:          releaseCatalog,
		Syncer:           &syncer.Syncer{Store: bundleStore, CloneRoot: tempDir},
		ExecutionBundles: preparer,
		GitSources:       registry,
	}))
	defer server.Close()

	response, err := http.Post(server.URL+"/api/w/ws-a/git_sources/1/sync", "application/json", bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("sync status = %d, want %d: %s", response.StatusCode, http.StatusOK, body)
	}
	if buildCalled {
		t.Fatal("sync prepared the runtime execution bundle")
	}
	candidate, err := releaseCatalog.GetLatestReleaseCandidate(context.Background(), "ws-a", "1")
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Deployment.Commit == "" || candidate.Deployment.GitSourceID != "1" || candidate.Deployment.BundleDigest != "" {
		t.Fatalf("synchronized source = %#v", candidate.Deployment)
	}
	materialized, err := bundleStore.Exists(context.Background(), "ws-a", "1", candidate.Deployment.Commit)
	if err != nil {
		t.Fatal(err)
	}
	if !materialized {
		t.Fatal("source bundle was not materialized before runtime preparation")
	}
	source, err := registry.Get(context.Background(), "ws-a", "1")
	if err != nil {
		t.Fatal(err)
	}
	if source.LastSyncedCommit == nil || *source.LastSyncedCommit != candidate.Deployment.Commit || source.LastSyncedAt == nil {
		t.Fatalf("source synchronization marker = %#v", source)
	}
}

func TestGitSourceDeployRejectsCandidateWhenExecutionBundleIsInvalid(t *testing.T) {
	tempDir := t.TempDir()
	ctx := context.Background()
	releaseCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	registry := gitsource.NewFileRegistry(filepath.Join(tempDir, "git-sources.json"))
	source, err := registry.Create(ctx, gitsource.Source{
		Workspace: "ws-a",
		Name:      "source-a",
		RepoURL:   filepath.ToSlash(filepath.Join(tempDir, "repo")),
		Branch:    "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	deployment := contract.Deployment{
		Workspace:   "ws-a",
		GitSourceID: source.ID,
		App:         "echo",
		Commit:      "commit-a",
		Entrypoint:  "main.py",
		ScriptLang:  "python",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo"},
		},
	}
	if _, err := releaseCatalog.SaveReleaseCandidate(ctx, deployment, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	buildCalled := false
	preparer := executionBundleManagerStub{
		build: func(_ context.Context, deployment contract.Deployment) (contract.Deployment, error) {
			buildCalled = true
			deployment.BundleDigest = "sha256:" + strings.Repeat("a", 64)
			deployment.BundleURI = "execution-bundle://sha256/" + strings.Repeat("a", 64)
			return deployment, nil
		},
		validate: func(context.Context, contract.Deployment) error {
			return errors.New("execution bundle digest mismatch")
		},
	}
	httpServer := httptest.NewServer(New(Config{
		Store:            state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		Catalog:          releaseCatalog,
		ExecutionBundles: preparer,
		GitSources:       registry,
	}))
	defer httpServer.Close()

	body := bytes.NewBufferString(`{"confirm":true}`)
	req, err := http.NewRequest(http.MethodPost, httpServer.URL+"/api/w/ws-a/git_sources/"+source.ID+"/deploy", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Windforce-Actor", "operator@example.test")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("deploy status = %d, want %d: %s", resp.StatusCode, http.StatusUnprocessableEntity, responseBody)
	}
	if !strings.Contains(string(responseBody), "execution bundle validation failed: execution bundle digest mismatch") {
		t.Fatalf("deploy body = %s", responseBody)
	}
	if !buildCalled {
		t.Fatal("deploy did not prepare the synchronized source")
	}
	if _, err := releaseCatalog.GetDeploymentForWorkspace(ctx, "ws-a", "echo"); !errors.Is(err, catalog.ErrDeploymentNotFound) {
		t.Fatalf("invalid candidate was activated: %v", err)
	}
}

func TestGitSourceDeployBuildFailureKeepsActiveReleaseAndUsesLatestSync(t *testing.T) {
	tempDir := t.TempDir()
	ctx := context.Background()
	releaseCatalog := catalog.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	registry := gitsource.NewFileRegistry(filepath.Join(tempDir, "git-sources.json"))
	source, err := registry.Create(ctx, gitsource.Source{
		Workspace: "ws-a",
		Name:      "source-a",
		RepoURL:   filepath.ToSlash(filepath.Join(tempDir, "repo")),
		Branch:    "main",
	})
	if err != nil {
		t.Fatal(err)
	}
	base := contract.Deployment{
		Workspace:   "ws-a",
		GitSourceID: source.ID,
		App:         "echo",
		Entrypoint:  "main.py",
		ScriptLang:  "python",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo"},
		},
	}
	active := base
	active.Commit = "active-commit"
	active.BundleDigest = "sha256:" + strings.Repeat("c", 64)
	active.BundleURI = "execution-bundle://sha256/" + strings.Repeat("c", 64)
	if _, err := releaseCatalog.PublishRelease(ctx, active, time.Now().UTC().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	first := base
	first.Commit = "synced-first"
	if _, err := releaseCatalog.SaveReleaseCandidate(ctx, first, time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	latest := base
	latest.Commit = "synced-latest"
	if _, err := releaseCatalog.SaveReleaseCandidate(ctx, latest, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	preparedCommit := ""
	httpServer := httptest.NewServer(New(Config{
		Store:   state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		Catalog: releaseCatalog,
		ExecutionBundles: executionBundleManagerStub{build: func(_ context.Context, deployment contract.Deployment) (contract.Deployment, error) {
			preparedCommit = deployment.Commit
			return contract.Deployment{}, errors.New("dependency version is unavailable")
		}},
		GitSources: registry,
	}))
	defer httpServer.Close()

	req, err := http.NewRequest(http.MethodPost, httpServer.URL+"/api/w/ws-a/git_sources/"+source.ID+"/deploy", bytes.NewBufferString(`{"confirm":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Windforce-Actor", "operator@example.test")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("deploy status = %d, want %d: %s", resp.StatusCode, http.StatusUnprocessableEntity, body)
	}
	if preparedCommit != latest.Commit {
		t.Fatalf("prepared commit = %q, want latest synchronized %q", preparedCommit, latest.Commit)
	}
	deployed, err := releaseCatalog.GetDeploymentForWorkspace(ctx, "ws-a", "echo")
	if err != nil {
		t.Fatal(err)
	}
	if deployed.Commit != active.Commit {
		t.Fatalf("active commit = %q, want unchanged %q", deployed.Commit, active.Commit)
	}
}
