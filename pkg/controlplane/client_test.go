package controlplane

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientSendsAuthenticationAndActor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-Windforce-Actor"); got != "developer@example.test" {
			t.Fatalf("X-Windforce-Actor = %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"message":"hello"}` {
			t.Fatalf("body = %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, Workspace: "default", Actor: "developer@example.test", Token: "secret"}
	result, err := client.DoJSON(context.Background(), http.MethodPost, client.WorkspacePath("jobs", "run", "app", "action"), map[string]string{"message": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != `{"ok":true}` {
		t.Fatalf("result = %s", result)
	}
}

func TestClientReturnsTypedAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":"invalid source"}`))
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, Workspace: "default"}
	_, err := client.DoJSON(context.Background(), http.MethodGet, client.WorkspacePath("apps"), nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("error = %#v", err)
	}
}
