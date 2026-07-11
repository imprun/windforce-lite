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

type gitCredential struct {
	Type     string `json:"type"`
	Token    string `json:"token"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func ResolveBranchCommit(ctx context.Context, repoURL string, branch string, token string) (string, error) {
	if branch == "" {
		branch = "main"
	}

	out, err := runGit(ctx, "", "ls-remote", authURL(repoURL, token), branch)
	if err != nil {
		return "", err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		out, err = runGit(ctx, "", "ls-remote", authURL(repoURL, token), "HEAD")
		if err != nil {
			return "", err
		}
	}

	fields := strings.Fields(out)
	if len(fields) == 0 {
		return "", fmt.Errorf("could not resolve commit for %s@%s", repoURL, branch)
	}
	return fields[0], nil
}

func ListRemoteBranches(ctx context.Context, repoURL string, token string) ([]string, error) {
	out, err := runGit(ctx, "", "ls-remote", "--heads", authURL(repoURL, token))
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
	out, err := runGit(ctx, repoDir, "log", "-1", "--format=%s")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func HeadCommit(ctx context.Context, repoDir string) (string, error) {
	out, err := runGit(ctx, repoDir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func CloneCommit(ctx context.Context, repoURL string, branch string, commit string, destinationDir string, token string) error {
	cloneURL := authURL(repoURL, token)
	args := []string{"clone"}
	if branch != "" {
		args = append(args, "--branch", branch)
	}
	args = append(args, cloneURL, destinationDir)
	if _, err := runGit(ctx, "", args...); err != nil {
		if _, retryErr := runGit(ctx, "", "clone", cloneURL, destinationDir); retryErr != nil {
			return fmt.Errorf("git clone: %w", retryErr)
		}
	}
	if commit != "" {
		if _, err := runGit(ctx, destinationDir, "checkout", "--detach", commit); err != nil {
			return fmt.Errorf("git checkout %s: %w", commit, err)
		}
	}
	return nil
}

func CloneCommitSparse(ctx context.Context, repoURL string, branch string, commit string, destinationDir string, subpath string, token string) error {
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
	if _, err := runGit(ctx, "", args...); err != nil {
		return fmt.Errorf("git clone (sparse): %w", err)
	}
	if _, err := runGit(ctx, destinationDir, "sparse-checkout", "set", filepath.ToSlash(subpath)); err != nil {
		return fmt.Errorf("git sparse-checkout set %s: %w", subpath, err)
	}
	ref := commit
	if ref == "" {
		ref = "HEAD"
	}
	if _, err := runGit(ctx, destinationDir, "checkout", "--detach", ref); err != nil {
		return fmt.Errorf("git checkout %s (sparse): %w", ref, err)
	}
	return nil
}

func authURL(repoURL string, credentialValue string) string {
	username, password, ok := parseGitCredential(credentialValue)
	if !ok || !strings.HasPrefix(repoURL, "https://") {
		return repoURL
	}
	parsed, err := url.Parse(repoURL)
	if err != nil {
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

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
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
