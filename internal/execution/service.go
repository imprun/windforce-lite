package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/imprun/windforce-core/internal/contract"
	"github.com/imprun/windforce-core/internal/state"
)

type Catalog interface {
	GetDeployment(ctx context.Context, app string) (contract.Deployment, error)
}

type Store interface {
	CreateRunAndEnqueue(ctx context.Context, run state.Run, job state.Job) error
	GetRun(ctx context.Context, runID string) (state.Run, error)
	CancelRun(ctx context.Context, runID string, reason string) (state.Run, error)
	GetClientByExternalKey(ctx context.Context, workspaceID string, externalKey string) (state.Client, error)
	ResolveInput(ctx context.Context, workspaceID string, appKey string, actionKey string, clientID string, request json.RawMessage) (json.RawMessage, error)
}

type FaultKind string

const (
	FaultUnavailable     FaultKind = "unavailable"
	FaultInvalidRequest  FaultKind = "invalid_request"
	FaultAppNotFound     FaultKind = "app_not_found"
	FaultActionNotFound  FaultKind = "action_not_found"
	FaultRoutingConflict FaultKind = "routing_conflict"
	FaultConflict        FaultKind = "conflict"
	FaultInternal        FaultKind = "internal"
)

type Fault struct {
	Kind    FaultKind
	Message string
	Err     error
}

func (e *Fault) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return string(e.Kind)
}

func (e *Fault) Unwrap() error { return e.Err }

func FaultKindOf(err error) FaultKind {
	var fault *Fault
	if errors.As(err, &fault) {
		return fault.Kind
	}
	return FaultInternal
}

type Service struct {
	store   Store
	catalog Catalog
	bundles BundleStore
}

func NewService(store Store, catalog Catalog, bundles BundleStore) *Service {
	return &Service{store: store, catalog: catalog, bundles: bundles}
}

type CreateRunRequest struct {
	Workspace      string
	App            string
	Action         string
	Input          json.RawMessage
	Adapter        string
	TriggerKind    string
	TriggerHeaders json.RawMessage
	CorrelationID  string
	IdempotencyKey string
	Env            []string
	ClientKey      string
	CreatedBy      string
	PermissionedAs string
}

type Admission struct {
	Run      state.Run
	Job      state.Job
	Replayed bool
}

type ActionDescription struct {
	Spec         contract.Action `json:"spec"`
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema"`
}

type AppDescription struct {
	Deployment contract.Deployment          `json:"deployment"`
	Actions    map[string]ActionDescription `json:"actions"`
}

