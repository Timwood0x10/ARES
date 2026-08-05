// Package core_test exercises the public core API surface, including the
// LLM service fallback behavior exposed through api/service/llm.
//
// This file lives in package core_test (external test package) so it can
// import api/service/llm without creating an import cycle (api/service/llm
// depends on api/core). The existing llm_test.go uses package core for
// internal access; both packages coexist in the same test binary.
package core_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/api/core"
	llmsvc "github.com/Timwood0x10/ares/api/service/llm"
)

// openAISuccessBody returns an OpenAI-style chat completion JSON body with the
// given content. The underlying llm.Client parses choices[0].message.content.
func openAISuccessBody(content string) string {
	return fmt.Sprintf(`{"choices":[{"message":{"content":%q}}]}`, content)
}

// countingHandler returns an http.Handler that always writes the given status
// and body, and atomically counts how many requests it received.
func countingHandler(status int, body string, counter *int32) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(counter, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, body)
	})
}

// hangingHandler returns an http.Handler that blocks until either blockCh is
// closed or the request context is cancelled (e.g. when the HTTP client
// timeout fires). This simulates a slow provider without leaking goroutines:
// when the client times out the request context is cancelled and the handler
// returns immediately.
func hangingHandler(blockCh <-chan struct{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-blockCh:
			w.WriteHeader(http.StatusGatewayTimeout)
		case <-r.Context().Done():
			// Client gave up (timeout); return so the goroutine exits.
			return
		}
	})
}

// newFallbackService builds an api/service/llm.Service whose primary config
// points at primaryURL and whose single fallback points at fallbackURL.
// The per-client HTTP timeout (timeoutSec) is applied to both providers so a
// hanging primary fails fast instead of stalling the test.
func newFallbackService(t *testing.T, primaryURL, fallbackURL string, timeoutSec int) *llmsvc.Service {
	t.Helper()
	cfg := &llmsvc.Config{
		LLMConfig: &core.LLMConfig{
			Provider: core.LLMProviderOpenRouter,
			APIKey:   "test-key",
			BaseURL:  primaryURL,
			Model:    "primary-model",
			Timeout:  timeoutSec,
		},
		Fallbacks: []*core.LLMConfig{
			{
				Provider: core.LLMProviderOpenRouter,
				APIKey:   "test-key",
				BaseURL:  fallbackURL,
				Model:    "fallback-model",
				Timeout:  timeoutSec,
			},
		},
	}
	svc, err := llmsvc.NewService(cfg)
	if err != nil {
		t.Fatalf("llmsvc.NewService: %v", err)
	}
	t.Cleanup(svc.Close)
	return svc
}

// TestLLMFallback verifies that when the primary LLM provider is unavailable
// (hanging or returning 504 Gateway Timeout), the service falls back to the
// fallback provider and returns its response without surfacing the primary
// error to the caller.
//
// No time.Sleep is used for synchronization: the 504 case returns immediately,
// and the hang case relies on the per-client HTTP timeout (1s) to fail the
// primary request. The assertion is always on the returned response content.
func TestLLMFallback(t *testing.T) {
	t.Parallel()

	const fallbackContent = "from-fallback"
	fallbackBody := openAISuccessBody(fallbackContent)

	tests := []struct {
		name       string
		timeoutSec int
		// setupPrimary creates the primary httptest.Server and registers its
		// cleanup (including closing any blocking channel) on t.Cleanup.
		setupPrimary func(t *testing.T) *httptest.Server
	}{
		{
			// Primary returns 504 Gateway Timeout immediately; the service
			// should skip it and use the fallback.
			name:       "primary_504_immediate",
			timeoutSec: 10,
			setupPrimary: func(t *testing.T) *httptest.Server {
				var count int32
				srv := httptest.NewServer(countingHandler(
					http.StatusGatewayTimeout, `{"error":"timeout"}`, &count))
				t.Cleanup(srv.Close)
				return srv
			},
		},
		{
			// Primary hangs; the per-client HTTP timeout (1s) fires, the
			// request fails, and the service falls back.
			name:       "primary_hangs_http_timeout",
			timeoutSec: 1,
			setupPrimary: func(t *testing.T) *httptest.Server {
				blockCh := make(chan struct{})
				srv := httptest.NewServer(hangingHandler(blockCh))
				t.Cleanup(func() {
					close(blockCh)
					srv.Close()
				})
				return srv
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			primarySrv := tt.setupPrimary(t)

			var fallbackCount int32
			fallbackSrv := httptest.NewServer(countingHandler(
				http.StatusOK, fallbackBody, &fallbackCount))
			t.Cleanup(fallbackSrv.Close)

			svc := newFallbackService(t, primarySrv.URL, fallbackSrv.URL, tt.timeoutSec)

			// Bound the overall test; the real synchronization is the primary
			// HTTP timeout or immediate 504, not this context deadline.
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			resp, err := svc.Generate(ctx, &core.GenerateRequest{
				Messages: []*core.LLMMessage{
					{Role: "user", Content: "hello"},
				},
			})
			if err != nil {
				t.Fatalf("Generate returned error (expected fallback): %v", err)
			}
			if resp == nil {
				t.Fatal("Generate returned nil response")
			}
			if resp.Content != fallbackContent {
				t.Errorf("Content = %q, want %q (fallback)", resp.Content, fallbackContent)
			}
			if got := atomic.LoadInt32(&fallbackCount); got < 1 {
				t.Errorf("fallback call count = %d, want >= 1", got)
			}
		})
	}
}
