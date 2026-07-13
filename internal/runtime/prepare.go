package runtime

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	windforcegoclient "github.com/imprun/windforce-lite/internal/sdk/go"
	windforcepyclient "github.com/imprun/windforce-lite/internal/sdk/python"
	windforceclient "github.com/imprun/windforce-lite/internal/sdk/typescript"
	"golang.org/x/sync/singleflight"
)

const pyVendorDir = ".windforce/site-packages"
const sourceReadyFile = ".ready"
const goSDKDir = ".windforce/sdk-go"
const goBinRel = ".windforce/bin/app"

var sourcePrepareGroup singleflight.Group
var defaultPythonOnce sync.Once
var resolvedDefaultPython string

func (r *Runner) ensureSource(ctx context.Context, workspace string, gitSourceID string, commit string, scriptLang string, entrypoint string) (string, error) {
	cacheRoot := r.CacheRoot
	if cacheRoot == "" {
		cacheRoot = filepath.Join(os.TempDir(), "windforce-lite-cache")
	}
	sourceDir := filepath.Join(cacheRoot, "src", safePath(workspace), safePath(gitSourceID), safePath(commit))
	key := sourceDir

	ch := sourcePrepareGroup.DoChan(key, func() (any, error) {
		pctx, cancel := context.WithTimeout(context.Background(), r.prepareTimeout())
		defer cancel()
		readyValue, err := sourceReadyValue(
			pctx,
			scriptLang,
			firstNonEmpty(r.PythonPath, defaultPythonPath()),
			firstNonEmpty(r.BunPath, "bun"),
			firstNonEmpty(r.GoPath, "go"),
		)
		if err != nil {
			return "", prepareErr(pctx, err)
		}
		readyPath := filepath.Join(sourceDir, sourceReadyFile)
		if current, readErr := os.ReadFile(readyPath); readErr == nil && string(current) == readyValue {
			return sourceDir, nil
		}
		exists, err := r.Store.Exists(pctx, workspace, gitSourceID, commit)
		if err != nil {
			return "", prepareErr(pctx, err)
		}
		if !exists {
			return "", fmt.Errorf("commit %s not materialized in object cache", commit)
		}
		if err := os.RemoveAll(sourceDir); err != nil {
			return "", prepareErr(pctx, err)
		}
		if err := r.Store.FetchTo(pctx, sourceDir, workspace, gitSourceID, commit); err != nil {
			return "", prepareErr(pctx, err)
		}
		if err := r.prepareSource(pctx, sourceDir, scriptLang, entrypoint); err != nil {
			return "", prepareErr(pctx, err)
		}
		if err := os.WriteFile(readyPath, []byte(readyValue), 0o644); err != nil {
			return "", prepareErr(pctx, err)
		}
		return sourceDir, nil
	})

	select {
	case <-ctx.Done():
		return "", newNamedError("PrepareCanceled", ctx.Err())
	case result := <-ch:
		if result.Err != nil {
			name := "PrepareError"
			if errors.Is(result.Err, context.DeadlineExceeded) {
				name = "PrepareTimeout"
			}
			return "", newNamedError(name, result.Err)
		}
		sourceDir, _ := result.Val.(string)
		if sourceDir == "" {
			sourceDir = filepath.Join(cacheRoot, "src", safePath(workspace), safePath(gitSourceID), safePath(commit))
		}
		return sourceDir, nil
	}
}

func prepareErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("prepare exceeded WORKER_PREPARE_TIMEOUT_S: %w", ctx.Err())
	}
	return err
}

func (r *Runner) prepareTimeout() time.Duration {
	if r.PrepareTimeout > 0 {
		return r.PrepareTimeout
	}
	return 5 * time.Minute
}

