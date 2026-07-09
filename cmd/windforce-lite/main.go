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
	case "sync":
		return runSync(args[1:])
	case "run":
		return runAction(args[1:])
	case "run-json":
		return runJSON(args[1:])
	case "trigger":
		return runServer(args[1:], "trigger")
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

func runSync(args []string) int {
	flags := flag.NewFlagSet("sync", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	sourceDir := flags.String("source", "", "local app source directory")
	repoURL := flags.String("repo", "", "git repository URL")
	branch := flags.String("branch", "main", "git branch")
	commit := flags.String("commit", "", "commit or local bundle id")
	var sourceSubpath string
	flags.StringVar(&sourceSubpath, "subpath", "", "source subpath inside the git repository or local source")
	flags.StringVar(&sourceSubpath, "path", "", "alias for --subpath")
	app := flags.String("app", "", "optional app name assertion")
	workspace := flags.String("workspace", "default", "source workspace")
	gitSourceID := flags.String("git-source-id", "", "registered git source id")
	storeDir := flags.String("store", defaultStoreDir(), "bundle store directory")
	catalogPath := flags.String("catalog", defaultCatalogPath(), "catalog JSON path")
	cloneRoot := flags.String("clone-root", "", "temporary clone root")
	tokenEnv := flags.String("token-env", "", "environment variable that contains a git token")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	token := ""
	if *tokenEnv != "" {
		token = os.Getenv(*tokenEnv)
	}

	store := bundle.NewLocalStore(*storeDir)
	fileCatalog := catalog.NewFileCatalog(*catalogPath)
	s := syncer.Syncer{Store: store, Catalog: fileCatalog, CloneRoot: *cloneRoot}
	deployment, err := s.Sync(context.Background(), syncer.Source{
		Workspace:   *workspace,
		GitSourceID: *gitSourceID,
		App:         *app,
		RepoURL:     *repoURL,
		Branch:      *branch,
		Commit:      *commit,
		Subpath:     sourceSubpath,
		Token:       token,
		LocalDir:    *sourceDir,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "sync: %v\n", err)
		return 1
	}
	if err := writeJSON(os.Stdout, deployment); err != nil {
		fmt.Fprintf(os.Stderr, "write deployment: %v\n", err)
		return 2
	}
	return 0
}

func runAction(args []string) int {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	app := flags.String("app", "", "app name")
	action := flags.String("action", "", "action name")
	inputPath := flags.String("input", "", "input JSON file path")
	outputPath := flags.String("output", "", "output JSON file path")
	storeDir := flags.String("store", defaultStoreDir(), "bundle store directory")
	catalogPath := flags.String("catalog", defaultCatalogPath(), "catalog JSON path")
	cacheRoot := flags.String("cache", defaultCacheDir(), "runtime cache directory")
	timeout := flags.Duration("timeout", 0, "action timeout override")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *app == "" || *action == "" {
		fmt.Fprintln(os.Stderr, "run: --app and --action are required")
		return 2
	}

	fileCatalog := catalog.NewFileCatalog(*catalogPath)
	deployment, err := fileCatalog.GetDeployment(context.Background(), *app)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run: %v\n", err)
		return 1
	}

	r := runtime.Runner{
		Store:     bundle.NewLocalStore(*storeDir),
		CacheRoot: *cacheRoot,
	}
	result, err := r.Run(context.Background(), runtime.RunRequest{
		Deployment: deployment,
		Action:     *action,
		InputPath:  *inputPath,
		OutputPath: *outputPath,
		Timeout:    *timeout,
	})
	if err != nil {
		_ = writeJSON(os.Stdout, result)
		fmt.Fprintf(os.Stderr, "run: %v\n", err)
		return 1
	}
	if err := writeJSON(os.Stdout, result); err != nil {
		fmt.Fprintf(os.Stderr, "write result: %v\n", err)
		return 2
	}
	if result.ExitCode != 0 {
		return result.ExitCode
	}
	return 0
}

