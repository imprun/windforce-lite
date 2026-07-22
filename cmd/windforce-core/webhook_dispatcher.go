package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/imprun/windforce-core/internal/state"
	"github.com/imprun/windforce-core/internal/webhook"
)

const (
	defaultWebhookDispatchInterval    = 500 * time.Millisecond
	defaultWebhookRequestTimeout      = 10 * time.Second
	defaultWebhookLeaseTTL            = 30 * time.Second
	defaultWebhookMaxAttempts         = 8
	defaultWebhookSuccessRetention    = 30 * 24 * time.Hour
	defaultWebhookFailureRetention    = 90 * 24 * time.Hour
	defaultWebhookRetentionInterval   = 10 * time.Minute
	defaultWebhookRetentionBatchSize  = 1000
	defaultWebhookRetentionTimeBudget = 5 * time.Second
)

type webhookDispatcherFlags struct {
	dispatchInterval         *time.Duration
	requestTimeout           *time.Duration
	leaseTTL                 *time.Duration
	maxAttempts              *int
	allowedHosts             *string
	allowedCIDRs             *string
	allowedInsecureHTTPHosts *string
	allowInsecureLoopback    *bool
	workerID                 *string
	successRetention         *time.Duration
	failureRetention         *time.Duration
	retentionInterval        *time.Duration
	retentionBatchSize       *int
	retentionTimeBudget      *time.Duration
}

func bindWebhookDispatcherFlags(flags *flag.FlagSet, prefix string) webhookDispatcherFlags {
	return webhookDispatcherFlags{
		dispatchInterval:         flags.Duration(prefix+"dispatch-interval", envParsedDuration("WINDFORCE_LITE_WEBHOOK_DISPATCH_INTERVAL", defaultWebhookDispatchInterval), "how often an idle webhook dispatcher polls for delivery work"),
		requestTimeout:           flags.Duration(prefix+"request-timeout", envParsedDuration("WINDFORCE_LITE_WEBHOOK_REQUEST_TIMEOUT", defaultWebhookRequestTimeout), "webhook HTTP request timeout"),
		leaseTTL:                 flags.Duration(prefix+"lease", envParsedDuration("WINDFORCE_LITE_WEBHOOK_LEASE_TTL", defaultWebhookLeaseTTL), "webhook delivery claim lease TTL"),
		maxAttempts:              flags.Int(prefix+"max-attempts", envInt("WINDFORCE_LITE_WEBHOOK_MAX_ATTEMPTS", defaultWebhookMaxAttempts), "maximum webhook delivery attempts"),
		allowedHosts:             flags.String(prefix+"allowed-hosts", os.Getenv("WINDFORCE_LITE_WEBHOOK_ALLOWED_HOSTS"), "comma-separated private HTTPS webhook endpoint host allowlist"),
		allowedCIDRs:             flags.String(prefix+"allowed-cidrs", os.Getenv("WINDFORCE_LITE_WEBHOOK_ALLOWED_CIDRS"), "comma-separated private HTTPS webhook endpoint CIDR allowlist"),
		allowedInsecureHTTPHosts: flags.String(prefix+"allowed-insecure-http-hosts", os.Getenv("WINDFORCE_LITE_WEBHOOK_ALLOWED_INSECURE_HTTP_HOSTS"), "comma-separated HTTP webhook endpoint host allowlist for local development"),
		allowInsecureLoopback:    flags.Bool(prefix+"allow-insecure-loopback", envBool("WINDFORCE_LITE_WEBHOOK_ALLOW_INSECURE_LOOPBACK", false), "allow HTTP loopback webhook endpoints for local development"),
		workerID:                 flags.String(prefix+"worker-id", "", "webhook dispatcher identity"),
		successRetention:         flags.Duration(prefix+"success-retention", envDays("WINDFORCE_LITE_WEBHOOK_SUCCESS_RETENTION_DAYS", defaultWebhookSuccessRetention), "how long succeeded/canceled webhook deliveries are kept; 0 keeps them forever"),
		failureRetention:         flags.Duration(prefix+"failure-retention", envDays("WINDFORCE_LITE_WEBHOOK_FAILURE_RETENTION_DAYS", defaultWebhookFailureRetention), "how long failed webhook deliveries are kept; 0 keeps them forever"),
		retentionInterval:        flags.Duration(prefix+"retention-interval", envParsedDuration("WINDFORCE_LITE_WEBHOOK_RETENTION_INTERVAL", defaultWebhookRetentionInterval), "how often webhook retention runs"),
		retentionBatchSize:       flags.Int(prefix+"retention-batch-size", envInt("WINDFORCE_LITE_WEBHOOK_RETENTION_BATCH_SIZE", defaultWebhookRetentionBatchSize), "maximum webhook records removed per retention batch"),
		retentionTimeBudget:      flags.Duration(prefix+"retention-time-budget", envParsedDuration("WINDFORCE_LITE_WEBHOOK_RETENTION_TIME_BUDGET", defaultWebhookRetentionTimeBudget), "maximum time spent in one webhook retention cycle"),
	}
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
	insecureHTTPHosts, err := webhook.ParseAllowedHosts(*flags.allowedInsecureHTTPHosts)
	if err != nil {
		return nil, err
	}
	policy := webhook.EgressPolicy{
		AllowedHosts:             hosts,
		AllowedCIDRs:             cidrs,
		AllowedInsecureHTTPHosts: insecureHTTPHosts,
		AllowInsecureLoopback:    *flags.allowInsecureLoopback,
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
