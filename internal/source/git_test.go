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

func TestAuthURLInjectsTokenForHTTP(t *testing.T) {
	got := authURL("http://git.example.test/group/project.git", "secret-token")
	want := "http://x-access-token:secret-token@git.example.test/group/project.git"
	if got != want {
		t.Fatalf("authURL() = %q, want %q", got, want)
	}
}

func TestAuthURLInjectsTokenCredentialJSONForHTTPS(t *testing.T) {
	got := authURL("https://git.example.test/group/project.git", `{"type":"pat","token":"secret-token"}`)
	want := "https://x-access-token:secret-token@git.example.test/group/project.git"
	if got != want {
		t.Fatalf("authURL() = %q, want %q", got, want)
	}
}

func TestAuthURLInjectsBasicCredentialJSONForHTTPS(t *testing.T) {
	got := authURL("https://git.example.test/group/project.git", `{"type":"basic","username":"user@example.com","password":"secret token"}`)
	want := "https://user%40example.com:secret%20token@git.example.test/group/project.git"
	if got != want {
		t.Fatalf("authURL() = %q, want %q", got, want)
	}
}

func TestAuthURLInjectsBasicCredentialJSONForHTTP(t *testing.T) {
	got := authURL("http://git.example.test/group/project.git", `{"type":"basic","username":"user@example.com","password":"secret token"}`)
	want := "http://user%40example.com:secret%20token@git.example.test/group/project.git"
	if got != want {
		t.Fatalf("authURL() = %q, want %q", got, want)
	}
}

func TestAuthURLKeepsNonHTTPRemotes(t *testing.T) {
	for _, repoURL := range []string{
		"ssh://git@git.example.test/group/project.git",
		"file:///tmp/repo.git",
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

func TestParseRemoteHeadCommitIgnoresGitWarnings(t *testing.T) {
	commit := strings.Repeat("a", 40)
	out := "warning: redirecting to http://git.example.test/group/project.git/\n" +
		commit + "\trefs/heads/main\n"

	got, ok := parseRemoteHeadCommit(out, "main")
	if !ok {
		t.Fatal("parseRemoteHeadCommit did not find main")
	}
	if got != commit {
		t.Fatalf("commit = %q, want %q", got, commit)
	}
}

func TestParseRemoteHeadCommitRejectsWarningOnlyOutput(t *testing.T) {
	out := "warning: redirecting to http://git.example.test/group/project.git/\n"

	if got, ok := parseRemoteHeadCommit(out, "main"); ok {
		t.Fatalf("parseRemoteHeadCommit = %q, true; want no commit", got)
	}
}

func TestResolveBranchCommitRequiresExistingBranch(t *testing.T) {
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

	if _, err := ResolveBranchCommit(context.Background(), filepath.ToSlash(repoDir), "missing", ""); err == nil || !strings.Contains(err.Error(), `branch "missing" was not found`) {
		t.Fatalf("ResolveBranchCommit missing branch error = %v", err)
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

func TestCloneCommitSparseMaterializesOnlySubpath(t *testing.T) {
	tempDir := t.TempDir()
	repoDir := filepath.Join(tempDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runTestGit(t, repoDir, "init")
	runTestGit(t, repoDir, "checkout", "-b", "main")
	runTestGit(t, repoDir, "config", "user.email", "test@example.com")
	runTestGit(t, repoDir, "config", "user.name", "Test User")
	writeTestFile(t, filepath.Join(repoDir, "unrelated", "big.txt"), "not needed by the app\n")
	writeTestFile(t, filepath.Join(repoDir, "apps", "echo", "windforce.json"), `{"app":"echo","entrypoint":"main.ts","actions":{"run":{}}}`)
	writeTestFile(t, filepath.Join(repoDir, "apps", "echo", "main.ts"), "export const main = 1;\n")
	runTestGit(t, repoDir, "add", "-A")
	runTestGit(t, repoDir, "commit", "-m", "subpath fixture")
	commitOut, err := exec.Command("git", "-C", repoDir, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse: %v: %s", err, string(commitOut))
	}
	commit := strings.TrimSpace(string(commitOut))

	cloneDir := filepath.Join(tempDir, "sparse-clone")
	if err := CloneCommitSparse(context.Background(), filepath.ToSlash(repoDir), "main", commit, cloneDir, "apps/echo", ""); err != nil {
		t.Fatalf("CloneCommitSparse returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cloneDir, "apps", "echo", "windforce.json")); err != nil {
		t.Fatalf("app manifest was not materialized: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cloneDir, "unrelated")); !os.IsNotExist(err) {
		t.Fatalf("unrelated dir should be excluded by sparse-checkout, stat err = %v", err)
	}
	if head, err := HeadCommit(context.Background(), cloneDir); err != nil || head != commit {
		t.Fatalf("HeadCommit = %q err %v, want %q", head, err, commit)
	}
	if subject, err := CommitSubject(context.Background(), cloneDir); err != nil || subject != "subpath fixture" {
		t.Fatalf("CommitSubject = %q err %v, want subpath fixture", subject, err)
	}
}

func TestCloneCommitSparseRequiresSubpath(t *testing.T) {
	if err := CloneCommitSparse(context.Background(), filepath.ToSlash(t.TempDir()), "main", "", filepath.Join(t.TempDir(), "clone"), "", ""); err == nil {
		t.Fatal("expected error for empty subpath")
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

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
