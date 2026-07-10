package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewRouter(t *testing.T) {
	r := NewRouter()
	if r == nil {
		t.Fatal("expected non-nil Router")
	}
}

func TestServeHTTPReturns404(t *testing.T) {
	r := NewRouter()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/nonexistent", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestServeHTTPServeMux(t *testing.T) {
	r := NewRouter()
	mux := r.Handler()
	if mux == nil {
		t.Fatal("expected non-nil Handler")
	}
}

func TestRegisterStreamEndpoint(t *testing.T) {
	r := NewRouter()
	r.RegisterStreamEndpoint(nil)
}

func TestRegisterEvolutionEndpoints(t *testing.T) {
	r := NewRouter()
	r.RegisterEvolutionEndpoints(nil)
}
