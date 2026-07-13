package source

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/imprun/windforce-lite/internal/contract"
)

var credentialPattern = regexp.MustCompile(`(https?://)[^@/\s]+@`)
var gitObjectIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}([0-9a-fA-F]{24})?$`)

type GitRunner interface {
	Run(ctx context.Context, dir string, args ...string) (string, error)
}

type GitCommandRunner struct{}

type GitClient struct {
	Runner GitRunner
}

var DefaultGitClient = GitClient{}

type gitCredential struct {
	Type     string `json:"type"`
	Token    string `json:"token"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func ResolveBranchCommit(ctx context.Context, repoURL string, branch string, token string) (string, error) {
	return DefaultGitClient.ResolveBranchCommit(ctx, repoURL, branch, token)
}

func (c GitClient) ResolveBranchCommit(ctx context.Context, repoURL string, branch string, token string) (string, error) {
	if branch == "" {
		branch = "main"
	}

	out, err := c.run(ctx, "", "ls-remote", "--heads", authURL(repoURL, token), branch)
	if err != nil {
		return "", err
	}
	commit, ok := parseRemoteHeadCommit(out, branch)
	if !ok {
		return "", fmt.Errorf("branch %q was not found in repository", branch)
	}
	return commit, nil
}

func parseRemoteHeadCommit(out string, branch string) (string, bool) {
	branch = strings.TrimPrefix(branch, "refs/heads/")
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !gitObjectIDPattern.MatchString(fields[0]) {
			continue
		}
		if fields[1] == branch || fields[1] == "refs/heads/"+branch {
			return fields[0], true
		}
	}
	return "", false
}

func ListRemoteBranches(ctx context.Context, repoURL string, token string) ([]string, error) {
	return DefaultGitClient.ListRemoteBranches(ctx, repoURL, token)
}

func (c GitClient) ListRemoteBranches(ctx context.Context, repoURL string, token string) ([]string, error) {
	out, err := c.run(ctx, "", "ls-remote", "--heads", authURL(repoURL, token))
	if err != nil {
		return nil, err
	}
	branches := []string{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		branch, ok := strings.CutPrefix(fields[1], "refs/heads/")
		if !ok || branch == "" {
			continue
		}
		branches = append(branches, branch)
	}
	sort.Strings(branches)
	return branches, nil
}

func CommitSubject(ctx context.Context, repoDir string) (string, error) {
	return DefaultGitClient.CommitSubject(ctx, repoDir)
}

func (c GitClient) CommitSubject(ctx context.Context, repoDir string) (string, error) {
	out, err := c.run(ctx, repoDir, "log", "-1", "--format=%s")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func HeadCommit(ctx context.Context, repoDir string) (string, error) {
	return DefaultGitClient.HeadCommit(ctx, repoDir)
}

func (c GitClient) HeadCommit(ctx context.Context, repoDir string) (string, error) {
	out, err := c.run(ctx, repoDir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func CloneCommit(ctx context.Context, repoURL string, branch string, commit string, destinationDir string, token string) error {
	return DefaultGitClient.CloneCommit(ctx, repoURL, branch, commit, destinationDir, token)
}

func (c GitClient) CloneCommit(ctx context.Context, repoURL string, branch string, commit string, destinationDir string, token string) error {
	cloneURL := authURL(repoURL, token)
	args := []string{"clone"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	args = append(args, cloneURL, destinationDir)
	if _, err := c.run(ctx, "", args...); err != nil {
		if _, retryErr := c.run(ctx, "", "clone", cloneURL, destinationDir); retryErr != nil {
			return fmt.Errorf("git clone: %w", retryErr)
		}
	}
	if commit != "" {
		if _, err := c.run(ctx, destinationDir, "checkout", "--detach", commit); err != nil {
			return fmt.Errorf("git checkout %s: %w", commit, err)
		}
	}
	return nil
}

func CloneCommitSparse(ctx context.Context, repoURL string, branch string, commit string, destinationDir string, subpath string, token string) error {
	return DefaultGitClient.CloneCommitSparse(ctx, repoURL, branch, commit, destinationDir, subpath, token)
}

func (c GitClient) CloneCommitSparse(ctx context.Context, repoURL string, branch string, commit string, destinationDir string, subpath string, token string) error {
	if err := contract.ValidateSourceSubpath(subpath); err != nil {
		return err
	}
	if subpath == "" {
		return fmt.Errorf("sparse clone requires a subpath")
	}
	cloneURL := authURL(repoURL, token)
	args := []string{"clone", "--depth", "1", "--filter=blob:none", "--no-checkout", "--sparse"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	args = append(args, cloneURL, destinationDir)
	if _, err := c.run(ctx, "", args...); err != nil {
		return fmt.Errorf("git clone (sparse): %w", err)
	}
	if _, err := c.run(ctx, destinationDir, "sparse-checkout", "set", filepath.ToSlash(subpath)); err != nil {
		return fmt.Errorf("git sparse-checkout set %s: %w", subpath, err)
	}
	ref := commit
	if ref == "" {
		ref = "HEAD"
	}
	if _, err := c.run(ctx, destinationDir, "checkout", "--detach", ref); err != nil {
		return fmt.Errorf("git checkout %s (sparse): %w", ref, err)
	}
	return nil
}

func authURL(repoURL string, credentialValue string) string {
	username, password, ok := parseGitCredential(credentialValue)
	if !ok {
		return repoURL
	}
	parsed, err := url.Parse(repoURL)
	if err != nil {
		return repoURL
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return repoURL
	}
	parsed.User = url.UserPassword(username, password)
	return parsed.String()
}

func parseGitCredential(value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", false
	}
	if strings.HasPrefix(value, "{") {
		var credential gitCredential
		if err := json.Unmarshal([]byte(value), &credential); err != nil {
			return "", "", false
		}
		credential.Type = strings.ToLower(strings.TrimSpace(credential.Type))
		switch credential.Type {
		case "basic":
			if credential.Username == "" || credential.Password == "" {
				return "", "", false
			}
			return credential.Username, credential.Password, true
		case "pat", "token", "access_token", "":
			if credential.Token == "" {
				return "", "", false
			}
			return "x-access-token", credential.Token, true
		default:
			return "", "", false
		}
	}
	return "x-access-token", value, true
}

func (c GitClient) run(ctx context.Context, dir string, args ...string) (string, error) {
	runner := c.Runner
	if runner == nil {
		runner = GitCommandRunner{}
	}
	return runner.Run(ctx, dir, args...)
}

func (GitCommandRunner) Run(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	hideGitCommandWindow(cmd)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(cmd.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", redact(strings.Join(args, " ")), err, redact(strings.TrimSpace(string(out))))
	}
	return string(out), nil
}

func redact(value string) string {
	return credentialPattern.ReplaceAllString(value, "${1}[REDACTED]@")
}
