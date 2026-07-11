package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/imprun/windforce-lite/internal/contract"
	gitsourcepkg "github.com/imprun/windforce-lite/internal/gitsource"
	"github.com/imprun/windforce-lite/internal/sampleapp"
	sourcepkg "github.com/imprun/windforce-lite/internal/source"
	"github.com/imprun/windforce-lite/internal/syncer"
)

type gitCredentialRequest struct {
	AuthMethod  string
	AccessToken string
	Username    string
	Password    string
}

func (h *Handler) handleCanonicalGitSources(w http.ResponseWriter, r *http.Request, workspaceID string) {
	snapshot, ok := h.loadGitSourceSnapshot(w, r)
	if !ok {
		return
	}
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	items := make([]canonicalGitSourceView, 0, len(snapshot.Sources))
	for _, source := range snapshot.Sources {
		if contract.NormalizeWorkspace(source.Workspace) != workspaceID {
			continue
		}
		items = append(items, newCanonicalGitSourceView(source))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].ID == items[j].ID {
			return items[i].Name < items[j].Name
		}
		return items[i].ID < items[j].ID
	})
	writeJSON(w, http.StatusOK, items)
}

func (h *Handler) handleCanonicalRegisterGitSource(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if h.gitSources == nil {
		writeError(w, http.StatusServiceUnavailable, "git source registry is not configured")
		return
	}
	var request struct {
		Name     string `json:"name"`
		RepoURL  string `json:"repo_url"`
		Branch   string `json:"branch"`
		Subpath  string `json:"subpath"`
		CredsRef string `json:"creds_ref"`

		AuthMethod  string `json:"auth_method"`
		AccessToken string `json:"access_token"`
		Username    string `json:"username"`
		Password    string `json:"password"`

		NameCamel     string `json:"Name"`
		RepoURLCamel  string `json:"RepoURL"`
		BranchCamel   string `json:"Branch"`
		SubpathCamel  string `json:"Subpath"`
		CredsRefCamel string `json:"CredsRef"`
	}
	if err := readOptionalJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "name and repo_url required")
		return
	}
	name := strings.TrimSpace(firstNonEmpty(request.Name, request.NameCamel))
	repoURL := strings.TrimSpace(firstNonEmpty(request.RepoURL, request.RepoURLCamel))
	branch := strings.TrimSpace(firstNonEmpty(request.Branch, request.BranchCamel))
	subpath := strings.TrimSpace(firstNonEmpty(request.Subpath, request.SubpathCamel))
	credsRef := strings.TrimSpace(firstNonEmpty(request.CredsRef, request.CredsRefCamel))
	credential, err := gitCredentialFromRequest(gitCredentialRequest{
		AuthMethod:  request.AuthMethod,
		AccessToken: request.AccessToken,
		Username:    request.Username,
		Password:    request.Password,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if name == "" || repoURL == "" {
		writeError(w, http.StatusBadRequest, "name and repo_url required")
		return
	}
	if credential != "" && credsRef == "" {
		if h.store == nil {
			writeError(w, http.StatusServiceUnavailable, "state store is not configured")
			return
		}
		credsRef = defaultGitCredentialPath(name)
		encrypted, err := h.encryptSecretVariable(r.Context(), workspaceID, credential)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := h.store.SetVariable(r.Context(), workspaceID, "", credsRef, encrypted, true, fmt.Sprintf("Git credential for source %s", name)); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	source := gitsourcepkg.Source{
		Workspace: workspaceID,
		Name:      name,
		RepoURL:   repoURL,
		Branch:    branch,
		Subpath:   subpath,
		TokenEnv:  credsRef,
	}
	source, ok := h.createGitSource(w, r, source)
	if !ok {
		return
	}
	writeJSON(w, http.StatusCreated, newCanonicalGitSourceView(source))
}

func (h *Handler) createGitSource(w http.ResponseWriter, r *http.Request, source gitsourcepkg.Source) (gitsourcepkg.Source, bool) {
	if creator, ok := h.gitSources.(interface {
		Create(context.Context, gitsourcepkg.Source) (gitsourcepkg.Source, error)
	}); ok {
		created, err := creator.Create(r.Context(), source)
		if errors.Is(err, gitsourcepkg.ErrGitSourceConflict) {
			writeError(w, http.StatusConflict, "git source name already exists")
			return gitsourcepkg.Source{}, false
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return gitsourcepkg.Source{}, false
		}
		return created, true
	}

	if _, err := h.gitSources.Get(r.Context(), source.Workspace, source.ID); err == nil {
		writeError(w, http.StatusConflict, "git source name already exists")
		return gitsourcepkg.Source{}, false
	} else if !errors.Is(err, gitsourcepkg.ErrGitSourceNotFound) {
		writeError(w, http.StatusInternalServerError, err.Error())
		return gitsourcepkg.Source{}, false
	}
	if err := h.gitSources.Upsert(r.Context(), source); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return gitsourcepkg.Source{}, false
	}
	source, err := h.gitSources.Get(r.Context(), source.Workspace, source.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return gitsourcepkg.Source{}, false
	}
	return source, true
}

func (h *Handler) handleCanonicalProbeGitSource(w http.ResponseWriter, r *http.Request, workspaceID string) {
	var request struct {
		RepoURL     string `json:"repo_url"`
		Branch      string `json:"branch"`
		AuthMethod  string `json:"auth_method"`
		AccessToken string `json:"access_token"`
		Username    string `json:"username"`
		Password    string `json:"password"`
		CredsRef    string `json:"creds_ref"`
	}
	if err := readOptionalJSON(r, &request); err != nil || strings.TrimSpace(request.RepoURL) == "" {
		writeError(w, http.StatusBadRequest, "repo_url required")
		return
	}
	token, err := gitCredentialFromRequest(gitCredentialRequest{
		AuthMethod:  request.AuthMethod,
		AccessToken: request.AccessToken,
		Username:    request.Username,
		Password:    request.Password,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if token == "" {
		resolved, err := h.resolveGitSourceCreds(r.Context(), workspaceID, request.CredsRef)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		token = resolved
	}
	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()
	branches, err := sourcepkg.ListRemoteBranches(ctx, strings.TrimSpace(request.RepoURL), token)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"reachable": false,
			"error":     err.Error(),
			"branches":  []string{},
		})
		return
	}
	branch := strings.TrimSpace(request.Branch)
	if branch == "" {
		branch = "main"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"reachable":     true,
		"branch":        branch,
		"branch_exists": stringSliceContains(branches, branch),
		"branches":      branches,
	})
}

