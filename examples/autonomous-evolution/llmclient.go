package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// LLMConfig holds the settings required to connect to an OpenAI-compatible LLM API.
type LLMConfig struct {
	APIKey         string  `yaml:"api_key"`
	Model          string  `yaml:"model"`
	BaseURL        string  `yaml:"base_url"`
	Temperature    float64 `yaml:"temperature"`
	MaxTokens      int     `yaml:"max_tokens"`
	TimeoutSeconds int     `yaml:"timeout_seconds"`
	Seed           int64   `yaml:"seed"`
}

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
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// chatEndpoint returns the full chat completions URL from the base URL.
// If baseURL already ends with /chat/completions, it's used directly.
func chatEndpoint(baseURL string) string {
	if strings.HasSuffix(baseURL, "/chat/completions") {
		return baseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	return baseURL + "/chat/completions"
}

// httpLLMClient implements apievol.LLMClient using an HTTP client.
type httpLLMClient struct {
	client      *http.Client
	apiKey      string
	model       string
	endpoint    string
	temperature float64
	maxTokens   int
	seed        *int64
}

// newHTTPLLMClient creates an httpLLMClient from an LLMConfig.
func newHTTPLLMClient(cfg LLMConfig) *httpLLMClient {
	timeout := 30 * time.Second
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

	return &httpLLMClient{
		client:      &http.Client{Timeout: timeout},
		apiKey:      cfg.APIKey,
		model:       cfg.Model,
		endpoint:    chatEndpoint(cfg.BaseURL),
		temperature: temp,
		maxTokens:   maxTok,
		seed:        seed,
	}
}

func (c *httpLLMClient) Generate(ctx context.Context, prompt string) (string, error) {
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

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	var parsed llmResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	if parsed.Error != nil {
		return "", fmt.Errorf("API error: %s", parsed.Error.Message)
	}

	if len(parsed.Choices) == 0 || parsed.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("empty response")
	}

	return parsed.Choices[0].Message.Content, nil
}

// loadLLMConfig reads the LLM configuration section from the YAML config files.
// It checks the same locations as loadProjectEvolutionConfig for consistency.
// Returns nil (not an error) if no LLM section is found.
func loadLLMConfig() (*LLMConfig, error) {
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

		var raw struct {
			LLM *LLMConfig `yaml:"llm"`
		}
		if err := yaml.Unmarshal(data, &raw); err != nil {
			continue
		}

		if raw.LLM != nil && raw.LLM.APIKey != "" && raw.LLM.BaseURL != "" {
			return raw.LLM, nil
		}
		return nil, nil
	}

	return nil, nil
}
