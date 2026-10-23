package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStaticHandlerServesSPAForRoutesContainingDots(t *testing.T) {
	if !hasEmbeddedStatic() {
		t.Skip("web assets are not embedded")
	}
	handler := staticHandler()
	get := func(path, accept string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}

	page := get("/library/detail/Qwen%2FQwen2-0.5B-Instruct-GGUF", "text/html,application/xhtml+xml")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "<div id=") {
		t.Fatalf("page load with a dot in the route: status %d", page.Code)
	}
	if asset := get("/assets/missing-file.js", "*/*"); asset.Code != http.StatusNotFound {
		t.Fatalf("missing asset: status %d, want 404", asset.Code)
	}
	if route := get("/library/detail/plain", ""); route.Code != http.StatusOK {
		t.Fatalf("dot-free route: status %d", route.Code)
	}
}