func (s *Service) CreateRun(ctx context.Context, request CreateRunRequest) (Admission, error) {
	if s == nil || s.store == nil || s.catalog == nil {
		return Admission{}, &Fault{Kind: FaultUnavailable, Message: "execution service is not configured"}
	}
	request.Workspace = contract.NormalizeWorkspace(request.Workspace)
	request.App = strings.TrimSpace(request.App)
	request.Action = strings.TrimSpace(request.Action)
	if !contract.ValidAppKey(request.App) || !contract.ValidActionKey(request.Action) {
		return Admission{}, &Fault{Kind: FaultInvalidRequest, Message: "invalid app/action key"}
	}
	if len(request.Input) == 0 {
		request.Input = json.RawMessage([]byte("{}"))
	}
	if !json.Valid(request.Input) {
		return Admission{}, &Fault{Kind: FaultInvalidRequest, Message: "input must be valid JSON"}
	}
	deployment, err := s.lookupDeployment(ctx, request.Workspace, request.App)
	if err != nil {
		return Admission{}, &Fault{Kind: FaultAppNotFound, Message: "app not found: " + request.App, Err: err}
	}
	actionSpec, ok := deployment.Actions[request.Action]
	if !ok {
		return Admission{}, &Fault{Kind: FaultActionNotFound, Message: "action not found: " + request.App + "/" + request.Action}
	}
	if strings.TrimSpace(deployment.BundleDigest) == "" {
		return Admission{}, &Fault{
			Kind:    FaultUnavailable,
			Message: "active release has no execution bundle; publish the synchronized source again",
		}
	}
	clientID := ""
	if clientKey := strings.TrimSpace(request.ClientKey); clientKey != "" {
		client, err := s.store.GetClientByExternalKey(ctx, request.Workspace, clientKey)
		if err != nil {
			if errors.Is(err, state.ErrNotFound) {
				return Admission{}, &Fault{Kind: FaultInvalidRequest, Message: "unknown client key"}
			}
			return Admission{}, &Fault{Kind: FaultInternal, Message: "could not resolve client", Err: err}
		}
		clientID = client.ID
	}
	if _, err := s.store.ResolveInput(ctx, request.Workspace, request.App, request.Action, clientID, request.Input); err != nil {
		var locked *state.LockedKeysError
		if errors.As(err, &locked) {
			return Admission{}, &Fault{Kind: FaultInvalidRequest, Message: locked.Error()}
		}
		return Admission{}, &Fault{Kind: FaultInternal, Message: "could not validate input settings", Err: err}
	}

	runID := ""
	if key := strings.TrimSpace(request.IdempotencyKey); key != "" {
		if clientID != "" {
			key += "\x00client:" + clientID
		}
		runID = deterministicRunID(request.Workspace, request.App, request.Action, key)
	}
	adapter := strings.TrimSpace(request.Adapter)
	if adapter == "" {
		adapter = "http"
	}
	run := state.NewRun(adapter, runID, request.App, request.Action, deployment, cloneRaw(request.Input))
	run.CorrelationID = state.CleanID(request.CorrelationID)
	run.Env = cloneStrings(request.Env)
	run.CreatedBy = strings.TrimSpace(request.CreatedBy)
	run.PermissionedAs = strings.TrimSpace(request.PermissionedAs)
	run.ClientID = clientID
	job := state.NewActionJob(run, cloneRaw(request.Input))
	job.Payload.TriggerKind = strings.TrimSpace(request.TriggerKind)
	if job.Payload.TriggerKind == "" {
		job.Payload.TriggerKind = adapter
	}
	job.Payload.TriggerHeaders = cloneRaw(request.TriggerHeaders)

	reader := NewSchemaReader(ctx, s.bundles, deployment)
	defer reader.Close()
	job.Payload.InputSchema, err = reader.Read(actionSpec.InputSchema, actionSpec.InputSchemaBody)
	if err != nil {
		return Admission{}, &Fault{Kind: FaultInternal, Message: fmt.Sprintf("input schema for %s/%s: %v", request.App, request.Action, err), Err: err}
	}
	job.Payload.OutputSchema, err = reader.Read(actionSpec.OutputSchema, actionSpec.OutputSchemaBody)
	if err != nil {
		return Admission{}, &Fault{Kind: FaultInternal, Message: fmt.Sprintf("output schema for %s/%s: %v", request.App, request.Action, err), Err: err}
	}
	if err := s.store.CreateRunAndEnqueue(ctx, run, job); err != nil {
		if errors.Is(err, state.ErrConflict) && runID != "" {
			existing, getErr := s.GetRun(ctx, request.Workspace, runID)
			if getErr == nil {
				return Admission{Run: existing, Replayed: true}, nil
			}
			return Admission{}, &Fault{Kind: FaultConflict, Message: "idempotent run already exists", Err: err}
		}
		kind := FaultInternal
		if errors.Is(err, state.ErrConflict) {
			kind = FaultConflict
		}
		return Admission{}, &Fault{Kind: kind, Err: err}
	}
	return Admission{Run: run, Job: job}, nil
}

