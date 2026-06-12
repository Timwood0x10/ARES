package output

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"goagentx/internal/core/models"
	"goagentx/internal/errors"
)

// OpenRouterAdapter implements LLMAdapter for OpenRouter.
// OpenRouter is compatible with OpenAI API, so it reuses most of OpenAIAdapter logic.
type OpenRouterAdapter struct {
	config *Config
	client *http.Client
}

// NewOpenRouterAdapter creates a new OpenRouterAdapter.
func NewOpenRouterAdapter(config *Config) *OpenRouterAdapter {
	if config == nil {
		config = &Config{}
	}
	if config.BaseURL == "" {
		config.BaseURL = "https://openrouter.ai/api/v1"
	}

	return &OpenRouterAdapter{
		config: config,
		client: &http.Client{
			Timeout: time.Duration(config.Timeout) * time.Second,
		},
	}
}

// Generate generates text from prompt.
func (a *OpenRouterAdapter) Generate(ctx context.Context, prompt string) (string, error) {
	messages := []map[string]string{
		{"role": "user", "content": prompt},
	}

	reqBody := map[string]interface{}{
		"model":       a.config.Model,
		"messages":    messages,
		"max_tokens":  a.config.MaxTokens,
		"temperature": a.config.Temperature,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", errors.Wrap(err, "marshal request")
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		a.config.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", errors.Wrap(err, "create request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.config.APIKey)
	req.Header.Set("HTTP-Referer", "https://github.com/Timwood0x10/GoAgentX")
	req.Header.Set("X-Title", "Agent Framework")

	resp, err := a.client.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "send request")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", errors.Newf("API request failed with status %d: %s", resp.StatusCode, respBody)
	}

	var result OpenAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", errors.Wrap(err, "decode response")
	}

	if len(result.Choices) == 0 {
		return "", ErrInvalidResponse
	}

	return result.Choices[0].Message.Content, nil
}

// GenerateStructured generates structured output.
func (a *OpenRouterAdapter) GenerateStructured(ctx context.Context, prompt string, schema string) (*models.RecommendResult, error) {
	messages := []map[string]interface{}{
		{
			"role":    "user",
			"content": prompt + "\n\nRespond with valid JSON only, matching this schema:\n" + schema,
		},
	}

	reqBody := map[string]interface{}{
		"model":       a.config.Model,
		"messages":    messages,
		"max_tokens":  a.config.MaxTokens,
		"temperature": a.config.Temperature,
		"response_format": map[string]string{
			"type": "json_object",
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, errors.Wrap(err, "marshal request")
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		a.config.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, errors.Wrap(err, "create request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.config.APIKey)
	req.Header.Set("HTTP-Referer", "https://github.com/Timwood0x10/GoAgentX")
	req.Header.Set("X-Title", "Agent Framework")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "send request")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, errors.Newf("openrouter error: %s", respBody)
	}

	var result OpenAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, errors.Wrap(err, "decode response")
	}

	if len(result.Choices) == 0 {
		return nil, ErrInvalidResponse
	}

	parser := NewParser()
	return parser.ParseRecommendResult(result.Choices[0].Message.Content)
}

// GetModel returns the model name.
func (a *OpenRouterAdapter) GetModel() string {
	return a.config.Model
}

// GenerateStream generates text as a stream of chunks using OpenRouter API.
func (a *OpenRouterAdapter) GenerateStream(ctx context.Context, prompt string) (<-chan StreamChunk, error) {
	if prompt == "" {
		return nil, errors.New("empty prompt")
	}

	reqBody := map[string]interface{}{
		"model":       a.config.Model,
		"messages":    []map[string]string{{"role": "user", "content": prompt}},
		"max_tokens":  a.config.MaxTokens,
		"temperature": a.config.Temperature,
		"stream":      true,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, errors.Wrap(err, "marshal stream request")
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		a.config.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, errors.Wrap(err, "create stream request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.config.APIKey)
	req.Header.Set("HTTP-Referer", "https://github.com/Timwood0x10/GoAgentX")
	req.Header.Set("X-Title", "Agent Framework")

	// Use a client without Timeout for streaming: http.Client.Timeout covers
	// the entire response body read, which would kill long-running streams.
	// Instead, timeout is controlled via the request context.
	streamClient := &http.Client{Transport: http.DefaultTransport}
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "send stream request")
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return nil, errors.Newf("openrouter stream error (status %d): %s", resp.StatusCode, string(respBody))
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

			// Skip empty lines.
			if line == "" {
				continue
			}

			// Check for stream termination.
			if line == "data: [DONE]" {
				return
			}

			// Strip "data: " prefix.
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")

			var chunk OpenAIChatResponse
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				// Log and skip malformed chunks instead of aborting.
				slog.Warn("Failed to unmarshal stream chunk", "error", err)
				continue
			}

			if len(chunk.Choices) == 0 {
				continue
			}

			content := chunk.Choices[0].Delta.Content

			select {
			case ch <- StreamChunk{Content: content}:
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
