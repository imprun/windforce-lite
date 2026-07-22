package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/imprun/windforce-core/internal/state"
)

func TestServerServesEveryHTTPPlaneAndWebUI(t *testing.T) {
	handler := New(Config{
		Store: state.NewLocalStore(filepath.Join(t.TempDir(), "state.json")),
	})

	assertHandlerStatus(t, handler, "/api/w/default/openapi.json", http.StatusOK)
	assertHandlerStatus(t, handler, "/ui/", http.StatusOK)
	assertHandlerStatus(t, handler, "/execution/v1/openapi.json", http.StatusOK)
	assertHandlerStatus(t, handler, "/api/w/default/state?path=runtime/value", http.StatusOK)
	assertHandlerMethodStatus(t, handler, http.MethodPost, "/api/v1/w/default/run/example/run", http.StatusUnauthorized)
	assertHandlerMethodStatus(t, handler, http.MethodPost, "/worker/v1/claims", http.StatusBadRequest)
}

func assertHandlerStatus(t *testing.T, handler http.Handler, path string, want int) {
	t.Helper()
	assertHandlerMethodStatus(t, handler, http.MethodGet, path, want)
}

func assertHandlerMethodStatus(t *testing.T, handler http.Handler, method string, path string, want int) {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(method, path, nil))
	if response.Code != want {
		t.Fatalf("%s %s status = %d, want %d; body=%s", method, path, response.Code, want, response.Body.String())
	}
}
