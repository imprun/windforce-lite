package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	windforcegoclient "github.com/imprun/windforce-lite/internal/sdk/go"
	windforcepyclient "github.com/imprun/windforce-lite/internal/sdk/python"
	windforceclient "github.com/imprun/windforce-lite/internal/sdk/typescript"
)

const sourcePrepareVersion = "prepare-v3"

var sourceRuntimeFingerprints sync.Map

type sourceReadyRecord struct {
	Version  string `json:"version"`
	Language string `json:"language"`
	Runtime  string `json:"runtime"`
	Platform string `json:"platform"`
	SDK      string `json:"sdk"`
}

func sourceReadyValue(ctx context.Context, scriptLang string, pythonPath string, bunPath string, goPath string) (string, error) {
	language := normalizedScriptLanguage(scriptLang)
	executable, args := runtimeIdentityCommand(language, pythonPath, bunPath, goPath)
	cacheKey := language + "\x00" + executable
	runtimeIdentity, ok := sourceRuntimeFingerprints.Load(cacheKey)
	if !ok {
		cmd := exec.CommandContext(ctx, executable, args...)
		cmd.Env = curatedPrepareEnv()
		output, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("inspect %s runtime: %w: %s", language, err, strings.TrimSpace(string(output)))
		}
		identity := lastOutputLine(output)
		if identity == "" {
			return "", fmt.Errorf("inspect %s runtime: empty version", language)
		}
		sourceRuntimeFingerprints.Store(cacheKey, identity)
		runtimeIdentity = identity
	}

	sdkDigest, err := sourceSDKDigest(language)
	if err != nil {
		return "", fmt.Errorf("fingerprint %s SDK: %w", language, err)
	}
	record := sourceReadyRecord{
		Version:  sourcePrepareVersion,
		Language: language,
		Runtime:  runtimeIdentity.(string),
		Platform: sourcePlatformIdentity(),
		SDK:      sdkDigest,
	}
	data, err := json.Marshal(record)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func normalizedScriptLanguage(scriptLang string) string {
	switch scriptLang {
	case "python", "go":
		return scriptLang
	default:
		return "typescript"
	}
}

func runtimeIdentityCommand(language string, pythonPath string, bunPath string, goPath string) (string, []string) {
	switch language {
	case "python":
		return pythonPath, []string{"-S", "-c", "import struct,sys,sysconfig; print('|'.join((sys.implementation.cache_tag, sys.version.split()[0], sysconfig.get_platform(), str(struct.calcsize('P') * 8))))"}
	case "go":
		return goPath, []string{"version"}
	default:
		return bunPath, []string{"--version"}
	}
}

func sourcePlatformIdentity() string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(runtime.GOOS + "/" + runtime.GOARCH + "\x00" + runtime.Version()))
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		_, _ = hash.Write(data)
	}
	return runtime.GOOS + "/" + runtime.GOARCH + ":" + hex.EncodeToString(hash.Sum(nil))[:16]
}

func sourceSDKDigest(language string) (string, error) {
	hash := sha256.New()
	var files fs.FS
	switch language {
	case "python":
		files = windforcepyclient.Files
	case "go":
		files = windforcegoclient.Files
		_, _ = hash.Write([]byte(wrapperGo()))
	default:
		files = windforceclient.Files
	}
	if err := fs.WalkDir(files, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, readErr := fs.ReadFile(files, path)
		if readErr != nil {
			return readErr
		}
		_, _ = hash.Write([]byte(filepath.ToSlash(path) + "\x00"))
		_, _ = hash.Write(data)
		return nil
	}); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
