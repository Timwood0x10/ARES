package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestLongPollHandlerSurvivesServerWriteTimeout pins the control-plane
// timeout contract.
//
// Regression: the serve http.Server set WriteTimeout to 15s while
// POST /api/graphs is designed to wait up to collabTimeout (10 minutes) for
// its DAG to settle. Go's WriteDeadline starts when the request headers are
// read, so any graph running longer than 15s had its connection killed:
// the caller got a connection EOF while the handler kept executing
// server-side, losing the outputs and task ids and leaving the caller unable
// to tell success from failure. The handler must extend its own write
// deadline to match its wait bound.
func TestLongPollHandlerSurvivesServerWriteTimeout(t *testing.T) {
	// A server whose WriteTimeout is far SHORTER than the handler's wait, the
	// exact production shape.
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The fix under test: a long-poll handler extends its write deadline
		// before it starts waiting.
		extendWriteDeadline(w, 2*time.Second)

		select {
		case <-r.Context().Done():
			return
		case <-time.After(600 * time.Millisecond):
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	srv.Config.WriteTimeout = 100 * time.Millisecond
	srv.Start()
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("request failed: %v — the server write timeout cut off a handler that "+
			"was still legitimately working", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

// TestLongPollHandlerWithoutExtensionIsTruncated is the control: without the
// extension the same handler is killed, proving the test above actually
// exercises the timeout rather than passing vacuously.
func TestLongPollHandlerWithoutExtensionIsTruncated(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(600 * time.Millisecond):
		}
		w.WriteHeader(http.StatusOK)
	}))
	srv.Config.WriteTimeout = 100 * time.Millisecond
	srv.Start()
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected the write timeout to truncate a handler that never extends its deadlines")
	}
}
