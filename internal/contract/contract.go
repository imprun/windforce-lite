package contract

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	DefaultWorkspace   = "default"
	DefaultGitSourceID = "local"
	DefaultRouteTag    = "default"

	ActionAdapterJSONFile = "json-file"
	ActionAdapterCommand  = "command"

	CapabilityBrowser = "browser"
)

var capabilityRouteTags = map[string]string{
	CapabilityBrowser: "browser",
}

// App is the deployable source bundle described by windforce.json.
type App struct {
	App        string `json:"app"`
	Name       string `json:"name,omitempty"`
	Entrypoint string `json:"entrypoint,omitempty"`
	Runtime    string `json:"runtime,omitempty"`
	ScriptLang string `json:"scriptLang,omitempty"`
	TimeoutS   int32  `json:"timeout,omitempty"`
	Tag        string `json:"tag,omitempty"`
	// MaxConcurrent caps concurrently running jobs for this app. Nil means unlimited.
	MaxConcurrent *int32            `json:"maxConcurrent,omitempty"`
	Capabilities  []string          `json:"capabilities,omitempty"`
	Actions       map[string]Action `json:"actions"`
}

// Action is one executable unit inside an app.
type Action struct {
	Action       string         `json:"action"`
	Tag          *string        `json:"tag,omitempty"`
	TagOverride  *string        `json:"tagOverride,omitempty"`
	Runtime      string         `json:"runtime,omitempty"`
	Entrypoint   string         `json:"entrypoint,omitempty"`
	Command      []string       `json:"command,omitempty"`
	Adapter      *ActionAdapter `json:"adapter,omitempty"`
	InputSchema  string         `json:"inputSchema,omitempty"`
	OutputSchema string         `json:"outputSchema,omitempty"`
	TimeoutS     *int32         `json:"timeout,omitempty"`
	TimeoutMs    int64          `json:"timeoutMs,omitempty"`
	Capabilities *[]string      `json:"capabilities,omitempty"`
	UpdatedAt    *time.Time     `json:"updatedAt,omitempty"`
}

// ActionAdapter selects the contract between windforce-lite and integration
// adapter code. Source windforce.json files use the app-level entrypoint model;
// these fields are for deployment/runtime compatibility surfaces outside the
// source manifest.
//
// Source-manifest actions with no command run through the built-in ctx-first
// entrypoint runner. A command adapter invokes an external adapter subprocess,
// letting solution-specific script contracts live outside the core runtime.
type ActionAdapter struct {
	Type    string                     `json:"type,omitempty"`
	Command []string                   `json:"command,omitempty"`
	Env     []string                   `json:"env,omitempty"`
	Options map[string]json.RawMessage `json:"options,omitempty"`
}

// Deployment is the active source bundle selected by the catalog.
type Deployment struct {
	Workspace            string            `json:"workspace,omitempty"`
	GitSourceID          string            `json:"gitSourceId,omitempty"`
	App                  string            `json:"app"`
	Version              string            `json:"version,omitempty"`
	Tag                  string            `json:"tag,omitempty"`
	TagOverride          *string           `json:"tagOverride,omitempty"`
	Entrypoint           string            `json:"entrypoint,omitempty"`
	Runtime              string            `json:"runtime,omitempty"`
	ScriptLang           string            `json:"scriptLang,omitempty"`
	TimeoutS             int32             `json:"timeout,omitempty"`
	MaxConcurrent        *int32            `json:"maxConcurrent,omitempty"`
	RequiredCapabilities []string          `json:"requiredCapabilities,omitempty"`
	Commit               string            `json:"commit"`
	Message              *string           `json:"message,omitempty"`
	BundleDigest         string            `json:"bundleDigest,omitempty"`
	ObjectURI            string            `json:"objectUri"`
	Actions              map[string]Action `json:"actions"`
	UpdatedAt            *time.Time        `json:"updatedAt,omitempty"`
}

// JobRequest is the runtime request passed into windforce-lite.
type JobRequest struct {
	JobID      string          `json:"jobId"`
	App        string          `json:"app"`
	Action     string          `json:"action"`
	Input      json.RawMessage `json:"input"`
	Deployment Deployment      `json:"deployment"`
}

// JobResult is the subprocess execution result as observed by the runtime.
type JobResult struct {
	JobID      string          `json:"jobId,omitempty"`
	App        string          `json:"app"`
	Action     string          `json:"action"`
	Output     json.RawMessage `json:"output,omitempty"`
	ExitCode   int             `json:"exitCode"`
	Stdout     string          `json:"stdout,omitempty"`
	Stderr     string          `json:"stderr,omitempty"`
	DurationMs int64           `json:"durationMs"`
	Error      string          `json:"error,omitempty"`
}

func (a Action) AdapterType() string {
	if a.Adapter == nil {
		return ActionAdapterJSONFile
	}
	value := strings.TrimSpace(a.Adapter.Type)
	if value == "" {
		return ActionAdapterJSONFile
	}
	return value
}

