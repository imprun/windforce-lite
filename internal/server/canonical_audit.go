package server

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/imprun/windforce-lite/internal/catalog"
	gitsourcepkg "github.com/imprun/windforce-lite/internal/gitsource"
)

// The audit trail records non-release state changes (repository settings,
// deletions, route tag overrides) keyed by workspace and git source id.
// Releases stay in the deployment history.

type auditAppender interface {
	AppendAudit(ctx context.Context, record catalog.AuditRecord) error
}

type auditReader interface {
	AuditTrail(ctx context.Context, workspace string, gitSourceID string) ([]catalog.AuditRecord, error)
}

type canonicalAuditRecord struct {
	ID          string    `json:"id"`
	GitSourceID int64     `json:"git_source_id"`
	AppKey      string    `json:"app_key,omitempty"`
	Kind        string    `json:"kind"`
	Detail      string    `json:"detail,omitempty"`
	Actor       string    `json:"actor"`
	CreatedAt   time.Time `json:"created_at"`
}

func (h *Handler) handleCanonicalGitSourceAudit(w http.ResponseWriter, r *http.Request, workspaceID string, sourceID string) {
	var ok bool
	sourceID, ok = requireCanonicalGitSourceRouteID(w, sourceID)
	if !ok {
		return
	}
	reader, ok := h.catalog.(auditReader)
	if !ok {
		writeError(w, http.StatusNotImplemented, "audit trail is not supported by this catalog")
		return
	}
	records, err := reader.AuditTrail(r.Context(), workspaceID, sourceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sort.SliceStable(records, func(i, j int) bool {
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})
	items := make([]canonicalAuditRecord, 0, len(records))
	for _, record := range records {
		actor := record.Actor
		if actor == "" {
			actor = "system"
		}
		items = append(items, canonicalAuditRecord{
			ID:          record.ID,
			GitSourceID: parseCanonicalGitSourceID(record.GitSourceID),
			AppKey:      record.App,
			Kind:        record.Kind,
			Detail:      record.Detail,
			Actor:       actor,
			CreatedAt:   record.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, items)
}

// recordAudit appends an audit record on a best-effort basis: audit must
// never fail the state change it describes.
func (h *Handler) recordAudit(r *http.Request, workspaceID string, gitSourceID string, appKey string, kind string, detail string) {
	appender, ok := h.catalog.(auditAppender)
	if !ok || gitSourceID == "" {
		return
	}
	_ = appender.AppendAudit(r.Context(), catalog.AuditRecord{
		Workspace:   workspaceID,
		GitSourceID: gitSourceID,
		App:         appKey,
		Kind:        kind,
		Detail:      detail,
		Actor:       strings.TrimSpace(requestActorSubject(r)),
	})
}

func gitSourceAuditDetail(source gitsourcepkg.Source) string {
	detail := source.RepoURL + "@" + sourceBranchOrDefault(source)
	if source.Subpath != "" {
		detail += " · " + source.Subpath
	}
	return detail
}

func gitSourceChangeDetail(before gitsourcepkg.Source, after gitsourcepkg.Source) string {
	changes := make([]string, 0, 5)
	appendChange := func(field, from, to string) {
		if from != to {
			changes = append(changes, fmt.Sprintf("%s: %q → %q", field, from, to))
		}
	}
	appendChange("name", before.Name, after.Name)
	appendChange("repo_url", before.RepoURL, after.RepoURL)
	appendChange("branch", sourceBranchOrDefault(before), sourceBranchOrDefault(after))
	appendChange("subpath", before.Subpath, after.Subpath)
	appendChange("creds_ref", before.TokenEnv, after.TokenEnv)
	return strings.Join(changes, ", ")
}

func sourceBranchOrDefault(source gitsourcepkg.Source) string {
	if strings.TrimSpace(source.Branch) == "" {
		return "main"
	}
	return source.Branch
}

func tagOverrideDetail(scope string, tagOverride *string) string {
	if tagOverride == nil {
		return scope + " tag_override cleared"
	}
	return fmt.Sprintf("%s tag_override set to %q", scope, *tagOverride)
}