func runServer(args []string, mode string) int {
	flags := flag.NewFlagSet(mode, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	addr := flags.String("addr", ":8080", "HTTP listen address")
	stateBackend := flags.String("state-backend", "local", "runtime state backend: local or postgres")
	statePath := flags.String("state", defaultStatePath(), "local runtime state JSON path")
	databaseURL := flags.String("database-url", "", "PostgreSQL database URL for --state-backend postgres")
	migrate := flags.Bool("migrate", false, "run state backend schema migration before starting")
	triggerTokenEnv := flags.String("trigger-token-env", "", "environment variable that contains the trigger bearer token")
	adminTokenEnv := flags.String("admin-token-env", "", "environment variable that contains the admin/API bearer token")
	baseURL := flags.String("base-url", "", "public API base URL injected into job ctx helpers")
	storeDir := flags.String("store", defaultStoreDir(), "bundle store directory")
	catalogPath := flags.String("catalog", defaultCatalogPath(), "catalog JSON path")
	gitSourcesPath := flags.String("git-sources", defaultGitSourcesPath(), "registered git sources JSON path")
	cacheRoot := flags.String("cache", defaultCacheDir(), "runtime cache directory")
	wait := flags.Duration("wait", 0, "maximum trigger wait for a completed or pending run")
	poll := flags.Duration("poll", 500*time.Millisecond, "standalone worker poll interval")
	leaseTTL := flags.Duration("lease", 30*time.Second, "worker job lease TTL")
	workerID := flags.String("worker-id", "", "worker identity for standalone processing")
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
	runtimeBaseURL := strings.TrimSpace(*baseURL)
	if runtimeBaseURL == "" && mode == "standalone" {
		runtimeBaseURL = localBaseURL(*addr)
	}
	handler := server.New(server.Config{
		Store:         stateStore,
		Catalog:       fileCatalog,
		Syncer:        &syncer.Syncer{Store: bundle.NewLocalStore(*storeDir), Catalog: fileCatalog},
		GitSources:    gitSources,
		EnableTrigger: mode == "trigger" || mode == "standalone",
		EnableAPI:     mode == "api" || mode == "standalone",
		TriggerToken:  tokenFromEnv(*triggerTokenEnv),
		AdminToken:    adminToken,
		Wait:          *wait,
	})

	if mode == "standalone" {
		processor := worker.Processor{
			Store: stateStore,
			Runner: runtime.Runner{
				Store:     bundle.NewLocalStore(*storeDir),
				CacheRoot: *cacheRoot,
				BaseURL:   runtimeBaseURL,
				APIToken:  adminToken,
			},
			WorkerID: *workerID,
			Tags:     parseTags(*workerTags),
			LeaseTTL: *leaseTTL,
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
	baseURL := flags.String("base-url", "", "public API base URL injected into job ctx helpers")
	apiTokenEnv := flags.String("api-token-env", "", "environment variable that contains the API bearer token for ctx helpers")
	poll := flags.Duration("poll", 500*time.Millisecond, "job poll interval")
	leaseTTL := flags.Duration("lease", 30*time.Second, "job lease TTL")
	workerID := flags.String("worker-id", "", "worker identity")
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
	processor := worker.Processor{
		Store: stateStore,
		Runner: runtime.Runner{
			Store:     bundle.NewLocalStore(*storeDir),
			CacheRoot: *cacheRoot,
			BaseURL:   strings.TrimSpace(*baseURL),
			APIToken:  tokenFromEnv(*apiTokenEnv),
		},
		WorkerID: *workerID,
		Tags:     parseTags(*workerTags),
		LeaseTTL: *leaseTTL,
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
	fmt.Fprintln(file, "  windforce-lite sync --source <dir> [--subpath <subdir>] [--store <dir>] [--catalog <path>]")
	fmt.Fprintln(file, "  windforce-lite sync --repo <url> [--branch main] [--subpath <subdir>] [--store <dir>] [--catalog <path>]")
	fmt.Fprintln(file, "  windforce-lite run --app <app> --action <action> [--input <path>] [--output <path>]")
	fmt.Fprintln(file, "  windforce-lite trigger [--addr :8080] [--state-backend local|postgres] [--wait 30s] [--git-sources <path>]")
	fmt.Fprintln(file, "  windforce-lite api [--addr :8080] [--state-backend local|postgres] [--git-sources <path>]")
	fmt.Fprintln(file, "  windforce-lite worker [--state-backend local|postgres] [--once]")
	fmt.Fprintln(file, "  windforce-lite standalone [--addr :8080] [--state-backend local|postgres] [--wait 30s] [--git-sources <path>]")
	fmt.Fprintln(file, "  windforce-lite run-json [flags] -- <command> [args...]")
}
