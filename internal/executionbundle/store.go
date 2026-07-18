package executionbundle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const markerFile = ".windforce-execution-bundle.json"

var ErrNotFound = errors.New("execution bundle not found")

type Descriptor struct {
	Digest    string    `json:"digest"`
	URI       string    `json:"uri"`
	CreatedAt time.Time `json:"createdAt"`
	FileCount int       `json:"fileCount"`
	SizeBytes int64     `json:"sizeBytes"`
}

type Store interface {
	Publish(ctx context.Context, sourceDir string) (Descriptor, error)
	Exists(ctx context.Context, digest string) (bool, error)
	Verify(ctx context.Context, digest string) (Descriptor, error)
	FetchTo(ctx context.Context, destinationDir string, digest string) (Descriptor, error)
}

type LocalStore struct {
	Root string
}

func NewLocalStore(root string) *LocalStore {
	return &LocalStore{Root: root}
}

func (s *LocalStore) Publish(ctx context.Context, sourceDir string) (Descriptor, error) {
	if strings.TrimSpace(s.Root) == "" {
		return Descriptor{}, errors.New("execution bundle store root is required")
	}
	info, err := os.Stat(sourceDir)
	if err != nil {
		return Descriptor{}, err
	}
	if !info.IsDir() {
		return Descriptor{}, errors.New("execution bundle source must be a directory")
	}
	digest, fileCount, sizeBytes, err := hashTree(ctx, sourceDir)
	if err != nil {
		return Descriptor{}, err
	}
	if existing, err := s.Verify(ctx, digest); err == nil {
		return existing, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Descriptor{}, err
	}

	targetDir, err := s.bundleDir(digest)
	if err != nil {
		return Descriptor{}, err
	}
	parentDir := filepath.Dir(targetDir)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return Descriptor{}, err
	}
	tempDir, err := os.MkdirTemp(parentDir, ".publish-")
	if err != nil {
		return Descriptor{}, err
	}
	defer os.RemoveAll(tempDir)

	if err := copyTree(ctx, sourceDir, tempDir); err != nil {
		return Descriptor{}, err
	}
	copiedDigest, _, _, err := hashTree(ctx, tempDir)
	if err != nil {
		return Descriptor{}, err
	}
	if copiedDigest != digest {
		return Descriptor{}, fmt.Errorf("execution bundle changed while publishing: got %s, want %s", copiedDigest, digest)
	}
	descriptor := Descriptor{
		Digest:    digest,
		URI:       "execution-bundle://" + strings.Replace(digest, ":", "/", 1),
		CreatedAt: time.Now().UTC(),
		FileCount: fileCount,
		SizeBytes: sizeBytes,
	}
	if err := writeDescriptor(filepath.Join(tempDir, markerFile), descriptor); err != nil {
		return Descriptor{}, err
	}
	if err := os.Rename(tempDir, targetDir); err != nil {
		if existing, readErr := s.readDescriptor(digest); readErr == nil {
			return existing, nil
		}
		return Descriptor{}, err
	}
	return descriptor, nil
}

func (s *LocalStore) Exists(ctx context.Context, digest string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	_, err := s.readDescriptor(digest)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return false, err
}

func (s *LocalStore) Verify(ctx context.Context, digest string) (Descriptor, error) {
	descriptor, err := s.readDescriptor(digest)
	if err != nil {
		return Descriptor{}, err
	}
	dir, err := s.bundleDir(digest)
	if err != nil {
		return Descriptor{}, err
	}
	actual, fileCount, sizeBytes, err := hashTree(ctx, dir)
	if err != nil {
		return Descriptor{}, err
	}
	if actual != digest {
		return Descriptor{}, fmt.Errorf("execution bundle digest mismatch: got %s, want %s", actual, digest)
	}
	if descriptor.FileCount != fileCount || descriptor.SizeBytes != sizeBytes {
		return Descriptor{}, errors.New("execution bundle descriptor does not match stored content")
	}
	return descriptor, nil
}

func (s *LocalStore) FetchTo(ctx context.Context, destinationDir string, digest string) (Descriptor, error) {
	descriptor, err := s.Verify(ctx, digest)
	if err != nil {
		return Descriptor{}, err
	}
	sourceDir, err := s.bundleDir(digest)
	if err != nil {
		return Descriptor{}, err
	}
	destinationDir, err = validateFetchDestination(sourceDir, destinationDir)
	if err != nil {
		return Descriptor{}, err
	}
	if err := os.MkdirAll(filepath.Dir(destinationDir), 0o755); err != nil {
		return Descriptor{}, err
	}
	tempDir, err := os.MkdirTemp(filepath.Dir(destinationDir), ".fetch-")
	if err != nil {
		return Descriptor{}, err
	}
	defer os.RemoveAll(tempDir)
	if err := copyTree(ctx, sourceDir, tempDir); err != nil {
		return Descriptor{}, err
	}
	actual, _, _, err := hashTree(ctx, tempDir)
	if err != nil {
		return Descriptor{}, err
	}
	if actual != digest {
		return Descriptor{}, fmt.Errorf("fetched execution bundle digest mismatch: got %s, want %s", actual, digest)
	}
	if err := os.RemoveAll(destinationDir); err != nil {
		return Descriptor{}, err
	}
	if err := os.Rename(tempDir, destinationDir); err != nil {
		return Descriptor{}, err
	}
	return descriptor, nil
}

