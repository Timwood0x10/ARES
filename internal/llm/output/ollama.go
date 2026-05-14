package output

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/Timwood0x10/goagent/internal/core/models"
	gerr "github.com/Timwood0x10/goagent/internal/errors"
)

// Ollama errors.
var (
	ErrInvalidResponse = stderrors.New("invalid response")
)

// OllamaAdapter implements LLMAdapter for Ollama.
type OllamaAdapter struct {
	config *Config
	client *http.Client
}

// NewOllamaAdapter creates a new OllamaAdapter.
func NewOllamaAdapter(config *Config) *OllamaAdapter {
	if config == nil {
		config = &Config{}
	}
	if config.BaseURL == "" {
		config.BaseURL = "http://localhost:11434"
	}

	return &OllamaAdapter{
		config: config,
		client: &http.Client{
			Timeout: time.Duration(config.Timeout) * time.Second,
		},
	}
}

// Generate generates text from prompt.
func (a *OllamaAdapter) Generate(ctx context.Context, prompt string) (string, error) {
	reqBody := map[string]interface{}{
		"model":       a.config.Model,
		"prompt":      prompt,
		"stream":      false,
		"temperature": a.config.Temperature,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", gerr.Wrap(err, "marshal request")
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		a.config.BaseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", gerr.Wrap(err, "create request")
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return "", gerr.Wrap(err, "send request")
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Error("Failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", gerr.Wrap(err, "read response body")
		}
		return "", gerr.Wrapf(err, "API request failed: ollama error: %s", respBody)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", gerr.Wrap(err, "decode response")
	}

	response, ok := result["response"].(string)
	if !ok {
		return "", ErrInvalidResponse
	}

	return response, nil
}

// GenerateStructured generates structured output.
func (a *OllamaAdapter) GenerateStructured(ctx context.Context, prompt string, schema string) (*models.RecommendResult, error) {
	fullPrompt := prompt + "\n\nRespond with valid JSON matching this schema:\n" + schema

	response, err := a.Generate(ctx, fullPrompt)
	if err != nil {
		return nil, err
	}

	parser := NewParser()
	return parser.ParseRecommendResult(response)
}

// GetModel returns the model name.
func (a *OllamaAdapter) GetModel() string {
	return a.config.Model
}

// OllamaResponse represents Ollama API response.
type OllamaResponse struct {
	Model     string `json:"model"`
	CreatedAt string `json:"created_at"`
	Response  string `json:"response"`
	Done      bool   `json:"done"`
}
