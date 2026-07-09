package sampleapp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	DefaultAppKey     = "sample_hello"
	DefaultSourceName = "sample-hello"
	DefaultBranch     = "main"
)

type Repository struct {
	AppKey     string
	SourceName string
	RepoURL    string
	Branch     string
}

func EnsureRepository(ctx context.Context, root, workspaceID, appKey string) (*Repository, error) {
	if appKey = strings.TrimSpace(appKey); appKey == "" {
		appKey = DefaultAppKey
	}
	if root = strings.TrimSpace(root); root == "" {
		root = filepath.Join(".", ".data", "sample-repos")
	}
	sourceName := strings.ReplaceAll(appKey, "_", "-")
	base := filepath.Join(root, safeSegment(workspaceID), safeSegment(appKey))
	base, err := filepath.Abs(base)
	if err != nil {
		return nil, err
	}
	remote := filepath.Join(base, "remote.git")
	if _, err := os.Stat(filepath.Join(remote, "HEAD")); err == nil {
		return &Repository{AppKey: appKey, SourceName: sourceName, RepoURL: filepath.ToSlash(remote), Branch: DefaultBranch}, nil
	}
	if err := os.RemoveAll(base); err != nil {
		return nil, err
	}
	work := filepath.Join(base, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		return nil, err
	}
	if _, err := runGit(ctx, base, "init", "--bare", remote); err != nil {
		return nil, err
	}
	if err := initWorktree(ctx, work); err != nil {
		return nil, err
	}
	if err := writeSampleFiles(work, appKey); err != nil {
		return nil, err
	}
	if _, err := runGit(ctx, work, "config", "user.name", "Windforce"); err != nil {
		return nil, err
	}
	if _, err := runGit(ctx, work, "config", "user.email", "windforce@example.invalid"); err != nil {
		return nil, err
	}
	if _, err := runGit(ctx, work, "add", "-A"); err != nil {
		return nil, err
	}
	if _, err := runGit(ctx, work, "commit", "-m", "Add Windforce sample app"); err != nil {
		return nil, err
	}
	if _, err := runGit(ctx, work, "push", remote, "HEAD:refs/heads/"+DefaultBranch); err != nil {
		return nil, err
	}
	return &Repository{AppKey: appKey, SourceName: sourceName, RepoURL: filepath.ToSlash(remote), Branch: DefaultBranch}, nil
}

func initWorktree(ctx context.Context, work string) error {
	if _, err := runGit(ctx, work, "init", "-b", DefaultBranch); err == nil {
		return nil
	}
	if _, err := runGit(ctx, work, "init"); err != nil {
		return err
	}
	_, err := runGit(ctx, work, "checkout", "-b", DefaultBranch)
	return err
}

func writeSampleFiles(root, appKey string) error {
	manifest := map[string]any{
		"app":        appKey,
		"entrypoint": "main.py",
		"scriptLang": "python",
		"timeout":    30,
		"actions": map[string]any{
			"echo": map[string]any{
				"inputSchema":  "input.schema.json",
				"outputSchema": "output.schema.json",
			},
		},
	}
	if err := writeJSON(filepath.Join(root, "windforce.json"), manifest); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "main.py"), []byte(sampleActionPy), 0o644); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(root, "input.schema.json"), map[string]any{
		"type": "object",
		"properties": map[string]any{
			"message": map[string]any{"type": "string"},
		},
	}); err != nil {
		return err
	}
	return writeJSON(filepath.Join(root, "output.schema.json"), map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok":     map[string]any{"type": "boolean"},
			"app":    map[string]any{"type": "string"},
			"action": map[string]any{"type": "string"},
			"input":  map[string]any{},
		},
		"required": []string{"ok", "app", "action", "input"},
	})
}

const sampleActionPy = `def main(ctx):
    return {
        "ok": True,
        "app": ctx.app,
        "action": ctx.action,
        "input": ctx.input,
    }
`

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(cmd.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func safeSegment(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "default"
	}
	return b.String()
}
