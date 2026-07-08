package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func TreeDigest(ctx context.Context, dir string) (string, error) {
	files := []string{}
	if err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		base := filepath.Base(rel)
		if entry.IsDir() {
			if skipDigestDir(base) {
				return filepath.SkipDir
			}
			return nil
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	}); err != nil {
		return "", err
	}
	sort.Strings(files)

	hash := sha256.New()
	for _, rel := range files {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		hash.Write([]byte(rel))
		hash.Write([]byte{0})
		file, err := os.Open(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(hash, file); err != nil {
			_ = file.Close()
			return "", err
		}
		if err := file.Close(); err != nil {
			return "", err
		}
		hash.Write([]byte{0})
	}
	return "sha256-" + hex.EncodeToString(hash.Sum(nil))[:16], nil
}

func skipDigestDir(base string) bool {
	switch strings.ToLower(base) {
	case ".git", "node_modules", ".venv", "__pycache__":
		return true
	default:
		return false
	}
}
