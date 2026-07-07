package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_callbacks"
	"github.com/Timwood0x10/ares/internal/errors"
)

// validatePrompt checks prompt constraints and records errors on failure.
// Returns nil on success, or an error describing the first violated constraint.
func (c *Client) validatePrompt(ctx context.Context, prompt string, start time.Time) error {
	if prompt == "" {
		err := errors.ErrInvalidArgument
		c.recordLLMCall(ctx, prompt, "", 0, start, err)
		return err
	}
	trimmed := bytes.TrimSpace([]byte(prompt))
	if len(trimmed) == 0 {
		err := errors.ErrInvalidArgument
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
//
//	ctx - operation context.
//	prompt - the prompt text.
//
// Returns generated text or error.
func (c *Client) Generate(ctx context.Context, prompt string) (string, error) {
	start := time.Now()
	model := ""
	if c.config != nil {
		model = c.config.Model
	}

	c.emitCallback(&ares_callbacks.Context{
		Event: ares_callbacks.EventLLMStart,
		Model: model,
		Input: prompt,
	})

	if err := c.validatePrompt(ctx, prompt, start); err != nil {
		c.emitCallback(&ares_callbacks.Context{
			Event: ares_callbacks.EventLLMError,
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
			c.emitCallback(&ares_callbacks.Context{
				Event: ares_callbacks.EventLLMError,
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

	if err != nil {
		c.emitCallback(&ares_callbacks.Context{
			Event:    ares_callbacks.EventLLMError,
			Model:    model,
			Input:    prompt,
			Error:    err,
			Duration: duration,
		})
	} else {
		c.emitCallback(&ares_callbacks.Context{
			Event:    ares_callbacks.EventLLMEnd,
			Model:    model,
			Input:    prompt,
			Output:   result,
			Duration: duration,
		})
	}

	return result, err
}

// generateOpenRouter generates text using OpenRouter API.
func (c *Client) generateOpenRouter(ctx context.Context, prompt string) (string, error) {
	if c.config.APIKey == "" {
		return "", fmt.Errorf("API key is required for OpenRouter")
	}

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
	req.Header.Set("X-Title", "GoAgent")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "send request")
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Error("failed to close response body: ", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		if readErr != nil {
			log.Warn("llm: failed to read error response body", "error", readErr)
		}
		return "", &HTTPError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("openrouter error (status %d): %s", resp.StatusCode, string(body))}
	}

	var response struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				Reasoning string `json:"reasoning"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", errors.Wrap(err, "decode response")
	}

	if len(response.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

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
			log.Error("failed to close response body: ", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		if readErr != nil {
			log.Warn("llm: failed to read error response body", "error", readErr)
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

	anthropicMaxTokens := c.config.MaxTokens
	if anthropicMaxTokens <= 0 {
		anthropicMaxTokens = 1024
	}

	requestBody := map[string]interface{}{
		"model": c.config.Model,
		"messages": []map[string]string{
			{
				"role":    "user",
				"content": prompt,
			},
		},
		"max_tokens": anthropicMaxTokens,
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return "", errors.Wrap(err, "marshal anthropic request")
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.config.BaseURL+"/messages", bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", errors.Wrap(err, "create anthropic request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.config.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "send anthropic request")
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Error("failed to close anthropic response body: ", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		if readErr != nil {
			log.Warn("llm: failed to read anthropic error response body", "error", readErr)
		}
		return "", &HTTPError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("anthropic error (status %d): %s", resp.StatusCode, string(body))}
	}

	var response struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", errors.Wrap(err, "decode anthropic response")
	}

	var result strings.Builder
	for _, block := range response.Content {
		if block.Type == "text" {
			result.WriteString(block.Text)
		}
	}

	return result.String(), nil
}
