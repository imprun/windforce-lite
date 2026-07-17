package sampleapp

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureRepositoryPointsHeadAtDefaultBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo, err := EnsureRepository(context.Background(), t.TempDir(), "default", "")
	if err != nil {
		t.Fatal(err)
	}
	remote := filepath.FromSlash(repo.RepoURL)
	out, err := exec.Command("git", "-C", remote, "symbolic-ref", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("symbolic-ref: %v\n%s", err, out)
	}
	if head := strings.TrimSpace(string(out)); head != "refs/heads/"+DefaultBranch {
		t.Fatalf("bare remote HEAD = %q, want refs/heads/%s (plain clones would check out nothing)", head, DefaultBranch)
	}

	// A plain clone (no --branch) must therefore yield the sample files.
	work := filepath.Join(t.TempDir(), "clone")
	if out, err := exec.Command("git", "clone", remote, work).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", work, "show", "HEAD:windforce.json").CombinedOutput(); err != nil {
		t.Fatalf("cloned repo missing windforce.json: %v\n%s", err, out)
	}
}
