package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/imprun/windforce-core/internal/bundle"
	"github.com/imprun/windforce-core/internal/catalog"
	"github.com/imprun/windforce-core/internal/contract"
	"github.com/imprun/windforce-core/internal/executionbundle"
	"github.com/imprun/windforce-core/internal/gitsource"
	"github.com/imprun/windforce-core/internal/runner"
	"github.com/imprun/windforce-core/internal/runtime"
	"github.com/imprun/windforce-core/internal/server"
	"github.com/imprun/windforce-core/internal/state"
	"github.com/imprun/windforce-core/internal/syncer"
	"github.com/imprun/windforce-core/internal/webhook"
	"github.com/imprun/windforce-core/internal/worker"
)

var version = "dev"

const (
	defaultLogFlushInterval = 2 * time.Second
	defaultLogCapBytes      = 20 << 20
)

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
	case "api", "control-plane":
		return runServer(args[1:], "control-plane")
	case "execution-api":
		return runServer(args[1:], "execution-api")
	case "worker":
		return runWorker(args[1:])
	case "webhook-dispatcher":
		return runWebhookDispatcher(args[1:])
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
	workerTokenEnv := flags.String("worker-token-env", "", "environment variable that contains the remote worker plane bearer token; defaults to the admin token")
	devMode := flags.Bool("dev", false, "development mode: allow starting without an admin token and with the built-in insecure secret key")
	jobTokenSecretEnv := flags.String("job-token-secret-env", "", "environment variable that contains the WF_TOKEN signing secret; defaults to admin token")
	secretKeyEnv := flags.String("secret-key-env", "SECRET_KEY", "environment variable that contains the instance secret used for secret variables")
	secretKeyPreviousEnv := flags.String("secret-key-previous-env", "SECRET_KEY_PREVIOUS", "environment variable that contains the previous instance secret during rotation")
	baseURL := flags.String("base-url", "", "public API base URL injected into job ctx helpers")
	storeDir := flags.String("store", defaultStoreDir(), "source snapshot and execution bundle store directory")
	catalogPath := flags.String("catalog", defaultCatalogPath(), "catalog JSON import path")
	gitSourcesPath := flags.String("git-sources", defaultGitSourcesPath(), "registered git sources JSON path")
	cacheRoot := flags.String("cache", defaultCacheDir(), "runtime cache directory")
	bunPath := flags.String("bun-path", "", "bun executable path")
	pythonPath := flags.String("python-path", "", "python executable path")
	goPath := flags.String("go-path", "", "go executable path")
	prepareTimeout := flags.Duration("prepare-timeout", 0, "source prepare timeout; defaults to 5m")
	poll := flags.Duration("poll", 500*time.Millisecond, "standalone worker poll interval")
	leaseTTL := flags.Duration("lease", 30*time.Second, "worker job lease TTL")
	logFlushInterval := flags.Duration("log-flush-interval", defaultLogFlushInterval, "worker log flush interval")
	logCapBytes := flags.Int("log-cap-bytes", defaultLogCapBytes, "per-job log size cap in bytes; 0 disables the cap")
	logJobPayloads := flags.Bool("log-job-payloads", false, "log complete decrypted job input and execution output")
	workerID := flags.String("worker-id", "", "worker identity for standalone processing")
	workerGroup := flags.String("worker-group", "default", "worker group name exposed to action ctx")
	egressProxy := flags.String("egress-proxy", "", "host:port of a co-located egress proxy sidecar")
	workerTags := flags.String("tags", "", "comma-separated route tags this worker claims")
	workerLabels := flags.String("labels", "", "comma-separated capability labels this worker offers; sys/ labels are operator-granted")
	jobSuccessRetention := flags.Duration("job-success-retention", envDays("WINDFORCE_LITE_JOB_SUCCESS_RETENTION_DAYS", defaultJobSuccessRetention), "how long succeeded job records are kept; 0 keeps them forever")
	jobFailureRetention := flags.Duration("job-failure-retention", envDays("WINDFORCE_LITE_JOB_FAILURE_RETENTION_DAYS", defaultJobFailureRetention), "how long failed/canceled/expired job records are kept; 0 keeps them forever")
	jobStuckAfter := flags.Duration("job-stuck-after", envHours("WINDFORCE_LITE_JOB_STUCK_AFTER_HOURS", defaultJobStuckAfter), "expire queued/running jobs with no progress for this long; 0 disables")
	jobRetentionInterval := flags.Duration("job-retention-interval", defaultJobRetentionInterval, "how often the retention pruner runs")
	webhookDispatcherFlags := bindWebhookDispatcherFlags(flags, "webhook-")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	stateStore, closeState, err := openStateStore(context.Background(), *stateBackend, *statePath, *databaseURL, *migrate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s state: %v\n", mode, err)
		return 1
	}
	defer closeState()
	releaseCatalog, ok := stateStore.(catalog.Store)
	if !ok {
		fmt.Fprintf(os.Stderr, "%s state backend does not provide a release catalog\n", mode)
		return 1
	}
	gitSources := gitsource.NewFileRegistry(*gitSourcesPath)
	if err := importReleaseCatalog(context.Background(), releaseCatalog, *catalogPath, gitSources); err != nil {
		fmt.Fprintf(os.Stderr, "%s release catalog import: %v\n", mode, err)
		return 1
	}
	adminToken := tokenFromEnv(*adminTokenEnv)
	rawSecretKey := tokenFromEnv(*secretKeyEnv)
	if err := requireProductionSecrets(*devMode, true, adminToken, rawSecretKey); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", mode, err)
		return 1
	}
	jobTokenSecret := firstNonEmpty(tokenFromEnv(*jobTokenSecretEnv), adminToken)
	secretKey := effectiveSecretKey(rawSecretKey)
	secretKeyPrevious := tokenFromEnv(*secretKeyPreviousEnv)
	configureInputCrypto(stateStore, secretKey, secretKeyPrevious)
	runtimeBaseURL := strings.TrimSpace(*baseURL)
	if runtimeBaseURL == "" && mode == "standalone" {
		runtimeBaseURL = localBaseURL(*addr)
	}
	bundleStore := bundle.NewLocalStore(*storeDir)
	executionBundleStore := executionbundle.NewLocalStore(executionBundleStoreRoot(*storeDir))
	runtimeRunner := runtime.Runner{
		Store:          bundleStore,
		ArtifactStore:  executionBundleStore,
		CacheRoot:      *cacheRoot,
		BaseURL:        runtimeBaseURL,
		JobTokenSecret: jobTokenSecret,
		BunPath:        *bunPath,
		PythonPath:     *pythonPath,
		GoPath:         *goPath,
		PrepareTimeout: *prepareTimeout,
	}
	combinedMode := mode == "standalone"
	var webhookMetrics *webhook.Metrics
	var webhookMetricsHandler http.Handler
	if combinedMode {
		webhookStore, ok := stateStore.(webhook.Store)
		if !ok {
			fmt.Fprintln(os.Stderr, "standalone state backend does not provide webhook delivery storage")
			return 1
		}
		webhookMetrics = webhook.NewMetrics()
		webhookMetricsHandler = webhookMetrics.Handler(webhookStore)
	}
	handler := server.New(server.Config{
		Store:              stateStore,
		Catalog:            releaseCatalog,
		Syncer:             &syncer.Syncer{Store: bundleStore},
		ExecutionBundles:   &runtimeRunner,
		GitSources:         gitSources,
		EnableControlAPI:   mode == "control-plane" || combinedMode,
		EnableExecutionAPI: mode == "execution-api" || combinedMode,
		EnableWebUI:        mode == "control-plane" || combinedMode,
		AdminToken:         adminToken,
		WorkerToken:        firstNonEmpty(tokenFromEnv(*workerTokenEnv), adminToken),
		ArtifactStore:      executionBundleStore,
		JobTokenSecret:     jobTokenSecret,
		SecretKey:          secretKey,
		SecretKeyPrevious:  secretKeyPrevious,
		MetricsHandler:     webhookMetricsHandler,
	})

	retention := jobRetentionPolicy{
		Success:    *jobSuccessRetention,
		Failure:    *jobFailureRetention,
		StuckAfter: *jobStuckAfter,
		Interval:   *jobRetentionInterval,
	}
	if retention.Enabled() && (mode == "execution-api" || combinedMode) {
		go runJobRetentionLoop(context.Background(), stateStore, retention)
	}

	if mode == "standalone" {
		dispatcher, err := newWebhookDispatcher(stateStore, webhookDispatcherFlags, webhookMetrics)
		if err != nil {
			fmt.Fprintf(os.Stderr, "standalone webhook dispatcher: %v\n", err)
			return 1
		}
		webhookRetention, err := webhookRetentionFromFlags(webhookDispatcherFlags)
		if err != nil {
			fmt.Fprintf(os.Stderr, "standalone webhook retention: %v\n", err)
			return 1
		}
		processor := worker.Processor{
			Store:            stateStore,
			Runner:           runtimeRunner,
			WorkerID:         *workerID,
			Group:            *workerGroup,
			Tags:             parseTags(*workerTags),
			Labels:           parseLabels(*workerLabels),
			EgressProxyAddr:  strings.TrimSpace(*egressProxy),
			LeaseTTL:         *leaseTTL,
			LogFlushInterval: *logFlushInterval,
			LogCapBytes:      *logCapBytes,
			LogJobPayloads:   *logJobPayloads,
		}
		go func() {
			if err := processor.RunLoop(context.Background(), *poll); err != nil {
				fmt.Fprintf(os.Stderr, "standalone worker: %v\n", err)
			}
		}()
		go func() {
			if err := dispatcher.RunLoop(context.Background(), *webhookDispatcherFlags.dispatchInterval); err != nil {
				fmt.Fprintf(os.Stderr, "standalone webhook dispatcher: %v\n", err)
			}
		}()
		if webhookRetention.Enabled() {
			go runWebhookRetentionLoop(context.Background(), dispatcher.Store, webhookRetention)
		}
	}

	fmt.Fprintf(os.Stderr, "windforce-core %s listening on %s\n", mode, *addr)
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
	storeDir := flags.String("store", defaultStoreDir(), "execution bundle store directory")
	cacheRoot := flags.String("cache", defaultCacheDir(), "runtime cache directory")
	bunPath := flags.String("bun-path", "", "bun executable path")
	pythonPath := flags.String("python-path", "", "python executable path")
	goPath := flags.String("go-path", "", "go executable path")
	prepareTimeout := flags.Duration("prepare-timeout", 0, "source prepare timeout; defaults to 5m")
	baseURL := flags.String("base-url", "", "public API base URL injected into job ctx helpers")
	apiTokenEnv := flags.String("api-token-env", "", "deprecated fallback for --job-token-secret-env")
	jobTokenSecretEnv := flags.String("job-token-secret-env", "", "environment variable that contains the WF_TOKEN signing secret")
	secretKeyEnv := flags.String("secret-key-env", "SECRET_KEY", "environment variable that contains the instance secret used for input encryption")
	devMode := flags.Bool("dev", false, "development mode: allow starting with the built-in insecure secret key")
	secretKeyPreviousEnv := flags.String("secret-key-previous-env", "SECRET_KEY_PREVIOUS", "environment variable that contains the previous instance secret during rotation")
	poll := flags.Duration("poll", 500*time.Millisecond, "job poll interval")
	leaseTTL := flags.Duration("lease", 30*time.Second, "job lease TTL")
	logFlushInterval := flags.Duration("log-flush-interval", defaultLogFlushInterval, "worker log flush interval")
	logCapBytes := flags.Int("log-cap-bytes", defaultLogCapBytes, "per-job log size cap in bytes; 0 disables the cap")
	logJobPayloads := flags.Bool("log-job-payloads", false, "log complete decrypted job input and execution output")
	workerID := flags.String("worker-id", "", "worker identity")
	workerGroup := flags.String("worker-group", "default", "worker group name exposed to action ctx")
	egressProxy := flags.String("egress-proxy", "", "host:port of a co-located egress proxy sidecar")
	workerTags := flags.String("tags", "", "comma-separated route tags this worker claims")
	workerLabels := flags.String("labels", "", "comma-separated capability labels this worker offers; sys/ labels are operator-granted")
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
	rawSecretKey := tokenFromEnv(*secretKeyEnv)
	if err := requireProductionSecrets(*devMode, false, "", rawSecretKey); err != nil {
		fmt.Fprintf(os.Stderr, "worker: %v\n", err)
		return 1
	}
	jobTokenSecret := firstNonEmpty(tokenFromEnv(*jobTokenSecretEnv), tokenFromEnv(*apiTokenEnv))
	secretKey := effectiveSecretKey(rawSecretKey)
	secretKeyPrevious := tokenFromEnv(*secretKeyPreviousEnv)
	configureInputCrypto(stateStore, secretKey, secretKeyPrevious)
	processor := worker.Processor{
		Store: stateStore,
		Runner: runtime.Runner{
			ArtifactStore:  executionbundle.NewLocalStore(executionBundleStoreRoot(*storeDir)),
			CacheRoot:      *cacheRoot,
			BaseURL:        strings.TrimSpace(*baseURL),
			JobTokenSecret: jobTokenSecret,
			BunPath:        *bunPath,
			PythonPath:     *pythonPath,
			GoPath:         *goPath,
			PrepareTimeout: *prepareTimeout,
		},
		WorkerID:         *workerID,
		Group:            *workerGroup,
		Tags:             parseTags(*workerTags),
		Labels:           parseLabels(*workerLabels),
		EgressProxyAddr:  strings.TrimSpace(*egressProxy),
		LeaseTTL:         *leaseTTL,
		LogFlushInterval: *logFlushInterval,
		LogCapBytes:      *logCapBytes,
		LogJobPayloads:   *logJobPayloads,
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

const (
	// Raw job records are short-lived incident data, not history
	// (ADR 0007): succeeded runs cover the Monitoring dashboard's largest
	// aggregation window, failures stay longer for incident analysis, and
	// stuck queued/running runs expire so they cannot pin storage forever.
	defaultJobSuccessRetention  = 7 * 24 * time.Hour
	defaultJobFailureRetention  = 30 * 24 * time.Hour
	defaultJobStuckAfter        = 24 * time.Hour
	defaultJobRetentionInterval = 10 * time.Minute
)

type jobRetentionPolicy struct {
	Success    time.Duration
	Failure    time.Duration
	StuckAfter time.Duration
	Interval   time.Duration
}

func (p jobRetentionPolicy) Enabled() bool {
	return p.Success > 0 || p.Failure > 0 || p.StuckAfter > 0
}

func runJobRetentionLoop(ctx context.Context, store state.Store, policy jobRetentionPolicy) {
	if policy.Interval <= 0 {
		policy.Interval = defaultJobRetentionInterval
	}
	// A zero TTL means "keep forever": collapse it to a cutoff no run can
	// ever be older than.
	cutoff := func(ttl time.Duration, now time.Time) time.Time {
		if ttl <= 0 {
			return time.Time{}
		}
		return now.Add(-ttl)
	}
	tick := func() {
		now := time.Now().UTC()
		if policy.StuckAfter > 0 {
			if expired, err := store.ExpireStuckJobs(ctx, now.Add(-policy.StuckAfter)); err != nil {
				fmt.Fprintf(os.Stderr, "job retention: expire stuck: %v\n", err)
			} else if expired > 0 {
				fmt.Fprintf(os.Stderr, "job retention: expired %d stuck run(s)\n", expired)
			}
		}
		if policy.Success > 0 || policy.Failure > 0 {
			pruned, err := store.PruneSettledJobs(ctx, cutoff(policy.Success, now), cutoff(policy.Failure, now))
			if err != nil {
				fmt.Fprintf(os.Stderr, "job retention: prune: %v\n", err)
			} else if pruned > 0 {
				fmt.Fprintf(os.Stderr, "job retention: pruned %d settled job(s)\n", pruned)
			}
		}
	}
	tick()
	ticker := time.NewTicker(policy.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tick()
		}
	}
}

