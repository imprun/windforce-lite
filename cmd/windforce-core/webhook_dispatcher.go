package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/imprun/windforce-core/internal/state"
	"github.com/imprun/windforce-core/internal/webhook"
)

const (
	defaultWebhookDispatchInterval    = 500 * time.Millisecond
	defaultWebhookRequestTimeout      = 10 * time.Second
	defaultWebhookLeaseTTL            = 30 * time.Second
	defaultWebhookMaxAttempts         = 8
	defaultWebhookMetricsAddr         = ":9090"
	defaultWebhookSuccessRetention    = 30 * 24 * time.Hour
	defaultWebhookFailureRetention    = 90 * 24 * time.Hour
	defaultWebhookRetentionInterval   = 10 * time.Minute
	defaultWebhookRetentionBatchSize  = 1000
	defaultWebhookRetentionTimeBudget = 5 * time.Second
)

type webhookDispatcherFlags struct {
	dispatchInterval      *time.Duration
	requestTimeout        *time.Duration
	leaseTTL              *time.Duration
	maxAttempts           *int
	allowedHosts          *string
	allowedCIDRs          *string
	allowInsecureLoopback *bool
	workerID              *string
	successRetention      *time.Duration
	failureRetention      *time.Duration
	retentionInterval     *time.Duration
	retentionBatchSize    *int
	retentionTimeBudget   *time.Duration
}

func bindWebhookDispatcherFlags(flags *flag.FlagSet, prefix string) webhookDispatcherFlags {
	return webhookDispatcherFlags{
		dispatchInterval:      flags.Duration(prefix+"dispatch-interval", envParsedDuration("WINDFORCE_LITE_WEBHOOK_DISPATCH_INTERVAL", defaultWebhookDispatchInterval), "how often an idle webhook dispatcher polls for delivery work"),
		requestTimeout:        flags.Duration(prefix+"request-timeout", envParsedDuration("WINDFORCE_LITE_WEBHOOK_REQUEST_TIMEOUT", defaultWebhookRequestTimeout), "webhook HTTP request timeout"),
		leaseTTL:              flags.Duration(prefix+"lease", envParsedDuration("WINDFORCE_LITE_WEBHOOK_LEASE_TTL", defaultWebhookLeaseTTL), "webhook delivery claim lease TTL"),
		maxAttempts:           flags.Int(prefix+"max-attempts", envInt("WINDFORCE_LITE_WEBHOOK_MAX_ATTEMPTS", defaultWebhookMaxAttempts), "maximum webhook delivery attempts"),
		allowedHosts:          flags.String(prefix+"allowed-hosts", os.Getenv("WINDFORCE_LITE_WEBHOOK_ALLOWED_HOSTS"), "comma-separated private webhook endpoint host allowlist"),
		allowedCIDRs:          flags.String(prefix+"allowed-cidrs", os.Getenv("WINDFORCE_LITE_WEBHOOK_ALLOWED_CIDRS"), "comma-separated private webhook endpoint CIDR allowlist"),
		allowInsecureLoopback: flags.Bool(prefix+"allow-insecure-loopback", envBool("WINDFORCE_LITE_WEBHOOK_ALLOW_INSECURE_LOOPBACK", false), "allow HTTP loopback webhook endpoints for local development"),
		workerID:              flags.String(prefix+"worker-id", "", "webhook dispatcher identity"),
		successRetention:      flags.Duration(prefix+"success-retention", envDays("WINDFORCE_LITE_WEBHOOK_SUCCESS_RETENTION_DAYS", defaultWebhookSuccessRetention), "how long succeeded/canceled webhook deliveries are kept; 0 keeps them forever"),
		failureRetention:      flags.Duration(prefix+"failure-retention", envDays("WINDFORCE_LITE_WEBHOOK_FAILURE_RETENTION_DAYS", defaultWebhookFailureRetention), "how long failed webhook deliveries are kept; 0 keeps them forever"),
		retentionInterval:     flags.Duration(prefix+"retention-interval", envParsedDuration("WINDFORCE_LITE_WEBHOOK_RETENTION_INTERVAL", defaultWebhookRetentionInterval), "how often webhook retention runs"),
		retentionBatchSize:    flags.Int(prefix+"retention-batch-size", envInt("WINDFORCE_LITE_WEBHOOK_RETENTION_BATCH_SIZE", defaultWebhookRetentionBatchSize), "maximum webhook records removed per retention batch"),
		retentionTimeBudget:   flags.Duration(prefix+"retention-time-budget", envParsedDuration("WINDFORCE_LITE_WEBHOOK_RETENTION_TIME_BUDGET", defaultWebhookRetentionTimeBudget), "maximum time spent in one webhook retention cycle"),
	}
}

