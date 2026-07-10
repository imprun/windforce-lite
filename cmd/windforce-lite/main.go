package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/imprun/windforce-lite/internal/bundle"
	"github.com/imprun/windforce-lite/internal/catalog"
	"github.com/imprun/windforce-lite/internal/gitsource"
	"github.com/imprun/windforce-lite/internal/runner"
	"github.com/imprun/windforce-lite/internal/runtime"
	"github.com/imprun/windforce-lite/internal/server"
	"github.com/imprun/windforce-lite/internal/state"
	"github.com/imprun/windforce-lite/internal/syncer"
	"github.com/imprun/windforce-lite/internal/worker"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		printUsage(os.Stderr)
		return 2
	}

	switch args[0] {
	case "version":
		fmt.Println(version)
		return 0
	case "run-json":
		return runJSON(args[1:])
	case "api":
		return runServer(args[1:], "api")
	case "worker":
		return runWorker(args[1:])
	case "standalone":
		return runServer(args[1:], "standalone")
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", args[0])
		printUsage(os.Stderr)
		return 2
	}
}

func runServer(args []string, mode string) int {
	flags := flag.NewFlagSet(mode, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	addr := flags.String("addr", ":8080", "HTTP listen address")
	stateBackend := flags.String("state-backend", "local", "runtime state backend: local or postgres")
	statePath := flags.String("state", defaultStatePath(), "local runtime state JSON path")
	databaseURL := flags.String("database-url", "", "PostgreSQL database URL for --state-backend postgres")
	migrate := flags.Bool("migrate", false, "run state backend schema migration before starting")
	adminTokenEnv := flags.String("admin-token-env", "", "environment variable that contains the admin/API bearer token")
	jobTokenSecretEnv := flags.String("job-token-secret-env", "", "environment variable that contains the WF_TOKEN signing secret; defaults to admin token")
	secretKeyEnv := flags.String("secret-key-env", "SECRET_KEY", "environment variable that contains the instance secret used for secret variables")
	secretKeyPreviousEnv := flags.String("secret-key-previous-env", "SECRET_KEY_PREVIOUS", "environment variable that contains the previous instance secret during rotation")
	baseURL := flags.String("base-url", "", "public API base URL injected into job ctx helpers")
	storeDir := flags.String("store", defaultStoreDir(), "bundle store directory")
	catalogPath := flags.String("catalog", defaultCatalogPath(), "catalog JSON path")
	gitSourcesPath := flags.String("git-sources", defaultGitSourcesPath(), "registered git sources JSON path")
	cacheRoot := flags.String("cache", defaultCacheDir(), "runtime cache directory")
	bunPath := flags.String("bun-path", "", "bun executable path")
	pythonPath := flags.String("python-path", "", "python executable path")
	goPath := flags.String("go-path", "", "go executable path")
	prepareTimeout := flags.Duration("prepare-timeout", 0, "source prepare timeout; defaults to 5m")
	poll := flags.Duration("poll", 500*time.Millisecond, "standalone worker poll interval")
	leaseTTL := flags.Duration("lease", 30*time.Second, "worker job lease TTL")
	workerID := flags.String("worker-id", "", "worker identity for standalone processing")
	workerGroup := flags.String("worker-group", "default", "worker group name exposed to action ctx")
	egressProxy := flags.String("egress-proxy", "", "host:port of a co-located egress proxy sidecar")
	workerTags := flags.String("tags", "", "comma-separated route tags this worker claims")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	stateStore, closeState, err := openStateStore(context.Background(), *stateBackend, *statePath, *databaseURL, *migrate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s state: %v\n", mode, err)
		return 1
	}
	defer closeState()
	fileCatalog := catalog.NewFileCatalog(*catalogPath)
	gitSources := gitsource.NewFileRegistry(*gitSourcesPath)
	adminToken := tokenFromEnv(*adminTokenEnv)
	jobTokenSecret := firstNonEmpty(tokenFromEnv(*jobTokenSecretEnv), adminToken)
	secretKey := effectiveSecretKey(tokenFromEnv(*secretKeyEnv))
	secretKeyPrevious := tokenFromEnv(*secretKeyPreviousEnv)
	configureInputCrypto(stateStore, secretKey, secretKeyPrevious)
	runtimeBaseURL := strings.TrimSpace(*baseURL)
	if runtimeBaseURL == "" && mode == "standalone" {
		runtimeBaseURL = localBaseURL(*addr)
	}
	handler := server.New(server.Config{
		Store:             stateStore,
		Catalog:           fileCatalog,
		Syncer:            &syncer.Syncer{Store: bundle.NewLocalStore(*storeDir), Catalog: fileCatalog},
		GitSources:        gitSources,
		EnableAPI:         mode == "api" || mode == "standalone",
		AdminToken:        adminToken,
		JobTokenSecret:    jobTokenSecret,
		SecretKey:         secretKey,
		SecretKeyPrevious: secretKeyPrevious,
	})

	if mode == "standalone" {
		processor := worker.Processor{
			Store: stateStore,
			Runner: runtime.Runner{
				Store:          bundle.NewLocalStore(*storeDir),
				CacheRoot:      *cacheRoot,
				BaseURL:        runtimeBaseURL,
				JobTokenSecret: jobTokenSecret,
				BunPath:        *bunPath,
				PythonPath:     *pythonPath,
				GoPath:         *goPath,
				PrepareTimeout: *prepareTimeout,
			},
			WorkerID:        *workerID,
			Group:           *workerGroup,
			Tags:            parseTags(*workerTags),
			EgressProxyAddr: strings.TrimSpace(*egressProxy),
			LeaseTTL:        *leaseTTL,
		}
		go func() {
			if err := processor.RunLoop(context.Background(), *poll); err != nil {
				fmt.Fprintf(os.Stderr, "standalone worker: %v\n", err)
			}
		}()
	}

	fmt.Fprintf(os.Stderr, "windforce-lite %s listening on %s\n", mode, *addr)
	if err := http.ListenAndServe(*addr, handler); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", mode, err)
		return 1
	}
	return 0
}

