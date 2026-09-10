package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestEmbedHandler_HappyPath verifies the full request→upstream→response
// path, including prefix concatenation.
func TestEmbedHandler_HappyPath(t *testing.T) {
	var gotInput string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Model string `json:"model"`
			Input string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("upstream decode: %v", err)
		}
		gotInput = payload.Input
		_, _ = w.Write([]byte(`{"embeddings":[[0.1,0.2,0.3]]}`))
	}))
	defer upstream.Close()

	handler := embedHandler(&http.Client{Timeout: 5 * time.Second}, upstream.URL)
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodPost, "/embed",
		strings.NewReader(`{"text":"hello","prefix":"pre: "}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	if gotInput != "pre: hello" {
		t.Errorf("upstream input = %q, want %q", gotInput, "pre: hello")
	}
	var resp struct {
		Embedding []float64 `json:"embedding"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Embedding) != 3 {
		t.Errorf("embedding = %v, want 3 dims", resp.Embedding)
	}
}

// TestEmbedHandler_InvalidJSONFails is the #55 regression: a malformed body
// used to be ignored (json.Unmarshal error dropped), producing a request
// with an empty text instead of an explicit 400.
func TestEmbedHandler_InvalidJSONFails(t *testing.T) {
	upstreamCalled := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalled = true
		_, _ = w.Write([]byte(`{"embeddings":[[0.1]]}`))
	}))
	defer upstream.Close()

	handler := embedHandler(&http.Client{Timeout: 5 * time.Second}, upstream.URL)
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodPost, "/embed",
		strings.NewReader(`{not json`)))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d for invalid JSON, want 400", rec.Code)
	}
	if upstreamCalled {
		t.Error("upstream must not be called for an invalid request body")
	}
}

// TestEmbedHandler_UpstreamGarbageFails is the #55 regression: a non-JSON
// upstream response used to be silently ignored, yielding a zero-value
// "embedding" answer. It must surface as 502.
func TestEmbedHandler_UpstreamGarbageFails(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html>proxy error page</html>`))
	}))
	defer upstream.Close()

	handler := embedHandler(&http.Client{Timeout: 5 * time.Second}, upstream.URL)
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodPost, "/embed",
		strings.NewReader(`{"text":"hi"}`)))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d for garbage upstream, want 502", rec.Code)
	}
}

// TestEmbedHandler_UpstreamEmptyEmbeddings verifies the explicit
// "no embeddings" error path.
func TestEmbedHandler_UpstreamEmptyEmbeddings(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"embeddings":[]}`))
	}))
	defer upstream.Close()

	handler := embedHandler(&http.Client{Timeout: 5 * time.Second}, upstream.URL)
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodPost, "/embed",
		strings.NewReader(`{"text":"hi"}`)))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500 (no embeddings)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "no embeddings") {
		t.Fatalf("body %q must name the failure", rec.Body.String())
	}
}

// TestEmbedHandler_BoundedRequestRead is the #55 regression: the request
// body used to be read with an unbounded io.ReadAll, so a large body
// exhausted memory. The 10 MiB LimitReader makes the read fail (and the
// subsequent unmarshal reject) instead of buffering the whole stream.
func TestEmbedHandler_BoundedRequestRead(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"embeddings":[[0.1]]}`))
	}))
	defer upstream.Close()

	handler := embedHandler(&http.Client{Timeout: 5 * time.Second}, upstream.URL)

	// A body larger than the cap: JSON-invalid once truncated, and never
	// fully buffered.
	big := `{"text":"` + strings.Repeat("a", maxBridgeBodyBytes+1024) + `"}`
	rec := httptest.NewRecorder()
	handler(rec, httptest.NewRequest(http.MethodPost, "/embed", strings.NewReader(big)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d for over-cap body, want 400 (bounded read + unmarshal failure)", rec.Code)
	}
}
