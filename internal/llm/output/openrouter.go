package output

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/errors"
)

// OpenRouterAdapter implements LLMAdapter for OpenRouter.
// OpenRouter is compatible with OpenAI API, so it reuses most of OpenAIAdapter logic.
type OpenRouterAdapter struct {
	config       *Config
	client       *http.Client
	streamClient *http.Client
}

// NewOpenRouterAdapter creates a new OpenRouterAdapter.
func NewOpenRouterAdapter(config *Config) *OpenRouterAdapter {
	if config == nil {
		config = &Config{}
	}
	if config.BaseURL == "" {
		config.BaseURL = "https://openrouter.ai/api/v1"
	}
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 60
	}

	return &OpenRouterAdapter{
		config: config,
		client: &http.Client{
			Timeout: time.Duration(timeout) * time.Second,
		},
		streamClient: &http.Client{
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// Generate generates text from prompt.
func (a *OpenRouterAdapter) Generate(ctx context.Context, prompt string) (string, error) {
	messages := []map[string]string{
		{keyRole: "user", keyContent: prompt},
	}

	reqBody := map[string]interface{}{
		keyModel:       a.config.Model,
		keyMessages:    messages,
		keyMaxTokens:   a.config.MaxTokens,
		keyTemperature: a.config.Temperature,
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
	req.Header.Set("HTTP-Referer", "https://github.com/Timwood0x10/ares")
	req.Header.Set("X-Title", "Agent Framework")

	resp, err := a.client.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "send request")
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Warn("openrouter: close response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
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

// GenerateWithParams generates text; per-call parameter overrides are
// not supported by this adapter, so it delegates to Generate.
func (a *OpenRouterAdapter) GenerateWithParams(ctx context.Context, prompt string, _ map[string]any) (string, error) {
	return a.Generate(ctx, prompt)
}

// GenerateStructured generates structured output.
func (a *OpenRouterAdapter) GenerateStructured(ctx context.Context, prompt string, schema string) (*models.RecommendResult, error) {
	messages := []map[string]interface{}{
		{
			keyRole:    "user",
			keyContent: prompt + "\n\nRespond with valid JSON only, matching this schema:\n" + schema,
		},
	}

	reqBody := map[string]interface{}{
		keyModel:       a.config.Model,
		keyMessages:    messages,
		keyMaxTokens:   a.config.MaxTokens,
		keyTemperature: a.config.Temperature,
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
	req.Header.Set("HTTP-Referer", "https://github.com/Timwood0x10/ares")
	req.Header.Set("X-Title", "Agent Framework")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "send request")
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Warn("openrouter: close response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
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
		keyModel:       a.config.Model,
		keyMessages:    []map[string]string{{keyRole: "user", keyContent: prompt}},
		keyMaxTokens:   a.config.MaxTokens,
		keyTemperature: a.config.Temperature,
		keyStream:      true,
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
	req.Header.Set("HTTP-Referer", "https://github.com/Timwood0x10/ares")
	req.Header.Set("X-Title", "Agent Framework")

	// Timeout is controlled via the request context, not the client.
	resp, err := a.streamClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "send stream request")
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		if err := resp.Body.Close(); err != nil {
			log.Warn("openrouter: close stream error response body", "error", err)
		}
		return nil, errors.Newf("openrouter stream error (status %d): %s", resp.StatusCode, string(respBody))
	}

	ch := make(chan StreamChunk, 64)

	go func() {
		defer close(ch)
		defer func() {
			if err := resp.Body.Close(); err != nil {
				log.Error("Failed to close stream response body", "error", err)
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
			if line == streamDataDone {
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
				log.Warn("Failed to unmarshal stream chunk", "error", err)
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
