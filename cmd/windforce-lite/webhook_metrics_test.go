package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestStartWebhookMetricsServerExposesHealthAndMetrics(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listener, err := startWebhookMetricsServer(ctx, "127.0.0.1:0", http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(response, "windforce_webhook_pending_deliveries 0\n")
	}))
	if err != nil {
		t.Fatal(err)
	}
	baseURL := "http://" + listener.Addr().String()
	for _, path := range []string{"/healthz", "/metrics"} {
		response, err := http.Get(baseURL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d", path, response.StatusCode)
		}
		if path == "/metrics" && !strings.Contains(string(body), "windforce_webhook_pending_deliveries") {
			t.Fatalf("metrics body = %q", body)
		}
	}
}
