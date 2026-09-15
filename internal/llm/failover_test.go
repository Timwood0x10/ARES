package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
)

// mockLLMServer creates an httptest.Server that returns the given status code
// and body for all POST requests. Tracks call count via the returned counter.
func mockLLMServer(status int, body string) (*httptest.Server, *int32) {
	var count int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&count, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, body)
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

func TestFailoverClient_Primary429Cooldown(t *testing.T) {
	// Primary returns 429, fallback returns 200.
	// Primary gets 5s cooldown on 429.
	primary, primaryCount := mockLLMServer(429, `{"error":"rate_limit_exceeded"}`)
	defer primary.Close()
	fallback, fallbackCount := mockLLMServer(200, successBody("fallback-ok"))
	defer fallback.Close()

	fc, err := NewFailoverClient([]*Config{
		{Provider: "openrouter", APIKey: "key1", BaseURL: primary.URL, Model: "primary"},
		{Provider: "openrouter", APIKey: "key2", BaseURL: fallback.URL, Model: "fallback"},
	}, 10*time.Second, 0, 0, WithCooldownDuration(60*time.Second))
	if err != nil {
		t.Fatalf("NewFailoverClient: %v", err)
	}

	// First call: primary 429 → cooldown 5s → fallback succeeds.
	resp, err := fc.Generate(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp != "fallback-ok" {
		t.Fatalf("expected fallback-ok, got %s", resp)
	}

	// Second call immediately: primary cooled (5s), fallback used directly.
	resp, err = fc.Generate(context.Background(), "hello2")
	if err != nil {
		t.Fatalf("Generate (2nd): %v", err)
	}
	if resp != "fallback-ok" {
		t.Fatalf("expected fallback-ok, got %s", resp)
	}

	// Primary called once (cooled down on 2nd call).
	if atomic.LoadInt32(primaryCount) != 1 {
		t.Fatalf("expected primary called once (5s cooldown), got %d", atomic.LoadInt32(primaryCount))
	}
	// Fallback called twice.
	if atomic.LoadInt32(fallbackCount) != 2 {
		t.Fatalf("expected fallback called twice, got %d", atomic.LoadInt32(fallbackCount))
	}
}

func TestFailoverClient_FallbackCooldownExpiry(t *testing.T) {
	// Primary always fails. Fallback fails first, then succeeds after cooldown.
	primary, _ := mockLLMServer(500, `{"error":"internal"}`)
	defer primary.Close()
	var fallbackCountVal int32
	fallbackCount := &fallbackCountVal
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(fallbackCount, 1)
		w.Header().Set("Content-Type", "application/json")
		if count == 1 {
			w.WriteHeader(500)
			_, _ = fmt.Fprint(w, `{"error":"internal"}`)
		} else {
			w.WriteHeader(200)
			_, _ = fmt.Fprint(w, successBody("fallback-ok"))
		}
	}))
	defer fallback.Close()

	fc, err := NewFailoverClient([]*Config{
		{Provider: "openrouter", APIKey: "key1", BaseURL: primary.URL, Model: "primary"},
		{Provider: "openrouter", APIKey: "key2", BaseURL: fallback.URL, Model: "fallback"},
	}, 10*time.Second, 0, 0, WithCooldownDuration(200*time.Millisecond))
	if err != nil {
		t.Fatalf("NewFailoverClient: %v", err)
	}

	// First call: primary 500, fallback 500 → all fail.
	_, err = fc.Generate(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error when all fail")
	}

	// Wait for the fallback cooldown to actually expire. A fixed 300ms slept
	// past the 200ms cooldown by luck; polling the client's own cooldown map
	// waits for the real condition and fails loudly if it never clears.
	waitForCooldownsClear(t, fc)

	// Second call: primary fails again, fallback cooldown expired → retried → succeeds.
	resp, err := fc.Generate(context.Background(), "hello2")
	if err != nil {
		t.Fatalf("Generate (after cooldown): %v", err)
	}
	if resp != "fallback-ok" {
		t.Fatalf("expected fallback-ok, got %s", resp)
	}

	// Primary always tried (never cooled), fallback retried after cooldown.
	if atomic.LoadInt32(fallbackCount) != 2 {
		t.Fatalf("expected fallback called twice, got %d", atomic.LoadInt32(fallbackCount))
	}
}