func EffectiveRouteTag(appTag string, appTagOverride *string, actionTag *string, actionTagOverride *string) string {
	if actionTagOverride != nil && strings.TrimSpace(*actionTagOverride) != "" {
		return strings.TrimSpace(*actionTagOverride)
	}
	if actionTag != nil && strings.TrimSpace(*actionTag) != "" {
		return strings.TrimSpace(*actionTag)
	}
	if appTagOverride != nil && strings.TrimSpace(*appTagOverride) != "" {
		return strings.TrimSpace(*appTagOverride)
	}
	if strings.TrimSpace(appTag) != "" {
		return strings.TrimSpace(appTag)
	}
	return DefaultRouteTag
}

func NormalizeCapabilities(caps []string) ([]string, error) {
	if len(caps) == 0 {
		return nil, nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(caps))
	for _, raw := range caps {
		capability := strings.TrimSpace(raw)
		if capability == "" {
			return nil, fmt.Errorf("capability must not be empty")
		}
		if _, ok := capabilityRouteTags[capability]; !ok {
			return nil, fmt.Errorf("unsupported capability %q", capability)
		}
		if !seen[capability] {
			seen[capability] = true
			out = append(out, capability)
		}
	}
	sort.Strings(out)
	return out, nil
}

func CapabilityRouteTag(caps []string) (string, bool, error) {
	normalized, err := NormalizeCapabilities(caps)
	if err != nil {
		return "", false, err
	}
	if len(normalized) == 0 {
		return "", false, nil
	}
	if len(normalized) > 1 {
		return "", false, fmt.Errorf("capability combination %v is not supported", normalized)
	}
	return capabilityRouteTags[normalized[0]], true, nil
}

func EffectiveCapabilities(appCaps []string, actionCaps *[]string) []string {
	if actionCaps != nil {
		return *actionCaps
	}
	return appCaps
}

func EffectiveRouteTagWithCapabilities(appTag string, appTagOverride *string, actionTag *string, actionTagOverride *string, effectiveCaps []string) (string, error) {
	capabilityTag, ok, err := CapabilityRouteTag(effectiveCaps)
	if err != nil {
		return "", err
	}
	if ok {
		return capabilityTag, nil
	}
	return EffectiveRouteTag(appTag, appTagOverride, actionTag, actionTagOverride), nil
}

func EffectiveRouteTagForApp(deployment Deployment) string {
	tag, err := EffectiveRouteTagWithCapabilities(deployment.Tag, deployment.TagOverride, nil, nil, deployment.RequiredCapabilities)
	if err != nil {
		return EffectiveRouteTag(deployment.Tag, deployment.TagOverride, nil, nil)
	}
	return tag
}

func EffectiveRouteTagForAction(deployment Deployment, action Action) string {
	caps := EffectiveCapabilities(deployment.RequiredCapabilities, action.Capabilities)
	tag, err := EffectiveRouteTagWithCapabilities(deployment.Tag, deployment.TagOverride, action.Tag, action.TagOverride, caps)
	if err != nil {
		return EffectiveRouteTag(deployment.Tag, deployment.TagOverride, action.Tag, action.TagOverride)
	}
	return tag
}

func NormalizeWorkspace(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return DefaultWorkspace
	}
	return value
}

func NormalizeGitSourceID(value string, app string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	app = strings.TrimSpace(app)
	if app != "" {
		return app
	}
	return DefaultGitSourceID
}

func ValidAppKey(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 2 || len(value) > 64 || !utf8.ValidString(value) {
		return false
	}
	for index, item := range value {
		if index == 0 {
			if item < 'a' || item > 'z' {
				return false
			}
			continue
		}
		if item >= 'a' && item <= 'z' {
			continue
		}
		if item >= '0' && item <= '9' {
			continue
		}
		if item == '_' {
			continue
		}
		return false
	}
	return true
}

func ValidActionKey(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 || !utf8.ValidString(value) {
		return false
	}
	if strings.ContainsAny(value, `/\`) {
		return false
	}
	segments := strings.Split(value, ".")
	if len(segments) > 8 {
		return false
	}
	for _, segment := range segments {
		if segment == "" {
			return false
		}
		for index, item := range segment {
			if index == 0 {
				if item < 'a' || item > 'z' {
					return false
				}
				continue
			}
			if item >= 'a' && item <= 'z' {
				continue
			}
			if item >= '0' && item <= '9' {
				continue
			}
			if item == '_' {
				continue
			}
			return false
		}
	}
	return true
}

func NormalizeSourcePath(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = strings.Trim(value, "/")
	if value == "" || value == "." {
		return "", nil
	}
	clean := path.Clean(value)
	if clean == "." {
		return "", nil
	}
	if clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return "", fmt.Errorf("source path %q must be a relative path inside the git source", value)
	}
	return clean, nil
}

func (d Deployment) SourceWorkspace() string {
	return NormalizeWorkspace(d.Workspace)
}

func (d Deployment) SourceGitSourceID() string {
	return NormalizeGitSourceID(d.GitSourceID, d.App)
}

func (d Deployment) SourceObjectURI() string {
	return fmt.Sprintf("bundle://%s/%s/%s", d.SourceWorkspace(), d.SourceGitSourceID(), d.Commit)
}
