package server

import (
	"net/http"
	"os"
	"path/filepath"
	"unicode/utf8"
)

const documentationReadmeCapBytes = 1024 * 1024

type canonicalAppDocumentationView struct {
	AppKey    string `json:"app_key"`
	CommitSHA string `json:"commit_sha"`
	Available bool   `json:"available"`
	Path      string `json:"path,omitempty"`
	Markdown  string `json:"markdown,omitempty"`
}

func (h *Handler) handleCanonicalAppDocumentation(w http.ResponseWriter, r *http.Request, workspaceID string, app string) {
	if !validAppKey(app) {
		writeError(w, http.StatusBadRequest, "invalid app key")
		return
	}
	deployment, ok := h.getCanonicalDeployment(w, r, workspaceID, app, "app not found")
	if !ok {
		return
	}
	if h.syncer == nil || h.syncer.Store == nil {
		writeError(w, http.StatusInternalServerError, "source storage not configured")
		return
	}
	exists, err := h.syncer.Store.Exists(r.Context(), deployment.SourceWorkspace(), deployment.SourceGitSourceID(), deployment.Commit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "source commit is not materialized — re-sync the app")
		return
	}
	reader := h.newCanonicalSchemaReader(r.Context(), deployment)
	defer reader.Close()
	sourceDir, err := reader.EnsureSourceDir()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sourceDir == "" {
		writeError(w, http.StatusInternalServerError, "source storage is not configured")
		return
	}
	path, markdown, available, err := readCanonicalReadme(sourceDir)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, canonicalAppDocumentationView{
		AppKey:    deployment.App,
		CommitSHA: deployment.Commit,
		Available: available,
		Path:      path,
		Markdown:  markdown,
	})
}

func readCanonicalReadme(root string) (path string, markdown string, available bool, err error) {
	for _, name := range []string{"README.md", "README.MD", "readme.md"} {
		candidate := filepath.Join(root, name)
		info, statErr := os.Stat(candidate)
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			return "", "", false, statErr
		}
		if info.IsDir() {
			continue
		}
		if info.Size() > documentationReadmeCapBytes {
			return "", "", false, &documentationFileSizeError{path: name}
		}
		contents, readErr := os.ReadFile(candidate)
		if readErr != nil {
			return "", "", false, readErr
		}
		if !utf8.Valid(contents) {
			return "", "", false, &documentationEncodingError{path: name}
		}
		return name, string(contents), true, nil
	}
	return "", "", false, nil
}

type documentationFileSizeError struct {
	path string
}

func (err *documentationFileSizeError) Error() string {
	return err.path + " exceeds the 1 MiB documentation limit"
}

type documentationEncodingError struct {
	path string
}

func (err *documentationEncodingError) Error() string {
	return err.path + " must be UTF-8 text"
}
