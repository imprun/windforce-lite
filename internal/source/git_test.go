package source

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthURLInjectsTokenForHTTPS(t *testing.T) {
	got := authURL("https://git.example.test/group/project.git", "secret-token")
	want := "https://x-access-token:secret-token@git.example.test/group/project.git"
	if got != want {
		t.Fatalf("authURL() = %q, want %q", got, want)
	}
}

func TestAuthURLKeepsNonHTTPSRepos(t *testing.T) {
	for _, repoURL := range []string{
		"http://git.example.test/group/project.git",
		"ssh://git@git.example.test/group/project.git",
	} {
		if got := authURL(repoURL, "secret-token"); got != repoURL {
			t.Fatalf("authURL(%q) = %q, want original URL", repoURL, got)
		}
	}
}

func TestRedactRemovesCredentials(t *testing.T) {
	got := redact("clone https://x-access-token:secret-token@git.example.test/group/project.git")
	want := "clone https://[REDACTED]@git.example.test/group/project.git"
	if got != want {
		t.Fatalf("redact() = %q, want %q", got, want)
	}
}

func TestListRemoteBranches(t *testing.T) {
	repoDir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repoDir, "init")
	runTestGit(t, repoDir, "checkout", "-b", "main")
	runTestGit(t, repoDir, "config", "user.email", "test@example.com")
	runTestGit(t, repoDir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repoDir, "add", "README.md")
	runTestGit(t, repoDir, "commit", "-m", "initial")
	runTestGit(t, repoDir, "checkout", "-b", "feature")

	branches, err := ListRemoteBranches(context.Background(), filepath.ToSlash(repoDir), "")
	if err != nil {
		t.Fatalf("ListRemoteBranches returned error: %v", err)
	}
	if len(branches) != 2 || branches[0] != "feature" || branches[1] != "main" {
		t.Fatalf("branches = %#v", branches)
	}
}

func TestCloneCommitPreservesTags(t *testing.T) {
	tempDir := t.TempDir()
	repoDir := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repoDir, "init")
	runTestGit(t, repoDir, "checkout", "-b", "main")
	runTestGit(t, repoDir, "config", "user.email", "test@example.com")
	runTestGit(t, repoDir, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repoDir, "add", "README.md")
	runTestGit(t, repoDir, "commit", "-m", "initial")
	runTestGit(t, repoDir, "tag", "v1")

	cloneDir := filepath.Join(tempDir, "clone")
	if err := CloneCommit(context.Background(), filepath.ToSlash(repoDir), "main", "", cloneDir, ""); err != nil {
		t.Fatalf("CloneCommit returned error: %v", err)
	}
	out, err := exec.Command("git", "-C", cloneDir, "tag", "--list", "v1").CombinedOutput()
	if err != nil {
		t.Fatalf("git tag --list: %v: %s", err, string(out))
	}
	if strings.TrimSpace(string(out)) != "v1" {
		t.Fatalf("cloned tags = %q, want v1", string(out))
	}
}

func TestCloneCommitSparseRejectsAbsoluteSubpathBeforeGit(t *testing.T) {
	err := CloneCommitSparse(context.Background(), "https://example.test/repo.git", "main", "commit-a", t.TempDir(), "/apps/echo", "")
	if err == nil {
		t.Fatal("CloneCommitSparse unexpectedly accepted absolute subpath")
	}
}

func runTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, string(out))
	}
}
