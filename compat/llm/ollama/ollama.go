// Package ollama is the official Ollama LLM adapter for ARES.
//
// It wraps github.com/Timwood0x10/ares/internal/llm under the compat/llm
// interface so a local Ollama service plugs into the ARES runtime via
// compat.RegisterLLM("ollama", …). Ollama does not require an API key.
package ollama

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/compat/llm"
	aresllm "github.com/Timwood0x10/ares/internal/llm"
)

// Adapter binds an internal/llm.Client (Ollama provider) to compat/llm.LLMProvider.
type Adapter struct {
	client *aresllm.Client
}

// New constructs an Adapter from a raw config map.
//
// Recognized keys:
//
//	base_url  string  — Ollama endpoint; defaults to http://localhost:11434.
//	model     string  — local model name (e.g. "llama3:8b").
func New(config map[string]any) (*Adapter, error) {
	baseURL, _ := config["base_url"].(string)
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	model, _ := config["model"].(string)
	if model == "" {
		return nil, fmt.Errorf("compat/llm/ollama: model is required")
	}

	cfg := &aresllm.Config{
		Provider: "ollama",
		BaseURL:  baseURL,
		Model:    model,
	}
	client, err := aresllm.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("compat/llm/ollama: create client: %w", err)
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
