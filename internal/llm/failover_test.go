package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// mockLLMServer creates an httptest.Server that returns the given status code
// and body for all POST requests. Tracks call count via the returned counter.
func mockLLMServer(status int, body string) (*httptest.Server, *int32) {
	var count int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&count, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	return server, &count
}

// successBody returns a standard OpenRouter-format success response body.
func successBody(content string) string {
	return fmt.Sprintf(`{"choices":[{"message":{"content":"%s"}}]}`, content)
}

func TestFailoverClient_BasicFailover(t *testing.T) {
	// Primary returns 500, fallback returns 200.
	primary, primaryCount := mockLLMServer(500, `{"error":"internal"}`)
	defer primary.Close()
	fallback, fallbackCount := mockLLMServer(200, successBody("fallback-ok"))
	defer fallback.Close()

	fc, err := NewFailoverClient([]*Config{
		{Provider: "openrouter", APIKey: "key1", BaseURL: primary.URL, Model: "primary"},
		{Provider: "openrouter", APIKey: "key2", BaseURL: fallback.URL, Model: "fallback"},
	}, 10*time.Second, 0, 0)
	if err != nil {
		t.Fatalf("NewFailoverClient: %v", err)
	}

	resp, err := fc.Generate(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp != "fallback-ok" {
		t.Fatalf("expected fallback-ok, got %s", resp)
	}
	if atomic.LoadInt32(primaryCount) != 1 {
		t.Fatalf("expected primary called once, got %d", atomic.LoadInt32(primaryCount))
	}
	if atomic.LoadInt32(fallbackCount) != 1 {
		t.Fatalf("expected fallback called once, got %d", atomic.LoadInt32(fallbackCount))
	}
}

func TestFailoverClient_RateLimitCooldown(t *testing.T) {
	// Primary returns 429, fallback returns 200.
	primary, primaryCount := mockLLMServer(429, `{"error":"rate_limit_exceeded"}`)
	defer primary.Close()
	fallback, fallbackCount := mockLLMServer(200, successBody("fallback-ok"))
	defer fallback.Close()

	fc, err := NewFailoverClient([]*Config{
		{Provider: "openrouter", APIKey: "key1", BaseURL: primary.URL, Model: "primary"},
		{Provider: "openrouter", APIKey: "key2", BaseURL: fallback.URL, Model: "fallback"},
	}, 10*time.Second, 0, 0, WithCooldownDuration(5*time.Second))
	if err != nil {
		t.Fatalf("NewFailoverClient: %v", err)
	}

	// First call: primary 429 → fallback succeeds.
	resp, err := fc.Generate(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp != "fallback-ok" {
		t.Fatalf("expected fallback-ok, got %s", resp)
	}

	// Second call: primary should be skipped (cooled down), fallback called directly.
	resp, err = fc.Generate(context.Background(), "hello2")
	if err != nil {
		t.Fatalf("Generate (2nd): %v", err)
	}
	if resp != "fallback-ok" {
		t.Fatalf("expected fallback-ok, got %s", resp)
	}

	// Primary should have been called only once (skipped on 2nd call).
	if atomic.LoadInt32(primaryCount) != 1 {
		t.Fatalf("expected primary called once (cooled down), got %d", atomic.LoadInt32(primaryCount))
	}
	// Fallback should have been called twice.
	if atomic.LoadInt32(fallbackCount) != 2 {
		t.Fatalf("expected fallback called twice, got %d", atomic.LoadInt32(fallbackCount))
	}
}

func TestFailoverClient_CooldownExpiry(t *testing.T) {
	// Primary returns 429 first, then 200.
	var primaryCountVal int32
	primaryCount := &primaryCountVal
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(primaryCount, 1)
		w.Header().Set("Content-Type", "application/json")
		if count == 1 {
			w.WriteHeader(429)
			fmt.Fprint(w, `{"error":"rate limited"}`)
		} else {
			w.WriteHeader(200)
			fmt.Fprint(w, successBody("primary-ok"))
		}
	}))
	defer primary.Close()
	fallback, fallbackCount := mockLLMServer(200, successBody("fallback-ok"))
	defer fallback.Close()

	fc, err := NewFailoverClient([]*Config{
		{Provider: "openrouter", APIKey: "key1", BaseURL: primary.URL, Model: "primary"},
		{Provider: "openrouter", APIKey: "key2", BaseURL: fallback.URL, Model: "fallback"},
	}, 10*time.Second, 0, 0, WithCooldownDuration(200*time.Millisecond))
	if err != nil {
		t.Fatalf("NewFailoverClient: %v", err)
	}

	// First call: primary 429 → fallback.
	_, err = fc.Generate(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Wait for cooldown to expire.
	time.Sleep(300 * time.Millisecond)

	// Third call: primary should be tried again (cooldown expired) and succeed.
	resp, err := fc.Generate(context.Background(), "hello2")
	if err != nil {
		t.Fatalf("Generate (after cooldown): %v", err)
	}
	if resp != "primary-ok" {
		t.Fatalf("expected primary-ok, got %s", resp)
	}

	if atomic.LoadInt32(primaryCount) != 2 {
		t.Fatalf("expected primary called twice, got %d", atomic.LoadInt32(primaryCount))
	}

	if atomic.LoadInt32(fallbackCount) != 1 {
		t.Fatalf("expected fallback called once, got %d", atomic.LoadInt32(fallbackCount))
	}
}

