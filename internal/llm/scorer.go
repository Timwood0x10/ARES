// Package llm provides LLM client functionality for various providers.
package llm

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Timwood0x10/ares/internal/ratelimit"
)

// FailoverScorer chains multiple LLM clients with automatic failover.
// Clients are tried in order (primary first, then fallbacks). If a client
// times out or returns an error, the next client is tried automatically.
//
// Usage:
//
//	scorer, _ := llm.NewFailoverScorer(configs, 30*time.Second, 10, 20)
//	resp, err := scorer.Generate(ctx, prompt)
//
// Integrates with config.LLMConfig.Fallbacks for declarative setup.
type FailoverScorer struct {
	clients []*Client
	timeout time.Duration
}

// NewFailoverScorer creates a FailoverScorer from a list of LLM configs.
// The first config is the primary client (gets rate limiting); subsequent
// configs are fallbacks tried in order on failure.
//
// Args:
//
//	configs  - list of LLM configs: configs[0] = primary, configs[1:] = fallbacks.
//	timeout  - per-call timeout applied to each client.
//	rate     - token bucket rate (req/s) for the primary client; 0 = no limiting.
//	burst    - token bucket burst size for the primary client.
//
// Returns an error if no clients could be created.
func NewFailoverScorer(configs []*Config, timeout time.Duration, rate float64, burst int) (*FailoverScorer, error) {
	if len(configs) == 0 {
		return nil, fmt.Errorf("at least one LLM config is required")
	}

	clients := make([]*Client, 0, len(configs))

	for i, cfg := range configs {
		var opts []Option

		// Rate limiting only on the primary client.
		if i == 0 && rate > 0 {
			limiter := ratelimit.NewTokenBucketLimiter(&ratelimit.LimiterConfig{
				Rate:  rate,
				Burst: burst,
			})
			opts = append(opts, WithRateLimiter(limiter))
		}

		client, err := NewClient(cfg, opts...)
		if err != nil {
			if i == 0 {
				return nil, fmt.Errorf("create primary LLM client: %w", err)
			}
			slog.Warn("FailoverScorer: failed to create fallback client, skipping",
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

	slog.Info("FailoverScorer created",
		"total_clients", len(clients),
		"fallback_count", len(clients)-1,
		"primary_model", clients[0].GetModel(),
		"timeout", timeout,
	)

	return &FailoverScorer{clients: clients, timeout: timeout}, nil
}

// Generate tries each LLM client in order and returns the first successful response.
// If all clients fail, returns an error with details of the last failure.
//
// Timeout behavior: The per-call timeout (fs.timeout) is applied via context.
// This timeout should be configured LARGER than each client's httpClient.Timeout
// to account for rate limiter wait time. Recommended: fs.timeout >= max(client.Timeout) + 10s.
func (fs *FailoverScorer) Generate(ctx context.Context, prompt string) (string, error) {
	var lastErr error

	for i, client := range fs.clients {
		// Apply per-call timeout via context. This covers rate limiter wait + HTTP request.
		cctx, cancel := context.WithTimeout(ctx, fs.timeout)
		resp, err := client.Generate(cctx, prompt)
		cancel()

		if err == nil {
			return resp, nil
		}

		lastErr = err

		// Don't log "trying next" for the last client.
		if i < len(fs.clients)-1 {
			slog.Warn("FailoverScorer: LLM client failed, trying next",
				"client_index", i,
				"model", client.GetModel(),
				"provider", client.GetProvider(),
				"error", err,
			)
		}
	}

	return "", fmt.Errorf("FailoverScorer: all %d clients failed; last error: %w",
		len(fs.clients), lastErr)
}

// Clients returns the underlying LLM clients (primary first, then fallbacks).
func (fs *FailoverScorer) Clients() []*Client {
	result := make([]*Client, len(fs.clients))
	copy(result, fs.clients)
	return result
}

// Timeout returns the per-call timeout.
func (fs *FailoverScorer) Timeout() time.Duration {
	return fs.timeout
}
