package runtime

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"time"

	windforcepyclient "github.com/imprun/windforce-lite/internal/sdk/python"
	windforceclient "github.com/imprun/windforce-lite/internal/sdk/typescript"
	"golang.org/x/sync/singleflight"
)

const pyVendorDir = ".windforce/site-packages"
const sourceReadyFile = ".ready"

var sourcePrepareGroup singleflight.Group

func (r *Runner) ensureSource(ctx context.Context, workspace string, gitSourceID string, commit string, scriptLang string) (string, error) {
	cacheRoot := r.CacheRoot
	if cacheRoot == "" {
		cacheRoot = filepath.Join(os.TempDir(), "windforce-lite-cache")
	}
	sourceDir := filepath.Join(cacheRoot, "src", safePath(workspace), safePath(gitSourceID), safePath(commit))
	key := sourceDir

	ch := sourcePrepareGroup.DoChan(key, func() (any, error) {
		if fileExists(filepath.Join(sourceDir, sourceReadyFile)) {
			return sourceDir, nil
		}
		pctx, cancel := context.WithTimeout(context.Background(), r.prepareTimeout())
		defer cancel()
		exists, err := r.Store.Exists(pctx, workspace, gitSourceID, commit)
		if err != nil {
			return "", err
		}
		if !exists {
			return "", os.ErrNotExist
		}
		if err := r.Store.FetchTo(pctx, sourceDir, workspace, gitSourceID, commit); err != nil {
			return "", err
		}
		if err := r.prepareSource(pctx, sourceDir, scriptLang); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(sourceDir, sourceReadyFile), []byte("ok"), 0o644); err != nil {
			return "", err
		}
		return sourceDir, nil
	})

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case result := <-ch:
		if result.Err != nil {
			return "", result.Err
		}
		sourceDir, _ := result.Val.(string)
		if sourceDir == "" {
			sourceDir = filepath.Join(cacheRoot, "src", safePath(workspace), safePath(gitSourceID), safePath(commit))
		}
		return sourceDir, nil
	}
}

func (r *Runner) prepareTimeout() time.Duration {
	if r.PrepareTimeout > 0 {
		return r.PrepareTimeout
	}
	return 5 * time.Minute
}

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
	case "python":
		if fileExists(filepath.Join(sourceDir, "requirements.txt")) {
			if err := pythonInstall(ctx, firstNonEmpty(r.PythonPath, defaultPythonPath()), sourceDir); err != nil {
				return fmt.Errorf("pip install: %w", err)
			}
		}
		if err := injectPythonSDK(sourceDir); err != nil {
			return fmt.Errorf("inject python sdk: %w", err)
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

func pythonInstall(ctx context.Context, pythonPath string, dir string) error {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, pythonPath, "-m", "pip", "install",
		"--target", filepath.Join(dir, pyVendorDir),
		"--no-input", "--disable-pip-version-check",
		"-r", filepath.Join(dir, "requirements.txt"))
	cmd.Dir = dir
	cmd.Env = curatedHostEnv()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, string(output))
	}
	return nil
}

func injectPythonSDK(dir string) error {
	target := filepath.Join(dir, pyVendorDir)
	return fs.WalkDir(windforcepyclient.Files, "windforce_client", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dest := filepath.Join(target, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		data, err := windforcepyclient.Files.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0o644)
	})
}

func appendPreparedSourceEnv(env []string, sourceDir string, scriptLang string) []string {
	if scriptLang == "python" {
		return append(env, "WF_PY_VENDOR="+filepath.Join(sourceDir, pyVendorDir))
	}
	return env
}

func defaultPythonPath() string {
	if goruntime.GOOS == "windows" {
		return "python"
	}
	return "python3"
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