func runWebhookDispatcher(args []string) int {
	flags := flag.NewFlagSet("webhook-dispatcher", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	stateBackend := flags.String("state-backend", "local", "runtime state backend: local or postgres")
	statePath := flags.String("state", defaultStatePath(), "local runtime state JSON path")
	databaseURL := flags.String("database-url", "", "PostgreSQL database URL for --state-backend postgres")
	migrate := flags.Bool("migrate", false, "run state backend schema migration before starting")
	secretKeyEnv := flags.String("secret-key-env", "SECRET_KEY", "environment variable that contains the instance secret used for webhook encryption")
	secretKeyPreviousEnv := flags.String("secret-key-previous-env", "SECRET_KEY_PREVIOUS", "environment variable that contains the previous instance secret during rotation")
	devMode := flags.Bool("dev", false, "development mode: allow starting with the built-in insecure secret key")
	dispatcherFlags := bindWebhookDispatcherFlags(flags, "")
	metricsAddr := flags.String("metrics-addr", firstNonEmpty(strings.TrimSpace(os.Getenv("WINDFORCE_LITE_WEBHOOK_METRICS_ADDR")), defaultWebhookMetricsAddr), "webhook dispatcher metrics listen address; empty disables metrics")
	once := flags.Bool("once", false, "process at most one pending webhook delivery and exit")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	stateStore, closeState, err := openStateStore(ctx, *stateBackend, *statePath, *databaseURL, *migrate)
	if err != nil {
		fmt.Fprintf(os.Stderr, "webhook-dispatcher state: %v\n", err)
		return 1
	}
	defer closeState()
	rawSecretKey := tokenFromEnv(*secretKeyEnv)
	if err := requireProductionSecrets(*devMode, false, "", rawSecretKey); err != nil {
		fmt.Fprintf(os.Stderr, "webhook-dispatcher: %v\n", err)
		return 1
	}
	configureInputCrypto(stateStore, effectiveSecretKey(rawSecretKey), tokenFromEnv(*secretKeyPreviousEnv))
	metrics := webhook.NewMetrics()
	dispatcher, err := newWebhookDispatcher(stateStore, dispatcherFlags, metrics)
	if err != nil {
		fmt.Fprintf(os.Stderr, "webhook-dispatcher config: %v\n", err)
		return 1
	}
	if *once {
		processed, err := dispatcher.ProcessOne(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "webhook-dispatcher: %v\n", err)
			return 1
		}
		_ = writeJSON(os.Stdout, map[string]bool{"processed": processed})
		return 0
	}
	if _, err := startWebhookMetricsServer(ctx, *metricsAddr, metrics.Handler(dispatcher.Store)); err != nil {
		fmt.Fprintf(os.Stderr, "webhook-dispatcher metrics: %v\n", err)
		return 1
	}
	retention, err := webhookRetentionFromFlags(dispatcherFlags)
	if err != nil {
		fmt.Fprintf(os.Stderr, "webhook-dispatcher retention: %v\n", err)
		return 1
	}
	if retention.Enabled() {
		go runWebhookRetentionLoop(ctx, dispatcher.Store, retention)
	}
	if err := dispatcher.RunLoop(ctx, *dispatcherFlags.dispatchInterval); err != nil {
		fmt.Fprintf(os.Stderr, "webhook-dispatcher: %v\n", err)
		return 1
	}
	return 0
}

func newWebhookDispatcher(stateStore state.Store, flags webhookDispatcherFlags, metrics *webhook.Metrics) (*webhook.Dispatcher, error) {
	webhookStore, ok := stateStore.(webhook.Store)
	if !ok {
		return nil, fmt.Errorf("state backend does not provide webhook delivery storage")
	}
	if *flags.requestTimeout <= 0 {
		return nil, fmt.Errorf("request timeout must be positive")
	}
	if *flags.leaseTTL <= *flags.requestTimeout {
		return nil, fmt.Errorf("delivery lease must be longer than request timeout")
	}
	if *flags.maxAttempts <= 0 {
		return nil, fmt.Errorf("max attempts must be positive")
	}
	hosts, err := webhook.ParseAllowedHosts(*flags.allowedHosts)
	if err != nil {
		return nil, err
	}
	cidrs, err := webhook.ParseAllowedCIDRs(*flags.allowedCIDRs)
	if err != nil {
		return nil, err
	}
	policy := webhook.EgressPolicy{
		AllowedHosts:          hosts,
		AllowedCIDRs:          cidrs,
		AllowInsecureLoopback: *flags.allowInsecureLoopback,
	}
	sender := webhook.NewHTTPSender(webhook.SenderConfig{
		Policy:         policy,
		RequestTimeout: *flags.requestTimeout,
		UserAgent:      "windforce-core-webhook/" + version,
	})
	return &webhook.Dispatcher{
		Store:       webhookStore,
		Sender:      sender,
		WorkerID:    strings.TrimSpace(*flags.workerID),
		LeaseTTL:    *flags.leaseTTL,
		MaxAttempts: *flags.maxAttempts,
		Metrics:     metrics,
	}, nil
}

func envInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envBool(name string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envParsedDuration(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed < 0 {
		fmt.Fprintf(os.Stderr, "ignoring %s=%q: expected a non-negative duration\n", name, value)
		return fallback
	}
	return parsed
}