func validateFetchDestination(sourceDir string, destinationDir string) (string, error) {
	if strings.TrimSpace(destinationDir) == "" {
		return "", errors.New("execution bundle destination is required")
	}
	sourceAbs, err := filepath.Abs(sourceDir)
	if err != nil {
		return "", err
	}
	destinationAbs, err := filepath.Abs(destinationDir)
	if err != nil {
		return "", err
	}
	volumeRoot := filepath.VolumeName(destinationAbs) + string(os.PathSeparator)
	if filepath.Clean(destinationAbs) == filepath.Clean(volumeRoot) {
		return "", errors.New("execution bundle destination cannot be a filesystem root")
	}
	if pathsOverlap(sourceAbs, destinationAbs) {
		return "", errors.New("execution bundle destination overlaps the artifact store")
	}
	return destinationAbs, nil
}

func pathsOverlap(left string, right string) bool {
	for _, pair := range [][2]string{{left, right}, {right, left}} {
		relative, err := filepath.Rel(pair[0], pair[1])
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func (s *LocalStore) readDescriptor(digest string) (Descriptor, error) {
	dir, err := s.bundleDir(digest)
	if err != nil {
		return Descriptor{}, err
	}
	data, err := os.ReadFile(filepath.Join(dir, markerFile))
	if errors.Is(err, os.ErrNotExist) {
		return Descriptor{}, ErrNotFound
	}
	if err != nil {
		return Descriptor{}, err
	}
	var descriptor Descriptor
	if err := json.Unmarshal(data, &descriptor); err != nil {
		return Descriptor{}, err
	}
	if descriptor.Digest != digest {
		return Descriptor{}, errors.New("execution bundle descriptor digest mismatch")
	}
	return descriptor, nil
}

func (s *LocalStore) bundleDir(digest string) (string, error) {
	hexDigest, err := parseDigest(digest)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.Root, "execution-bundles", "sha256", hexDigest), nil
}

func parseDigest(digest string) (string, error) {
	algorithm, value, ok := strings.Cut(strings.TrimSpace(digest), ":")
	if !ok || algorithm != "sha256" || len(value) != sha256.Size*2 {
		return "", fmt.Errorf("invalid execution bundle digest %q", digest)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "", fmt.Errorf("invalid execution bundle digest %q", digest)
	}
	return value, nil
}

// HashTree computes the bundle digest of a directory tree (names, modes,
// symlink targets, file contents; the descriptor marker excluded) — the same
// hash Publish/Verify use, exported so remote fetchers can re-verify.
func HashTree(ctx context.Context, root string) (string, error) {
	digest, _, _, err := hashTree(ctx, root)
	return digest, err
}

// ValidateSymlink rejects symlinks whose target is absolute or escapes root;
// path is the link's location inside root.
func ValidateSymlink(root string, path string, target string) error {
	return validateSymlink(root, path, target)
}

func hashTree(ctx context.Context, root string) (string, int, int64, error) {
	h := sha256.New()
	fileCount := 0
	var sizeBytes int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." || rel == markerFile {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		writeHashString(h, filepath.ToSlash(rel))
		writeHashString(h, strconv.FormatUint(uint64(info.Mode().Perm()), 8))
		switch {
		case entry.Type()&os.ModeSymlink != 0:
			writeHashString(h, "symlink")
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			writeHashString(h, target)
			fileCount++
		case entry.IsDir():
			writeHashString(h, "dir")
		case info.Mode().IsRegular():
			writeHashString(h, "file")
			writeHashString(h, strconv.FormatInt(info.Size(), 10))
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			written, copyErr := io.Copy(h, file)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			sizeBytes += written
			fileCount++
		default:
			return fmt.Errorf("unsupported execution bundle file type: %s", rel)
		}
		return nil
	})
	if err != nil {
		return "", 0, 0, err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), fileCount, sizeBytes, nil
}

func writeHashString(h hash.Hash, value string) {
	_, _ = io.WriteString(h, strconv.Itoa(len(value)))
	_, _ = io.WriteString(h, ":")
	_, _ = io.WriteString(h, value)
	_, _ = io.WriteString(h, "\x00")
}

func copyTree(ctx context.Context, sourceRoot string, destinationRoot string) error {
	return filepath.WalkDir(sourceRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(destinationRoot, 0o755)
		}
		if rel == markerFile {
			return nil
		}
		destination := filepath.Join(destinationRoot, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.Type()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if err := validateSymlink(sourceRoot, path, target); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
				return err
			}
			return os.Symlink(target, destination)
		case entry.IsDir():
			return os.MkdirAll(destination, info.Mode().Perm())
		case info.Mode().IsRegular():
			return copyFile(path, destination, info.Mode().Perm())
		default:
			return fmt.Errorf("unsupported execution bundle file type: %s", rel)
		}
	})
}

func validateSymlink(root string, path string, target string) error {
	if filepath.IsAbs(target) {
		return fmt.Errorf("execution bundle symlink %s has an absolute target", path)
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(path), target))
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("execution bundle symlink %s escapes source root", path)
	}
	return nil
}

func copyFile(source string, destination string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func writeDescriptor(path string, descriptor Descriptor) error {
	data, err := json.MarshalIndent(descriptor, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}
