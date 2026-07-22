package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/imprun/windforce-core/internal/catalog"
	"github.com/imprun/windforce-core/internal/contract"
	controlevent "github.com/imprun/windforce-core/internal/event"
	"github.com/imprun/windforce-core/internal/state"
)

func TestReleaseRollbackMovesActivePointerWithoutRebuildingOrChangingCandidate(t *testing.T) {
	ctx := context.Background()
	store := state.NewLocalStore(filepath.Join(t.TempDir(), "state.json"))
	first := rollbackTestDeployment("commit-a", "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	second := rollbackTestDeployment("commit-b", "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if _, err := store.SaveReleaseCandidate(ctx, first, time.Date(2026, 7, 18, 1, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishRelease(ctx, first, time.Date(2026, 7, 18, 1, 1, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveReleaseCandidate(ctx, second, time.Date(2026, 7, 18, 1, 2, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishRelease(ctx, second, time.Date(2026, 7, 18, 1, 3, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	before, err := store.LoadCatalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	firstReleaseID := before.History[0].ID
	secondReleaseID := before.History[1].ID
	buildCalls := 0
	validatedDigest := ""
	manager := executionBundleManagerStub{
		build: func(_ context.Context, deployment contract.Deployment) (contract.Deployment, error) {
			buildCalls++
			return contract.Deployment{}, errors.New("rollback must not build an execution bundle")
		},
		validate: func(_ context.Context, deployment contract.Deployment) error {
			validatedDigest = deployment.BundleDigest
			return nil
		},
	}
	httpServer := httptest.NewServer(New(Config{
		Store:            store,
		Catalog:          store,
		ExecutionBundles: manager,
	}))
	defer httpServer.Close()

	beforeRun := createRollbackTestRun(t, httpServer.URL, "before-rollback")
	if beforeRun.PinnedRelease.Commit != "commit-b" {
		t.Fatalf("run before rollback pinned %q, want commit-b", beforeRun.PinnedRelease.Commit)
	}

	rollbackBody := []byte(`{"confirm":true,"reason":"restore stable release"}`)
	request, err := http.NewRequest(http.MethodPost, httpServer.URL+"/api/w/ws-a/apps/echo/releases/"+firstReleaseID+"/rollback", bytes.NewReader(rollbackBody))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("content-type", "application/json")
	request.Header.Set("x-windforce-actor", "operator@example.test")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("rollback status = %d, want 200", response.StatusCode)
	}
	var rollbackResult canonicalReleaseRollbackResult
	if err := json.NewDecoder(response.Body).Decode(&rollbackResult); err != nil {
		t.Fatal(err)
	}
	if rollbackResult.ActiveReleaseID != firstReleaseID || rollbackResult.PreviousReleaseID != secondReleaseID {
		t.Fatalf("rollback result = %#v", rollbackResult)
	}
	if buildCalls != 0 || validatedDigest != first.BundleDigest {
		t.Fatalf("bundle operations: builds=%d validated=%q", buildCalls, validatedDigest)
	}

	after, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	catalogSnapshot := after.ReleaseCatalog
	key := catalog.DeploymentKey("ws-a", "echo")
	if catalogSnapshot.ActiveHistoryIDs[key] != firstReleaseID || catalogSnapshot.Deployments[key].Commit != "commit-a" {
		t.Fatalf("active release = id %q deployment %#v", catalogSnapshot.ActiveHistoryIDs[key], catalogSnapshot.Deployments[key])
	}
	if len(catalogSnapshot.History) != 2 {
		t.Fatalf("history count = %d, want 2", len(catalogSnapshot.History))
	}
	latest, err := store.GetLatestReleaseCandidate(ctx, "ws-a", "source-a")
	if err != nil {
		t.Fatal(err)
	}
	if latest.Deployment.Commit != "commit-b" {
		t.Fatalf("latest synchronized commit = %q, want commit-b", latest.Deployment.Commit)
	}
	marker := catalogSnapshot.SourceMarkers[catalog.SourceReleaseKey("ws-a", "source-a")]
	if marker.Commit != "commit-a" {
		t.Fatalf("active source marker = %#v", marker)
	}
	if got, err := store.GetRun(ctx, beforeRun.RunID); err != nil || got.Deployment.Commit != "commit-b" {
		t.Fatalf("existing run changed after rollback: run=%#v err=%v", got, err)
	}
	afterRun := createRollbackTestRun(t, httpServer.URL, "after-rollback")
	if afterRun.PinnedRelease.Commit != "commit-a" {
		t.Fatalf("run after rollback pinned %q, want commit-a", afterRun.PinnedRelease.Commit)
	}
	rollbackEvents := 0
	for _, event := range after.ControlPlaneEvents {
		if event.Type == controlevent.ReleaseRolledBackType {
			rollbackEvents++
		}
	}
	if rollbackEvents != 1 {
		t.Fatalf("rollback event count = %d, want 1", rollbackEvents)
	}
	rollbackAudits := 0
	for _, audit := range catalogSnapshot.Audit {
		if audit.Kind == "release_rolled_back" {
			rollbackAudits++
			if audit.Actor != "operator@example.test" || audit.App != "echo" || audit.Detail == "" {
				t.Fatalf("rollback audit = %#v", audit)
			}
		}
	}
	if rollbackAudits != 1 {
		t.Fatalf("rollback audit count = %d, want 1", rollbackAudits)
	}

	historyResponse, err := http.Get(httpServer.URL + "/api/w/ws-a/apps/echo/history")
	if err != nil {
		t.Fatal(err)
	}
	defer historyResponse.Body.Close()
	if historyResponse.StatusCode != http.StatusOK {
		t.Fatalf("history status = %d, want 200", historyResponse.StatusCode)
	}
	var history []canonicalAppHistoryItem
	if err := json.NewDecoder(historyResponse.Body).Decode(&history); err != nil {
		t.Fatal(err)
	}
	activeHistoryItems := 0
	for _, item := range history {
		if item.Active {
			activeHistoryItems++
			if item.ID != firstReleaseID || item.BundleStatus != "ready" {
				t.Fatalf("active history item = %#v", item)
			}
		}
	}
	if activeHistoryItems != 1 {
		t.Fatalf("active history item count = %d, want 1", activeHistoryItems)
	}
}

func TestReleaseRollbackRejectsBundlelessAndAlreadyActiveReleases(t *testing.T) {
	ctx := context.Background()
	store := state.NewLocalStore(filepath.Join(t.TempDir(), "state.json"))
	bundleless := rollbackTestDeployment("commit-a", "")
	ready := rollbackTestDeployment("commit-b", testExecutionBundleDigest)
	if _, err := store.PublishRelease(ctx, bundleless, time.Now().UTC().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishRelease(ctx, ready, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.LoadCatalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(New(Config{
		Store:   store,
		Catalog: store,
		ExecutionBundles: executionBundleManagerStub{build: func(_ context.Context, deployment contract.Deployment) (contract.Deployment, error) {
			return deployment, nil
		}},
	}))
	defer httpServer.Close()

	assertRollbackStatus(t, httpServer.URL, snapshot.History[0].ID, http.StatusUnprocessableEntity)
	assertRollbackStatus(t, httpServer.URL, snapshot.History[1].ID, http.StatusConflict)
}

func createRollbackTestRun(t *testing.T, baseURL string, idempotencyKey string) executionRunView {
	t.Helper()
	body := []byte(`{"app":"echo","action":"run","input":{},"adapter":"queue","idempotency_key":"` + idempotencyKey + `"}`)
	response, err := http.Post(baseURL+"/execution/v1/workspaces/ws-a/runs", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create run status = %d, want 201", response.StatusCode)
	}
	var run executionRunView
	if err := json.NewDecoder(response.Body).Decode(&run); err != nil {
		t.Fatal(err)
	}
	return run
}

func assertRollbackStatus(t *testing.T, baseURL string, releaseID string, want int) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, baseURL+"/api/w/ws-a/apps/echo/releases/"+releaseID+"/rollback", bytes.NewBufferString(`{"confirm":true,"reason":"test rollback"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("content-type", "application/json")
	request.Header.Set("x-windforce-actor", "tester")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != want {
		t.Fatalf("rollback status = %d, want %d", response.StatusCode, want)
	}
}

func rollbackTestDeployment(commit string, digest string) contract.Deployment {
	return contract.Deployment{
		Workspace:    "ws-a",
		GitSourceID:  "source-a",
		App:          "echo",
		Commit:       commit,
		Entrypoint:   "main.py",
		BundleDigest: digest,
		BundleURI:    "bundle://ws-a/source-a/" + commit,
		Actions: map[string]contract.Action{
			"run": {Action: "run", Entrypoint: "main.py"},
		},
	}
}
