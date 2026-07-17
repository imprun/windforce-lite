package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/imprun/windforce-core/internal/contract"
	"golang.org/x/sync/singleflight"
)

const executionBundleReadyFile = ".windforce-execution-ready"

var executionBundleFetchGroup singleflight.Group

func (r *Runner) BuildExecutionBundle(ctx context.Context, deployment contract.Deployment) (contract.Deployment, error) {
	if r.ArtifactStore == nil {
		return contract.Deployment{}, errors.New("execution bundle store is required")
	}
	preparedDir, err := r.Prepare(ctx, deployment)
	if err != nil {
		return contract.Deployment{}, err
	}
	if err := r.validatePreparedSource(ctx, preparedDir, deployment); err != nil {
		return contract.Deployment{}, fmt.Errorf("validate prepared runtime: %w", err)
	}
	descriptor, err := r.ArtifactStore.Publish(ctx, preparedDir)
	if err != nil {
		return contract.Deployment{}, fmt.Errorf("publish execution bundle: %w", err)
	}
	deployment.BundleDigest = descriptor.Digest
	deployment.BundleURI = descriptor.URI
	return deployment, nil
}

func (r *Runner) ValidateExecutionBundle(ctx context.Context, deployment contract.Deployment) error {
	if r.ArtifactStore == nil {
		return errors.New("execution bundle store is required")
	}
	if strings.TrimSpace(deployment.BundleDigest) == "" {
		return errors.New("release candidate has no execution bundle; sync the source again")
	}
	descriptor, err := r.ArtifactStore.Verify(ctx, deployment.BundleDigest)
	if err != nil {
		return fmt.Errorf("verify execution bundle: %w", err)
	}
	if deployment.BundleURI != "" && descriptor.URI != deployment.BundleURI {
		return fmt.Errorf("execution bundle URI mismatch: got %s, want %s", descriptor.URI, deployment.BundleURI)
	}
	return nil
}

func (r *Runner) openExecutionBundle(ctx context.Context, deployment contract.Deployment) (string, error) {
	if r.ArtifactStore == nil {
		return "", errors.New("execution bundle store is required")
	}
	if strings.TrimSpace(deployment.BundleDigest) == "" {
		return "", errors.New("deployment has no execution bundle; sync and publish the app before running jobs")
	}
	cacheRoot := r.CacheRoot
	if cacheRoot == "" {
		cacheRoot = filepath.Join(os.TempDir(), "windforce-core-cache")
	}
	cacheRoot, err := filepath.Abs(cacheRoot)
	if err != nil {
		return "", err
	}
	bundleDir := filepath.Join(cacheRoot, "execution-bundles", safePath(deployment.BundleDigest))
	readyPath := filepath.Join(bundleDir, executionBundleReadyFile)
	key := bundleDir

	result := executionBundleFetchGroup.DoChan(key, func() (any, error) {
		bundleCtx, cancel := context.WithTimeout(context.Background(), r.prepareTimeout())
		defer cancel()
		if current, err := os.ReadFile(readyPath); err == nil && string(current) == deployment.BundleDigest {
			if err := r.validateBundleRuntime(bundleCtx, bundleDir, deployment); err != nil {
				return "", err
			}
			return bundleDir, nil
		}
		if _, err := r.ArtifactStore.FetchTo(bundleCtx, bundleDir, deployment.BundleDigest); err != nil {
			return "", fmt.Errorf("fetch execution bundle: %w", err)
		}
		if err := r.validateBundleRuntime(bundleCtx, bundleDir, deployment); err != nil {
			_ = os.RemoveAll(bundleDir)
			return "", err
		}
		if err := os.WriteFile(readyPath, []byte(deployment.BundleDigest), 0o644); err != nil {
			return "", err
		}
		return bundleDir, nil
	})

	select {
	case <-ctx.Done():
		return "", newNamedError("BundleFetchCanceled", ctx.Err())
	case fetched := <-result:
		if fetched.Err != nil {
			return "", newNamedError("BundleFetchError", fetched.Err)
		}
		dir, _ := fetched.Val.(string)
		return dir, nil
	}
}

