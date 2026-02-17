package wsbase

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCorsHandlerSetsCORSHeaders(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := CorsHandler(inner)
	req := httptest.NewRequest("GET", "http://localhost/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, "*")
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want %q", got, "no-store")
	}
}

func TestCorsHandlerCallsNextHandler(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
	})

	handler := CorsHandler(inner)
	req := httptest.NewRequest("GET", "http://localhost/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected inner handler to be called")
	}
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status code = %d, want %d", rec.Code, http.StatusTeapot)
	}
}
