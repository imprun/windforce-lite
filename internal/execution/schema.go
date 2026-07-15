package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/imprun/windforce-lite/internal/contract"
)

// BundleStore materializes a source bundle pinned by workspace, source, and commit.
type BundleStore interface {
	FetchTo(ctx context.Context, destinationDir string, workspace string, gitSourceID string, commit string) error
}

// SchemaReader reads schemas from the immutable bundle selected by a deployment.
type SchemaReader struct {
	ctx        context.Context
	store      BundleStore
	deployment contract.Deployment
	sourceDir  string
	err        error
}

func NewSchemaReader(ctx context.Context, store BundleStore, deployment contract.Deployment) *SchemaReader {
	return &SchemaReader{ctx: ctx, store: store, deployment: deployment}
}

func (r *SchemaReader) Close() {
	if r.sourceDir != "" {
		_ = os.RemoveAll(r.sourceDir)
	}
}

func (r *SchemaReader) Read(schemaPath string, schemaBody json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(schemaBody)
	if len(trimmed) > 0 && string(trimmed) != "null" {
		if !json.Valid(trimmed) {
			return nil, errors.New("materialized schema is not valid JSON")
		}
		return json.RawMessage(append([]byte(nil), trimmed...)), nil
	}
	if schemaPath == "" {
		return json.RawMessage([]byte("{}")), nil
	}
	if filepath.IsAbs(schemaPath) || strings.HasPrefix(schemaPath, "/") || strings.Contains(schemaPath, "..") {
		return nil, fmt.Errorf("schema path %q must be a relative path inside the app", schemaPath)
	}
	if r.store == nil {
		return nil, errors.New("source storage is not configured")
	}
	sourceDir, err := r.EnsureSourceDir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(sourceDir, filepath.FromSlash(schemaPath)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("manifest references schema %q but the file is missing", schemaPath)
		}
		return nil, err
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("schema %q is not valid JSON", schemaPath)
	}
	return json.RawMessage(append([]byte(nil), data...)), nil
}

func (r *SchemaReader) EnsureSourceDir() (string, error) {
	if r.err != nil {
		return "", r.err
	}
	if r.sourceDir != "" {
		return r.sourceDir, nil
	}
	sourceDir, err := os.MkdirTemp("", "windforce-lite-schema-")
	if err != nil {
		r.err = err
		return "", err
	}
	if err := r.store.FetchTo(r.ctx, sourceDir, r.deployment.SourceWorkspace(), r.deployment.SourceGitSourceID(), r.deployment.Commit); err != nil {
		_ = os.RemoveAll(sourceDir)
		r.err = err
		return "", err
	}
	r.sourceDir = sourceDir
	return sourceDir, nil
}
