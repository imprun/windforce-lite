package syncer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imprun/windforce-lite/internal/bundle"
	catalogpkg "github.com/imprun/windforce-lite/internal/catalog"
	"github.com/imprun/windforce-lite/internal/contract"
)

type checkingCatalog struct {
	t      *testing.T
	store  bundle.Store
	called bool
}

func (c *checkingCatalog) UpsertDeployment(ctx context.Context, deployment contract.Deployment) error {
	exists, err := c.store.Exists(ctx, deployment.SourceWorkspace(), deployment.SourceGitSourceID(), deployment.Commit)
	if err != nil {
		c.t.Fatalf("store.Exists returned error: %v", err)
	}
	if !exists {
		c.t.Fatalf("catalog updated before bundle materialized")
	}
	c.called = true
	return nil
}

func TestCheckLockfileAllowsSourcesWithoutDeclaredDependencies(t *testing.T) {
	root := t.TempDir()
	if err := checkLockfile(root); err != nil {
		t.Fatalf("no package.json should not require lockfile: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"scripts"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkLockfile(root); err != nil {
		t.Fatalf("package.json without dependencies should not require lockfile: %v", err)
	}
}

func TestCheckLockfileRequiresCommittedLockfileWhenDependenciesAreDeclared(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"dependencies":{"left-pad":"1.3.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	err := checkLockfile(root)
	if err == nil {
		t.Fatal("dependencies without lockfile unexpectedly passed")
	}
	want := "package.json declares dependencies but no bun.lock (or bun.lockb) is committed at the source root — commit a lockfile so installs are reproducible (bun install --frozen-lockfile)"
	if err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}

	if err := os.WriteFile(filepath.Join(root, "bun.lock"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkLockfile(root); err != nil {
		t.Fatalf("dependencies with bun.lock should pass: %v", err)
	}
}

func TestCheckLockfileAcceptsLegacyBunLockb(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"devDependencies":{"typescript":"6.0.3"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bun.lockb"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := checkLockfile(root); err != nil {
		t.Fatalf("dependencies with bun.lockb should pass: %v", err)
	}
}

func TestCheckLockfileRejectsMalformedPackageJSON(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := checkLockfile(root); err == nil {
		t.Fatal("malformed package.json unexpectedly passed")
	}
}

func TestSyncMaterializesBeforeCatalogUpdate(t *testing.T) {
	tempDir := t.TempDir()
	sourceDir := filepath.Join(tempDir, "source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "windforce.json"), []byte(`{
		"app": "echo",
		"entrypoint": "main.ts",
		"scriptLang": "typescript",
		"timeout": 120,
		"tag": "app-main",
		"maxConcurrent": 2,
		"actions": {
			"echo": {
				"tag": "action-fast",
				"inputSchema": "input.schema.json",
				"outputSchema": "output.schema.json"
			}
		}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "input.schema.json"), []byte(`{"type":"object","properties":{"message":{"type":"string"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "output.schema.json"), []byte(`{"type":"object","properties":{"ok":{"type":"boolean"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	store := bundle.NewLocalStore(filepath.Join(tempDir, "store"))
	catalog := &checkingCatalog{t: t, store: store}
	syncer := Syncer{Store: store, Catalog: catalog}

	deployment, err := syncer.Sync(context.Background(), Source{
		Workspace:   "workspace-a",
		GitSourceID: "source-a",
		App:         "echo",
		Commit:      "commit-a",
		LocalDir:    sourceDir,
	})
	if err != nil {
		t.Fatalf("Sync returned error: %v", err)
	}
	if !catalog.called {
		t.Fatalf("catalog was not updated")
	}
	if deployment.ObjectURI != "bundle://workspace-a/source-a/commit-a" {
		t.Fatalf("object URI = %q", deployment.ObjectURI)
	}
	if deployment.Actions["echo"].Action != "echo" {
		t.Fatalf("action metadata was not loaded from manifest")
	}
	if deployment.Tag != "app-main" || deployment.Actions["echo"].Tag == nil || *deployment.Actions["echo"].Tag != "action-fast" {
		t.Fatalf("route tags were not loaded from manifest: %#v", deployment)
	}
	if deployment.Entrypoint != "main.ts" || deployment.ScriptLang != "typescript" || deployment.TimeoutS != 120 {
		t.Fatalf("canonical app metadata was not pinned: %#v", deployment)
	}
	if deployment.MaxConcurrent == nil || *deployment.MaxConcurrent != 2 {
		t.Fatalf("maxConcurrent = %v, want 2", deployment.MaxConcurrent)
	}
	if deployment.Actions["echo"].Entrypoint != "main.ts" || deployment.Actions["echo"].TimeoutMs != 120000 {
		t.Fatalf("canonical app defaults were not pinned: %#v", deployment.Actions["echo"])
	}
	if !strings.Contains(string(deployment.Actions["echo"].InputSchemaBody), `"message"`) ||
		!strings.Contains(string(deployment.Actions["echo"].OutputSchemaBody), `"ok"`) {
		t.Fatalf("action schemas were not materialized: input=%s output=%s", deployment.Actions["echo"].InputSchemaBody, deployment.Actions["echo"].OutputSchemaBody)
	}
	if deployment.UpdatedAt == nil || deployment.Actions["echo"].UpdatedAt == nil ||
		!deployment.Actions["echo"].UpdatedAt.Equal(*deployment.UpdatedAt) {
		t.Fatalf("canonical updatedAt was not pinned: deployment=%v action=%v", deployment.UpdatedAt, deployment.Actions["echo"].UpdatedAt)
	}
}

func TestSyncCapturesGitCommitMessageInHistory(t *testing.T) {
	tempDir := t.TempDir()
	repoDir := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runSyncerTestGit(t, repoDir, "init")
	runSyncerTestGit(t, repoDir, "checkout", "-b", "main")
	runSyncerTestGit(t, repoDir, "config", "user.email", "test@example.com")
	runSyncerTestGit(t, repoDir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repoDir, "windforce.json"), []byte(`{
		"app": "echo",
		"entrypoint": "main.ts",
		"actions": {"echo": {}}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "main.ts"), []byte(`export async function main(ctx) { return ctx.input }`), 0o644); err != nil {
		t.Fatal(err)
	}
	runSyncerTestGit(t, repoDir, "add", ".")
	runSyncerTestGit(t, repoDir, "commit", "-m", "Add echo app")

	store := bundle.NewLocalStore(filepath.Join(tempDir, "store"))
	catalog := catalogpkg.NewFileCatalog(filepath.Join(tempDir, "catalog.json"))
	syncer := Syncer{Store: store, Catalog: catalog, CloneRoot: filepath.Join(tempDir, "clones")}

	deployment, err := syncer.Sync(context.Background(), Source{
		Workspace:   "workspace-a",
		GitSourceID: "1",
		App:         "echo",
		RepoURL:     filepath.ToSlash(repoDir),
		Branch:      "main",
	})
	if err != nil {
		t.Fatalf("Sync returned error: %v", err)
	}
	if deployment.Message == nil || *deployment.Message != "Add echo app" {
		t.Fatalf("deployment message = %v, want Add echo app", deployment.Message)
	}
	snapshot, err := catalog.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.History) != 1 || snapshot.History[0].Message == nil || *snapshot.History[0].Message != "Add echo app" {
		t.Fatalf("history message = %#v", snapshot.History)
	}
}

func TestSyncRejectsInvalidSchemaReferences(t *testing.T) {
	tests := []struct {
		name        string
		manifest    string
		files       map[string]string
		wantMessage string
	}{
		{
			name: "missing input schema",
			manifest: `{
				"app": "echo",
				"entrypoint": "main.ts",
				"actions": {"echo": {"inputSchema": "missing.schema.json"}}
			}`,
			wantMessage: `manifest references schema "missing.schema.json" but the file is missing`,
		},
		{
			name: "invalid output schema json",
			manifest: `{
				"app": "echo",
				"entrypoint": "main.ts",
				"actions": {"echo": {"outputSchema": "output.schema.json"}}
			}`,
			files:       map[string]string{"output.schema.json": `{bad json`},
			wantMessage: `schema "output.schema.json" is not valid JSON`,
		},
		{
			name: "escaping schema path",
			manifest: `{
				"app": "echo",
				"entrypoint": "main.ts",
				"actions": {"echo": {"inputSchema": "../input.schema.json"}}
			}`,
			wantMessage: `schema path "../input.schema.json" must be a relative path inside the app`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tempDir := t.TempDir()
			sourceDir := filepath.Join(tempDir, "source")
			if err := os.MkdirAll(sourceDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(sourceDir, "windforce.json"), []byte(test.manifest), 0o644); err != nil {
				t.Fatal(err)
			}
			for name, content := range test.files {
				path := filepath.Join(sourceDir, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			store := bundle.NewLocalStore(filepath.Join(tempDir, "store"))
			syncer := Syncer{Store: store}
			_, err := syncer.Sync(context.Background(), Source{
				Workspace:   "workspace-a",
				GitSourceID: "source-a",
				App:         "echo",
				Commit:      "commit-a",
				LocalDir:    sourceDir,
			})
			if err == nil || !strings.Contains(err.Error(), test.wantMessage) {
				t.Fatalf("Sync error = %v, want %q", err, test.wantMessage)
			}
			exists, existsErr := store.Exists(context.Background(), "workspace-a", "source-a", "commit-a")
			if existsErr != nil {
				t.Fatalf("store.Exists returned error: %v", existsErr)
			}
			if exists {
				t.Fatalf("invalid source should not be materialized")
			}
		})
	}
}

func runSyncerTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, string(out))
	}
}