func envDays(name string, fallback time.Duration) time.Duration {
	return envDuration(name, 24*time.Hour, fallback)
}

func envHours(name string, fallback time.Duration) time.Duration {
	return envDuration(name, time.Hour, fallback)
}

func envDuration(name string, unit time.Duration, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		fmt.Fprintf(os.Stderr, "ignoring %s=%q: expected a non-negative integer\n", name, raw)
		return fallback
	}
	return time.Duration(value) * unit
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

func importReleaseCatalog(ctx context.Context, target catalog.Store, catalogPath string, sources *gitsource.FileRegistry) error {
	imported, err := catalog.NewFileCatalog(catalogPath).Load(ctx)
	if err != nil {
		return err
	}
	sourceSnapshot, err := sources.Load(ctx)
	if err != nil {
		return err
	}
	catalog.NormalizeSnapshot(&imported)
	for _, source := range sourceSnapshot.Sources {
		if source.LastSyncedCommit == nil || source.LastSyncedAt == nil {
			continue
		}
		marker := catalog.SourceReleaseMarker{
			Workspace:   contract.NormalizeWorkspace(source.Workspace),
			GitSourceID: source.ID,
			Commit:      *source.LastSyncedCommit,
			ReleasedAt:  source.LastSyncedAt.UTC(),
		}
		imported.SourceMarkers[catalog.SourceReleaseKey(marker.Workspace, marker.GitSourceID)] = marker
	}
	return target.ImportCatalog(ctx, imported)
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

// requireProductionSecrets enforces the fail-closed startup posture:
// outside explicit dev mode a running instance must have a real admin
// token (server modes) and a real secret key — never the built-in
// default-open/default-key fallbacks.
func requireProductionSecrets(dev bool, needAdminToken bool, adminToken, secretKeyValue string) error {
	if dev {
		return nil
	}
	if needAdminToken && strings.TrimSpace(adminToken) == "" {
		return errors.New("an admin token is required: set --admin-token-env and its environment variable, or pass --dev")
	}
	if strings.TrimSpace(secretKeyValue) == "" {
		return errors.New("a secret key is required: set the --secret-key-env environment variable, or pass --dev")
	}
	return nil
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

func parseLabels(raw string) []string {
	labels, err := contract.NormalizeLabels(parseTags(raw), true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid --labels: %v", err)
		os.Exit(2)
	}
	return labels
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
	return filepath.Join(".windforce-core", "store")
}

func executionBundleStoreRoot(storeDir string) string {
	return filepath.Join(storeDir, "artifacts")
}

func defaultCatalogPath() string {
	return filepath.Join(".windforce-core", "catalog.json")
}

func defaultGitSourcesPath() string {
	return filepath.Join(".windforce-core", "git-sources.json")
}

func defaultCacheDir() string {
	return filepath.Join(".windforce-core", "cache")
}

func defaultStatePath() string {
	return filepath.Join(".windforce-core", "state.json")
}

func printUsage(file *os.File) {
	fmt.Fprintln(file, "usage:")
	fmt.Fprintln(file, "  windforce-core version")
	fmt.Fprintln(file, "  windforce-core control-plane [--addr :8080] [--state-backend local|postgres] [--git-sources <path>]")
	fmt.Fprintln(file, "  windforce-core execution-api [--addr :8080] [--state-backend local|postgres]")
	fmt.Fprintln(file, "  windforce-core worker [--state-backend local|postgres] [--worker-group default] [--egress-proxy host:port] [--bun-path <path>] [--python-path <path>] [--go-path <path>] [--prepare-timeout 5m] [--once]")
	fmt.Fprintln(file, "  windforce-core webhook-dispatcher [--state-backend local|postgres] [--database-url <url>] [--once]")
	fmt.Fprintln(file, "  windforce-core standalone [--addr :8080] [--state-backend local|postgres] [--worker-group default] [--egress-proxy host:port] [--git-sources <path>] [--bun-path <path>] [--python-path <path>] [--go-path <path>] [--prepare-timeout 5m]")
	fmt.Fprintln(file, "  windforce-core run-json [flags] -- <command> [args...]")
}