func (r *Runner) validateBundleRuntime(ctx context.Context, bundleDir string, deployment contract.Deployment) error {
	preparedFingerprint, err := os.ReadFile(filepath.Join(bundleDir, sourceReadyFile))
	if err != nil {
		return errors.New("execution bundle runtime fingerprint is missing")
	}
	currentFingerprint, err := sourceReadyValue(
		ctx,
		firstNonEmpty(deployment.ScriptLang, "typescript"),
		firstNonEmpty(r.PythonPath, defaultPythonPath()),
		firstNonEmpty(r.BunPath, "bun"),
		firstNonEmpty(r.GoPath, "go"),
	)
	if err != nil {
		return err
	}
	if string(preparedFingerprint) != currentFingerprint {
		return errors.New("execution bundle runtime is incompatible with this worker")
	}
	return nil
}

func (r *Runner) validatePreparedSource(ctx context.Context, sourceDir string, deployment contract.Deployment) error {
	var err error
	sourceDir, err = filepath.Abs(sourceDir)
	if err != nil {
		return err
	}
	entrypoint := strings.TrimSpace(deployment.Entrypoint)
	if entrypoint == "" {
		if deploymentUsesOnlyCommandAdapters(deployment) {
			return nil
		}
		return fmt.Errorf("app %q has no entrypoint", deployment.App)
	}
	normalized, err := contract.NormalizeSourcePath(entrypoint)
	if err != nil {
		return err
	}
	language := firstNonEmpty(deployment.ScriptLang, "typescript")
	if language == "go" {
		normalized = goBinaryRel()
	}
	entrypointPath := filepath.Join(sourceDir, filepath.FromSlash(normalized))
	if info, err := os.Stat(entrypointPath); err != nil {
		return fmt.Errorf("entrypoint %q is not runnable: %w", entrypoint, err)
	} else if info.IsDir() {
		return fmt.Errorf("entrypoint %q is a directory", entrypoint)
	}

	switch language {
	case "python":
		return r.validatePythonEntrypoint(ctx, sourceDir, entrypointPath)
	case "go":
		return nil
	default:
		return r.validateBunEntrypoint(ctx, sourceDir, entrypointPath)
	}
}

func deploymentUsesOnlyCommandAdapters(deployment contract.Deployment) bool {
	if len(deployment.Actions) == 0 {
		return false
	}
	for _, action := range deployment.Actions {
		if action.Adapter == nil || action.Adapter.Type != contract.ActionAdapterCommand || len(action.Adapter.Command) == 0 {
			return false
		}
	}
	return true
}

func (r *Runner) validatePythonEntrypoint(ctx context.Context, sourceDir string, entrypointPath string) error {
	const check = `import importlib.util, os, sys
entry = sys.argv[1]
entry_dir = os.path.dirname(entry)
if entry_dir and entry_dir not in sys.path:
    sys.path.insert(0, entry_dir)
spec = importlib.util.spec_from_file_location("__windforce_check__", entry)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
if not callable(getattr(module, "main", None)):
    raise RuntimeError("main function is missing")
`
	pythonPath := firstNonEmpty(r.PythonPath, defaultPythonPath())
	command := exec.CommandContext(ctx, pythonPath, "-c", check, entrypointPath)
	command.Dir = sourceDir
	command.Env = appendPreparedSourceEnv(curatedPrepareEnv(), sourceDir, "python")
	command.Env = append(command.Env, "PYTHONPATH="+strings.Join([]string{
		filepath.Join(sourceDir, pyVendorDir),
		sourceDir,
	}, string(os.PathListSeparator)))
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("python entrypoint check: %w: %s", err, compactCommandOutput(output))
	}
	return nil
}

func (r *Runner) validateBunEntrypoint(ctx context.Context, sourceDir string, entrypointPath string) error {
	outDir, err := os.MkdirTemp("", "windforce-core-bun-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(outDir)
	command := exec.CommandContext(
		ctx,
		firstNonEmpty(r.BunPath, "bun"),
		"build",
		entrypointPath,
		"--target=bun",
		"--outdir="+outDir,
	)
	command.Dir = sourceDir
	command.Env = appendPreparedSourceEnv(curatedPrepareEnv(), sourceDir, "typescript")
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("bun entrypoint check: %w: %s", err, compactCommandOutput(output))
	}
	return nil
}

func compactCommandOutput(output []byte) string {
	value := strings.TrimSpace(string(output))
	const limit = 4096
	if len(value) <= limit {
		return value
	}
	return value[len(value)-limit:]
}