func (s *Service) GetRun(ctx context.Context, workspace string, runID string) (state.Run, error) {
	if s == nil || s.store == nil {
		return state.Run{}, &Fault{Kind: FaultUnavailable, Message: "execution service is not configured"}
	}
	run, err := s.store.GetRun(ctx, strings.TrimSpace(runID))
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return state.Run{}, &Fault{Kind: FaultAppNotFound, Message: "run not found", Err: err}
		}
		return state.Run{}, &Fault{Kind: FaultInternal, Err: err}
	}
	if contract.NormalizeWorkspace(run.Deployment.SourceWorkspace()) != contract.NormalizeWorkspace(workspace) {
		return state.Run{}, &Fault{Kind: FaultAppNotFound, Message: "run not found"}
	}
	return run, nil
}

func (s *Service) CancelRun(ctx context.Context, workspace string, runID string, reason string) (state.Run, error) {
	if _, err := s.GetRun(ctx, workspace, runID); err != nil {
		return state.Run{}, err
	}
	run, err := s.store.CancelRun(ctx, strings.TrimSpace(runID), strings.TrimSpace(reason))
	if err != nil {
		return state.Run{}, &Fault{Kind: FaultInternal, Err: err}
	}
	return run, nil
}

func (s *Service) DescribeApp(ctx context.Context, workspace string, app string) (AppDescription, error) {
	if s == nil || s.catalog == nil {
		return AppDescription{}, &Fault{Kind: FaultUnavailable, Message: "execution service is not configured"}
	}
	app = strings.TrimSpace(app)
	if !contract.ValidAppKey(app) {
		return AppDescription{}, &Fault{Kind: FaultInvalidRequest, Message: "invalid app key"}
	}
	deployment, err := s.lookupDeployment(ctx, contract.NormalizeWorkspace(workspace), app)
	if err != nil {
		return AppDescription{}, &Fault{Kind: FaultAppNotFound, Message: "app not found: " + app, Err: err}
	}
	reader := NewSchemaReader(ctx, s.bundles, deployment)
	defer reader.Close()
	actions := make(map[string]ActionDescription, len(deployment.Actions))
	for key, spec := range deployment.Actions {
		inputSchema, readErr := reader.Read(spec.InputSchema, spec.InputSchemaBody)
		if readErr != nil {
			return AppDescription{}, &Fault{Kind: FaultInternal, Message: fmt.Sprintf("input schema for %s/%s: %v", app, key, readErr), Err: readErr}
		}
		outputSchema, readErr := reader.Read(spec.OutputSchema, spec.OutputSchemaBody)
		if readErr != nil {
			return AppDescription{}, &Fault{Kind: FaultInternal, Message: fmt.Sprintf("output schema for %s/%s: %v", app, key, readErr), Err: readErr}
		}
		actions[key] = ActionDescription{Spec: spec, InputSchema: inputSchema, OutputSchema: outputSchema}
	}
	return AppDescription{Deployment: deployment, Actions: actions}, nil
}

func (s *Service) lookupDeployment(ctx context.Context, workspace string, app string) (contract.Deployment, error) {
	if scoped, ok := s.catalog.(interface {
		GetDeploymentForWorkspace(context.Context, string, string) (contract.Deployment, error)
	}); ok {
		return scoped.GetDeploymentForWorkspace(ctx, workspace, app)
	}
	deployment, err := s.catalog.GetDeployment(ctx, app)
	if err != nil {
		return contract.Deployment{}, err
	}
	if contract.NormalizeWorkspace(deployment.SourceWorkspace()) != contract.NormalizeWorkspace(workspace) {
		return contract.Deployment{}, state.ErrNotFound
	}
	return deployment, nil
}

func deterministicRunID(workspace string, app string, action string, key string) string {
	digest := sha256.Sum256([]byte(workspace + "\x00" + app + "\x00" + action + "\x00" + key))
	return "run_" + hex.EncodeToString(digest[:12])
}

func cloneRaw(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}

func cloneStrings(values []string) []string {
	return append([]string(nil), values...)
}