func runWorker(args []string) int {
	flags := flag.NewFlagSet("worker", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	stateBackend := flags.String("state-backend", "local", "runtime state backend: local or postgres")
	statePath := flags.String("state", defaultStatePath(), "local runtime state JSON path")
	databaseURL := flags.String("database-url", "", "PostgreSQL database URL for --state-backend postgres")
	migrate := flags.Bool("migrate", false, "run state backend schema migration before starting")
	storeDir := flags.String("store", defaultStoreDir(), "bundle store directory")
	cacheRoot := flags.String("cache", defaultCacheDir(), "runtime cache directory")
	bunPath := flags.String("bun-path", "", "bun executable path")
	pythonPath := flags.String("python-path", "", "python executable path")
	goPath := flags.String("go-path", "", "go executable path")
	prepareTimeout := flags.Duration("prepare-timeout", 0, "source prepare timeout; defaults to 5m")
	baseURL := flags.String("base-url", "", "public API base URL injected into job ctx helpers")
	apiTokenEnv := flags.String("api-token-env", "", "deprecated fallback for --job-token-secret-env")
	jobTokenSecretEnv := flags.String("job-token-secret-env", "", "environment variable that contains the WF_TOKEN signing secret")
	secretKeyEnv := flags.String("secret-key-env", "SECRET_KEY", "environment variable that contains the instance secret used for input encryption")
	secretKeyPreviousEnv := flags.String("secret-key-previous-env", "SECRET_KEY_PREVIOUS", "environment variable that contains the previous instance secret during rotation")
	poll := flags.Duration("poll", 500*time.Millisecond, "job poll interval")
	leaseTTL := flags.Duration("lease", 30*time.Second, "job lease TTL")
	workerID := flags.String("worker-id", "", "worker identity")
	workerGroup := flags.String("worker-group", "default", "worker group name exposed to action ctx")
	egressProxy := flags.String("egress-proxy", "", "host:port of a co-located egress proxy sidecar")
	workerTags := flags.String("tags", "", "comma-separated route tags this worker claims")
	once := flags.Bool("once", false, "process at most one queued job and exit")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	stateStore, closeState, err := openStateStore(context.Background(), *stateBackend, *statePath, *databaseURL, *migrate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "worker state: %v\n", err)
		return 1
	}
	defer closeState()
	jobTokenSecret := firstNonEmpty(tokenFromEnv(*jobTokenSecretEnv), tokenFromEnv(*apiTokenEnv))
	secretKey := effectiveSecretKey(tokenFromEnv(*secretKeyEnv))
	secretKeyPrevious := tokenFromEnv(*secretKeyPreviousEnv)
	configureInputCrypto(stateStore, secretKey, secretKeyPrevious)
	processor := worker.Processor{
		Store: stateStore,
		Runner: runtime.Runner{
			Store:          bundle.NewLocalStore(*storeDir),
			CacheRoot:      *cacheRoot,
			BaseURL:        strings.TrimSpace(*baseURL),
			JobTokenSecret: jobTokenSecret,
			BunPath:        *bunPath,
			PythonPath:     *pythonPath,
			GoPath:         *goPath,
			PrepareTimeout: *prepareTimeout,
		},
		WorkerID:        *workerID,
		Group:           *workerGroup,
		Tags:            parseTags(*workerTags),
		EgressProxyAddr: strings.TrimSpace(*egressProxy),
		LeaseTTL:        *leaseTTL,
	}
	if *once {
		processed, err := processor.ProcessOne(context.Background())
		if err != nil {
			fmt.Fprintf(os.Stderr, "worker: %v\n", err)
			return 1
		}
		_ = writeJSON(os.Stdout, map[string]bool{"processed": processed})
		return 0
	}

	if err := processor.RunLoop(context.Background(), *poll); err != nil {
		fmt.Fprintf(os.Stderr, "worker: %v\n", err)
		return 1
	}
	return 0
}

