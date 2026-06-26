// Package llm provides LLM client functionality for various providers.
package llm

import (
	"bufio"
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

	"github.com/Timwood0x10/ares/internal/callbacks"
	coreerrors "github.com/Timwood0x10/ares/internal/core/errors"
	"github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/observability"
	"github.com/Timwood0x10/ares/internal/ratelimit"
)

// Default configuration constants for LLM client.
const (
	defaultTimeoutSeconds = 60
	maxPromptLength       = 8192
	defaultStreamBuffer   = 64
	defaultMaxTokens      = 4096
)

// HTTPError represents an HTTP request error.
type HTTPError struct {
	StatusCode int
	Message    string
}

// Error returns the error message.
func (e *HTTPError) Error() string {
	return e.Message
}

// ProviderType represents the LLM provider type.
type ProviderType string

const (
	ProviderOpenAI     ProviderType = "openai"
	ProviderOpenRouter ProviderType = "openrouter"
	ProviderOllama     ProviderType = "ollama"
	ProviderAnthropic  ProviderType = "anthropic"

	// DefaultOllamaBaseURL is the default base URL for Ollama provider.
	DefaultOllamaBaseURL = "http://localhost:11434"

	// DefaultOpenRouterBaseURL is the default base URL for OpenRouter provider.
	DefaultOpenRouterBaseURL = "https://openrouter.ai/api/v1"

	// DefaultOllamaModel is the default model for Ollama provider.
	DefaultOllamaModel = "llama3.2"

	// DefaultOpenRouterModel is the default model for OpenRouter provider.
	DefaultOpenRouterModel = "openai/gpt-3.5-turbo"
)

// Config holds LLM client configuration.
type Config struct {
	Provider        string            `yaml:"provider"`
	APIKey          string            `yaml:"api_key"`
	BaseURL         string            `yaml:"base_url"`
	Model           string            `yaml:"model"`
	Timeout         int               `yaml:"timeout"`
	MaxTokens       int               `yaml:"max_tokens"`        // Maximum tokens in response (0 = use defaultMaxTokens)
	MaxPromptLength int               `yaml:"max_prompt_length"` // Maximum prompt characters (0 = use maxPromptLength default)
	Extra           map[string]string `yaml:"extra"`
}

// Client represents an LLM client that supports multiple providers.
type Client struct {
	config       *Config
	httpClient   *http.Client
	streamClient *http.Client // No Timeout — streaming uses context for cancellation.
	tracer       observability.Tracer
	callbacks    callbacks.Emitter // Optional: emits lifecycle events for LLM calls.
	limiter      ratelimit.Limiter // Optional: rate limiter for API calls.
	closeOnce    sync.Once         // Ensures Close() is idempotent and safe for concurrent calls.
}

// Option configures a Client instance during construction.
type Option func(*Client)

// WithCallbacks sets the callback emitter on the LLM client.
// When set, Generate and GenerateStream will emit lifecycle events.
func WithCallbacks(emitter callbacks.Emitter) Option {
	return func(c *Client) {
		c.callbacks = emitter
	}
}

// WithRateLimiter sets an optional rate limiter on the LLM client.
// When set, Generate and GenerateStream will call limiter.Wait(ctx)
// before making each API request, preventing the caller from exceeding
// the configured rate limit (e.g., token bucket or sliding window).
//
// Args:
//
//	limiter - the rate limiter to use (use ratelimit.NewTokenBucketLimiter, etc.).
//
// Returns:
//
//	Option - the configuration function.
func WithRateLimiter(limiter ratelimit.Limiter) Option {
	return func(c *Client) {
		c.limiter = limiter
	}
}

// Close releases idle HTTP connections held by the client.
// It is safe to call Close multiple times; subsequent calls are no-ops.
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		c.httpClient.CloseIdleConnections()
		c.streamClient.CloseIdleConnections()
	})
}

// SetTracer sets an optional observability tracer on the client.
// When set, Generate and GenerateStream will record LLM call spans.
func (c *Client) SetTracer(t observability.Tracer) {
	c.tracer = t
}

