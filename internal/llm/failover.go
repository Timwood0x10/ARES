// Package llm provides LLM client functionality for various providers.
package llm

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/observability"
	"github.com/Timwood0x10/ares/internal/ratelimit"
)

// Default cooldown duration for rate-limited providers.
const defaultCooldownDuration = 60 * time.Second

// FailoverClient chains multiple LLM clients with automatic failover and
// rate-limit-aware cooldown. Clients are tried in order (primary first, then
// fallbacks). When a provider returns HTTP 429, it is marked as cooled down
// and skipped for a configurable duration.
//
// Usage:
//
//	client, _ := llm.NewFailoverClient(configs, 30*time.Second, 10, 20)
//	resp, err := client.Generate(ctx, prompt)
//
// Integrates with config.LLMConfig.Fallbacks for declarative setup.
type FailoverClient struct {
	clients          []*Client
	timeout          time.Duration
	cooldownDuration time.Duration
	mu               sync.RWMutex
	cooldowns        map[string]time.Time // provider+model → cooldown expiry
}

// FailoverOption configures a FailoverClient.
type FailoverOption func(*FailoverClient)

// WithCooldownDuration sets how long a rate-limited provider is skipped.
func WithCooldownDuration(d time.Duration) FailoverOption {
	return func(fc *FailoverClient) {
		fc.cooldownDuration = d
	}
}

// NewFailoverClient creates a FailoverClient from a list of LLM configs.
// The first config is the primary client (gets rate limiting); subsequent
// configs are fallbacks tried in order on failure.
//
// Args:
//
//	configs  - list of LLM configs: configs[0] = primary, configs[1:] = fallbacks.
//	timeout  - per-call timeout applied to each client.
//	rate     - token bucket rate (req/s) for the primary client; 0 = no limiting.
//	burst    - token bucket burst size for the primary client.
//	opts     - optional FailoverOption functions.
//
// Returns an error if no clients could be created.
func NewFailoverClient(configs []*Config, timeout time.Duration, rate float64, burst int, opts ...FailoverOption) (*FailoverClient, error) {
	if len(configs) == 0 {
		return nil, fmt.Errorf("at least one LLM config is required")
	}

	clients := make([]*Client, 0, len(configs))

	for i, cfg := range configs {
		var clientOpts []Option

		// Rate limiting only on the primary client.
		if i == 0 && rate > 0 {
			limiter := ratelimit.NewTokenBucketLimiter(&ratelimit.LimiterConfig{
				Rate:  rate,
				Burst: burst,
			})
			clientOpts = append(clientOpts, WithRateLimiter(limiter))
		}

		client, err := NewClient(cfg, clientOpts...)
		if err != nil {
			if i == 0 {
				return nil, fmt.Errorf("create primary LLM client: %w", err)
			}
			slog.Warn("FailoverClient: failed to create fallback client, skipping",
				"index", i,
				"model", cfg.Model,
				"provider", cfg.Provider,
				"error", err,
			)
			continue
		}
		clients = append(clients, client)
	}

	if len(clients) == 0 {
		return nil, fmt.Errorf("no LLM clients could be created")
	}

	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	fc := &FailoverClient{
		clients:          clients,
		timeout:          timeout,
		cooldownDuration: defaultCooldownDuration,
		cooldowns:        make(map[string]time.Time),
	}
	for _, opt := range opts {
		opt(fc)
	}

	slog.Info("FailoverClient created",
		"total_clients", len(clients),
		"fallback_count", len(clients)-1,
		"primary_model", clients[0].GetModel(),
		"timeout", timeout,
		"cooldown", fc.cooldownDuration,
	)

	return fc, nil
}

// NewFailoverScorer is a backward-compatible alias for NewFailoverClient.
func NewFailoverScorer(configs []*Config, timeout time.Duration, rate float64, burst int) (*FailoverClient, error) {
	return NewFailoverClient(configs, timeout, rate, burst)
}

// clientKey returns a unique key for cooldown tracking.
func (fc *FailoverClient) clientKey(c *Client) string {
	return c.GetProvider() + "/" + c.GetModel()
}

// isCooledDown returns true if the client is in a rate-limit cooldown.
// Expired entries are cleaned up eagerly to prevent unbounded map growth.
func (fc *FailoverClient) isCooledDown(key string) bool {
	fc.mu.RLock()
	expiry, ok := fc.cooldowns[key]
	fc.mu.RUnlock()
	if !ok {
		return false
	}
	if time.Now().Before(expiry) {
		return true
	}
	// Cooldown expired; clean up.
	fc.mu.Lock()
	delete(fc.cooldowns, key)
	fc.mu.Unlock()
	return false
}

// markCooldown records a rate-limit cooldown for the given client key.
func (fc *FailoverClient) markCooldown(key string) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.cooldowns[key] = time.Now().Add(fc.cooldownDuration)
}

// clearCooldown removes a cooldown on success.
func (fc *FailoverClient) clearCooldown(key string) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	delete(fc.cooldowns, key)
}