func (r *Runner) prepareSource(ctx context.Context, sourceDir string, scriptLang string, entrypoint string) error {
	switch scriptLang {
	case "python":
		pythonPath := firstNonEmpty(r.PythonPath, defaultPythonPath())
		if fileExists(filepath.Join(sourceDir, "requirements.txt")) {
			if err := pythonInstallRequirements(ctx, pythonPath, sourceDir); err != nil {
				return fmt.Errorf("pip install: %w", err)
			}
		} else if fileExists(filepath.Join(sourceDir, "pyproject.toml")) {
			if err := pythonInstallProject(ctx, pythonPath, sourceDir); err != nil {
				return fmt.Errorf("pip install project: %w", err)
			}
		}
		if err := injectPythonSDK(sourceDir); err != nil {
			return fmt.Errorf("inject python sdk: %w", err)
		}
	case "go":
		goPath := firstNonEmpty(r.GoPath, "go")
		if err := injectGoSDK(goPath, sourceDir); err != nil {
			return fmt.Errorf("inject go sdk: %w", err)
		}
		if err := goBuild(ctx, goPath, sourceDir, entrypoint); err != nil {
			return fmt.Errorf("go build: %w", err)
		}
	default:
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

func pythonInstallRequirements(ctx context.Context, pythonPath string, dir string) error {
	return pythonInstall(ctx, pythonPath, dir, "-r", filepath.Join(dir, "requirements.txt"))
}

func pythonInstallProject(ctx context.Context, pythonPath string, dir string) error {
	return pythonInstall(ctx, pythonPath, dir, ".")
}

func pythonInstall(ctx context.Context, pythonPath string, dir string, installSpec ...string) error {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	args := []string{"-m", "pip", "install",
		"--target", filepath.Join(dir, pyVendorDir),
		"--no-input", "--disable-pip-version-check"}
	args = append(args, installSpec...)
	cmd := exec.CommandContext(cctx, pythonPath, args...)
	cmd.Dir = dir
	cmd.Env = curatedPrepareEnv()
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

func injectGoSDK(goPath string, dir string) error {
	if !fileExists(filepath.Join(dir, "go.mod")) {
		return fmt.Errorf("go app missing go.mod at source root")
	}
	target := filepath.Join(dir, filepath.FromSlash(goSDKDir))
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	for name, dest := range map[string]string{"windforce.go": "windforce.go", "gomod.txt": "go.mod"} {
		data, err := windforcegoclient.Files.ReadFile(name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(target, dest), data, 0o644); err != nil {
			return err
		}
	}
	cmd := exec.Command(goPath, "mod", "edit",
		"-require=windforce-client@v0.0.0",
		"-replace=windforce-client=./"+goSDKDir)
	cmd.Dir = dir
	cmd.Env = curatedHostEnv()
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go mod edit: %w: %s", err, output)
	}
	return nil
}

func goBuild(ctx context.Context, goPath string, dir string, entrypoint string) error {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	pkgDir := filepath.Dir(filepath.Join(dir, filepath.FromSlash(entrypoint)))
	if err := os.WriteFile(filepath.Join(pkgDir, "windforce_main.go"), []byte(wrapperGo()), 0o644); err != nil {
		return err
	}
	bin := filepath.Join(dir, filepath.FromSlash(goBinaryRel()))
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		return err
	}
	cmd := exec.CommandContext(cctx, goPath, "build", "-o", bin, ".")
	cmd.Dir = pkgDir
	cmd.Env = append(curatedHostEnv(),
		"GOCACHE="+filepath.Join(dir, ".windforce", "go", "cache"),
		"GOMODCACHE="+filepath.Join(dir, ".windforce", "go", "mod"),
		"GOFLAGS=-mod=mod",
		"GOTOOLCHAIN=local",
		"CGO_ENABLED=0",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, output)
	}
	return nil
}

func wrapperGo() string {
	return `// Code generated by the windforce worker. DO NOT EDIT.
package main

import (
	"os"

	wf "windforce-client"
)

func main() { os.Exit(wf.RunMain(Main)) }
`
}

func goBinaryRel() string {
	if goruntime.GOOS == "windows" {
		return goBinRel + ".exe"
	}
	return goBinRel
}

func appendPreparedSourceEnv(env []string, sourceDir string, scriptLang string) []string {
	if scriptLang == "python" {
		return append(env,
			"WF_PY_VENDOR="+filepath.Join(sourceDir, pyVendorDir),
			"WF_PY_SOURCE_ROOT="+sourceDir,
		)
	}
	return env
}

func defaultPythonPath() string {
	if goruntime.GOOS != "windows" {
		return "python3"
	}
	defaultPythonOnce.Do(func() {
		cmd := exec.Command("py", "-3", "-c", "import sys; print(sys.executable)")
		cmd.Env = os.Environ()
		if output, err := cmd.CombinedOutput(); err == nil {
			resolvedDefaultPython = lastOutputLine(output)
		}
		if resolvedDefaultPython == "" {
			resolvedDefaultPython = "python"
		}
	})
	return resolvedDefaultPython
}

func lastOutputLine(output []byte) string {
	lines := strings.Split(strings.ReplaceAll(string(output), "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if candidate := strings.TrimSpace(lines[i]); candidate != "" {
			return candidate
		}
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