func openStateStore(ctx context.Context, backend string, path string, databaseURL string, migrate bool) (state.Store, func(), error) {
	switch backend {
	case "local":
		return state.NewLocalStore(path), func() {}, nil
	case "postgres":
		store, err := state.OpenPostgresStore(ctx, databaseURL)
		if err != nil {
			return nil, func() {}, err
		}
		if migrate {
			if err := store.Migrate(ctx); err != nil {
				store.Close()
				return nil, func() {}, err
			}
		}
		return store, store.Close, nil
	default:
		return nil, func() {}, fmt.Errorf("unsupported state backend %q", backend)
	}
}

func tokenFromEnv(name string) string {
	if name == "" {
		return ""
	}
	return os.Getenv(name)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

type inputCryptoConfigurer interface {
	ConfigureInputCrypto(secretKey string, previous string)
}

func configureInputCrypto(store state.Store, secretKey string, previous string) {
	if configurable, ok := store.(inputCryptoConfigurer); ok {
		configurable.ConfigureInputCrypto(secretKey, previous)
	}
}

func effectiveSecretKey(value string) string {
	if strings.TrimSpace(value) == "" {
		return server.DefaultSecretKey
	}
	return strings.TrimSpace(value)
}

func localBaseURL(addr string) string {
	addr = strings.TrimSpace(addr)
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return strings.TrimRight(addr, "/")
	}
	host, port, err := net.SplitHostPort(addr)
	if err == nil {
		if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
			host = "127.0.0.1"
		}
		if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
			host = "[" + host + "]"
		}
		return "http://" + host + ":" + port
	}
	if strings.HasPrefix(addr, ":") {
		return "http://127.0.0.1" + addr
	}
	return "http://" + strings.TrimRight(addr, "/")
}

func parseTags(value string) []string {
	parts := strings.Split(value, ",")
	tags := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		tag := strings.TrimSpace(part)
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		tags = append(tags, tag)
	}
	return tags
}

func runJSON(args []string) int {
	flags := flag.NewFlagSet("run-json", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	workDir := flags.String("workdir", "", "subprocess working directory")
	inputPath := flags.String("input", "", "input JSON file path")
	outputPath := flags.String("output", "", "output JSON file path")
	app := flags.String("app", "", "app name")
	action := flags.String("action", "", "action name")
	timeout := flags.Duration("timeout", 0, "subprocess timeout")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	command := flags.Args()
	if len(command) > 0 && command[0] == "--" {
		command = command[1:]
	}

	res, err := runner.RunJSONSubprocess(context.Background(), runner.JSONSubprocessRequest{
		WorkDir:    *workDir,
		Command:    command,
		InputPath:  *inputPath,
		OutputPath: *outputPath,
		App:        *app,
		Action:     *action,
		Timeout:    time.Duration(*timeout),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "run-json: %v\n", err)
		_ = writeJSON(os.Stdout, res)
		return 2
	}
	if err := writeJSON(os.Stdout, res); err != nil {
		fmt.Fprintf(os.Stderr, "write result: %v\n", err)
		return 2
	}
	if res.ExitCode != 0 {
		return res.ExitCode
	}
	return 0
}

func writeJSON(file *os.File, value any) error {
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func defaultStoreDir() string {
	return filepath.Join(".windforce-lite", "store")
}

func defaultCatalogPath() string {
	return filepath.Join(".windforce-lite", "catalog.json")
}

func defaultGitSourcesPath() string {
	return filepath.Join(".windforce-lite", "git-sources.json")
}

func defaultCacheDir() string {
	return filepath.Join(".windforce-lite", "cache")
}

func defaultStatePath() string {
	return filepath.Join(".windforce-lite", "state.json")
}

func printUsage(file *os.File) {
	fmt.Fprintln(file, "usage:")
	fmt.Fprintln(file, "  windforce-lite version")
	fmt.Fprintln(file, "  windforce-lite api [--addr :8080] [--state-backend local|postgres] [--git-sources <path>]")
	fmt.Fprintln(file, "  windforce-lite worker [--state-backend local|postgres] [--worker-group default] [--egress-proxy host:port] [--bun-path <path>] [--python-path <path>] [--go-path <path>] [--prepare-timeout 5m] [--once]")
	fmt.Fprintln(file, "  windforce-lite standalone [--addr :8080] [--state-backend local|postgres] [--worker-group default] [--egress-proxy host:port] [--git-sources <path>] [--bun-path <path>] [--python-path <path>] [--go-path <path>] [--prepare-timeout 5m]")
	fmt.Fprintln(file, "  windforce-lite run-json [flags] -- <command> [args...]")
}