// NewClient creates a new LLM client with the given configuration.
// Args:
//   - config: LLM client configuration, must not be nil. Model, Provider, and
//     BaseURL are required fields (except Ollama which provides defaults).
//   - opts: optional client configuration functions.
//
// Returns:
//   - *Client: the configured LLM client.
//   - error: coreerrors.ErrInvalidArgument if config is nil or required fields
//     are missing, or an error describing which field is invalid.
func NewClient(config *Config, opts ...Option) (*Client, error) {
	if config == nil {
		return nil, fmt.Errorf("%w: config must not be nil", coreerrors.ErrInvalidArgument)
	}
	if config.Model == "" {
		return nil, fmt.Errorf("%w: model is required", coreerrors.ErrInvalidArgument)
	}
	if config.Provider == "" {
		return nil, fmt.Errorf("%w: provider is required", coreerrors.ErrInvalidArgument)
	}
	// BaseURL is required for non-Ollama providers; Ollama has a default.
	if config.BaseURL == "" && ProviderType(config.Provider) != ProviderOllama {
		return nil, fmt.Errorf("%w: base_url is required for provider %s", coreerrors.ErrInvalidArgument, config.Provider)
	}

	if config.Timeout <= 0 {
		config.Timeout = defaultTimeoutSeconds
	}

	c := &Client{
		config: config,
		httpClient: &http.Client{
			Timeout: time.Duration(config.Timeout) * time.Second,
		},
		// streamClient has no Timeout: for streaming, timeout is controlled
		// entirely via context so that the goroutine reading the body is
		// properly cancelled when the context expires.
		streamClient: &http.Client{
			Transport: http.DefaultTransport,
		},
	}

	for _, opt := range opts {
		opt(c)
	}

	return c, nil
}

// validatePrompt checks prompt constraints and records errors on failure.
// Returns nil on success, or an error describing the first violated constraint.
func (c *Client) validatePrompt(ctx context.Context, prompt string, start time.Time) error {
	if prompt == "" {
		err := coreerrors.ErrInvalidArgument
		c.recordLLMCall(ctx, prompt, "", 0, start, err)
		return err
	}
	trimmed := bytes.TrimSpace([]byte(prompt))
	if len(trimmed) == 0 {
		err := coreerrors.ErrInvalidArgument
		c.recordLLMCall(ctx, prompt, "", 0, start, err)
		return err
	}
	if len(prompt) > c.promptMaxLength() {
		err := fmt.Errorf("prompt exceeds maximum length of %d characters", c.promptMaxLength())
		c.recordLLMCall(ctx, prompt, "", 0, start, err)
		return err
	}
	return nil
}

// promptMaxLength returns the configured max prompt length, or the default.
func (c *Client) promptMaxLength() int {
	if c.config != nil && c.config.MaxPromptLength > 0 {
		return c.config.MaxPromptLength
	}
	return maxPromptLength
}

// Generate sends a text generation request to the LLM.
// Args:
// ctx - operation context.
// prompt - the prompt text.
// Returns generated text or error.
func (c *Client) Generate(ctx context.Context, prompt string) (string, error) {
	start := time.Now()
	model := ""
	if c.config != nil {
		model = c.config.Model
	}

	// Emit LLM start event.
	c.emitCallback(&callbacks.Context{
		Event: callbacks.EventLLMStart,
		Model: model,
		Input: prompt,
	})

	if err := c.validatePrompt(ctx, prompt, start); err != nil {
		c.emitCallback(&callbacks.Context{
			Event: callbacks.EventLLMError,
			Model: model,
			Input: prompt,
			Error: err,
		})
		return "", err
	}

	var result string
	var err error

	// Apply rate limiter before making the API call.
	if c.limiter != nil {
		if waitErr := c.limiter.Wait(ctx); waitErr != nil {
			c.recordLLMCall(ctx, prompt, "", 0, start, waitErr)
			c.emitCallback(&callbacks.Context{
				Event: callbacks.EventLLMError,
				Model: model,
				Input: prompt,
				Error: waitErr,
			})
			return "", waitErr
		}
	}

	switch ProviderType(c.config.Provider) {
	case ProviderOpenAI, ProviderOpenRouter:
		result, err = c.generateOpenRouter(ctx, prompt)
	case ProviderOllama:
		result, err = c.generateOllama(ctx, prompt)
	case ProviderAnthropic:
		result, err = c.generateAnthropic(ctx, prompt)
	default:
		err = fmt.Errorf("unsupported provider: %s", c.config.Provider)
	}

	duration := time.Since(start)
	c.recordLLMCall(ctx, prompt, result, 0, start, err)

	// Emit LLM end or error event.
	if err != nil {
		c.emitCallback(&callbacks.Context{
			Event:    callbacks.EventLLMError,
			Model:    model,
			Input:    prompt,
			Error:    err,
			Duration: duration,
		})
	} else {
		c.emitCallback(&callbacks.Context{
			Event:    callbacks.EventLLMEnd,
			Model:    model,
			Input:    prompt,
			Output:   result,
			Duration: duration,
		})
	}

	return result, err
}

