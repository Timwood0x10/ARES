package router

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Timwood0x10/ares/internal/agents/base"
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

func TestAgentProcessorFuncType(t *testing.T) {
	fn := AgentProcessorFunc(func(ctx any, input any) (<-chan base.AgentEvent, error) {
		return nil, errors.New("not implemented")
	})
	_ = fn
}
