package contract

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	DefaultWorkspace   = "default"
	DefaultGitSourceID = "local"
)

// App is the deployable source bundle described by windforce.json.
type App struct {
	App     string            `json:"app"`
	Name    string            `json:"name,omitempty"`
	Actions map[string]Action `json:"actions"`
}

// Action is one executable unit inside an app.
type Action struct {
	Action       string   `json:"action"`
	Runtime      string   `json:"runtime,omitempty"`
	Entrypoint   string   `json:"entrypoint,omitempty"`
	Command      []string `json:"command,omitempty"`
	InputSchema  string   `json:"inputSchema,omitempty"`
	OutputSchema string   `json:"outputSchema,omitempty"`
	TimeoutMs    int64    `json:"timeoutMs,omitempty"`
}

// Deployment is the active source bundle selected by the catalog.
type Deployment struct {
	Workspace    string            `json:"workspace,omitempty"`
	GitSourceID  string            `json:"gitSourceId,omitempty"`
	App          string            `json:"app"`
	Version      string            `json:"version,omitempty"`
	Commit       string            `json:"commit"`
	BundleDigest string            `json:"bundleDigest,omitempty"`
	ObjectURI    string            `json:"objectUri"`
	Actions      map[string]Action `json:"actions"`
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

func (d Deployment) SourceWorkspace() string {
	return NormalizeWorkspace(d.Workspace)
}

func (d Deployment) SourceGitSourceID() string {
	return NormalizeGitSourceID(d.GitSourceID, d.App)
}

func (d Deployment) SourceObjectURI() string {
	return fmt.Sprintf("bundle://%s/%s/%s", d.SourceWorkspace(), d.SourceGitSourceID(), d.Commit)
}
