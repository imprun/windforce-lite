package server

import (
	"os"
	"path/filepath"
	"sort"
	"unicode/utf8"
)

const (
	sourceFileCapBytes  = 512 * 1024
	sourceTotalCapBytes = 8 * 1024 * 1024
)

func readCanonicalSourceFiles(root string) (map[string]string, []string, error) {
	files := map[string]string{}
	skipped := []string{}
	total := 0
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > sourceFileCapBytes || total+int(info.Size()) > sourceTotalCapBytes {
			skipped = append(skipped, rel)
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !utf8.Valid(content) {
			skipped = append(skipped, rel)
			return nil
		}
		files[rel] = string(content)
		total += len(content)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(skipped)
	return files, skipped, nil
}
