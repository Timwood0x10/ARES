package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Timwood0x10/ares/internal/ares_security"
)

// TestDispatchActionRecoversFromPanic locks the Phase 3 panic guard: a
// panicking handler must produce a structured 500 carrying the request ID —
// not a dropped connection. net/http would keep the process alive, but the
// client would see a reset and the incident would leave no trace.
func TestDispatchActionRecoversFromPanic(t *testing.T) {
	h := &actionHandler{} // audit nil: the panic path must tolerate that too
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/boom", nil)
	req.Header.Set("X-Request-Id", "panic-trace-1")

	spec := routeSpec{
		Method: http.MethodPost,
		Path:   "/api/boom",
		Handler: func(*actionHandler, http.ResponseWriter, *http.Request, *ares_security.Principal) {
			panic("boom")
		},
	}
	h.dispatchAction(rec, req, nil, spec)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic response status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "panic-trace-1") {
		t.Fatalf("panic response must carry the request ID, got body: %s", rec.Body.String())
	}
}

// TestDispatchActionKeepsWrittenResponse verifies the guard does not
// double-write: a handler that already wrote a response body must keep it
// (the 500 only applies to an unwritten response).
func TestDispatchActionKeepsWrittenResponse(t *testing.T) {
	h := &actionHandler{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/boom", nil)

	spec := routeSpec{
		Method: http.MethodPost,
		Path:   "/api/boom",
		Handler: func(_ *actionHandler, w http.ResponseWriter, _ *http.Request, _ *ares_security.Principal) {
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"partial":"streamed"}`))
			panic("boom after write")
		},
	}
	h.dispatchAction(rec, req, nil, spec)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("already-written response must be preserved, got status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "streamed") {
		t.Fatalf("already-written body must be preserved, got: %s", rec.Body.String())
	}
}

// TestServeHTTPCanonicalizesRequestID locks the C-8 correlation contract:
// an inbound (sane) X-Request-Id is honored and echoed; oversized or
// non-printable values are replaced by a minted one; requests without one
// get one. Non-API paths fall through to the inner handler, which is enough
// to observe the header behavior.
func TestServeHTTPCanonicalizesRequestID(t *testing.T) {
	newHandler := func() *actionHandler {
		return &actionHandler{inner: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})}
	}

	t.Run("inbound id honored", func(t *testing.T) {
		h := newHandler()
		req := httptest.NewRequest(http.MethodGet, "/anything", nil)
		req.Header.Set("X-Request-Id", "client-trace-42")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get("X-Request-Id"); got != "client-trace-42" {
			t.Fatalf("echoed request id = %q, want inbound client-trace-42", got)
		}
	})

	t.Run("oversized id replaced", func(t *testing.T) {
		h := newHandler()
		req := httptest.NewRequest(http.MethodGet, "/anything", nil)
		req.Header.Set("X-Request-Id", strings.Repeat("A", 100))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get("X-Request-Id"); got == strings.Repeat("A", 100) || got == "" {
			t.Fatalf("oversized inbound id must be replaced by a minted one, got %q", got)
		}
	})

	t.Run("minted when absent", func(t *testing.T) {
		h := newHandler()
		req := httptest.NewRequest(http.MethodGet, "/anything", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get("X-Request-Id"); got == "" {
			t.Fatal("every response must carry a request id")
		}
	})
}

// TestValidatePprofAddr locks the loopback-only contract for the pprof
// listener: heap and goroutine dumps must never be reachable from a
// wildcard or routable address.
func TestValidatePprofAddr(t *testing.T) {
	for _, ok := range []string{"127.0.0.1:6060", "localhost:6060", "[::1]:6060"} {
		if err := validatePprofAddr(ok); err != nil {
			t.Errorf("validatePprofAddr(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{
		"0.0.0.0:6060",      // wildcard
		"192.168.1.10:6060", // routable LAN
		"example.com:6060",  // hostname (non-local)
		":6060",             // all-interfaces shorthand
		"not-an-address",    // unparseable
	} {
		if err := validatePprofAddr(bad); err == nil {
			t.Errorf("validatePprofAddr(%q) = nil, want error", bad)
		}
	}
}