// recordLLMCall records an LLM call via the tracer if set.
func (c *Client) recordLLMCall(ctx context.Context, prompt, response string, tokens int, start time.Time, err error) {
	if c.tracer == nil {
		return
	}
	model := ""
	if c.config != nil {
		model = c.config.Model
	}
	c.tracer.RecordLLMCall(ctx, &observability.LLMCall{
		TraceID:    c.tracer.GetTraceID(ctx),
		Model:      model,
		Prompt:     prompt,
		Response:   response,
		TokensUsed: tokens,
		Duration:   time.Since(start),
		Error:      err,
	})
}

// emitCallback emits a lifecycle event via the callback emitter if set.
func (c *Client) emitCallback(ctx *callbacks.Context) {
	if c.callbacks == nil {
		return
	}
	c.callbacks.Emit(ctx)
}

// generateOpenRouter generates text using OpenRouter API.
func (c *Client) generateOpenRouter(ctx context.Context, prompt string) (string, error) {
	if c.config.APIKey == "" {
		return "", fmt.Errorf("API key is required for OpenRouter")
	}

	// Use configured MaxTokens, fallback to defaultMaxTokens if not set or invalid.
	maxTokens := c.config.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	requestBody := map[string]interface{}{
		"model": c.config.Model,
		"messages": []map[string]string{
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"temperature": 0.7,
		"max_tokens":  maxTokens,
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return "", errors.Wrap(err, "marshal request")
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.config.BaseURL+"/chat/completions", bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", errors.Wrap(err, "create request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	// Privacy: Omit referer to avoid exposing repository details.
	req.Header.Set("X-Title", "GoAgent")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "send request")
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Error("failed to close response body: ", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			slog.Warn("llm: failed to read error response body", "error", readErr)
		}
		return "", fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
	}

	var response struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				Reasoning string `json:"reasoning"` // Some providers (e.g., Sensenova) use this field
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", errors.Wrap(err, "decode response")
	}

	if len(response.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	// Use content field, fallback to reasoning (for Sensenova and similar providers)
	result := response.Choices[0].Message.Content
	if result == "" {
		result = response.Choices[0].Message.Reasoning
	}

	return result, nil
}

