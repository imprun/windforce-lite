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

	"github.com/imprun/windforce-core/internal/bundle"
	"github.com/imprun/windforce-core/internal/catalog"
	"github.com/imprun/windforce-core/internal/contract"
	"github.com/imprun/windforce-core/internal/gitsource"
	"github.com/imprun/windforce-core/internal/state"
	"github.com/imprun/windforce-core/internal/syncer"
)

type candidatePreparerFunc func(context.Context, contract.Deployment) (string, error)

func (f candidatePreparerFunc) Prepare(ctx context.Context, deployment contract.Deployment) (string, error) {
	return f(ctx, deployment)
}

func allowCandidatePreparation() CandidatePreparer {
	return candidatePreparerFunc(func(context.Context, contract.Deployment) (string, error) {
		return "prepared", nil
	})
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
	preparer := candidatePreparerFunc(func(_ context.Context, deployment contract.Deployment) (string, error) {
		prepared = deployment
		return "", errors.New("dependency version is unavailable")
	})
	server := httptest.NewServer(New(Config{
		Store:             state.NewLocalStore(filepath.Join(tempDir, "state.json")),
		Catalog:           releaseCatalog,
		Syncer:            &syncer.Syncer{Store: bundleStore, CloneRoot: tempDir},
		CandidatePreparer: preparer,
		GitSources:        registry,
		EnableAPI:         true,
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
	if !strings.Contains(string(body), "release candidate preparation failed: dependency version is unavailable") {
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
