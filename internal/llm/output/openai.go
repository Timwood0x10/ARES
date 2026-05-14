package output

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/Timwood0x10/goagent/internal/core/models"
	"github.com/Timwood0x10/goagent/internal/errors"
)

// OpenAIAdapter implements LLMAdapter for OpenAI.
type OpenAIAdapter struct {
	config *Config
	client *http.Client
}

// NewOpenAIAdapter creates a new OpenAIAdapter.
func NewOpenAIAdapter(config *Config) *OpenAIAdapter {
	if config == nil {
		config = &Config{}
	}
	if config.BaseURL == "" {
		config.BaseURL = "https://api.openai.com/v1"
	}

	return &OpenAIAdapter{
		config: config,
		client: &http.Client{
			Timeout: time.Duration(config.Timeout) * time.Second,
		},
	}
}

// Generate generates text from prompt.
func (a *OpenAIAdapter) Generate(ctx context.Context, prompt string) (string, error) {
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

	resp, err := a.client.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "send request")
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Error("close response body failed", "err", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", errors.Wrap(errors.Newf("openai error: %s", respBody), "API request failed")
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
func (a *OpenAIAdapter) GenerateStructured(ctx context.Context, prompt string, schema string) (*models.RecommendResult, error) {
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

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "send request")
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Error("close response body failed", "err", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, errors.Wrap(err, "read response body")
		}
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(respBody))
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
func (a *OpenAIAdapter) GetModel() string {
	return a.config.Model
}

// OpenAIChatResponse represents OpenAI chat completion response.
type OpenAIChatResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// Choice represents a chat completion choice.
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// Message represents a chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Usage represents token usage.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
