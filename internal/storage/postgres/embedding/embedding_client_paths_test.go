package embedding

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestEmbeddingClientGetModelAndGetTimeout pins the trivial getters against
// constructor inputs.
func TestEmbeddingClientGetModelAndGetTimeout(t *testing.T) {
	timeout := 3 * time.Second
	client := NewEmbeddingClient("http://127.0.0.1:1", "stub-model-x", nil, timeout)

	if got := client.GetModel(); got != "stub-model-x" {
		t.Errorf("GetModel = %q, want stub-model-x", got)
	}
	if got := client.GetTimeout(); got != timeout {
		t.Errorf("GetTimeout = %v, want %v", got, timeout)
	}
}

// newStubEmbeddingServer builds an httptest server whose /embed_batch handler
// returns the given status and body.
func newStubEmbeddingServer(status int, body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embed_batch" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		if _, err := w.Write([]byte(body)); err != nil {
			// Handler has no *testing.T; response write failures surface as
			// client-side decode errors in the assertions below.
			return
		}
	}))
}

// TestEmbeddingClientCallEmbeddingBatchService_StatusError covers the
// non-200 branch: EmbedBatch must surface an *HTTPError with the status code.
func TestEmbeddingClientCallEmbeddingBatchService_StatusError(t *testing.T) {
	srv := newStubEmbeddingServer(http.StatusInternalServerError, `{"error":"boom"}`)
	defer srv.Close()

	client := NewEmbeddingClient(srv.URL, "stub-model", nil, time.Second)
	_, err := client.EmbedBatch(context.Background(), []string{"a", "b"})
	if err == nil {
		t.Fatal("expected error from 500 response, got nil")
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("err = %v, want *HTTPError", err)
	}
	if httpErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want 500", httpErr.StatusCode)
	}
}

// TestEmbeddingClientCallEmbeddingBatchService_InvalidJSON covers the decode
// failure branch.
func TestEmbeddingClientCallEmbeddingBatchService_InvalidJSON(t *testing.T) {
	srv := newStubEmbeddingServer(http.StatusOK, "not-json")
	defer srv.Close()

	client := NewEmbeddingClient(srv.URL, "stub-model", nil, time.Second)
	_, err := client.EmbedBatch(context.Background(), []string{"a"})
	if err == nil {
		t.Fatal("expected decode error, got nil")
	}
	if !strings.Contains(err.Error(), "decode response") {
		t.Errorf("err = %v, want wrapped decode failure", err)
	}
}

// TestEmbeddingClientCallEmbeddingBatchService_Accept drives the batch
// success path through the public EmbedBatch entry point.
func TestEmbeddingClientCallEmbeddingBatchService_Accept(t *testing.T) {
	srv := newStubEmbeddingServer(http.StatusOK,
		`{"embeddings":[[0.1],[0.2]],"dimension":1}`)
	defer srv.Close()

	client := NewEmbeddingClient(srv.URL, "stub-model", nil, time.Second)
	got, err := client.EmbedBatch(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d embeddings, want 2", len(got))
	}
	if len(got[0]) != 1 || got[0][0] != 0.1 || len(got[1]) != 1 || got[1][0] != 0.2 {
		t.Fatalf("embeddings = %v, want [[0.1],[0.2]] in input order", got)
	}
}

// TestEmbeddingClientHealthCheck_ErrorStatusAndConnRefused covers both
// HealthCheck failure arms: non-200 status and unreachable endpoint.
func TestEmbeddingClientHealthCheck_ErrorStatusAndConnRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	client := NewEmbeddingClient(srv.URL, "stub-model", nil, time.Second)
	err := client.HealthCheck(context.Background())
	if err == nil {
		t.Fatal("expected error from 503 health response, got nil")
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("err = %v, want *HTTPError", err)
	}
	if httpErr.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("StatusCode = %d, want 503", httpErr.StatusCode)
	}

	dead := NewEmbeddingClient("http://127.0.0.1:1", "stub-model", nil, time.Second)
	if err := dead.HealthCheck(context.Background()); err == nil {
		t.Fatal("expected connection error from dead endpoint, got nil")
	}
}

// TestEmbeddingClientDisabledBlocksBatchAndHealth pins the enabled-gate on
// the batch and health entry points.
func TestEmbeddingClientDisabledBlocksBatchAndHealth(t *testing.T) {
	client := NewEmbeddingClient("http://127.0.0.1:1", "stub-model", nil, time.Second)
	client.Disable()

	if client.IsEnabled() {
		t.Error("IsEnabled = true after Disable")
	}
	if _, err := client.EmbedBatch(context.Background(), []string{"a"}); err == nil {
		t.Error("EmbedBatch must fail when client is disabled")
	}
	if err := client.HealthCheck(context.Background()); err == nil {
		t.Error("HealthCheck must fail when client is disabled")
	}
	client.Enable()
	if !client.IsEnabled() {
		t.Error("IsEnabled = false after Enable")
	}
}
