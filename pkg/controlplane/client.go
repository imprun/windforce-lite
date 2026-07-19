// Package controlplane provides a small, transport-focused client for the
// Windforce Core control-plane API.
package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	BaseURL   string
	Workspace string
	Actor     string
	Token     string
	HTTP      *http.Client
}

type APIError struct {
	StatusCode int
	Body       []byte
}

func (e *APIError) Error() string {
	message := strings.TrimSpace(string(e.Body))
	if message == "" {
		message = http.StatusText(e.StatusCode)
	}
	return fmt.Sprintf("control plane returned HTTP %d: %s", e.StatusCode, message)
}

func (c *Client) WorkspacePath(parts ...string) string {
	segments := []string{"api", "w", url.PathEscape(c.Workspace)}
	for _, part := range parts {
		segments = append(segments, url.PathEscape(strings.TrimSpace(part)))
	}
	return "/" + strings.Join(segments, "/")
}

func (c *Client) DoJSON(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request body: %w", err)
		}
		payload = bytes.NewReader(encoded)
	}
	data, _, err := c.do(ctx, method, path, payload, "application/json")
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return json.RawMessage("null"), nil
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("control plane returned invalid JSON")
	}
	return json.RawMessage(data), nil
}

func (c *Client) DoRaw(ctx context.Context, method, path, contentType string, body []byte) ([]byte, string, error) {
	var payload io.Reader
	if body != nil {
		payload = bytes.NewReader(body)
	}
	return c.do(ctx, method, path, payload, contentType)
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader, contentType string) ([]byte, string, error) {
	base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if base == "" {
		return nil, "", fmt.Errorf("control-plane API URL is required")
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil && contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token := strings.TrimSpace(c.Token); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if actor := strings.TrimSpace(c.Actor); actor != "" {
		req.Header.Set("X-Windforce-Actor", actor)
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("control-plane request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read control-plane response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.Header.Get("Content-Type"), &APIError{StatusCode: resp.StatusCode, Body: data}
	}
	return data, resp.Header.Get("Content-Type"), nil
}
