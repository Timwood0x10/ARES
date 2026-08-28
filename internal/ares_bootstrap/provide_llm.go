// Package ares_bootstrap — LLM provider.
package ares_bootstrap

import (
	"fmt"

	"github.com/Timwood0x10/ares/compat"
	compatllm "github.com/Timwood0x10/ares/compat/llm"
	"github.com/Timwood0x10/ares/compat/llm/ollama"
	"github.com/Timwood0x10/ares/compat/llm/openai"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/ares_callbacks"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_security"
	"github.com/Timwood0x10/ares/internal/llm"
)

func ProvideLLM(cfg ares_config.LLMConfig) (*LLMComponents, error) {
	reg := ares_callbacks.NewRegistry()
	llmCfg := &llm.Config{
		Provider:        cfg.Provider,
		APIKey:          cfg.APIKey,
		BaseURL:         cfg.BaseURL,
		Model:           cfg.Model,
		Timeout:         cfg.Timeout,
		MaxTokens:       cfg.MaxTokens,
		MaxPromptLength: cfg.MaxPromptLength,
		Extra:           cfg.Extra,
	}
	client, err := llm.NewClient(llmCfg, llm.WithCallbacks(reg), llm.WithSanitizer(ares_security.NewSanitizer()))
	if err != nil {
		return nil, fmt.Errorf("bootstrap: LLM client: %w", err)
	}

	// Register the LLM provider in the compat layer for ecosystem access.
	// B31: Dispatch to the correct adapter based on provider name instead of
	// always using openai.New. For unknown providers, fall back to openai
	// (which covers openai-compatible endpoints like openrouter/azure).
	provider := cfg.Provider
	if provider == "" {
		provider = "openai"
	}
	if err := compat.RegisterLLM(provider, func(config map[string]any) (compatllm.LLMProvider, error) {
		switch provider {
		case "ollama":
			return ollama.New(config)
		default:
			// openai, openrouter, anthropic (via proxy), azure — all
			// speak the OpenAI-compatible API surface.
			return openai.New(config)
		}
	}); err != nil {
		log.Warn("bootstrap: compat LLM registration skipped", "provider", provider, "error", err)
	}

	return &LLMComponents{
		Client:      client,
		CallbackReg: reg,
	}, nil
}

// NewCallbackRegistry creates a callback registry — kept for backward compatibility.
func NewCallbackRegistry() *ares_callbacks.Registry {
	return ares_callbacks.NewRegistry()
}

// NewLLMClientWithCallbacks creates an LLM client with callbacks — kept for backward compatibility.
func NewLLMClientWithCallbacks(cfg *llm.Config, reg *ares_callbacks.Registry) (*llm.Client, error) {
	return llm.NewClient(cfg, llm.WithCallbacks(reg))
}

// WireTaskExecutorCallbacks returns a TaskExecutorOption that injects a callback emitter.
func WireTaskExecutorCallbacks(reg *ares_callbacks.Registry) sub.TaskExecutorOption {
	if reg == nil {
		return nil
	}
	return sub.WithTaskExecutorCallbacks(reg)
}
