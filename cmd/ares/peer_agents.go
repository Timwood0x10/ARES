package main

import (
	"time"

	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/core/models"
	llm "github.com/Timwood0x10/ares/internal/llm"
	"github.com/Timwood0x10/ares/internal/llm/output"
)

// createPeerSubAgents builds the sub.Agent executors for the C1 flat peer
// population (cfg.Agents.Peers). Each peer's first capability is its primary
// Type; the full set is offered to the scheduler's candidate scorer via
// subAgentCapability.Caps.
//
// C1 convergence (review P1): in peer mode the sub.Agent is ONLY a static
// CapabilityExecutor for the scheduler's executor pool — the real execution
// body is the self-contained ChatCognition the fabric spawns (peer_mode.go:
// SpawnSpec.CognitionFactory), so the legacy Process/Launch machinery
// (heartbeat monitor + message queue) is NOT wired here. This mirrors
// newPeerExecutor (which already passes nil heartbeat/queue for dynamically
// spawned peers) and matches the review's demand to converge peer mode onto
// the fabric executor: no partially-used sub.Agent lifecycle.
func createPeerSubAgents(
	cfg *ares_config.Config,
	peers []ares_config.PeerAgentConfig,
	llmAdapter output.LLMAdapter,
	chatClient sub.ChatClient,
	toolBinder sub.ToolBinder,
	store ares_events.EventStore,
	strategySrc agents.StrategySource,
) []sub.Agent {
	agents := make([]sub.Agent, 0, len(peers))
	for _, p := range peers {
		typ := ""
		if len(p.Capabilities) > 0 {
			typ = p.Capabilities[0]
		}
		subCfg := ares_config.SubAgentConfig{
			ID:            p.ID,
			Type:          typ,
			Priority:      p.Priority,
			MaxToolRounds: p.MaxToolRounds,
		}
		executor := createExecutor(llmAdapter, chatClient, toolBinder, cfg, subCfg, strategySrc)
		handler := sub.NewMessageHandler(p.ID)
		agent := sub.New(
			p.ID,
			models.AgentType(typ),
			executor,
			handler,
			nil, // message queue: the fabric owns scheduling; no AHP queue loop
			nil, // heartbeat monitor: no Process/Launch lifecycle in peer mode
			&sub.SubAgentConfig{
				Config: base.Config{
					ID:   p.ID,
					Type: models.AgentType(typ),
				},
				EnableTools: true,
			},
			sub.WithEventStore(store),
		)
		agents = append(agents, agent)
	}
	return agents
}

func createExecutor(
	llmAdapter output.LLMAdapter,
	chatClient sub.ChatClient,
	toolBinder sub.ToolBinder,
	cfg *ares_config.Config,
	subCfg ares_config.SubAgentConfig,
	strategySrc agents.StrategySource,
) sub.TaskExecutor {
	opts := []sub.TaskExecutorOption{
		sub.WithChatClient(chatClient),
		sub.WithStrategySource(strategySrc),
	}
	// Configurable tool-loop depth: max_tool_rounds per sub-agent overrides the
	// executor default (5). 0/unset keeps the library default (config over
	// magic constants, code_rules_v2).
	if subCfg.MaxToolRounds > 0 {
		opts = append(opts, sub.WithMaxToolRounds(subCfg.MaxToolRounds))
	}
	return sub.NewTaskExecutorWithValidation(
		toolBinder,
		llmAdapter,
		output.NewTemplateEngine(),
		cfg.Prompts.Recommendation,
		output.NewValidator(output.WithSchemaType(cfg.Validation.SchemaType)),
		subCfg.MaxRetries,
		cfg.Validation.RetryOnFail,
		cfg.Validation.StrictMode,
		opts...,
	)
}

// createChatClient creates a FailoverClient from the LLM config for Chat API support.
func createChatClient(cfg *ares_config.Config) (sub.ChatClient, error) {
	configs := make([]*llm.Config, 0, 1+len(cfg.LLM.Fallbacks))
	configs = append(configs, &llm.Config{
		Provider:  cfg.LLM.Provider,
		APIKey:    cfg.LLM.APIKey,
		BaseURL:   cfg.LLM.BaseURL,
		Model:     cfg.LLM.Model,
		Timeout:   cfg.LLM.Timeout,
		MaxTokens: cfg.LLM.MaxTokens,
	})
	for _, fb := range cfg.LLM.Fallbacks {
		provider := fb.Provider
		if provider == "" {
			provider = "openai"
		}
		configs = append(configs, &llm.Config{
			Provider:  provider,
			APIKey:    fb.APIKey,
			BaseURL:   fb.BaseURL,
			Model:     fb.Model,
			Timeout:   fb.Timeout,
			MaxTokens: fb.MaxTokens,
		})
	}

	timeout := time.Duration(cfg.LLM.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	rate := cfg.LLM.ScorerAPIRate
	burst := cfg.LLM.ScorerAPIBurst
	return llm.NewFailoverClient(configs, timeout, rate, burst)
}
