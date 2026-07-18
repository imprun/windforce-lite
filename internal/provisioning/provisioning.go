package provisioning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/imprun/windforce-core/internal/contract"
	"github.com/imprun/windforce-core/internal/gitsource"
	"github.com/imprun/windforce-core/internal/state"
	"gopkg.in/yaml.v3"
)

const APIVersion = "windforce-lite.imprun.dev/v1"

type Document struct {
	APIVersion string   `json:"apiVersion" yaml:"apiVersion"`
	Kind       string   `json:"kind" yaml:"kind"`
	Metadata   Metadata `json:"metadata" yaml:"metadata"`
	Spec       Spec     `json:"spec" yaml:"spec"`
	SourcePath string   `json:"-" yaml:"-"`
}

type Metadata struct {
	Name string            `json:"name" yaml:"name"`
	Tags map[string]string `json:"tags,omitempty" yaml:"tags,omitempty"`
}

type Spec struct {
	Name        string      `json:"name,omitempty" yaml:"name,omitempty"`
	AppKey      string      `json:"appKey,omitempty" yaml:"appKey,omitempty"`
	ActionKey   string      `json:"actionKey,omitempty" yaml:"actionKey,omitempty"`
	ClientRef   string      `json:"clientRef,omitempty" yaml:"clientRef,omitempty"`
	ClientKey   ValueSource `json:"clientKey,omitempty" yaml:"clientKey,omitempty"`
	ExternalKey ValueSource `json:"externalKey,omitempty" yaml:"externalKey,omitempty"`

	Method     string      `json:"method,omitempty" yaml:"method,omitempty"`
	StorageRef string      `json:"storageRef,omitempty" yaml:"storageRef,omitempty"`
	Username   ValueSource `json:"username,omitempty" yaml:"username,omitempty"`
	Password   ValueSource `json:"password,omitempty" yaml:"password,omitempty"`
	Token      ValueSource `json:"token,omitempty" yaml:"token,omitempty"`

	Path        string      `json:"path,omitempty" yaml:"path,omitempty"`
	AppScope    string      `json:"appScope,omitempty" yaml:"appScope,omitempty"`
	Value       ValueSource `json:"value,omitempty" yaml:"value,omitempty"`
	Secret      bool        `json:"secret,omitempty" yaml:"secret,omitempty"`
	Description string      `json:"description,omitempty" yaml:"description,omitempty"`

	Repository Repository     `json:"repository,omitempty" yaml:"repository,omitempty"`
	Config     map[string]any `json:"config,omitempty" yaml:"config,omitempty"`
	LockedKeys []string       `json:"lockedKeys,omitempty" yaml:"lockedKeys,omitempty"`
}

type Repository struct {
	URL           string `json:"url,omitempty" yaml:"url,omitempty"`
	Branch        string `json:"branch,omitempty" yaml:"branch,omitempty"`
	Subpath       string `json:"subpath,omitempty" yaml:"subpath,omitempty"`
	AuthRef       string `json:"authRef,omitempty" yaml:"authRef,omitempty"`
	CredentialRef string `json:"credentialRef,omitempty" yaml:"credentialRef,omitempty"`
}

type ValueSource struct {
	Value     any        `json:"value,omitempty" yaml:"value,omitempty"`
	ValueFrom *ValueFrom `json:"valueFrom,omitempty" yaml:"valueFrom,omitempty"`
	Redacted  bool       `json:"redacted,omitempty" yaml:"redacted,omitempty"`
}

type ValueFrom struct {
	Env  string `json:"env,omitempty" yaml:"env,omitempty"`
	File string `json:"file,omitempty" yaml:"file,omitempty"`
}

type Options struct {
	Workspace string
	Actor     string
	DryRun    bool
}

type Result struct {
	Applied []AppliedResource `json:"applied"`
}

type AppliedResource struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Action string `json:"action"`
	Detail string `json:"detail,omitempty"`
}

type GitSourceRegistry interface {
	Create(context.Context, gitsource.Source) (gitsource.Source, error)
	Get(context.Context, string, string) (gitsource.Source, error)
	Patch(context.Context, string, string, gitsource.Patch) (gitsource.Source, error)
	Load(context.Context) (gitsource.Snapshot, error)
}

