package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	catalogpkg "github.com/imprun/windforce-core/internal/catalog"
	"github.com/imprun/windforce-core/internal/contract"
	executionpkg "github.com/imprun/windforce-core/internal/execution"
	gitsourcepkg "github.com/imprun/windforce-core/internal/gitsource"
)

type canonicalGitSourceView struct {
	ID               int64      `json:"id"`
	WorkspaceID      string     `json:"workspace_id"`
	Name             string     `json:"name"`
	RepoURL          string     `json:"repo_url"`
	Branch           string     `json:"branch"`
	Subpath          string     `json:"subpath"`
	CredsRef         string     `json:"creds_ref"`
	Kind             string     `json:"kind"`
	LastSyncedCommit *string    `json:"last_synced_commit"`
	LastSyncedAt     *time.Time `json:"last_synced_at"`
	CreatedAt        time.Time  `json:"created_at"`
}

func newCanonicalGitSourceView(source gitsourcepkg.Source) canonicalGitSourceView {
	return canonicalGitSourceView{
		ID:               parseCanonicalGitSourceID(source.ID),
		WorkspaceID:      contract.NormalizeWorkspace(source.Workspace),
		Name:             source.Name,
		RepoURL:          source.RepoURL,
		Branch:           firstNonEmpty(source.Branch, "main"),
		Subpath:          source.Subpath,
		CredsRef:         source.TokenEnv,
		Kind:             firstNonEmpty(source.Kind, "external"),
		LastSyncedCommit: cloneStringPtr(source.LastSyncedCommit),
		LastSyncedAt:     cloneTimePtr(source.LastSyncedAt),
		CreatedAt:        timeValue(source.CreatedAt),
	}
}

func parseCanonicalGitSourceID(id string) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func requireCanonicalGitSourceRouteID(w http.ResponseWriter, id string) (string, bool) {
	id = strings.TrimSpace(id)
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		writeError(w, http.StatusBadRequest, "bad git source id")
		return "", false
	}
	return id, true
}

func canonicalGitSourceIDPtr(id string) *int64 {
	value := parseCanonicalGitSourceID(id)
	if value == 0 {
		return nil
	}
	return &value
}

const probeTimeout = 15 * time.Second

type canonicalGitSourcePatchRequest struct {
	Name     *string `json:"name"`
	RepoURL  *string `json:"repo_url"`
	Branch   *string `json:"branch"`
	Subpath  *string `json:"subpath"`
	CredsRef *string `json:"creds_ref"`

	NameCamel     *string `json:"Name"`
	RepoURLCamel  *string `json:"RepoURL"`
	BranchCamel   *string `json:"Branch"`
	SubpathCamel  *string `json:"Subpath"`
	CredsRefCamel *string `json:"CredsRef"`
}

func canonicalGitSourcePatchFromRequest(w http.ResponseWriter, request canonicalGitSourcePatchRequest) (gitsourcepkg.Patch, bool) {
	var patch gitsourcepkg.Patch
	if value, ok := firstPresentString(request.Name, request.NameCamel); ok {
		value = strings.TrimSpace(value)
		if value == "" {
			writeError(w, http.StatusBadRequest, "name cannot be empty")
			return patch, false
		}
		patch.Name = &value
	}
	if value, ok := firstPresentString(request.RepoURL, request.RepoURLCamel); ok {
		value = strings.TrimSpace(value)
		if value == "" {
			writeError(w, http.StatusBadRequest, "repo_url cannot be empty")
			return patch, false
		}
		patch.RepoURL = &value
	}
	if value, ok := firstPresentString(request.Branch, request.BranchCamel); ok {
		value = strings.TrimSpace(value)
		if value == "" {
			value = "main"
		}
		patch.Branch = &value
	}
	if value, ok := firstPresentString(request.Subpath, request.SubpathCamel); ok {
		value = strings.TrimSpace(value)
		patch.Subpath = &value
	}
	if value, ok := firstPresentString(request.CredsRef, request.CredsRefCamel); ok {
		value = strings.TrimSpace(value)
		patch.TokenEnv = &value
	}
	return patch, true
}

func firstPresentString(values ...*string) (string, bool) {
	for _, value := range values {
		if value != nil {
			return *value, true
		}
	}
	return "", false
}

func stringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type canonicalSyncResult struct {
	Commit           string   `json:"commit"`
	App              string   `json:"app"`
	Actions          []string `json:"actions"`
	Flows            []string `json:"flows,omitempty"`
	Source           string   `json:"source,omitempty"`
	DeploymentID     *string  `json:"deployment_id,omitempty"`
	CreatedBy        *string  `json:"created_by,omitempty"`
	Message          *string  `json:"message,omitempty"`
	BundleStatus     string   `json:"bundle_status"`
	BundleDigest     string   `json:"bundle_digest,omitempty"`
	BundleURI        string   `json:"bundle_uri,omitempty"`
	Runtime          string   `json:"runtime"`
	ValidationChecks []string `json:"validation_checks"`
}

func newCanonicalSyncResult(deployment contract.Deployment) canonicalSyncResult {
	actions := make([]string, 0, len(deployment.Actions))
	for key := range deployment.Actions {
		actions = append(actions, deployment.App+"."+key)
	}
	sort.Strings(actions)
	return canonicalSyncResult{
		Commit:           deployment.Commit,
		App:              deployment.App,
		Actions:          actions,
		Source:           strings.TrimSpace(deployment.Source),
		DeploymentID:     cloneStringPtr(deployment.DeploymentID),
		CreatedBy:        cloneStringPtr(deployment.CreatedBy),
		Message:          cloneStringPtr(deployment.Message),
		BundleStatus:     canonicalBundleStatus(deployment),
		BundleDigest:     strings.TrimSpace(deployment.BundleDigest),
		BundleURI:        strings.TrimSpace(deployment.BundleURI),
		Runtime:          canonicalDeploymentScriptLang(deployment),
		ValidationChecks: []string{"dependencies_prepared", "entrypoint_validated", "artifact_stored"},
	}
}

