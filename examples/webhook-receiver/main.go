package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:19090", "HTTP listen address")
	secretEnv := flag.String("secret-env", "WINDFORCE_WEBHOOK_SECRET", "environment variable containing the signing secret")
	timestampTolerance := flag.Duration("timestamp-tolerance", 5*time.Minute, "accepted signature timestamp window")
	failFirst := flag.Int("fail-first", 0, "return 503 for the first N valid requests")
	flag.Parse()

	secret := strings.TrimSpace(os.Getenv(*secretEnv))
	if secret == "" {
		fmt.Fprintf(os.Stderr, "%s is required\n", *secretEnv)
		os.Exit(2)
	}
	if *timestampTolerance <= 0 || *failFirst < 0 {
		fmt.Fprintln(os.Stderr, "timestamp-tolerance must be positive and fail-first must be non-negative")
		os.Exit(2)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	server := &http.Server{
		Addr:              *addr,
		Handler:           newReceiver(secret, *timestampTolerance, *failFirst, logger),
		ReadHeaderTimeout: 5 * time.Second,
	}
	logger.Info("webhook receiver listening", "addr", *addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("webhook receiver stopped", "error", err)
		os.Exit(1)
	}
}
