package controlcli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/imprun/windforce-core/pkg/controlplane"
)

const (
	ExitOK        = 0
	ExitUsage     = 2
	ExitConfig    = 3
	ExitTransport = 10
	ExitAPIClient = 20
	ExitAPIServer = 21
)

var Version = "dev"

type runner struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	pretty bool
	client *controlplane.Client
}

type usageError struct{ message string }

func (e usageError) Error() string { return e.message }

func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	global := flag.NewFlagSet("windforce", flag.ContinueOnError)
	global.SetOutput(stderr)
	global.Usage = func() {}
	var profileName string
	var overrides Profile
	var pretty bool
	var timeout time.Duration
	global.StringVar(&profileName, "profile", "", "connection profile")
	global.StringVar(&overrides.APIURL, "api-url", "", "control-plane API base URL")
	global.StringVar(&overrides.Workspace, "workspace", "", "workspace id")
	global.StringVar(&overrides.Actor, "actor", "", "actor sent as X-Windforce-Actor")
	global.StringVar(&overrides.TokenEnv, "token-env", "", "environment variable containing the bearer token")
	global.BoolVar(&pretty, "pretty", false, "pretty-print JSON")
	global.DurationVar(&timeout, "request-timeout", 60*time.Second, "HTTP request timeout")
	if err := global.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(stdout)
			return ExitOK
		}
		return ExitUsage
	}
	remaining := global.Args()
	if len(remaining) == 0 {
		printUsage(stderr)
		return ExitUsage
	}

	path, err := configPath()
	if err != nil {
		writeError(stderr, err)
		return ExitConfig
	}
	config, err := loadConfig(path)
	if err != nil {
		writeError(stderr, err)
		return ExitConfig
	}
	r := &runner{stdin: stdin, stdout: stdout, stderr: stderr, pretty: pretty}
	if remaining[0] == "profile" {
		if err := r.profile(path, config, remaining[1:]); err != nil {
			var usage usageError
			if errors.As(err, &usage) {
				return r.finishError(err)
			}
			writeError(stderr, err)
			return ExitConfig
		}
		return ExitOK
	}
	resolved, err := resolveProfile(config, profileName, overrides)
	if err != nil {
		writeError(stderr, err)
		return ExitConfig
	}
	r.client = &controlplane.Client{
		BaseURL: resolved.APIURL, Workspace: resolved.Workspace, Actor: resolved.Actor,
		Token: resolved.Token, HTTP: &http.Client{Timeout: timeout},
	}
	if err := r.command(remaining); err != nil {
		return r.finishError(err)
	}
	return ExitOK
}

func (r *runner) command(args []string) error {
	switch args[0] {
	case "source":
		return r.source(args[1:])
	case "app":
		return r.app(args[1:])
	case "action":
		return r.action(args[1:])
	case "job":
		return r.job(args[1:])
	case "provisioning":
		return r.provisioning(args[1:])
	case "openapi":
		return r.json(http.MethodGet, r.client.WorkspacePath("openapi.json"), nil)
	case "version":
		_, err := fmt.Fprintln(r.stdout, Version)
		return err
	case "help", "--help", "-h":
		printUsage(r.stdout)
		return nil
	default:
		return usageError{fmt.Sprintf("unknown command %q", args[0])}
	}
}

func (r *runner) json(method, path string, body any) error {
	result, err := r.client.DoJSON(context.Background(), method, path, body)
	if err != nil {
		return err
	}
	return r.outputJSON(result)
}

func (r *runner) jsonDiscard(method, path string, body any) error {
	_, err := r.client.DoJSON(context.Background(), method, path, body)
	return err
}

func (r *runner) raw(method, path, contentType string, body []byte) error {
	result, _, err := r.client.DoRaw(context.Background(), method, path, contentType, body)
	if err != nil {
		return err
	}
	_, err = r.stdout.Write(result)
	return err
}