func TestFailoverClient_AllErrorsCooldown(t *testing.T) {
	// Primary returns 500, fallback returns 500 → 200.
	// Both primary and fallback get cooldown on 500.
	primary, primaryCount := mockLLMServer(500, `{"error":"internal"}`)
	defer primary.Close()
	var fallbackCountVal int32
	fallbackCount := &fallbackCountVal
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(fallbackCount, 1)
		w.Header().Set("Content-Type", "application/json")
		if count == 1 {
			w.WriteHeader(500)
			_, _ = fmt.Fprint(w, `{"error":"internal"}`)
		} else {
			w.WriteHeader(200)
			_, _ = fmt.Fprint(w, successBody("fallback-ok"))
		}
	}))
	defer fallback.Close()

	fc, err := NewFailoverClient([]*Config{
		{Provider: "openrouter", APIKey: "key1", BaseURL: primary.URL, Model: "primary"},
		{Provider: "openrouter", APIKey: "key2", BaseURL: fallback.URL, Model: "fallback"},
	}, 10*time.Second, 0, 0, WithCooldownDuration(200*time.Millisecond))
	if err != nil {
		t.Fatalf("NewFailoverClient: %v", err)
	}

	// First call: primary 500 → cooldown, fallback 500 → cooldown. All fail.
	_, _ = fc.Generate(context.Background(), "hello")

	// Second call immediately: both cooled down → all fail (no retry).
	_, _ = fc.Generate(context.Background(), "hello2")

	// Primary called once (cooled down on 2nd call).
	if atomic.LoadInt32(primaryCount) != 1 {
		t.Fatalf("expected primary called once (cooled), got %d", atomic.LoadInt32(primaryCount))
	}
	// Fallback called once (cooled down on 2nd call).
	if atomic.LoadInt32(fallbackCount) != 1 {
		t.Fatalf("expected fallback called once (cooled), got %d", atomic.LoadInt32(fallbackCount))
	}

	// Wait for both cooldowns to actually expire (see waitForCooldownsClear).
	waitForCooldownsClear(t, fc)

	// Third call: both cooldowns expired, primary fails, fallback succeeds.
	resp, err := fc.Generate(context.Background(), "hello3")
	if err != nil {
		t.Fatalf("Generate (after cooldown): %v", err)
	}
	if resp != "fallback-ok" {
		t.Fatalf("expected fallback-ok, got %s", resp)
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
	// Both fail → both cooled down → no active providers.
	primary, _ := mockLLMServer(500, `{"error":"internal"}`)
	defer primary.Close()
	fallback, _ := mockLLMServer(500, `{"error":"internal"}`)
	defer fallback.Close()

	fc, err := NewFailoverClient([]*Config{
		{Provider: "openrouter", APIKey: "key1", BaseURL: primary.URL, Model: "primary"},
		{Provider: "openrouter", APIKey: "key2", BaseURL: fallback.URL, Model: "fallback"},
	}, 10*time.Second, 0, 0, WithCooldownDuration(60*time.Second))
	if err != nil {
		t.Fatalf("NewFailoverClient: %v", err)
	}

	// Both fail → both cooled down.
	_, _ = fc.Generate(context.Background(), "hello")

	active := fc.ActiveProviders()
	// Both cooled down → no active providers.
	if len(active) != 0 {
		t.Fatalf("expected 0 active providers (both cooled), got %d: %v", len(active), active)
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

// TestFailoverClient_StreamFirstChunkErrorFailsOver locks REVIEW 3.8: the
// first-chunk handshake check must treat a chunk carrying Err as a FAILED
// attempt. The old code only checked channel closure, so a provider whose
// HTTP handshake succeeded but whose stream immediately errored was marked
// successful — cooldown cleared, no failover, caller stuck with a dead
// stream.
func TestFailoverClient_StreamFirstChunkErrorFailsOver(t *testing.T) {
	// Primary: HTTP 200 (handshake OK) but a body that is not valid
	// NDJSON, so the client's very FIRST stream chunk carries Err.
	primary, primaryCount := mockLLMServer(200, "definitely-not-json")
	defer primary.Close()

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{"response":"fallback-stream","done":false}`+"\n"+`{"done":true}`+"\n")
	}))
	defer fallback.Close()

	fc, err := NewFailoverClient([]*Config{
		{Provider: "ollama", BaseURL: primary.URL, Model: "primary"},
		{Provider: "ollama", BaseURL: fallback.URL, Model: "fallback"},
	}, 5*time.Second, 0, 0)
	if err != nil {
		t.Fatalf("NewFailoverClient: %v", err)
	}
	defer fc.Close()

	ch, err := fc.GenerateStream(context.Background(), "hello")
	if err != nil {
		t.Fatalf("GenerateStream must fail over instead of returning the primary's stream error: %v", err)
	}

	var got string
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatalf("stream chunk carried error after failover: %v", chunk.Err)
		}
		got += chunk.Content
	}
	if got != "fallback-stream" {
		t.Fatalf("expected fallback-stream, got %q", got)
	}
	if atomic.LoadInt32(primaryCount) != 1 {
		t.Fatalf("expected primary called once, got %d", atomic.LoadInt32(primaryCount))
	}
}

// cooledKeys reports the provider keys currently in cooldown, read under the
// client's own lock. Tests use it to assert cooldown side effects directly.
func cooledKeys(fc *FailoverClient) []string {
	fc.mu.RLock()
	defer fc.mu.RUnlock()
	out := make([]string, 0, len(fc.cooldowns))
	for k := range fc.cooldowns {
		out = append(out, k)
	}
	return out
}

// waitForCooldownsClear blocks until every recorded cooldown has EXPIRED.
//
// It deliberately tests expiry rather than map emptiness: expired entries are
// removed lazily inside isCoolingDown, so a caller that only inspects the map
// sees stale keys forever and would hang. Reading the stored expiry
// timestamps is the condition the callers actually depend on.
//
// It replaces fixed time.Sleep calls that guessed at cooldown expiry: those
// slept a constant past the configured cooldown and passed by luck, and were
// load-sensitive.
func waitForCooldownsClear(t *testing.T, fc *FailoverClient) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		now := time.Now()
		// The snapshot (including its capacity hint) is built entirely under
		// the lock: reading len(fc.cooldowns) before RLock would race with
		// isCoolingDown pruning entries.
		var stillCooling []string
		fc.mu.RLock()
		stillCooling = make([]string, 0, len(fc.cooldowns))
		for k, expiry := range fc.cooldowns {
			if now.Before(expiry) {
				stillCooling = append(stillCooling, k)
			}
		}
		fc.mu.RUnlock()
		if len(stillCooling) == 0 {
			return
		}
		if now.After(deadline) {
			t.Fatalf("cooldowns never expired, still cooling: %v", stillCooling)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// cancelledFailoverClient builds a two-provider failover client over servers
// that always succeed, plus its key list in registration order.
func cancelledFailoverClient(t *testing.T) (*FailoverClient, *httptest.Server, *httptest.Server) {
	t.Helper()
	primary, _ := mockLLMServer(200, successBody("primary-ok"))
	t.Cleanup(primary.Close)
	fallback, _ := mockLLMServer(200, successBody("fallback-ok"))
	t.Cleanup(fallback.Close)
	fc, err := NewFailoverClient([]*Config{
		{Provider: "openrouter", APIKey: "key1", BaseURL: primary.URL, Model: "primary"},
		{Provider: "openrouter", APIKey: "key2", BaseURL: fallback.URL, Model: "fallback"},
	}, 10*time.Second, 0, 0)
	if err != nil {
		t.Fatalf("NewFailoverClient: %v", err)
	}
	t.Cleanup(fc.Close)
	return fc, primary, fallback
}

// TestFailoverClient_CancelledContextDoesNotPoisonCooldowns locks the
// caller-cancellation contract for Generate: aborting a request is not a
// provider fault, so no provider may be put into cooldown. Pre-fix, every
// provider in the chain was marked on the way through, so one user abort
// during a busy window made the next ~cooldownDuration of unrelated requests
// fail with "no provider available (all N cooled down)".
func TestFailoverClient_CancelledContextDoesNotPoisonCooldowns(t *testing.T) {
	fc, _, _ := cancelledFailoverClient(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := fc.Generate(ctx, "hello"); err == nil {
		t.Fatal("Generate on a cancelled context must fail")
	}
	if keys := cooledKeys(fc); len(keys) != 0 {
		t.Fatalf("caller cancellation must not cool down any provider, got %v", keys)
	}
}

// TestFailoverClient_MidCallCancelDoesNotPoisonCooldowns locks the same
// contract when the cancel lands while the request is in flight (the server
// sees the disconnect and the client surfaces context.Canceled), which is
// the shape a real user abort produces.
func TestFailoverClient_MidCallCancelDoesNotPoisonCooldowns(t *testing.T) {
	release := make(chan struct{})
	arrived := make(chan struct{})
	var arriveOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arriveOnce.Do(func() { close(arrived) })
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})
	fc, err := NewFailoverClient([]*Config{
		{Provider: "ollama", BaseURL: server.URL, Model: "slow"},
	}, 10*time.Second, 0, 0)
	if err != nil {
		t.Fatalf("NewFailoverClient: %v", err)
	}
	t.Cleanup(fc.Close)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = fc.Generate(ctx, "hello")
	}()
	// Wait until the server has actually received the request, then abort it
	// mid-flight. A fixed sleep here raced the dial under load: cancel could
	// land before the request left the client, and the test would then assert
	// the wrong path (pre-connect abort, not in-flight abort).
	select {
	case <-arrived:
	case <-time.After(10 * time.Second):
		t.Fatal("request never reached the server")
	}
	cancel()
	<-done

	if keys := cooledKeys(fc); len(keys) != 0 {
		t.Fatalf("mid-call caller cancel must not cool down any provider, got %v", keys)
	}
}

// TestFailoverClient_CancelledContextDoesNotPoisonCooldownsChat locks the
// Chat path, which has its own copy of the failover loop.
func TestFailoverClient_CancelledContextDoesNotPoisonCooldownsChat(t *testing.T) {
	fc, _, _ := cancelledFailoverClient(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	msgs := []*llmcore.LLMMessage{{Role: "user", Content: "hello"}}
	if _, err := fc.Chat(ctx, msgs, nil, nil); err == nil {
		t.Fatal("Chat on a cancelled context must fail")
	}
	if keys := cooledKeys(fc); len(keys) != 0 {
		t.Fatalf("caller cancellation must not cool down any provider on Chat, got %v", keys)
	}
}

// TestFailoverClient_CancelledContextDoesNotPoisonCooldownsStream locks the
// GenerateStream path, whose handshake loop marks cooldown on the initial
// GenerateStream error.
func TestFailoverClient_CancelledContextDoesNotPoisonCooldownsStream(t *testing.T) {
	fc, _, _ := cancelledFailoverClient(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := fc.GenerateStream(ctx, "hello"); err == nil {
		t.Fatal("GenerateStream on a cancelled context must fail")
	}
	if keys := cooledKeys(fc); len(keys) != 0 {
		t.Fatalf("caller cancellation must not cool down any provider on stream, got %v", keys)
	}
}
