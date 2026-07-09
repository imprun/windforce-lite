package runtime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	windforceclient "github.com/imprun/windforce-lite/internal/sdk/typescript"
)

func (r *Runner) prepareSource(ctx context.Context, sourceDir string, scriptLang string) error {
	switch scriptLang {
	case "", "typescript":
		if fileExists(filepath.Join(sourceDir, "package.json")) {
			if err := bunInstall(ctx, firstNonEmpty(r.BunPath, "bun"), sourceDir); err != nil {
				return fmt.Errorf("bun install: %w", err)
			}
		}
		if err := injectTypeScriptSDK(sourceDir); err != nil {
			return fmt.Errorf("inject sdk: %w", err)
		}
	}
	return nil
}

func bunInstall(ctx context.Context, bunPath string, dir string) error {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, bunPath, "install", "--frozen-lockfile", "--no-progress")
	cmd.Dir = dir
	cmd.Env = curatedHostEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, string(output))
	}
	return nil
}

func injectTypeScriptSDK(dir string) error {
	target := filepath.Join(dir, "node_modules", "windforce-client")
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	for _, name := range []string{"index.ts", "index.d.ts", "package.json"} {
		data, err := windforceclient.Files.ReadFile(name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(target, name), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
