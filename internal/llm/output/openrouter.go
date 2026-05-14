package output

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/Timwood0x10/goagent/internal/core/models"
	"github.com/Timwood0x10/goagent/internal/errors"
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
	req.Header.Set("HTTP-Referer", "https://github.com/goagent")
	req.Header.Set("X-Title", "Agent Framework")

	resp, err := a.client.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "send request")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", errors.Newf("openrouter error: %s", respBody)
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
	req.Header.Set("HTTP-Referer", "https://github.com/goagent")
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
