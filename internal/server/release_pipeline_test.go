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

func TestGitSourceSyncRejectsCandidateWhenRuntimePreparationFails(t *testing.T) {
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

	var prepared contract.Deployment
	preparer := executionBundleManagerStub{build: func(_ context.Context, deployment contract.Deployment) (contract.Deployment, error) {
		prepared = deployment
		return contract.Deployment{}, errors.New("dependency version is unavailable")
	}}
	server := httptest.NewServer(New(Config{
		Store:            state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		Catalog:          releaseCatalog,
		Syncer:           &syncer.Syncer{Store: bundleStore, CloneRoot: tempDir},
		ExecutionBundles: preparer,
		GitSources:       registry,
		EnableAPI:        true,
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
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("sync status = %d, want %d: %s", response.StatusCode, http.StatusUnprocessableEntity, body)
	}
	if !strings.Contains(string(body), "execution bundle build failed: dependency version is unavailable") {
		t.Fatalf("sync body = %s", body)
	}
	if prepared.Commit == "" || prepared.GitSourceID != "1" {
		t.Fatalf("prepared deployment = %#v", prepared)
	}
	if _, err := releaseCatalog.GetLatestReleaseCandidate(context.Background(), "ws-a", "1"); !errors.Is(err, catalog.ErrReleaseCandidateNotFound) {
		t.Fatalf("candidate error = %v, want not found", err)
	}
	materialized, err := bundleStore.Exists(context.Background(), "ws-a", "1", prepared.Commit)
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
	if source.LastSyncedCommit != nil || source.LastSyncedAt != nil {
		t.Fatalf("failed source was marked synchronized: %#v", source)
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
		Workspace:    "ws-a",
		GitSourceID:  source.ID,
		App:          "echo",
		Commit:       "commit-a",
		BundleDigest: "sha256:" + strings.Repeat("a", 64),
		BundleURI:    "execution-bundle://sha256/" + strings.Repeat("a", 64),
		Entrypoint:   "main.py",
		ScriptLang:   "python",
		Actions: map[string]contract.Action{
			"echo": {Action: "echo"},
		},
	}
	if _, err := releaseCatalog.SaveReleaseCandidate(ctx, deployment, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	preparer := executionBundleManagerStub{
		build: func(_ context.Context, deployment contract.Deployment) (contract.Deployment, error) {
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
		EnableAPI:        true,
	}))
	defer httpServer.Close()

	body := bytes.NewBufferString(`{"confirm":true,"commit":"commit-a"}`)
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
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("deploy status = %d, want %d: %s", resp.StatusCode, http.StatusConflict, responseBody)
	}
	if !strings.Contains(string(responseBody), "release candidate is not ready: execution bundle digest mismatch") {
		t.Fatalf("deploy body = %s", responseBody)
	}
	if _, err := releaseCatalog.GetDeploymentForWorkspace(ctx, "ws-a", "echo"); !errors.Is(err, catalog.ErrDeploymentNotFound) {
		t.Fatalf("invalid candidate was activated: %v", err)
	}
}