func (h *Handler) handleCanonicalSampleGitSource(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if h.syncer == nil {
		writeError(w, http.StatusServiceUnavailable, "sync API is not configured")
		return
	}
	if h.gitSources == nil {
		writeError(w, http.StatusServiceUnavailable, "git source registry is not configured")
		return
	}
	var request struct {
		AppKey string `json:"app_key"`
	}
	if err := readOptionalJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	repo, err := sampleapp.EnsureRepository(r.Context(), h.sampleRoot, workspaceID, request.AppKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	source := gitsourcepkg.Source{
		Workspace: workspaceID,
		Name:      repo.SourceName,
		RepoURL:   repo.RepoURL,
		Branch:    repo.Branch,
		Kind:      "managed",
	}
	status := http.StatusCreated
	existing, err := h.gitSources.Get(r.Context(), workspaceID, repo.SourceName)
	if err == nil {
		source.CreatedAt = existing.CreatedAt
		source.LastSyncedCommit = existing.LastSyncedCommit
		source.LastSyncedAt = existing.LastSyncedAt
		if err := h.gitSources.Upsert(r.Context(), source); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		source, err = h.gitSources.Get(r.Context(), workspaceID, repo.SourceName)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		status = http.StatusOK
	} else if errors.Is(err, gitsourcepkg.ErrGitSourceNotFound) {
		created, ok := h.createGitSource(w, r, source)
		if !ok {
			return
		}
		source = created
	} else {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	deployment, ok := h.syncGitSource(w, r, workspaceID, source)
	if !ok {
		return
	}
	writeJSON(w, status, map[string]any{
		"source":      newCanonicalGitSourceView(source),
		"sync_result": newCanonicalSyncResult(deployment),
	})
}

func (h *Handler) handleCanonicalPatchGitSource(w http.ResponseWriter, r *http.Request, workspaceID string, sourceID string) {
	var ok bool
	sourceID, ok = requireCanonicalGitSourceRouteID(w, sourceID)
	if !ok {
		return
	}
	patcher, ok := h.gitSources.(interface {
		Patch(context.Context, string, string, gitsourcepkg.Patch) (gitsourcepkg.Source, error)
	})
	if !ok {
		writeError(w, http.StatusNotImplemented, "git source patch is not supported")
		return
	}
	var request canonicalGitSourcePatchRequest
	if err := readRequiredJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	patch, ok := canonicalGitSourcePatchFromRequest(w, request)
	if !ok {
		return
	}
	source, err := patcher.Patch(r.Context(), workspaceID, sourceID, patch)
	if errors.Is(err, gitsourcepkg.ErrGitSourceConflict) {
		writeError(w, http.StatusConflict, "git source name already exists")
		return
	}
	if errors.Is(err, gitsourcepkg.ErrGitSourceNotFound) {
		writeError(w, http.StatusNotFound, "git source not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, newCanonicalGitSourceView(source))
}

func (h *Handler) handleCanonicalDeleteGitSource(w http.ResponseWriter, r *http.Request, workspaceID string, sourceID string) {
	var ok bool
	sourceID, ok = requireCanonicalGitSourceRouteID(w, sourceID)
	if !ok {
		return
	}
	deleter, ok := h.gitSources.(interface {
		Delete(context.Context, string, string) (bool, error)
	})
	if !ok {
		writeError(w, http.StatusNotImplemented, "git source delete is not supported")
		return
	}
	deleted, err := deleter.Delete(r.Context(), workspaceID, sourceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !deleted {
		writeError(w, http.StatusNotFound, "git source not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleCanonicalGitSourceSync(w http.ResponseWriter, r *http.Request, workspaceID string, sourceID string) {
	var ok bool
	sourceID, ok = requireCanonicalGitSourceRouteID(w, sourceID)
	if !ok {
		return
	}
	if h.syncer == nil {
		writeError(w, http.StatusServiceUnavailable, "sync API is not configured")
		return
	}
	if h.gitSources == nil {
		writeError(w, http.StatusServiceUnavailable, "git source registry is not configured")
		return
	}
	source, err := h.gitSources.Get(r.Context(), workspaceID, sourceID)
	if err != nil {
		if errors.Is(err, gitsourcepkg.ErrGitSourceNotFound) {
			writeError(w, http.StatusNotFound, "git source not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	deployment, ok := h.syncGitSource(w, r, workspaceID, source)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, newCanonicalSyncResult(deployment))
}

func (h *Handler) syncGitSource(w http.ResponseWriter, r *http.Request, workspaceID string, source gitsourcepkg.Source) (contract.Deployment, bool) {
	release, ok := h.acquireGitSourceOperation(workspaceID, source)
	if !ok {
		writeError(w, http.StatusConflict, "git source operation already in progress")
		return contract.Deployment{}, false
	}
	defer release()

	token, err := h.resolveGitSourceCreds(r.Context(), workspaceID, source.TokenEnv)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return contract.Deployment{}, false
	}
	s := *h.syncer
	deployment, err := s.Sync(r.Context(), syncer.Source{
		Workspace:   workspaceID,
		GitSourceID: source.ID,
		RepoURL:     source.RepoURL,
		Branch:      source.Branch,
		Subpath:     source.Subpath,
		Token:       token,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return contract.Deployment{}, false
	}
	if marker, ok := h.gitSources.(interface {
		MarkSynced(context.Context, string, string, string, time.Time) (gitsourcepkg.Source, error)
	}); ok {
		if _, err := marker.MarkSynced(r.Context(), workspaceID, source.ID, deployment.Commit, time.Now().UTC()); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return contract.Deployment{}, false
		}
	}
	return deployment, true
}

func (h *Handler) acquireGitSourceOperation(workspaceID string, source gitsourcepkg.Source) (func(), bool) {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	sourceID := strings.TrimSpace(source.ID)
	if sourceID == "" {
		sourceID = strings.TrimSpace(source.Name)
	}
	key := workspaceID + "\x00" + sourceID
	value, _ := h.syncLocks.LoadOrStore(key, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	if !lock.TryLock() {
		return nil, false
	}
	return lock.Unlock, true
}

func (h *Handler) resolveGitSourceCreds(ctx context.Context, workspaceID string, credsRef string) (string, error) {
	credsRef = strings.TrimSpace(credsRef)
	if credsRef == "" || h.store == nil {
		return "", nil
	}
	variable, found, err := h.store.GetVariable(ctx, workspaceID, "", credsRef)
	if err != nil || !found {
		return "", err
	}
	if variable.IsSecret {
		return h.decryptSecretVariable(ctx, workspaceID, variable.Value)
	}
	return variable.Value, nil
}

func gitCredentialFromRequest(request gitCredentialRequest) (string, error) {
	authMethod := strings.ToLower(strings.TrimSpace(request.AuthMethod))
	token := strings.TrimSpace(request.AccessToken)
	username := strings.TrimSpace(request.Username)
	password := strings.TrimSpace(request.Password)
	if authMethod == "" {
		authMethod = "pat"
		if token == "" && username == "" && password == "" {
			return "", nil
		}
		if username != "" || password != "" {
			authMethod = "basic"
		}
	}
	switch authMethod {
	case "none", "public":
		return "", nil
	case "pat", "token", "access_token":
		if token == "" {
			return "", errors.New("access_token is required for personal access token authentication")
		}
		return mustGitCredentialJSON(gitCredentialRequest{AuthMethod: "pat", AccessToken: token})
	case "basic", "password":
		if username == "" || password == "" {
			return "", errors.New("username and password are required for username/password authentication")
		}
		return mustGitCredentialJSON(gitCredentialRequest{AuthMethod: "basic", Username: username, Password: password})
	default:
		return "", fmt.Errorf("unsupported auth_method %q", request.AuthMethod)
	}
}

func mustGitCredentialJSON(request gitCredentialRequest) (string, error) {
	payload := map[string]string{"type": strings.ToLower(strings.TrimSpace(request.AuthMethod))}
	if payload["type"] == "basic" {
		payload["username"] = strings.TrimSpace(request.Username)
		payload["password"] = strings.TrimSpace(request.Password)
	} else {
		payload["token"] = strings.TrimSpace(request.AccessToken)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func defaultGitCredentialPath(sourceName string) string {
	var builder strings.Builder
	lastWasDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(sourceName)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			builder.WriteRune(r)
			lastWasDash = r == '-'
		default:
			if builder.Len() > 0 && !lastWasDash {
				builder.WriteByte('-')
				lastWasDash = true
			}
		}
	}
	slug := strings.Trim(builder.String(), "-")
	if slug == "" {
		slug = "source"
	}
	return "git/" + slug + "/credential"
}