// Generate tries each LLM client in order and returns the first successful
// response. Rate-limited providers (HTTP 429) are cooled down and skipped
// for the configured cooldown duration. Non-rate-limit errors cause immediate
// failover without cooldown.
func (fc *FailoverClient) Generate(ctx context.Context, prompt string) (string, error) {
	var lastErr error

	for i, client := range fc.clients {
		key := fc.clientKey(client)

		// Skip cooled-down providers.
		if fc.isCooledDown(key) {
			slog.Debug("FailoverClient: skipping cooled-down provider",
				"provider", client.GetProvider(),
				"model", client.GetModel(),
			)
			continue
		}

		cctx, cancel := context.WithTimeout(ctx, fc.timeout)
		resp, err := client.Generate(cctx, prompt)
		cancel()

		if err == nil {
			fc.clearCooldown(key)
			return resp, nil
		}

		lastErr = err

		if isRateLimitError(err) {
			fc.markCooldown(key)
			slog.Warn("FailoverClient: rate limited, cooling down provider",
				"provider", client.GetProvider(),
				"model", client.GetModel(),
				"cooldown", fc.cooldownDuration,
			)
		} else if i < len(fc.clients)-1 {
			slog.Warn("FailoverClient: LLM client failed, trying next",
				"client_index", i,
				"model", client.GetModel(),
				"provider", client.GetProvider(),
				"error", err,
			)
		}
	}

	return "", fmt.Errorf("FailoverClient: all %d clients failed; last error: %w",
		len(fc.clients), lastErr)
}

// GenerateStream tries each LLM client in order and returns the first
// successful stream. Rate-limited providers are cooled down just like Generate.
func (fc *FailoverClient) GenerateStream(ctx context.Context, prompt string) (<-chan StreamChunk, error) {
	var lastErr error

	for i, client := range fc.clients {
		key := fc.clientKey(client)

		if fc.isCooledDown(key) {
			slog.Debug("FailoverClient: skipping cooled-down provider (stream)",
				"provider", client.GetProvider(),
				"model", client.GetModel(),
			)
			continue
		}

		ch, err := client.GenerateStream(ctx, prompt)
		if err == nil {
			fc.clearCooldown(key)
			return ch, nil
		}

		lastErr = err

		if isRateLimitError(err) {
			fc.markCooldown(key)
			slog.Warn("FailoverClient: rate limited on stream, cooling down provider",
				"provider", client.GetProvider(),
				"model", client.GetModel(),
				"cooldown", fc.cooldownDuration,
			)
		} else if i < len(fc.clients)-1 {
			slog.Warn("FailoverClient: stream client failed, trying next",
				"client_index", i,
				"model", client.GetModel(),
				"provider", client.GetProvider(),
				"error", err,
			)
		}
	}

	return nil, fmt.Errorf("FailoverClient: all %d stream clients failed; last error: %w",
		len(fc.clients), lastErr)
}

// IsEnabled returns true if the primary client is enabled.
func (fc *FailoverClient) IsEnabled() bool {
	if len(fc.clients) == 0 {
		return false
	}
	return fc.clients[0].IsEnabled()
}

// GetProvider returns the primary client's provider.
func (fc *FailoverClient) GetProvider() string {
	if len(fc.clients) == 0 {
		return ""
	}
	return fc.clients[0].GetProvider()
}

// GetModel returns the primary client's model.
func (fc *FailoverClient) GetModel() string {
	if len(fc.clients) == 0 {
		return ""
	}
	return fc.clients[0].GetModel()
}

// SetTracer sets the tracer on all underlying clients.
func (fc *FailoverClient) SetTracer(t observability.Tracer) {
	for _, c := range fc.clients {
		c.SetTracer(t)
	}
}

// Close closes all underlying clients.
func (fc *FailoverClient) Close() {
	for _, c := range fc.clients {
		c.Close()
	}
}

// Clients returns the underlying LLM clients (primary first, then fallbacks).
func (fc *FailoverClient) Clients() []*Client {
	result := make([]*Client, len(fc.clients))
	copy(result, fc.clients)
	return result
}

// Timeout returns the per-call timeout.
func (fc *FailoverClient) Timeout() time.Duration {
	return fc.timeout
}

// ActiveProviders returns the names of providers not currently cooled down.
func (fc *FailoverClient) ActiveProviders() []string {
	fc.mu.RLock()
	defer fc.mu.RUnlock()
	var active []string
	now := time.Now()
	for _, c := range fc.clients {
		key := fc.clientKey(c)
		if expiry, ok := fc.cooldowns[key]; !ok || now.After(expiry) {
			active = append(active, c.GetProvider()+":"+c.GetModel())
		}
	}
	return active
}

// FailoverScorer is a backward-compatible alias for FailoverClient.
type FailoverScorer = FailoverClient

// Ensure FailoverClient satisfies the common Generate interface.
var _ interface {
	Generate(ctx context.Context, prompt string) (string, error)
	GenerateStream(ctx context.Context, prompt string) (<-chan StreamChunk, error)
	IsEnabled() bool
	GetProvider() string
	GetModel() string
	Close()
} = (*FailoverClient)(nil)