// generateOllama generates text using Ollama API.
func (c *Client) generateOllama(ctx context.Context, prompt string) (string, error) {
	requestBody := map[string]interface{}{
		"model":  c.config.Model,
		"prompt": prompt,
		"stream": false,
		"options": map[string]interface{}{
			"temperature": 0.7,
			"num_predict": defaultMaxTokens,
		},
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return "", errors.Wrap(err, "marshal request")
	}

	baseURL := c.config.BaseURL
	if baseURL == "" {
		baseURL = DefaultOllamaBaseURL
	}

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/api/generate", bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", errors.Wrap(err, "create request")
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "send request")
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Error("failed to close response body: ", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			slog.Warn("llm: failed to read error response body", "error", readErr)
		}
		return "", &HTTPError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("unexpected status code: %d, body: %s", resp.StatusCode, string(body)),
		}
	}

	var response struct {
		Response string `json:"response"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", errors.Wrap(err, "decode response")
	}

	return response.Response, nil
}

// generateAnthropic generates text using Anthropic API.
// Anthropic uses a different API format: /v1/messages endpoint with required max_tokens.
func (c *Client) generateAnthropic(ctx context.Context, prompt string) (string, error) {
	if c.config.APIKey == "" {
		return "", fmt.Errorf("API key is required for Anthropic")
	}

	// Use configured MaxTokens, fallback to reasonable default for Anthropic (must be > 0).
	anthropicMaxTokens := c.config.MaxTokens
	if anthropicMaxTokens <= 0 {
		anthropicMaxTokens = 1024 // Anthropic requires max_tokens, default to 1024
	}

	requestBody := map[string]interface{}{
		"model": c.config.Model,
		"messages": []map[string]string{
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"max_tokens": anthropicMaxTokens, // Anthropic requires this field
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return "", errors.Wrap(err, "marshal anthropic request")
	}

	// Anthropic uses /v1/messages endpoint (not /v1/chat/completions like OpenAI)
	req, err := http.NewRequestWithContext(ctx, "POST", c.config.BaseURL+"/messages", bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", errors.Wrap(err, "create anthropic request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.config.APIKey)      // Anthropic uses x-api-key header
	req.Header.Set("anthropic-version", "2023-06-01") // Required API version header

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "send anthropic request")
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Error("failed to close anthropic response body: ", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			slog.Warn("llm: failed to read anthropic error response body", "error", readErr)
		}
		return "", fmt.Errorf("anthropic error (status %d): %s", resp.StatusCode, string(body))
	}

	// Anthropic response format: {"content": [{"type": "text", "text": "..."}]}
	var response struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", errors.Wrap(err, "decode anthropic response")
	}

	// Extract text from content blocks
	var result strings.Builder
	for _, block := range response.Content {
		if block.Type == "text" {
			result.WriteString(block.Text)
		}
	}

	return result.String(), nil
}

// IsEnabled checks if the LLM client is properly configured.
func (c *Client) IsEnabled() bool {
	if c == nil || c.config == nil {
		return false
	}

	switch ProviderType(c.config.Provider) {
	case ProviderOpenAI, ProviderOpenRouter, ProviderAnthropic:
		return c.config.APIKey != ""
	case ProviderOllama:
		return true // Ollama doesn't require API key
	default:
		return false
	}
}

// GetProvider returns the current provider type.
func (c *Client) GetProvider() string {
	if c.config != nil {
		return c.config.Provider
	}
	return ""
}

// GetModel returns the current model name.
func (c *Client) GetModel() string {
	if c.config != nil {
		return c.config.Model
	}
	return ""
}

// NewClientFromEnv creates an LLM client from environment variables.
func NewClientFromEnv() (*Client, error) {
	config := &Config{
		Provider: os.Getenv("LLM_PROVIDER"),
		APIKey:   os.Getenv("LLM_API_KEY"),
		BaseURL:  os.Getenv("LLM_BASE_URL"),
		Model:    os.Getenv("LLM_MODEL"),
	}

	// Set defaults
	if config.Provider == "" {
		config.Provider = "ollama"
	}
	if config.BaseURL == "" {
		if config.Provider == "openrouter" || config.Provider == "openai" {
			config.BaseURL = DefaultOpenRouterBaseURL
		} else {
			config.BaseURL = DefaultOllamaBaseURL
		}
	}
	if config.Model == "" {
		if config.Provider == "ollama" {
			config.Model = DefaultOllamaModel
		} else {
			config.Model = DefaultOpenRouterModel
		}
	}

	return NewClient(config)
}

// StreamChunk represents a single chunk in a streaming response.
type StreamChunk struct {
	Content string
	Done    bool
	Err     error
}

// GenerateStream sends a streaming text generation request.
// Returns a channel of StreamChunk that is closed when streaming completes.
func (c *Client) GenerateStream(ctx context.Context, prompt string) (<-chan StreamChunk, error) {
	start := time.Now()
	model := ""
	if c.config != nil {
		model = c.config.Model
	}

	if err := c.validatePrompt(ctx, prompt, start); err != nil {
		c.emitCallback(&callbacks.Context{
			Event: callbacks.EventLLMError,
			Model: model,
			Input: prompt,
			Error: err,
		})
		return nil, err
	}

	var rawCh <-chan StreamChunk
	var err error

	// Apply rate limiter before making the API call.
	if c.limiter != nil {
		if waitErr := c.limiter.Wait(ctx); waitErr != nil {
			c.recordLLMCall(ctx, prompt, "", 0, start, waitErr)
			c.emitCallback(&callbacks.Context{
				Event: callbacks.EventLLMError,
				Model: model,
				Input: prompt,
				Error: waitErr,
			})
			return nil, waitErr
		}
	}

	switch ProviderType(c.config.Provider) {
	case ProviderOpenAI, ProviderOpenRouter:
		rawCh, err = c.streamOpenRouter(ctx, prompt)
	case ProviderOllama:
		rawCh, err = c.streamOllama(ctx, prompt)
	case ProviderAnthropic:
		rawCh, err = c.streamAnthropic(ctx, prompt)
	default:
		err = fmt.Errorf("unsupported provider: %s", c.config.Provider)
	}

	if err != nil {
		c.recordLLMCall(ctx, prompt, "", 0, start, err)
		c.emitCallback(&callbacks.Context{
			Event: callbacks.EventLLMError,
			Model: model,
			Input: prompt,
			Error: err,
		})
		return nil, err
	}

	// Emit LLM start event here: all validation passed, streaming will actually begin.
	c.emitCallback(&callbacks.Context{
		Event: callbacks.EventLLMStart,
		Model: model,
		Input: prompt,
	})

	// Wrap the channel to record the LLM call when streaming completes.
	ch := make(chan StreamChunk, defaultStreamBuffer)
	go func() {
		defer close(ch)
		var builder strings.Builder
		var streamErr error
		for chunk := range rawCh {
			if chunk.Content != "" {
				builder.WriteString(chunk.Content)
			}
			if chunk.Err != nil {
				streamErr = chunk.Err
			}
			if chunk.Content != "" || chunk.Done {
				select {
				case ch <- chunk:
				case <-ctx.Done():
					return
				}
			}
			if chunk.Done {
				break
			}
		}
		fullResponse := builder.String()
		duration := time.Since(start)
		c.recordLLMCall(ctx, prompt, fullResponse, 0, start, streamErr)

		// Emit LLM end or error event for streaming.
		if streamErr != nil {
			c.emitCallback(&callbacks.Context{
				Event:    callbacks.EventLLMError,
				Model:    model,
				Input:    prompt,
				Error:    streamErr,
				Duration: duration,
			})
		} else {
			c.emitCallback(&callbacks.Context{
				Event:    callbacks.EventLLMEnd,
				Model:    model,
				Input:    prompt,
				Output:   fullResponse,
				Duration: duration,
			})
		}
	}()
	return ch, nil
}

// streamOllama streams text generation using Ollama API.
func (c *Client) streamOllama(ctx context.Context, prompt string) (<-chan StreamChunk, error) {
	requestBody := map[string]interface{}{
		"model":  c.config.Model,
		"prompt": prompt,
		"stream": true,
		"options": map[string]interface{}{
			"temperature": 0.7,
			"num_predict": defaultMaxTokens,
		},
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, errors.Wrap(err, "marshal stream request")
	}

	baseURL := c.config.BaseURL
	if baseURL == "" {
		baseURL = DefaultOllamaBaseURL
	}

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/api/generate", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, errors.Wrap(err, "create stream request")
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.streamClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "send stream request")
	}

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			slog.Warn("llm: failed to read error response body", "error", readErr)
		}
		if err := resp.Body.Close(); err != nil {
			slog.Warn("http: close response body failed", "error", err)
		}
		return nil, fmt.Errorf("ollama stream error (status %d): %s", resp.StatusCode, string(body))
	}

	ch := make(chan StreamChunk, defaultStreamBuffer)

	go func() {
		defer close(ch)
		defer func() {
			if err := resp.Body.Close(); err != nil {
				slog.Error("Failed to close stream response body", "error", err)
			}
		}()

		decoder := json.NewDecoder(resp.Body)
		for {
			var result struct {
				Response string `json:"response"`
				Done     bool   `json:"done"`
			}
			if err := decoder.Decode(&result); err != nil {
				if err != io.EOF {
					select {
					case ch <- StreamChunk{Done: true, Err: errors.Wrap(err, "decode stream chunk")}:
					case <-ctx.Done():
					}
				}
				return
			}

			if result.Done {
				return
			}

			select {
			case ch <- StreamChunk{Content: result.Response}:
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch, nil
}

// streamAnthropic streams text generation using Anthropic API.
// Anthropic streaming uses Server-Sent Events (SSE) with event: content_block_delta.
func (c *Client) streamAnthropic(ctx context.Context, prompt string) (<-chan StreamChunk, error) {
	if c.config.APIKey == "" {
		return nil, fmt.Errorf("API key is required for Anthropic streaming")
	}

	// Use configured MaxTokens, fallback to reasonable default for Anthropic.
	streamAnthropicMaxTokens := c.config.MaxTokens
	if streamAnthropicMaxTokens <= 0 {
		streamAnthropicMaxTokens = 1024
	}

	requestBody := map[string]interface{}{
		"model": c.config.Model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"max_tokens": streamAnthropicMaxTokens,
		"stream":     true, // Enable streaming mode for Anthropic
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, errors.Wrap(err, "marshal anthropic stream request")
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.config.BaseURL+"/messages", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, errors.Wrap(err, "create anthropic stream request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.config.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.streamClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "send anthropic stream request")
	}

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			slog.Warn("llm: failed to read anthropic stream error response body", "error", readErr)
		}
		_ = resp.Body.Close()
		return nil, fmt.Errorf("anthropic stream error (status %d): %s", resp.StatusCode, string(body))
	}

	ch := make(chan StreamChunk, defaultStreamBuffer)

	go func() {
		defer close(ch)
		defer func() {
			if err := resp.Body.Close(); err != nil {
				slog.Error("Failed to close anthropic stream response body", "error", err)
			}
		}()

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 1MB max line
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}

			// Anthropic SSE format: {"type": "content_block_delta", "delta": {"type": "text_delta", "text": "..."}}
			var event struct {
				Type  string `json:"type"`
				Delta struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"delta,omitempty"`
			}

			if err := json.Unmarshal([]byte(line), &event); err != nil {
				slog.Warn("Failed to unmarshal anthropic stream chunk", "error", err)
				continue
			}

			// Only extract text from content_block_delta events
			if event.Type == "content_block_delta" && event.Delta.Type == "text_delta" && event.Delta.Text != "" {
				select {
				case ch <- StreamChunk{Content: event.Delta.Text}:
				case <-ctx.Done():
					return
				}
			}

			// Check for message_stop event (end of stream)
			if event.Type == "message_stop" {
				return
			}
		}

		if err := scanner.Err(); err != nil {
			select {
			case ch <- StreamChunk{Done: true, Err: errors.Wrap(err, "read anthropic stream")}:
			case <-ctx.Done():
			}
		}
	}()

	return ch, nil
}

