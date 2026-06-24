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
	"time"

	"github.com/Timwood0x10/ares/internal/callbacks"
	coreerrors "github.com/Timwood0x10/ares/internal/core/errors"
	"github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/observability"
	"github.com/Timwood0x10/ares/internal/ratelimit"
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
	Provider string            `yaml:"provider"`
	APIKey   string            `yaml:"api_key"`
	BaseURL  string            `yaml:"base_url"`
	Model    string            `yaml:"model"`
	Timeout  int               `yaml:"timeout"`
	Extra    map[string]string `yaml:"extra"`
}

// Client represents an LLM client that supports multiple providers.
type Client struct {
	config       *Config
	httpClient   *http.Client
	streamClient *http.Client // No Timeout — streaming uses context for cancellation.
	tracer       observability.Tracer
	callbacks    callbacks.Emitter // Optional: emits lifecycle events for LLM calls.
	limiter      ratelimit.Limiter // Optional: rate limiter for API calls.
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
func (c *Client) Close() {
	c.httpClient.CloseIdleConnections()
	c.streamClient.CloseIdleConnections()
}

// SetTracer sets an optional observability tracer on the client.
// When set, Generate and GenerateStream will record LLM call spans.
func (c *Client) SetTracer(t observability.Tracer) {
	c.tracer = t
}

// NewClient creates a new LLM client.
func NewClient(config *Config, opts ...Option) (*Client, error) {
	if config == nil {
		return nil, coreerrors.ErrInvalidArgument
	}

	if config.Timeout <= 0 {
		config.Timeout = 60
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

	// Validate prompt input
	if prompt == "" {
		err := coreerrors.ErrInvalidArgument
		c.recordLLMCall(ctx, prompt, "", 0, start, err)
		c.emitCallback(&callbacks.Context{
			Event: callbacks.EventLLMError,
			Model: model,
			Input: prompt,
			Error: err,
		})
		return "", err
	}

	// Check if prompt is too long (max 8192 characters)
	const maxPromptLength = 8192
	if len(prompt) > maxPromptLength {
		err := fmt.Errorf("prompt exceeds maximum length of %d characters", maxPromptLength)
		c.recordLLMCall(ctx, prompt, "", 0, start, err)
		c.emitCallback(&callbacks.Context{
			Event: callbacks.EventLLMError,
			Model: model,
			Input: prompt,
			Error: err,
		})
		return "", err
	}

	// Check if prompt contains only whitespace
	trimmed := []byte(prompt)
	trimmed = bytes.TrimSpace(trimmed)
	if len(trimmed) == 0 {
		err := coreerrors.ErrInvalidArgument
		c.recordLLMCall(ctx, prompt, "", 0, start, err)
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

	requestBody := map[string]interface{}{
		"model": c.config.Model,
		"messages": []map[string]string{
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"temperature": 0.7,
		"max_tokens":  4096, // Increased for code generation
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
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", errors.Wrap(err, "decode response")
	}

	if len(response.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	return response.Choices[0].Message.Content, nil
}

// generateOllama generates text using Ollama API.
func (c *Client) generateOllama(ctx context.Context, prompt string) (string, error) {
	requestBody := map[string]interface{}{
		"model":  c.config.Model,
		"prompt": prompt,
		"stream": false,
		"options": map[string]interface{}{
			"temperature": 0.7,
			"num_predict": 4096, // Increased for code generation
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

// IsEnabled checks if the LLM client is properly configured.
func (c *Client) IsEnabled() bool {
	if c == nil || c.config == nil {
		return false
	}

	switch ProviderType(c.config.Provider) {
	case ProviderOpenAI, ProviderOpenRouter:
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

	if prompt == "" {
		err := coreerrors.ErrInvalidArgument
		c.recordLLMCall(ctx, prompt, "", 0, start, err)
		c.emitCallback(&callbacks.Context{
			Event: callbacks.EventLLMError,
			Model: model,
			Input: prompt,
			Error: err,
		})
		return nil, err
	}

	trimmed := []byte(prompt)
	trimmed = bytes.TrimSpace(trimmed)
	if len(trimmed) == 0 {
		err := coreerrors.ErrInvalidArgument
		c.recordLLMCall(ctx, prompt, "", 0, start, err)
		c.emitCallback(&callbacks.Context{
			Event: callbacks.EventLLMError,
			Model: model,
			Input: prompt,
			Error: err,
		})
		return nil, err
	}

	const maxPromptLength = 8192
	if len(prompt) > maxPromptLength {
		err := fmt.Errorf("prompt exceeds maximum length of %d characters", maxPromptLength)
		c.recordLLMCall(ctx, prompt, "", 0, start, err)
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
	ch := make(chan StreamChunk, 64)
	go func() {
		defer close(ch)
		var fullResponse string
		var streamErr error
		for chunk := range rawCh {
			fullResponse += chunk.Content
			if chunk.Err != nil {
				streamErr = chunk.Err
			}
			if chunk.Done {
				break
			}
			select {
			case ch <- chunk:
			case <-ctx.Done():
				return
			}
		}
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
			"num_predict": 4096,
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
		_ = resp.Body.Close()
		return nil, fmt.Errorf("ollama stream error (status %d): %s", resp.StatusCode, string(body))
	}

	ch := make(chan StreamChunk, 64)

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

// streamOpenRouter streams text generation using OpenRouter API.
func (c *Client) streamOpenRouter(ctx context.Context, prompt string) (<-chan StreamChunk, error) {
	if c.config.APIKey == "" {
		return nil, fmt.Errorf("API key is required for OpenRouter streaming")
	}

	requestBody := map[string]interface{}{
		"model": c.config.Model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"temperature": 0.7,
		"max_tokens":  4096,
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
		_ = resp.Body.Close()
		return nil, fmt.Errorf("openrouter stream error (status %d): %s", resp.StatusCode, string(body))
	}

	ch := make(chan StreamChunk, 64)

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