func TestFailoverClient_NonRateLimitNoCooldown(t *testing.T) {
	// Primary returns 500 (not 429), fallback returns 200.
	// Primary should NOT be cooled down.
	primary, primaryCount := mockLLMServer(500, `{"error":"internal"}`)
	defer primary.Close()
	fallback, fallbackCount := mockLLMServer(200, successBody("fallback-ok"))
	defer fallback.Close()

	fc, err := NewFailoverClient([]*Config{
		{Provider: "openrouter", APIKey: "key1", BaseURL: primary.URL, Model: "primary"},
		{Provider: "openrouter", APIKey: "key2", BaseURL: fallback.URL, Model: "fallback"},
	}, 10*time.Second, 0, 0)
	if err != nil {
		t.Fatalf("NewFailoverClient: %v", err)
	}

	// First call: primary 500 → fallback.
	_, _ = fc.Generate(context.Background(), "hello")

	// Second call: primary should be tried again (no cooldown for 500).
	_, _ = fc.Generate(context.Background(), "hello2")

	// Primary should be called twice (no cooldown).
	if atomic.LoadInt32(primaryCount) != 2 {
		t.Fatalf("expected primary called twice (no cooldown), got %d", atomic.LoadInt32(primaryCount))
	}
	// Fallback should be called twice.
	if atomic.LoadInt32(fallbackCount) != 2 {
		t.Fatalf("expected fallback called twice, got %d", atomic.LoadInt32(fallbackCount))
	}
}

func TestFailoverClient_AllFail(t *testing.T) {
	primary, _ := mockLLMServer(500, `{"error":"internal"}`)
	defer primary.Close()
	fallback, _ := mockLLMServer(500, `{"error":"internal"}`)
	defer fallback.Close()

	fc, err := NewFailoverClient([]*Config{
		{Provider: "openrouter", APIKey: "key1", BaseURL: primary.URL, Model: "primary"},
		{Provider: "openrouter", APIKey: "key2", BaseURL: fallback.URL, Model: "fallback"},
	}, 10*time.Second, 0, 0)
	if err != nil {
		t.Fatalf("NewFailoverClient: %v", err)
	}

	_, err = fc.Generate(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error when all clients fail")
	}
}

func TestFailoverClient_PrimarySuccess(t *testing.T) {
	primary, primaryCount := mockLLMServer(200, successBody("primary-ok"))
	defer primary.Close()
	fallback, fallbackCount := mockLLMServer(200, successBody("fallback-ok"))
	defer fallback.Close()

	fc, err := NewFailoverClient([]*Config{
		{Provider: "openrouter", APIKey: "key1", BaseURL: primary.URL, Model: "primary"},
		{Provider: "openrouter", APIKey: "key2", BaseURL: fallback.URL, Model: "fallback"},
	}, 10*time.Second, 0, 0)
	if err != nil {
		t.Fatalf("NewFailoverClient: %v", err)
	}

	resp, err := fc.Generate(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp != "primary-ok" {
		t.Fatalf("expected primary-ok, got %s", resp)
	}
	if atomic.LoadInt32(primaryCount) != 1 {
		t.Fatalf("expected primary called once, got %d", atomic.LoadInt32(primaryCount))
	}
	if atomic.LoadInt32(fallbackCount) != 0 {
		t.Fatalf("expected fallback not called, got %d", atomic.LoadInt32(fallbackCount))
	}
}

func TestFailoverClient_ActiveProviders(t *testing.T) {
	primary, _ := mockLLMServer(429, `{"error":"rate limited"}`)
	defer primary.Close()
	fallback, _ := mockLLMServer(200, successBody("ok"))
	defer fallback.Close()

	fc, err := NewFailoverClient([]*Config{
		{Provider: "openrouter", APIKey: "key1", BaseURL: primary.URL, Model: "primary"},
		{Provider: "openrouter", APIKey: "key2", BaseURL: fallback.URL, Model: "fallback"},
	}, 10*time.Second, 0, 0, WithCooldownDuration(60*time.Second))
	if err != nil {
		t.Fatalf("NewFailoverClient: %v", err)
	}

	// Trigger cooldown on primary.
	_, _ = fc.Generate(context.Background(), "hello")

	active := fc.ActiveProviders()
	if len(active) != 1 {
		t.Fatalf("expected 1 active provider, got %d: %v", len(active), active)
	}
}

func TestFailoverClient_EmptyConfigs(t *testing.T) {
	_, err := NewFailoverClient(nil, 0, 0, 0)
	if err == nil {
		t.Fatal("expected error for empty configs")
	}
}

func TestIsRateLimitError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"HTTPError 429", &HTTPError{StatusCode: 429, Message: "rate limited"}, true},
		{"HTTPError 500", &HTTPError{StatusCode: 500, Message: "internal"}, false},
		{"message contains 429", fmt.Errorf("unexpected status code: 429"), true},
		{"message contains rate_limit", fmt.Errorf("rate_limit_exceeded"), true},
		{"message contains rate limit", fmt.Errorf("rate limit exceeded"), true},
		{"unrelated error", fmt.Errorf("connection refused"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRateLimitError(tt.err); got != tt.want {
				t.Fatalf("isRateLimitError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
