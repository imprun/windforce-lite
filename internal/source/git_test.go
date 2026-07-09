package source

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAuthURLUsesGitLabTokenForHTTP(t *testing.T) {
	got := authURL("http://gitlab.scraping.co.kr/gitlab/group/project.git", "secret-token")
	want := "http://oauth2:secret-token@gitlab.scraping.co.kr/gitlab/group/project.git"
	if got != want {
		t.Fatalf("authURL() = %q, want %q", got, want)
	}
}

func TestAuthURLKeepsNonHTTPRepos(t *testing.T) {
	got := authURL("ssh://git@gitlab.scraping.co.kr/group/project.git", "secret-token")
	want := "ssh://git@gitlab.scraping.co.kr/group/project.git"
	if got != want {
		t.Fatalf("authURL() = %q, want %q", got, want)
	}
}

func TestRedactRemovesCredentials(t *testing.T) {
	got := redact("clone http://oauth2:secret-token@gitlab.scraping.co.kr/gitlab/group/project.git")
	want := "clone http://[REDACTED]@gitlab.scraping.co.kr/gitlab/group/project.git"
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