type Service struct {
	Store      state.Store
	GitSources GitSourceRegistry
	Encrypt    func(context.Context, string, string) (string, error)
	AppKeys    []string
}

func LoadDir(dir string) ([]Document, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	paths := []string{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext == ".yaml" || ext == ".yml" || ext == ".json" {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(paths)
	docs := []Document{}
	for _, path := range paths {
		loaded, err := LoadFile(path)
		if err != nil {
			return nil, err
		}
		docs = append(docs, loaded...)
	}
	return docs, nil
}

func LoadFile(path string) ([]Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	docs, err := Decode(data, filepath.Ext(path))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for i := range docs {
		docs[i].SourcePath = path
	}
	return docs, nil
}

func Decode(data []byte, ext string) ([]Document, error) {
	ext = strings.ToLower(ext)
	var docs []Document
	var err error
	if ext == ".json" {
		docs, err = decodeJSON(data)
	} else {
		docs, err = decodeYAML(data)
	}
	if err != nil {
		return nil, err
	}
	for i := range docs {
		if err := normalizeDocument(&docs[i]); err != nil {
			return nil, err
		}
	}
	return docs, nil
}

func (s Service) Apply(ctx context.Context, docs []Document, options Options) (Result, error) {
	if s.Store == nil {
		return Result{}, errors.New("state store is required")
	}
	options.Workspace = contract.NormalizeWorkspace(options.Workspace)
	if options.Actor == "" {
		options.Actor = "provisioning"
	}
	result := Result{}
	credentials := map[string]string{}
	clients := map[string]state.Client{}

	for _, doc := range docs {
		if doc.Kind != "GitCredential" {
			continue
		}
		ref, credentialJSON, err := gitCredential(doc)
		if err != nil {
			return result, resourceError(doc, err)
		}
		credentials[doc.Metadata.Name] = ref
		if credentialJSON == "" || options.DryRun {
			result.Applied = append(result.Applied, AppliedResource{Kind: doc.Kind, Name: doc.Metadata.Name, Action: dryRunAction(options, "validated"), Detail: ref})
			continue
		}
		value := credentialJSON
		if s.Encrypt != nil {
			encrypted, err := s.Encrypt(ctx, options.Workspace, credentialJSON)
			if err != nil {
				return result, resourceError(doc, err)
			}
			value = encrypted
		}
		if err := s.Store.SetVariable(ctx, options.Workspace, "", ref, value, true, "Git credential managed by provisioning"); err != nil {
			return result, resourceError(doc, err)
		}
		result.Applied = append(result.Applied, AppliedResource{Kind: doc.Kind, Name: doc.Metadata.Name, Action: "stored", Detail: ref})
	}

	for _, doc := range docs {
		switch doc.Kind {
		case "GitCredential":
			continue
		case "Client":
			client, action, err := s.applyClient(ctx, doc, options)
			if err != nil {
				return result, resourceError(doc, err)
			}
			clients[doc.Metadata.Name] = client
			result.Applied = append(result.Applied, AppliedResource{Kind: doc.Kind, Name: doc.Metadata.Name, Action: action, Detail: client.ID})
		case "Variable":
			action, detail, err := s.applyVariable(ctx, doc, options)
			if err != nil {
				return result, resourceError(doc, err)
			}
			result.Applied = append(result.Applied, AppliedResource{Kind: doc.Kind, Name: doc.Metadata.Name, Action: action, Detail: detail})
		case "AppSource":
			action, detail, err := s.applyAppSource(ctx, doc, options, credentials)
			if err != nil {
				return result, resourceError(doc, err)
			}
			result.Applied = append(result.Applied, AppliedResource{Kind: doc.Kind, Name: doc.Metadata.Name, Action: action, Detail: detail})
		case "InputSettings":
			action, detail, err := s.applyInputSettings(ctx, doc, options, clients)
			if err != nil {
				return result, resourceError(doc, err)
			}
			result.Applied = append(result.Applied, AppliedResource{Kind: doc.Kind, Name: doc.Metadata.Name, Action: action, Detail: detail})
		default:
			return result, resourceError(doc, fmt.Errorf("unsupported kind %q", doc.Kind))
		}
	}
	return result, nil
}

func (s Service) Export(ctx context.Context, workspace string, includeValues bool) ([]Document, error) {
	workspace = contract.NormalizeWorkspace(workspace)
	docs := []Document{}
	if s.GitSources != nil {
		snapshot, err := s.GitSources.Load(ctx)
		if err != nil {
			return nil, err
		}
		sources := make([]gitsource.Source, 0, len(snapshot.Sources))
		for _, source := range snapshot.Sources {
			if contract.NormalizeWorkspace(source.Workspace) == workspace {
				sources = append(sources, source)
			}
		}
		sort.Slice(sources, func(i, j int) bool { return sources[i].Name < sources[j].Name })
		for _, source := range sources {
			doc := Document{
				APIVersion: APIVersion,
				Kind:       "AppSource",
				Metadata:   Metadata{Name: source.Name},
				Spec: Spec{
					Name: source.Name,
					Repository: Repository{
						URL:           source.RepoURL,
						Branch:        source.Branch,
						Subpath:       source.Subpath,
						CredentialRef: source.TokenEnv,
					},
				},
			}
			docs = append(docs, doc)
		}
	}
	clients, err := s.Store.ListClients(ctx, workspace)
	if err != nil {
		return nil, err
	}
	for _, client := range clients {
		externalKey := ValueSource{Redacted: true}
		if includeValues {
			externalKey.Value = client.ExternalKey
			externalKey.Redacted = false
		}
		docs = append(docs, Document{
			APIVersion: APIVersion,
			Kind:       "Client",
			Metadata:   Metadata{Name: client.Name},
			Spec:       Spec{Name: client.Name, ExternalKey: externalKey},
		})
	}
	variables, err := s.Store.ListVariables(ctx, workspace)
	if err != nil {
		return nil, err
	}
	sort.Slice(variables, func(i, j int) bool {
		if variables[i].AppKey != variables[j].AppKey {
			return variables[i].AppKey < variables[j].AppKey
		}
		return variables[i].Path < variables[j].Path
	})
	for _, variable := range variables {
		value := ValueSource{Redacted: true}
		if includeValues && !variable.IsSecret {
			value = ValueSource{Value: variable.Value}
		}
		docs = append(docs, Document{
			APIVersion: APIVersion,
			Kind:       "Variable",
			Metadata:   Metadata{Name: resourceName(variable.AppKey, variable.Path)},
			Spec: Spec{
				Path:        variable.Path,
				AppScope:    variable.AppKey,
				Value:       value,
				Secret:      variable.IsSecret,
				Description: variable.Description,
			},
		})
	}
	inputDocs, err := s.exportInputSettings(ctx, workspace, includeValues)
	if err != nil {
		return nil, err
	}
	docs = append(docs, inputDocs...)
	return docs, nil
}

func (s Service) exportInputSettings(ctx context.Context, workspace string, includeValues bool) ([]Document, error) {
	seen := map[string]state.InputConfig{}
	for _, appKey := range s.AppKeys {
		configs, err := s.Store.ListInputConfigsForApp(ctx, workspace, appKey)
		if err != nil {
			return nil, err
		}
		for _, config := range configs {
			seen[inputConfigKeyForExport(config)] = config
		}
	}
	clients, err := s.Store.ListClients(ctx, workspace)
	if err != nil {
		return nil, err
	}
	for _, client := range clients {
		configs, err := s.Store.ListInputConfigsForClient(ctx, workspace, client.ID)
		if err != nil {
			return nil, err
		}
		for _, config := range configs {
			seen[inputConfigKeyForExport(config)] = config
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	docs := []Document{}
	for _, key := range keys {
		config := seen[key]
		values := map[string]any{}
		if includeValues {
			var decoded map[string]any
			_ = json.Unmarshal(config.Config, &decoded)
			values = decoded
		} else {
			var decoded map[string]json.RawMessage
			_ = json.Unmarshal(config.Config, &decoded)
			for configKey := range decoded {
				values[configKey] = ValueSource{Redacted: true}
			}
		}
		docs = append(docs, Document{
			APIVersion: APIVersion,
			Kind:       "InputSettings",
			Metadata:   Metadata{Name: resourceName(config.AppKey, inputDetail(config.AppKey, config.ActionKey, config.ClientID))},
			Spec: Spec{
				AppKey:     config.AppKey,
				ActionKey:  config.ActionKey,
				ClientRef:  config.ClientID,
				Config:     values,
				LockedKeys: append([]string(nil), config.LockedKeys...),
			},
		})
	}
	return docs, nil
}

func EncodeYAML(docs []Document) ([]byte, error) {
	data, err := yaml.Marshal(docs)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func (s Service) applyClient(ctx context.Context, doc Document, options Options) (state.Client, string, error) {
	name := firstNonEmpty(doc.Spec.Name, doc.Metadata.Name)
	externalKey, err := resolveString(doc.Spec.ExternalKey, doc.Spec.ClientKey)
	if err != nil {
		return state.Client{}, "", err
	}
	if name == "" || externalKey == "" {
		return state.Client{}, "", errors.New("client name and externalKey are required")
	}
	if options.DryRun {
		return state.Client{ID: stableID("client", externalKey), Name: name, ExternalKey: externalKey}, "validated", nil
	}
	client, err := s.Store.GetClientByExternalKey(ctx, options.Workspace, externalKey)
	if err == nil {
		if client.Name == name {
			return client, "unchanged", nil
		}
		updated, err := s.Store.UpdateClient(ctx, options.Workspace, client.ID, name, externalKey, options.Actor)
		return updated, "updated", err
	}
	if !errors.Is(err, state.ErrNotFound) {
		return state.Client{}, "", err
	}
	created, err := s.Store.CreateClient(ctx, options.Workspace, name, externalKey, options.Actor)
	return created, "created", err
}

func (s Service) applyVariable(ctx context.Context, doc Document, options Options) (string, string, error) {
	path := strings.TrimSpace(doc.Spec.Path)
	if path == "" {
		path = strings.TrimSpace(doc.Metadata.Name)
	}
	value, err := resolveString(doc.Spec.Value)
	if err != nil {
		return "", "", err
	}
	if path == "" {
		return "", "", errors.New("variable path is required")
	}
	if options.DryRun {
		return "validated", path, nil
	}
	if doc.Spec.Secret && s.Encrypt != nil {
		value, err = s.Encrypt(ctx, options.Workspace, value)
		if err != nil {
			return "", "", err
		}
	}
	if err := s.Store.SetVariable(ctx, options.Workspace, doc.Spec.AppScope, path, value, doc.Spec.Secret, doc.Spec.Description); err != nil {
		return "", "", err
	}
	return "stored", path, nil
}

func (s Service) applyAppSource(ctx context.Context, doc Document, options Options, credentials map[string]string) (string, string, error) {
	if s.GitSources == nil {
		return "", "", errors.New("git source registry is required")
	}
	name := firstNonEmpty(doc.Spec.Name, doc.Metadata.Name)
	repo := doc.Spec.Repository
	branch := firstNonEmpty(repo.Branch, "main")
	credsRef := strings.TrimSpace(repo.CredentialRef)
	if credsRef == "" && repo.AuthRef != "" {
		credsRef = credentials[repo.AuthRef]
	}
	if name == "" || strings.TrimSpace(repo.URL) == "" {
		return "", "", errors.New("source name and repository.url are required")
	}
	source := gitsource.Source{
		Workspace: options.Workspace,
		Name:      name,
		RepoURL:   strings.TrimSpace(repo.URL),
		Branch:    branch,
		Subpath:   strings.TrimSpace(repo.Subpath),
		TokenEnv:  credsRef,
		Kind:      "external",
	}
	if options.DryRun {
		return "validated", name, nil
	}
	existing, err := s.GitSources.Get(ctx, options.Workspace, name)
	if err == nil {
		updated, err := s.GitSources.Patch(ctx, options.Workspace, existing.ID, gitsource.Patch{
			Name:     stringPtr(name),
			RepoURL:  stringPtr(source.RepoURL),
			Branch:   stringPtr(source.Branch),
			Subpath:  stringPtr(source.Subpath),
			TokenEnv: stringPtr(source.TokenEnv),
		})
		if err != nil {
			return "", "", err
		}
		return "updated", updated.ID, nil
	}
	if !errors.Is(err, gitsource.ErrGitSourceNotFound) {
		return "", "", err
	}
	created, err := s.GitSources.Create(ctx, source)
	if err != nil {
		return "", "", err
	}
	return "created", created.ID, nil
}

func (s Service) applyInputSettings(ctx context.Context, doc Document, options Options, clients map[string]state.Client) (string, string, error) {
	appKey := strings.TrimSpace(doc.Spec.AppKey)
	if appKey == "" {
		return "", "", errors.New("appKey is required")
	}
	clientID := ""
	if ref := strings.TrimSpace(doc.Spec.ClientRef); ref != "" {
		if client, ok := clients[ref]; ok {
			clientID = client.ID
		} else {
			client, err := s.Store.GetClient(ctx, options.Workspace, ref)
			if err != nil {
				return "", "", fmt.Errorf("clientRef %q was not found", ref)
			}
			clientID = client.ID
		}
	}
	values, err := resolveConfig(doc.Spec.Config)
	if err != nil {
		return "", "", err
	}
	configJSON, err := json.Marshal(values)
	if err != nil {
		return "", "", err
	}
	if options.DryRun {
		return "validated", inputDetail(appKey, doc.Spec.ActionKey, clientID), nil
	}
	_, err = s.Store.SetInputConfig(ctx, state.InputConfig{
		WorkspaceID: options.Workspace,
		AppKey:      appKey,
		ActionKey:   strings.TrimSpace(doc.Spec.ActionKey),
		ClientID:    clientID,
		Config:      configJSON,
		LockedKeys:  doc.Spec.LockedKeys,
	}, options.Actor)
	return "stored", inputDetail(appKey, doc.Spec.ActionKey, clientID), err
}

func decodeJSON(data []byte) ([]Document, error) {
	var list []Document
	if err := json.Unmarshal(data, &list); err == nil {
		return list, nil
	}
	var envelope struct {
		Resources []Document `json:"resources"`
	}
	if err := json.Unmarshal(data, &envelope); err == nil && envelope.Resources != nil {
		return envelope.Resources, nil
	}
	var single Document
	if err := json.Unmarshal(data, &single); err != nil {
		return nil, err
	}
	return []Document{single}, nil
}

func decodeYAML(data []byte) ([]Document, error) {
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, err
	}
	if len(node.Content) == 0 {
		return nil, nil
	}
	root := node.Content[0]
	var docs []Document
	if root.Kind == yaml.SequenceNode {
		if err := root.Decode(&docs); err != nil {
			return nil, err
		}
		return docs, nil
	}
	var envelope struct {
		Resources []Document `yaml:"resources"`
	}
	if err := root.Decode(&envelope); err == nil && envelope.Resources != nil {
		return envelope.Resources, nil
	}
	var single Document
	if err := root.Decode(&single); err != nil {
		return nil, err
	}
	return []Document{single}, nil
}

func normalizeDocument(doc *Document) error {
	doc.APIVersion = strings.TrimSpace(doc.APIVersion)
	doc.Kind = strings.TrimSpace(doc.Kind)
	doc.Metadata.Name = strings.TrimSpace(doc.Metadata.Name)
	if doc.APIVersion == "" {
		doc.APIVersion = APIVersion
	}
	if doc.APIVersion != APIVersion {
		return fmt.Errorf("unsupported apiVersion %q", doc.APIVersion)
	}
	if doc.Kind == "" {
		return errors.New("kind is required")
	}
	if doc.Metadata.Name == "" {
		return errors.New("metadata.name is required")
	}
	return nil
}

func gitCredential(doc Document) (string, string, error) {
	ref := strings.TrimSpace(doc.Spec.StorageRef)
	if ref == "" {
		ref = "git/" + doc.Metadata.Name + "/credential"
	}
	method := strings.ToLower(strings.TrimSpace(doc.Spec.Method))
	if method == "" {
		method = "pat"
	}
	switch method {
	case "none", "public":
		return ref, "", nil
	case "pat", "token", "access_token":
		token, err := resolveString(doc.Spec.Token, doc.Spec.Value)
		if err != nil {
			return "", "", err
		}
		if token == "" {
			return "", "", errors.New("token is required")
		}
		value, err := marshalStringMap(map[string]string{"type": "pat", "token": token})
		return ref, value, err
	case "basic", "password":
		username, err := resolveString(doc.Spec.Username)
		if err != nil {
			return "", "", err
		}
		password, err := resolveString(doc.Spec.Password, doc.Spec.Token)
		if err != nil {
			return "", "", err
		}
		if username == "" || password == "" {
			return "", "", errors.New("username and password are required")
		}
		value, err := marshalStringMap(map[string]string{"type": "basic", "username": username, "password": password})
		return ref, value, err
	default:
		return "", "", fmt.Errorf("unsupported credential method %q", doc.Spec.Method)
	}
}

func resolveConfig(values map[string]any) (map[string]json.RawMessage, error) {
	resolved := map[string]json.RawMessage{}
	for key, raw := range values {
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, errors.New("input setting key must not be empty")
		}
		value, err := resolveAny(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		data, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		resolved[key] = data
	}
	return resolved, nil
}

func resolveString(sources ...ValueSource) (string, error) {
	for _, source := range sources {
		value, err := source.Resolve()
		if err != nil {
			return "", err
		}
		if value != "" {
			return value, nil
		}
	}
	return "", nil
}

func (source ValueSource) Resolve() (string, error) {
	if source.Redacted {
		return "", errors.New("redacted value cannot be applied; provide valueFrom.env or valueFrom.file")
	}
	if source.ValueFrom != nil {
		if source.ValueFrom.Env != "" {
			value, ok := os.LookupEnv(source.ValueFrom.Env)
			if !ok {
				return "", fmt.Errorf("environment variable %s is not set", source.ValueFrom.Env)
			}
			return value, nil
		}
		if source.ValueFrom.File != "" {
			data, err := os.ReadFile(source.ValueFrom.File)
			if err != nil {
				return "", err
			}
			return strings.TrimRight(string(data), "\r\n"), nil
		}
	}
	if source.Value == nil {
		return "", nil
	}
	switch value := source.Value.(type) {
	case string:
		return value, nil
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
}

func resolveAny(raw any) (any, error) {
	if m, ok := raw.(map[string]any); ok {
		if _, hasValue := m["value"]; hasValue || m["valueFrom"] != nil || m["redacted"] != nil {
			source := ValueSource{}
			data, _ := json.Marshal(m)
			if err := json.Unmarshal(data, &source); err != nil {
				return nil, err
			}
			value, err := source.Resolve()
			if err != nil {
				return nil, err
			}
			if source.Value != nil {
				return source.Value, nil
			}
			return value, nil
		}
	}
	return raw, nil
}

func marshalStringMap(value map[string]string) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func dryRunAction(options Options, action string) string {
	if options.DryRun {
		return "validated"
	}
	return action
}

func resourceError(doc Document, err error) error {
	location := doc.Metadata.Name
	if doc.SourcePath != "" {
		location = doc.SourcePath + ":" + location
	}
	return fmt.Errorf("%s %s: %w", doc.Kind, location, err)
}

func inputDetail(appKey string, actionKey string, clientID string) string {
	parts := []string{appKey}
	if actionKey != "" {
		parts = append(parts, actionKey)
	}
	if clientID != "" {
		parts = append(parts, "client="+clientID)
	}
	return strings.Join(parts, "/")
}

func inputConfigKeyForExport(config state.InputConfig) string {
	return config.AppKey + "\x00" + config.ActionKey + "\x00" + config.ClientID
}

func resourceName(appKey string, path string) string {
	value := strings.Trim(strings.ReplaceAll(appKey+"-"+path, "/", "-"), "-")
	if value == "" {
		value = "variable"
	}
	return value
}

func stableID(prefix string, value string) string {
	sum := sha256.Sum256([]byte(value))
	return prefix + "_" + hex.EncodeToString(sum[:6])
}

func stringPtr(value string) *string {
	return &value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
