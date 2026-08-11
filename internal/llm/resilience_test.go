// Package llm provides tests for the retry/backoff and circuit-breaker
// resilience layer of the LLM client.
package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	aerrors "github.com/Timwood0x10/ares/internal/errors"
)

func TestRetryPolicy_Backoff(t *testing.T) {
	tests := []struct {
		name     string
		policy   RetryPolicy
		attempt  int
		wantLow  time.Duration
		wantHigh time.Duration
	}{
		{
			name: "first failure uses initial backoff",
			policy: RetryPolicy{
				InitialBackoff: 100 * time.Millisecond,
				MaxBackoff:     10 * time.Second,
				Factor:         2,
			},
			attempt:  1,
			wantLow:  80 * time.Millisecond,
			wantHigh: 120 * time.Millisecond,
		},
		{
			name: "second failure doubles",
			policy: RetryPolicy{
				InitialBackoff: 100 * time.Millisecond,
				MaxBackoff:     10 * time.Second,
				Factor:         2,
			},
			attempt:  2,
			wantLow:  160 * time.Millisecond,
			wantHigh: 240 * time.Millisecond,
		},
		{
			name: "capped at max backoff",
			policy: RetryPolicy{
				InitialBackoff: 100 * time.Millisecond,
				MaxBackoff:     150 * time.Millisecond,
				Factor:         2,
			},
			attempt:  5,
			wantLow:  120 * time.Millisecond,
			wantHigh: 180 * time.Millisecond, // cap 150ms + 20% jitter
		},
		{
			name: "non-positive attempt treated as first",
			policy: RetryPolicy{
				InitialBackoff: 100 * time.Millisecond,
				MaxBackoff:     10 * time.Second,
				Factor:         2,
			},
			attempt:  0,
			wantLow:  80 * time.Millisecond,
			wantHigh: 120 * time.Millisecond,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.policy.backoff(tt.attempt)
			if got < tt.wantLow || got > tt.wantHigh {
				t.Errorf("backoff(%d) = %v, want within [%v, %v]",
					tt.attempt, got, tt.wantLow, tt.wantHigh)
			}
		})
	}
}

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error is not retryable", nil, false},
		{"429 rate limit is retryable", &HTTPError{StatusCode: 429}, true},
		{"500 server error is retryable", &HTTPError{StatusCode: 500}, true},
		{"503 service unavailable is retryable", &HTTPError{StatusCode: 503}, true},
		{"400 bad request is not retryable", &HTTPError{StatusCode: 400}, false},
		{"404 not found is not retryable", &HTTPError{StatusCode: 404}, false},
		{"wrapped 429 is retryable", aerrors.Wrap(&HTTPError{StatusCode: 429}, "upstream"), true},
		{"transport error is retryable", &url.Error{Op: "Post", Err: errors.New("connection refused")}, true},
		{"plain error is not retryable", errors.New("decode failed"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryableError(tt.err); got != tt.want {
				t.Errorf("isRetryableError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	cb := NewCircuitBreaker(3, time.Minute)
	for i := 0; i < 3; i++ {
		if err := cb.Allow(); err != nil {
			t.Fatalf("Allow() attempt %d: unexpected error %v", i+1, err)
		}
		cb.RecordFailure()
	}
	if !cb.IsOpen() {
		t.Fatal("circuit should be open after 3 consecutive failures")
	}
	if err := cb.Allow(); err == nil {
		t.Fatal("Allow() should reject while open")
	} else if !errors.Is(err, aerrors.ErrCircuitBreakerOpen) {
		t.Fatalf("Allow() open error = %v, want ErrCircuitBreakerOpen", err)
	}
}

func TestCircuitBreaker_ResetsOnSuccess(t *testing.T) {
	cb := NewCircuitBreaker(3, time.Minute)
	cb.RecordFailure()
	cb.RecordFailure()
	if err := cb.Allow(); err != nil {
		t.Fatalf("Allow() before success: %v", err)
	}
	cb.RecordSuccess()
	// A success resets the consecutive failure count: two more failures must
	// NOT open the circuit.
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.IsOpen() {
		t.Fatal("circuit must not open after success reset + 2 failures (threshold 3)")
	}
}

func TestCircuitBreaker_HalfOpenProbeSuccessCloses(t *testing.T) {
	cb := NewCircuitBreaker(1, time.Millisecond)
	cb.RecordFailure() // opens
	if err := cb.Allow(); err == nil {
		t.Fatal("Allow() must reject while open before timeout")
	}
	time.Sleep(2 * time.Millisecond)
	if err := cb.Allow(); err != nil {
		t.Fatalf("Allow() should admit probe after open timeout: %v", err)
	}
	cb.RecordSuccess()
	cb.RecordSuccess() // second half-open success closes the circuit
	if cb.IsOpen() {
		t.Fatal("circuit should be closed after two half-open successes")
	}
}

func TestCircuitBreaker_HalfOpenProbeFailureReopens(t *testing.T) {
	cb := NewCircuitBreaker(2, time.Millisecond)
	cb.RecordFailure()
	cb.RecordFailure() // opens
	time.Sleep(2 * time.Millisecond)
	if err := cb.Allow(); err != nil {
		t.Fatalf("Allow() should admit probe after open timeout: %v", err)
	}
	cb.RecordFailure() // probe fails -> re-open
	if !cb.IsOpen() {
		t.Fatal("circuit must re-open after failed probe")
	}
}

func TestCircuitBreaker_HalfOpenSingleInflight(t *testing.T) {
	cb := NewCircuitBreaker(1, time.Millisecond)
	cb.RecordFailure()
	time.Sleep(2 * time.Millisecond)
	if err := cb.Allow(); err != nil {
		t.Fatalf("first probe should be admitted: %v", err)
	}
	// Second concurrent probe must be rejected while the first is in flight.
	if err := cb.Allow(); err == nil {
		t.Fatal("second probe must be rejected while one is in flight")
	}
}

func TestWithRetry_SucceedsImmediately(t *testing.T) {
	c := &Client{retryPolicy: DefaultRetryPolicy()}
	got, err := withRetry(c, context.Background(), func() (string, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("withRetry returned error: %v", err)
	}
	if got != "ok" {
		t.Errorf("withRetry result = %q, want %q", got, "ok")
	}
}

func TestWithRetry_RetriesThenSucceeds(t *testing.T) {
	c := &Client{retryPolicy: RetryPolicy{
		MaxAttempts:    3,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
		Factor:         2,
	}}
	attempts := 0
	got, err := withRetry(c, context.Background(), func() (string, error) {
		attempts++
		if attempts < 3 {
			return "", &HTTPError{StatusCode: http.StatusTooManyRequests, Message: "rate limited"}
		}
		return "recovered", nil
	})
	if err != nil {
		t.Fatalf("withRetry returned error: %v", err)
	}
	if got != "recovered" {
		t.Errorf("withRetry result = %q, want %q", got, "recovered")
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

func TestWithRetry_ExhaustsAttempts(t *testing.T) {
	c := &Client{retryPolicy: RetryPolicy{
		MaxAttempts:    3,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
		Factor:         2,
	}}
	attempts := 0
	wantErr := &HTTPError{StatusCode: http.StatusServiceUnavailable, Message: "down"}
	_, err := withRetry(c, context.Background(), func() (string, error) {
		attempts++
		return "", wantErr
	})
	if err == nil {
		t.Fatal("withRetry should return the last error")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("withRetry error = %v, want %v", err, wantErr)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
}

func TestWithRetry_NonRetryableReturnsImmediately(t *testing.T) {
	c := &Client{retryPolicy: DefaultRetryPolicy()}
	attempts := 0
	_, err := withRetry(c, context.Background(), func() (string, error) {
		attempts++
		return "", &HTTPError{StatusCode: http.StatusBadRequest, Message: "bad"}
	})
	if err == nil {
		t.Fatal("withRetry should return the error")
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (no retry for 4xx)", attempts)
	}
}

func TestWithRetry_ContextCancelledStopsRetry(t *testing.T) {
	c := &Client{retryPolicy: RetryPolicy{
		MaxAttempts:    5,
		InitialBackoff: time.Second,
		MaxBackoff:     time.Second,
		Factor:         2,
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	attempts := 0
	_, err := withRetry(c, ctx, func() (string, error) {
		attempts++
		return "", &HTTPError{StatusCode: http.StatusTooManyRequests, Message: "slow"}
	})
	if err == nil {
		t.Fatal("withRetry should return an error on cancellation")
	}
	if attempts >= 5 {
		t.Errorf("attempts = %d, want fewer than MaxAttempts when cancelled", attempts)
	}
}

func TestWithRetry_CircuitBreakerFailsFast(t *testing.T) {
	cb := NewCircuitBreaker(2, time.Minute)
	cb.RecordFailure()
	cb.RecordFailure() // open
	c := &Client{retryPolicy: DefaultRetryPolicy(), circuit: cb}
	attempts := 0
	_, err := withRetry(c, context.Background(), func() (string, error) {
		attempts++
		return "", nil
	})
	if !errors.Is(err, aerrors.ErrCircuitBreakerOpen) {
		t.Fatalf("withRetry error = %v, want ErrCircuitBreakerOpen", err)
	}
	if attempts != 0 {
		t.Errorf("attempts = %d, want 0 (breaker rejects before fn runs)", attempts)
	}
}

// newGenerateTestClient builds a real LLM client wired to a test server and
// returns it together with a request counter.
func newGenerateTestClient(t *testing.T, handler http.Handler) (*Client, *atomic.Int32) {
	t.Helper()

	var requests atomic.Int32
	// Wrap BEFORE creating the server so every request is counted.
	counting := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		handler.ServeHTTP(w, r)
	})
	server := httptest.NewServer(counting)
	t.Cleanup(server.Close)

	client, err := NewClient(&Config{
		Provider: "openai",
		BaseURL:  server.URL,
		Model:    "test-model",
		APIKey:   "test-key",
		Timeout:  5,
	}, WithRetryPolicy(RetryPolicy{
		MaxAttempts:    3,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
		Factor:         2,
	}), WithCircuitBreaker(NewCircuitBreaker(3, time.Minute)))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client, &requests
}

func TestClientGenerate_Retries429ThenSucceeds(t *testing.T) {
	call := 0
	client, requests := newGenerateTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call++
		if call == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limit"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"final answer"}}]}`))
	}))

	got, err := client.Generate(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	if got != "final answer" {
		t.Errorf("Generate result = %q, want %q", got, "final answer")
	}
	if requests.Load() != 2 {
		t.Errorf("requests = %d, want 2 (one 429 + one success)", requests.Load())
	}
}

func TestClientGenerate_ExhaustsOnPersistent429(t *testing.T) {
	client, requests := newGenerateTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limit"}`))
	}))

	_, err := client.Generate(context.Background(), "hello")
	if err == nil {
		t.Fatal("Generate should return an error after exhausting retries")
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("Generate error = %v, want HTTPError 429", err)
	}
	if requests.Load() != 3 {
		t.Errorf("requests = %d, want 3 (MaxAttempts)", requests.Load())
	}
}

func TestClientGenerate_CircuitBreakerOpensOn5xx(t *testing.T) {
	client, requests := newGenerateTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))

	// First call: 3 attempts (MaxAttempts) all fail with 500.
	if _, err := client.Generate(context.Background(), "hello"); err == nil {
		t.Fatal("first Generate should fail")
	}
	if requests.Load() != 3 {
		t.Errorf("first call requests = %d, want 3", requests.Load())
	}
	// Second call: breaker open, fails fast with zero requests.
	before := requests.Load()
	_, err := client.Generate(context.Background(), "hello")
	if !errors.Is(err, aerrors.ErrCircuitBreakerOpen) {
		t.Fatalf("second Generate error = %v, want ErrCircuitBreakerOpen", err)
	}
	if requests.Load() != before {
		t.Errorf("requests grew from %d to %d while breaker open", before, requests.Load())
	}
}
