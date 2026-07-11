package server

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestWebUIServedWithoutAPIAuth(t *testing.T) {
	handler := New(Config{EnableAPI: true, AdminToken: "secret"})

	root := httptest.NewRecorder()
	handler.ServeHTTP(root, httptest.NewRequest(http.MethodGet, "/", nil))
	if root.Code != http.StatusFound {
		t.Fatalf("root status = %d, want %d", root.Code, http.StatusFound)
	}
	if got := root.Header().Get("Location"); got != "/ui/" {
		t.Fatalf("root location = %q, want /ui/", got)
	}

	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/ui/", nil))
	if page.Code != http.StatusOK {
		t.Fatalf("ui status = %d, want %d", page.Code, http.StatusOK)
	}
	if !strings.Contains(page.Body.String(), "windforce-lite") {
		t.Fatalf("ui page did not contain product name")
	}

	assetPath := regexp.MustCompile(`src="/ui/([^"]+\.js)"`).FindStringSubmatch(page.Body.String())
	if len(assetPath) != 2 {
		t.Fatalf("ui page did not reference a Next.js script asset")
	}
	script := httptest.NewRecorder()
	handler.ServeHTTP(script, httptest.NewRequest(http.MethodGet, "/ui/"+assetPath[1], nil))
	if script.Code != http.StatusOK {
		t.Fatalf("ui script status = %d, want %d", script.Code, http.StatusOK)
	}

	api := httptest.NewRecorder()
	handler.ServeHTTP(api, httptest.NewRequest(http.MethodGet, "/api/w/default/apps", nil))
	if api.Code != http.StatusUnauthorized {
		t.Fatalf("api status = %d, want %d", api.Code, http.StatusUnauthorized)
	}
}