type canonicalAppModel struct {
	ID                   string    `json:"id"`
	WorkspaceID          string    `json:"workspace_id"`
	AppKey               string    `json:"app_key"`
	GitSourceID          int64     `json:"git_source_id"`
	CommitSha            string    `json:"commit_sha"`
	Entrypoint           string    `json:"entrypoint"`
	Tag                  string    `json:"tag"`
	TagOverride          *string   `json:"tag_override,omitempty"`
	TimeoutS             int32     `json:"timeout_s"`
	ScriptLang           string    `json:"script_lang"`
	BundleStatus         string    `json:"bundle_status"`
	BundleDigest         string    `json:"bundle_digest,omitempty"`
	BundleURI            string    `json:"bundle_uri,omitempty"`
	RequiredCapabilities []string  `json:"required_capabilities"`
	MaxConcurrent        *int32    `json:"max_concurrent,omitempty"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type canonicalAppView struct {
	canonicalAppModel
	EffectiveRouteTag string `json:"effective_route_tag"`
}

type canonicalAppSummaryView struct {
	canonicalAppView
	ActionsCount   int64 `json:"actions_count"`
	SchedulesCount int64 `json:"schedules_count"`
	FlowsCount     int64 `json:"flows_count"`
}

type canonicalAppHistoryItem struct {
	ID           string    `json:"id"`
	CommitSha    string    `json:"commit_sha"`
	Entrypoint   string    `json:"entrypoint"`
	Source       string    `json:"source"`
	DeploymentID *string   `json:"deployment_id,omitempty"`
	Message      *string   `json:"message,omitempty"`
	CreatedBy    *string   `json:"created_by,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type canonicalActionModel struct {
	ID                   string    `json:"id"`
	WorkspaceID          string    `json:"workspace_id"`
	AppKey               string    `json:"app_key"`
	ActionKey            string    `json:"action_key"`
	DisplayName          string    `json:"display_name,omitempty"`
	InputSchema          []byte    `json:"input_schema"`
	OutputSchema         []byte    `json:"output_schema"`
	Tag                  *string   `json:"tag,omitempty"`
	TagOverride          *string   `json:"tag_override,omitempty"`
	TimeoutS             *int32    `json:"timeout_s,omitempty"`
	RequiredCapabilities []string  `json:"required_capabilities,omitempty"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type canonicalActionSchemaView struct {
	WorkspaceID            string          `json:"workspace_id"`
	AppKey                 string          `json:"app_key"`
	ActionKey              string          `json:"action_key"`
	InputSchema            json.RawMessage `json:"input_schema"`
	OutputSchema           json.RawMessage `json:"output_schema"`
	OperatorSettingsSchema json.RawMessage `json:"operator_settings_schema"`
}

type canonicalActionView struct {
	canonicalActionModel
	EffectiveCapabilities []string `json:"effective_capabilities"`
	EffectiveRouteTag     string   `json:"effective_route_tag"`
}

type canonicalWorkerTagsView struct {
	Tags         []canonicalTagLiveness `json:"tags"`
	DedicatedTag *string                `json:"dedicated_tag"`
}

type canonicalTagLiveness struct {
	Tag          string   `json:"tag"`
	LiveWorkers  int64    `json:"live_workers"`
	Capabilities []string `json:"capabilities"`
	Workers      []any    `json:"workers"`
}

func canonicalDeployments(snapshot catalogpkg.Snapshot, workspaceID string) []contract.Deployment {
	workspaceID = contract.NormalizeWorkspace(workspaceID)
	deployments := make([]contract.Deployment, 0, len(snapshot.Deployments))
	for _, deployment := range snapshot.Deployments {
		if contract.NormalizeWorkspace(deployment.SourceWorkspace()) != workspaceID {
			continue
		}
		deployments = append(deployments, deployment)
	}
	sort.Slice(deployments, func(i, j int) bool {
		return deployments[i].App < deployments[j].App
	})
	return deployments
}

func newCanonicalAppSummaryView(deployment contract.Deployment) canonicalAppSummaryView {
	return canonicalAppSummaryView{
		canonicalAppView: newCanonicalAppView(deployment),
		ActionsCount:     int64(len(deployment.Actions)),
	}
}

func newCanonicalAppHistoryItem(item catalogpkg.DeploymentHistory) canonicalAppHistoryItem {
	return canonicalAppHistoryItem{
		ID:           item.ID,
		CommitSha:    item.Commit,
		Entrypoint:   item.Entrypoint,
		Source:       firstNonEmpty(item.Source, "external_sync"),
		DeploymentID: cloneStringPtr(item.DeploymentID),
		Message:      item.Message,
		CreatedBy:    cloneStringPtr(item.CreatedBy),
		CreatedAt:    item.CreatedAt,
	}
}

func newCanonicalAppModel(deployment contract.Deployment) canonicalAppModel {
	return canonicalAppModel{
		ID:                   canonicalAppID(deployment),
		WorkspaceID:          contract.NormalizeWorkspace(deployment.SourceWorkspace()),
		AppKey:               deployment.App,
		GitSourceID:          parseCanonicalGitSourceID(deployment.SourceGitSourceID()),
		CommitSha:            deployment.Commit,
		Entrypoint:           canonicalDeploymentEntrypoint(deployment),
		Tag:                  firstNonEmpty(strings.TrimSpace(deployment.Tag), defaultRouteTag()),
		TagOverride:          cloneStringPtr(deployment.TagOverride),
		TimeoutS:             canonicalDeploymentTimeoutSeconds(deployment),
		ScriptLang:           canonicalDeploymentScriptLang(deployment),
		BundleStatus:         canonicalBundleStatus(deployment),
		BundleDigest:         strings.TrimSpace(deployment.BundleDigest),
		BundleURI:            strings.TrimSpace(deployment.BundleURI),
		RequiredCapabilities: cloneStringSlice(deployment.RequiredCapabilities),
		MaxConcurrent:        cloneInt32Ptr(deployment.MaxConcurrent),
		UpdatedAt:            canonicalDeploymentUpdatedAt(deployment),
	}
}

func canonicalBundleStatus(deployment contract.Deployment) string {
	if strings.TrimSpace(deployment.BundleDigest) == "" {
		return "missing"
	}
	return "ready"
}

func newCanonicalAppView(deployment contract.Deployment) canonicalAppView {
	return canonicalAppView{
		canonicalAppModel: newCanonicalAppModel(deployment),
		EffectiveRouteTag: contract.EffectiveRouteTagForApp(deployment),
	}
}

func (h *Handler) newCanonicalActionViews(schemaReader *canonicalSchemaReader, deployment contract.Deployment) ([]canonicalActionView, error) {
	keys := make([]string, 0, len(deployment.Actions))
	for key := range deployment.Actions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	actions := make([]canonicalActionView, 0, len(keys))
	for _, key := range keys {
		action, err := h.newCanonicalActionView(schemaReader, deployment, key, deployment.Actions[key])
		if err != nil {
			return nil, err
		}
		actions = append(actions, action)
	}
	return actions, nil
}

func (h *Handler) newCanonicalActionModel(schemaReader *canonicalSchemaReader, deployment contract.Deployment, actionKey string, action contract.Action) (canonicalActionModel, error) {
	schemaView, err := h.newCanonicalActionSchemaView(schemaReader, deployment, actionKey, action)
	if err != nil {
		return canonicalActionModel{}, err
	}
	return canonicalActionModel{
		ID:                   canonicalAppID(deployment) + "/" + actionKey,
		WorkspaceID:          contract.NormalizeWorkspace(deployment.SourceWorkspace()),
		AppKey:               deployment.App,
		ActionKey:            actionKey,
		DisplayName:          canonicalActionDisplayName(schemaView.InputSchema, schemaView.OutputSchema),
		InputSchema:          canonicalCatalogSchemaBytes(schemaView.InputSchema),
		OutputSchema:         canonicalCatalogSchemaBytes(schemaView.OutputSchema),
		Tag:                  cloneStringPtr(action.Tag),
		TagOverride:          cloneStringPtr(action.TagOverride),
		TimeoutS:             cloneInt32Ptr(action.TimeoutS),
		RequiredCapabilities: cloneStringSlicePtr(action.Capabilities),
		UpdatedAt:            canonicalActionUpdatedAt(deployment, action),
	}, nil
}

// canonicalActionDisplayName projects JSON Schema's standard top-level title
// into the control-plane list view. The key remains the executable identity.
func canonicalActionDisplayName(inputSchema, outputSchema json.RawMessage) string {
	return firstNonEmpty(canonicalSchemaTitle(inputSchema), canonicalSchemaTitle(outputSchema))
}

func canonicalSchemaTitle(schema json.RawMessage) string {
	var document struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(schema, &document); err != nil {
		return ""
	}
	return strings.TrimSpace(document.Title)
}

func canonicalCatalogSchemaBytes(schema json.RawMessage) []byte {
	if len(bytes.TrimSpace(schema)) == 0 {
		return []byte("{}")
	}
	return append([]byte(nil), schema...)
}

func (h *Handler) newCanonicalActionSchemaView(schemaReader *canonicalSchemaReader, deployment contract.Deployment, actionKey string, action contract.Action) (canonicalActionSchemaView, error) {
	inputSchema, err := schemaReader.Read(action.InputSchema, action.InputSchemaBody)
	if err != nil {
		return canonicalActionSchemaView{}, fmt.Errorf("action %s.%s input schema: %w", deployment.App, actionKey, err)
	}
	outputSchema, err := schemaReader.Read(action.OutputSchema, action.OutputSchemaBody)
	if err != nil {
		return canonicalActionSchemaView{}, fmt.Errorf("action %s.%s output schema: %w", deployment.App, actionKey, err)
	}
	operatorSettingsSchema, err := schemaReader.Read(action.OperatorSettingsSchema, action.OperatorSettingsSchemaBody)
	if err != nil {
		return canonicalActionSchemaView{}, fmt.Errorf("action %s.%s operator settings schema: %w", deployment.App, actionKey, err)
	}
	return canonicalActionSchemaView{
		WorkspaceID:            contract.NormalizeWorkspace(deployment.SourceWorkspace()),
		AppKey:                 deployment.App,
		ActionKey:              actionKey,
		InputSchema:            inputSchema,
		OutputSchema:           outputSchema,
		OperatorSettingsSchema: operatorSettingsSchema,
	}, nil
}

func (h *Handler) newCanonicalActionView(schemaReader *canonicalSchemaReader, deployment contract.Deployment, actionKey string, action contract.Action) (canonicalActionView, error) {
	model, err := h.newCanonicalActionModel(schemaReader, deployment, actionKey, action)
	if err != nil {
		return canonicalActionView{}, err
	}
	effectiveCapabilities := contract.EffectiveCapabilities(deployment.RequiredCapabilities, action.Capabilities)
	return canonicalActionView{
		canonicalActionModel:  model,
		EffectiveCapabilities: cloneStringSlice(effectiveCapabilities),
		EffectiveRouteTag:     contract.EffectiveRouteTagForAction(deployment, action),
	}, nil
}

type canonicalSchemaReader = executionpkg.SchemaReader

func (h *Handler) newCanonicalSchemaReader(ctx context.Context, deployment contract.Deployment) *canonicalSchemaReader {
	var store executionpkg.BundleStore
	if h.syncer != nil && h.syncer.Store != nil {
		store = h.syncer.Store
	}
	return executionpkg.NewSchemaReader(ctx, store, deployment)
}

func canonicalAppID(deployment contract.Deployment) string {
	return contract.NormalizeWorkspace(deployment.SourceWorkspace()) + "/" + deployment.App
}

func canonicalDeploymentEntrypoint(deployment contract.Deployment) string {
	return deployment.Entrypoint
}

func canonicalDeploymentScriptLang(deployment contract.Deployment) string {
	if deployment.ScriptLang == "" {
		return "typescript"
	}
	return deployment.ScriptLang
}

func canonicalDeploymentTimeoutSeconds(deployment contract.Deployment) int32 {
	if deployment.TimeoutS > 0 {
		return deployment.TimeoutS
	}
	return contract.DefaultTimeoutS
}

func canonicalDeploymentUpdatedAt(deployment contract.Deployment) time.Time {
	if deployment.UpdatedAt != nil {
		return *deployment.UpdatedAt
	}
	return time.Time{}
}

func canonicalActionUpdatedAt(deployment contract.Deployment, action contract.Action) time.Time {
	if action.UpdatedAt != nil {
		return *action.UpdatedAt
	}
	return canonicalDeploymentUpdatedAt(deployment)
}

func defaultRouteTag() string {
	return contract.DefaultRouteTag
}

func decodeCanonicalTagOverride(w http.ResponseWriter, r *http.Request) (*string, bool) {
	var request struct {
		TagOverride json.RawMessage `json:"tag_override"`
	}
	if err := readOptionalJSON(r, &request); err != nil || request.TagOverride == nil {
		writeError(w, http.StatusBadRequest, "tag_override required (string to set, null to clear)")
		return nil, false
	}
	if string(bytes.TrimSpace(request.TagOverride)) == "null" {
		return nil, true
	}
	var value string
	if err := json.Unmarshal(request.TagOverride, &value); err != nil || !validRouteTag(value) {
		writeError(w, http.StatusBadRequest, "tag_override must be a valid tag (lowercase alphanumeric, _ or -, max 64) or null")
		return nil, false
	}
	return &value, true
}

func validRouteTag(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for index, item := range value {
		if item >= 'a' && item <= 'z' {
			continue
		}
		if item >= '0' && item <= '9' {
			continue
		}
		if index > 0 && (item == '_' || item == '-') {
			continue
		}
		return false
	}
	return true
}

func validAppKey(value string) bool {
	return contract.ValidAppKey(value)
}

func validActionKey(value string) bool {
	return contract.ValidActionKey(value)
}

func cloneRawMessage(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), value...)
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneStringSlice(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string(nil), values...)
}

func cloneStringSlicePtr(values *[]string) []string {
	if values == nil {
		return nil
	}
	return cloneStringSlice(*values)
}

func cloneTimePtr(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func timeValue(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

func cloneInt32Ptr(value *int32) *int32 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func newCanonicalWorkerTagsView(tags map[string]struct{}) canonicalWorkerTagsView {
	if tags == nil {
		tags = map[string]struct{}{}
	}
	keys := make([]string, 0, len(tags))
	for tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		keys = append(keys, tag)
	}
	sort.Strings(keys)
	items := make([]canonicalTagLiveness, 0, len(keys))
	for _, tag := range keys {
		items = append(items, canonicalTagLiveness{
			Tag:          tag,
			Capabilities: []string{},
			Workers:      []any{},
		})
	}
	return canonicalWorkerTagsView{
		Tags: items,
	}
}
