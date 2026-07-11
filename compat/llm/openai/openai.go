// Package openai is the official OpenAI-compatible LLM adapter for ARES.
//
// It wraps github.com/Timwood0x10/ares/internal/llm under the compat/llm
// interface so any OpenAI-API-compatible service (OpenAI, OpenRouter, local
// vLLM, …) plugs into the ARES runtime via compat.RegisterLLM("openai", …).
package openai

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/compat/llm"
	aresllm "github.com/Timwood0x10/ares/internal/llm"
)

// Adapter binds an internal/llm.Client to the compat/llm.LLMProvider interface.
type Adapter struct {
	client *aresllm.Client
}

// New constructs an Adapter from a raw config map.
//
// Recognized keys:
//
//	api_key   string  — API key for the OpenAI-compatible service.
//	base_url  string  — optional override for non-OpenAI endpoints.
//	model     string  — default model name.
//	provider  string  — internal provider label; defaults to "openai".
func New(config map[string]any) (*Adapter, error) {
	apiKey, _ := config["api_key"].(string)
	if apiKey == "" {
		return nil, fmt.Errorf("compat/llm/openai: api_key is required")
	}
	model, _ := config["model"].(string)
	if model == "" {
		model = "gpt-4o-mini"
	}
	baseURL, _ := config["base_url"].(string)
	provider, _ := config["provider"].(string)
	if provider == "" {
		provider = "openai"
	}

	cfg := &aresllm.Config{
		Provider: provider,
		APIKey:   apiKey,
		BaseURL:  baseURL,
		Model:    model,
	}
	client, err := aresllm.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("compat/llm/openai: create client: %w", err)
	}
	return &Adapter{client: client}, nil
}

// Generate produces a single completion from the given prompt.
func (a *Adapter) Generate(ctx context.Context, prompt string) (string, error) {
	return a.client.Generate(ctx, prompt)
}

// IsEnabled reports whether the underlying client is properly configured.
func (a *Adapter) IsEnabled() bool { return a.client.IsEnabled() }

// GetProvider returns the provider label.
func (a *Adapter) GetProvider() string { return a.client.GetProvider() }

// Compile-time interface assertion.
var _ llm.LLMProvider = (*Adapter)(nil)