func (r *runner) outputJSON(value any) error {
	var data []byte
	var err error
	if raw, ok := value.(json.RawMessage); ok {
		if r.pretty {
			var decoded any
			if err = json.Unmarshal(raw, &decoded); err == nil {
				data, err = json.MarshalIndent(decoded, "", "  ")
			}
		} else {
			data = raw
		}
	} else if r.pretty {
		data, err = json.MarshalIndent(value, "", "  ")
	} else {
		data, err = json.Marshal(value)
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(r.stdout, string(data))
	return err
}

func (r *runner) finishError(err error) int {
	var apiErr *controlplane.APIError
	var usage usageError
	switch {
	case errors.As(err, &apiErr):
		if json.Valid(apiErr.Body) {
			_ = r.outputErrorJSON(apiErr.Body)
		} else {
			writeError(r.stderr, err)
		}
		if apiErr.StatusCode >= 500 {
			return ExitAPIServer
		}
		return ExitAPIClient
	case errors.As(err, &usage):
		writeError(r.stderr, err)
		return ExitUsage
	default:
		writeError(r.stderr, err)
		return ExitTransport
	}
}

func (r *runner) outputErrorJSON(data []byte) error {
	if r.pretty {
		var value any
		if err := json.Unmarshal(data, &value); err == nil {
			data, _ = json.MarshalIndent(value, "", "  ")
		}
	}
	_, err := fmt.Fprintln(r.stderr, string(data))
	return err
}

func (r *runner) flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(r.stderr)
	return fs
}

func (r *runner) readJSON(inline, file string) (any, error) {
	data := []byte(inline)
	var err error
	if file != "" {
		data, err = r.readFile(file)
		if err != nil {
			return nil, err
		}
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("invalid JSON input: %w", err)
	}
	return value, nil
}

func (r *runner) readFile(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(r.stdin)
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return data, nil
}

func gitCredential(method, username, passwordEnv, tokenEnv string) (map[string]any, string, error) {
	password, token := "", ""
	if passwordEnv != "" {
		password = os.Getenv(passwordEnv)
	}
	if tokenEnv != "" {
		token = os.Getenv(tokenEnv)
	}
	if method == "" {
		if username != "" || password != "" {
			method = "basic"
		} else if token != "" {
			method = "pat"
		}
	}
	switch method {
	case "":
		return map[string]any{}, "", nil
	case "pat":
		if token == "" {
			return nil, "", fmt.Errorf("environment variable %s is not set", tokenEnv)
		}
		value, _ := json.Marshal(map[string]string{"type": "pat", "token": token})
		return map[string]any{"auth_method": "pat", "access_token": token}, string(value), nil
	case "basic":
		if username == "" || password == "" {
			return nil, "", fmt.Errorf("--username and a populated --password-env are required for basic auth")
		}
		value, _ := json.Marshal(map[string]string{"type": "basic", "username": username, "password": password})
		return map[string]any{"auth_method": "basic", "username": username, "password": password}, string(value), nil
	default:
		return nil, "", usageError{"--auth-method must be pat or basic"}
	}
}

func defaultCredentialPath(name string) string {
	re := regexp.MustCompile(`[/\\\s\x00-\x1f\x7f]+`)
	segment := strings.Trim(re.ReplaceAllString(strings.TrimSpace(name), "-"), "-")
	if segment == "" || segment == "." || segment == ".." {
		segment = "source"
	}
	return "git/" + segment + "/credential"
}

func compact(input map[string]any) map[string]any {
	output := map[string]any{}
	for key, value := range input {
		if value != nil && value != "" {
			output[key] = value
		}
	}
	return output
}
func addQuery(values url.Values, key, value string) {
	if value != "" {
		values.Set(key, value)
	}
}
func query(values url.Values) string {
	if len(values) == 0 {
		return ""
	}
	return "?" + values.Encode()
}
func writeError(writer io.Writer, err error) { _, _ = fmt.Fprintln(writer, err) }

func printUsage(writer io.Writer) {
	fmt.Fprintln(writer, `usage: windforce [global flags] <command>

Global flags: --profile, --api-url, --workspace, --actor, --token-env, --pretty

Commands:
  profile list|show|set|use
  source list|register|probe|sync|deploy
  app list|show|history|source|openapi
  action show|schema
  job run|list|show|result|logs|cancel
  provisioning export|apply
  openapi
  version`)
}
