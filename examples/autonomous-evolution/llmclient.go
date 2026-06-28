package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// LLMProviderConfig holds the settings for a single LLM provider.
type LLMProviderConfig struct {
	APIKey         string  `yaml:"api_key"`
	Model          string  `yaml:"model"`
	BaseURL        string  `yaml:"base_url"`
	Temperature    float64 `yaml:"temperature"`
	MaxTokens      int     `yaml:"max_tokens"`
	TimeoutSeconds int     `yaml:"timeout_seconds"`
	Seed           int64   `yaml:"seed"`
}

// LLMTopConfig holds the full LLM configuration with primary + fallbacks.
type LLMTopConfig struct {
	Primary   LLMProviderConfig   `yaml:"primary"`
	Fallbacks []LLMProviderConfig `yaml:"fallbacks,omitempty"`
}

// llmRequest is the OpenAI-compatible chat completion request body.
type llmRequest struct {
	Model       string       `json:"model"`
	Messages    []llmMessage `json:"messages"`
	Temperature float64      `json:"temperature"`
	MaxTokens   int          `json:"max_tokens"`
	Seed        *int64       `json:"seed,omitempty"`
}

type llmMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type llmResponse struct {
	Choices []struct {
		Message struct {
			Content          string `json:"content"`
			Reasoning        string `json:"reasoning"`         // Sensenova, Stepfun
			ReasoningContent string `json:"reasoning_content"` // Stepfun alias
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// chatEndpoint returns the full chat completions URL from the base URL.
func chatEndpoint(baseURL string) string {
	if strings.HasSuffix(baseURL, "/chat/completions") {
		return baseURL
	}
	return strings.TrimRight(baseURL, "/") + "/chat/completions"
}

// providerClient is a single LLM provider's HTTP client.
type providerClient struct {
	name        string // provider:model for logging
	client      *http.Client
	apiKey      string
	model       string
	endpoint    string
	temperature float64
	maxTokens   int
	seed        *int64
}

func newProviderClient(cfg LLMProviderConfig) *providerClient {
	// HTTP client timeout is set high; actual timeout is controlled by
	// context.WithTimeout in failoverLLMClient.Generate.
	timeout := 60 * time.Second
	if cfg.TimeoutSeconds > 0 {
		timeout = time.Duration(cfg.TimeoutSeconds) * time.Second
	}
	temp := 0.3
	if cfg.Temperature > 0 {
		temp = cfg.Temperature
	}
	maxTok := 500
	if cfg.MaxTokens > 0 {
		maxTok = cfg.MaxTokens
	}
	var seed *int64
	if cfg.Seed != 0 {
		s := cfg.Seed
		seed = &s
	}
	return &providerClient{
		name:        cfg.BaseURL + ":" + cfg.Model,
		client:      &http.Client{Timeout: timeout},
		apiKey:      cfg.APIKey,
		model:       cfg.Model,
		endpoint:    chatEndpoint(cfg.BaseURL),
		temperature: temp,
		maxTokens:   maxTok,
		seed:        seed,
	}
}

func (c *providerClient) generate(ctx context.Context, prompt string) (string, error) {
	payload := llmRequest{
		Model: c.model,
		Messages: []llmMessage{
			{Role: "system", Content: "You are an AI strategy evaluation engine."},
			{Role: "user", Content: prompt},
		},
		Temperature: c.temperature,
		MaxTokens:   c.maxTokens,
		Seed:        c.seed,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("API request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return "", &rateLimitError{status: resp.StatusCode, msg: string(respBody)}
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var parsed llmResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("API error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("empty response")
	}

	// Use content field, fallback to reasoning (for Sensenova, Stepfun, and similar providers).
	result := parsed.Choices[0].Message.Content
	if result == "" {
		result = parsed.Choices[0].Message.Reasoning
	}
	if result == "" {
		result = parsed.Choices[0].Message.ReasoningContent
	}
	if result == "" {
		return "", fmt.Errorf("empty response")
	}

	return result, nil
}

// rateLimitError indicates HTTP 429 from the provider.
type rateLimitError struct {
	status int
	msg    string
}

func (e *rateLimitError) Error() string {
	return fmt.Sprintf("rate limited (status %d): %s", e.status, e.msg)
}

// failoverLLMClient chains multiple providers with automatic failover.
// Cooldown policy:
//   - HTTP 429 (rate limit) → cool down for 30s
//   - Timeout / connection error → cool down for 15s
//   - Empty response / parse error → cool down for 10s
//   - Other HTTP errors → cool down for 10s
//   - Success → clear cooldown immediately
type failoverLLMClient struct {
	providers []*providerClient
	timeout   time.Duration // per-provider timeout; 0 = 5s
	mu        sync.RWMutex
	cooldowns map[string]time.Time // provider name → expiry
}

// newFailoverLLMClient creates a failover client from primary + fallback configs.
// Per-provider timeout defaults to 5s if not specified.
func newFailoverLLMClient(primary LLMProviderConfig, fallbacks []LLMProviderConfig) *failoverLLMClient {
	providers := []*providerClient{newProviderClient(primary)}
	for _, fb := range fallbacks {
		providers = append(providers, newProviderClient(fb))
	}
	slog.Info("failover LLM client created",
		"providers", len(providers),
		"primary", providers[0].name,
		"timeout", 20*time.Second,
	)
	return &failoverLLMClient{
		providers: providers,
		timeout:   20 * time.Second, // stepfun needs ~12s per request
		cooldowns: make(map[string]time.Time),
	}
}

func (fc *failoverLLMClient) isCooledDown(name string) bool {
	fc.mu.RLock()
	expiry, ok := fc.cooldowns[name]
	fc.mu.RUnlock()
	if !ok {
		return false
	}
	if time.Now().Before(expiry) {
		return true
	}
	fc.mu.Lock()
	delete(fc.cooldowns, name)
	fc.mu.Unlock()
	return false
}

func (fc *failoverLLMClient) markCooldown(name string, d time.Duration) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.cooldowns[name] = time.Now().Add(d)
}

func (fc *failoverLLMClient) clearCooldown(name string) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	delete(fc.cooldowns, name)
}

// Generate tries each provider in order. Failed providers are cooled down
// for 30s so subsequent calls skip them instead of waiting for the same
// timeout/429 again.
func (fc *failoverLLMClient) Generate(ctx context.Context, prompt string) (string, error) {
	timeout := fc.timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}

	var lastErr error
	for _, p := range fc.providers {
		if fc.isCooledDown(p.name) {
			slog.Debug("failover: skipping cooled-down provider", "provider", p.name)
			continue
		}
		// Apply per-provider timeout so we don't wait 30s for a dead provider.
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		result, err := p.generate(callCtx, prompt)
		cancel()
		if err == nil {
			fc.clearCooldown(p.name)
			return result, nil
		}
		lastErr = err

		// All errors trigger cooldown so the next call skips this provider
		// instead of waiting for the same timeout/429 again.
		cd := 30 * time.Second
		if _, ok := err.(*rateLimitError); ok {
			slog.Warn("failover: rate limited, cooling down",
				"provider", p.name,
				"cooldown", cd,
			)
		} else {
			slog.Warn("failover: provider failed, cooling down",
				"provider", p.name,
				"cooldown", cd,
				"error", err,
			)
		}
		fc.markCooldown(p.name, cd)
	}
	return "", fmt.Errorf("failover: all %d providers failed; last error: %w",
		len(fc.providers), lastErr)
}

// loadLLMConfig reads the LLM configuration section from the YAML config files.
// Supports both the new primary/fallbacks format and the legacy single-provider format.
func loadLLMConfig() (*LLMTopConfig, error) {
	locations := []string{
		"config/config.yaml",
		"../config/config.yaml",
		"../../config.yaml",
		"examples/autonomous-evolution/config/config.yaml",
	}

	for _, loc := range locations {
		data, err := os.ReadFile(loc)
		if err != nil {
			continue
		}

		// Try new format first (primary + fallbacks).
		var raw struct {
			LLM *LLMTopConfig `yaml:"llm"`
		}
		if err := yaml.Unmarshal(data, &raw); err == nil && raw.LLM != nil {
			if raw.LLM.Primary.APIKey != "" && raw.LLM.Primary.BaseURL != "" {
				return raw.LLM, nil
			}
		}

		// Try legacy format (flat single provider).
		var legacy struct {
			LLM *LLMProviderConfig `yaml:"llm"`
		}
		if err := yaml.Unmarshal(data, &legacy); err == nil && legacy.LLM != nil {
			if legacy.LLM.APIKey != "" && legacy.LLM.BaseURL != "" {
				return &LLMTopConfig{Primary: *legacy.LLM}, nil
			}
		}
	}

	return nil, nil
}
