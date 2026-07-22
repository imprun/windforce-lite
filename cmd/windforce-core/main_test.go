package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/imprun/windforce-core/internal/catalog"
	"github.com/imprun/windforce-core/internal/contract"
	"github.com/imprun/windforce-core/internal/gitsource"
	"github.com/imprun/windforce-core/internal/state"
)

func TestCanonicalCommandExposesThreeRolesAndAcceptsServerAliases(t *testing.T) {
	for _, alias := range []string{"api", "control-plane", "execution-api"} {
		if got := canonicalCommand(alias); got != "server" {
			t.Fatalf("canonicalCommand(%q) = %q, want server", alias, got)
		}
	}
	for _, command := range []string{"server", "worker", "standalone"} {
		if got := canonicalCommand(command); got != command {
			t.Fatalf("canonicalCommand(%q) = %q", command, got)
		}
	}
	if got := canonicalCommand("webhook-dispatcher"); got != "webhook-dispatcher" {
		t.Fatalf("removed command unexpectedly mapped to %q", got)
	}
}

func TestUsageShowsOnlyServerWorkerAndStandaloneRoles(t *testing.T) {
	var output bytes.Buffer
	printUsage(&output)
	usage := output.String()
	for _, role := range []string{"windforce-core server", "windforce-core worker", "windforce-core standalone"} {
		if !strings.Contains(usage, role) {
			t.Fatalf("usage missing %q:\n%s", role, usage)
		}
	}
	for _, hidden := range []string{"control-plane", "execution-api", "webhook-dispatcher"} {
		if strings.Contains(usage, hidden) {
			t.Fatalf("usage exposed %q:\n%s", hidden, usage)
		}
	}
}

func TestImportReleaseCatalogMigratesFileStateIdempotently(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	catalogPath := filepath.Join(tempDir, "catalog.json")
	sourcesPath := filepath.Join(tempDir, "git-sources.json")
	legacyCatalog := catalog.NewFileCatalog(catalogPath)
	deployment := contract.Deployment{
		Workspace:   "workspace-a",
		GitSourceID: "1",
		App:         "echo",
		Commit:      "commit-a",
		Entrypoint:  "main.py",
		ObjectURI:   "bundle://workspace-a/1/commit-a",
		Actions:     map[string]contract.Action{"run": {Action: "run"}},
	}
	if err := legacyCatalog.UpsertDeployment(ctx, deployment); err != nil {
		t.Fatal(err)
	}
	releasedAt := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	commit := "commit-a"
	sources := gitsource.NewFileRegistry(sourcesPath)
	if err := sources.Upsert(ctx, gitsource.Source{
		Workspace:        "workspace-a",
		ID:               "1",
		Name:             "echo",
		RepoURL:          "https://example.test/echo.git",
		LastSyncedCommit: &commit,
		LastSyncedAt:     &releasedAt,
	}); err != nil {
		t.Fatal(err)
	}

	target := state.NewLocalStore(filepath.Join(tempDir, "state.json"))
	if err := importReleaseCatalog(ctx, target, catalogPath, sources); err != nil {
		t.Fatal(err)
	}
	if err := importReleaseCatalog(ctx, target, catalogPath, sources); err != nil {
		t.Fatal(err)
	}
	snapshot, err := target.LoadCatalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Deployments) != 1 || len(snapshot.History) != 1 {
		t.Fatalf("import counts = deployments:%d history:%d", len(snapshot.Deployments), len(snapshot.History))
	}
	marker := snapshot.SourceMarkers[catalog.SourceReleaseKey("workspace-a", "1")]
	if marker.Commit != commit || !marker.ReleasedAt.Equal(releasedAt) {
		t.Fatalf("imported marker = %#v", marker)
	}
}

func TestRequireProductionSecrets(t *testing.T) {
	if err := requireProductionSecrets(true, true, "", ""); err != nil {
		t.Fatalf("dev mode must allow empty secrets: %v", err)
	}
	if err := requireProductionSecrets(false, true, "", "secret"); err == nil {
		t.Fatal("missing admin token must fail closed")
	}
	if err := requireProductionSecrets(false, true, "token", ""); err == nil {
		t.Fatal("missing secret key must fail closed")
	}
	if err := requireProductionSecrets(false, false, "", "secret"); err != nil {
		t.Fatalf("worker-style check must not need an admin token: %v", err)
	}
	if err := requireProductionSecrets(false, true, "token", "secret"); err != nil {
		t.Fatalf("full secrets must pass: %v", err)
	}
}
