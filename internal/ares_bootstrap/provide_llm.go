// Package ares_bootstrap — LLM provider.
package ares_bootstrap

import (
	"fmt"

	"github.com/Timwood0x10/ares/compat"
	compatllm "github.com/Timwood0x10/ares/compat/llm"
	"github.com/Timwood0x10/ares/compat/llm/openai"
	"github.com/Timwood0x10/ares/internal/agents/leader"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/ares_callbacks"
	"github.com/Timwood0x10/ares/internal/ares_config"
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
	client, err := llm.NewClient(llmCfg, llm.WithCallbacks(reg))
	if err != nil {
		return nil, fmt.Errorf("bootstrap: LLM client: %w", err)
	}

	// Register the LLM provider in the compat layer for ecosystem access.
	compat.RegisterLLM(cfg.Provider, func(config map[string]any) (compatllm.LLMProvider, error) {
		return openai.New(config)
	})

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

// WireLeaderAgentCallbacks returns a LeaderOption that injects a callback emitter.
func WireLeaderAgentCallbacks(reg *ares_callbacks.Registry) leader.LeaderOption {
	if reg == nil {
		return nil
	}
	return leader.WithCallbacks(reg)
}