// streamOpenRouter streams text generation using OpenRouter API.
func (c *Client) streamOpenRouter(ctx context.Context, prompt string) (<-chan StreamChunk, error) {
	if c.config.APIKey == "" {
		return nil, fmt.Errorf("API key is required for OpenRouter streaming")
	}

	// Use configured MaxTokens, fallback to defaultMaxTokens if not set or invalid.
	streamMaxTokens := c.config.MaxTokens
	if streamMaxTokens <= 0 {
		streamMaxTokens = defaultMaxTokens
	}

	requestBody := map[string]interface{}{
		"model": c.config.Model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"temperature": 0.7,
		"max_tokens":  streamMaxTokens,
		"stream":      true,
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, errors.Wrap(err, "marshal stream request")
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.config.BaseURL+"/chat/completions", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, errors.Wrap(err, "create stream request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	req.Header.Set("X-Title", "GoAgent")

	resp, err := c.streamClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "send stream request")
	}

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			slog.Warn("llm: failed to read error response body", "error", readErr)
		}
		if err := resp.Body.Close(); err != nil {
			slog.Warn("http: close response body failed", "error", err)
		}
		return nil, fmt.Errorf("openrouter stream error (status %d): %s", resp.StatusCode, string(body))
	}

	ch := make(chan StreamChunk, defaultStreamBuffer)

	go func() {
		defer close(ch)
		defer func() {
			if err := resp.Body.Close(); err != nil {
				slog.Error("Failed to close stream response body", "error", err)
			}
		}()

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 1MB max line for large SSE chunks
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			if line == "data: [DONE]" {
				return
			}
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")

			var result struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
			}
			if err := json.Unmarshal([]byte(data), &result); err != nil {
				slog.Warn("Failed to unmarshal stream chunk", "error", err)
				continue
			}

			if len(result.Choices) == 0 {
				continue
			}

			select {
			case ch <- StreamChunk{Content: result.Choices[0].Delta.Content}:
			case <-ctx.Done():
				return
			}
		}

		if err := scanner.Err(); err != nil {
			select {
			case ch <- StreamChunk{Done: true, Err: errors.Wrap(err, "read stream")}:
			case <-ctx.Done():
			}
		}
	}()

	return ch, nil
}
