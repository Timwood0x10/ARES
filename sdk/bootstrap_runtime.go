// Package sdk — Bootstrap-backed runtime core (Stage 8).
//
// These helpers route the SDK's core component graph through the single
// assembly kernel (ares_bootstrap.Bootstrap + System Runtime) so the SDK
// reuses the same EventStore / NewEvolution / Memory / KnowledgeRuntime
// instances as serve and start, instead of building a parallel runtime graph.
// SDK-only options that Bootstrap cannot express (SQLite knowledge store,
// extra knowledge providers) keep the SDK wiring as a compatibility fallback.
package sdk

import (
	"context"
	"log/slog"

	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
	"github.com/Timwood0x10/ares/internal/ares_config"
)

// bootstrapCapable reports whether the SDK config can be fully expressed by
// the Bootstrap kernel. SQLite store paths and extra knowledge providers have
// no Bootstrap equivalent, so those configs keep the SDK-specific wiring.
func bootstrapCapable(cfg *config) bool {
	return cfg.sqliteStorePath == "" && len(cfg.extraProviders) == 0
}

// buildBootstrapConfig maps the SDK config onto ares_config.Config so the
// Bootstrap kernel assembles the same core component graph (LLM, Memory,
// Knowledge/AKG, Evolution, Storage, Embedding, MCP) that serve/start use.
func buildBootstrapConfig(cfg *config) *ares_config.Config {
	out := &ares_config.Config{
		LLM: ares_config.LLMConfig{
			Provider: string(cfg.llmCfg.Provider),
			APIKey:   cfg.llmCfg.APIKey,
			BaseURL:  cfg.llmCfg.BaseURL,
			Model:    cfg.llmCfg.Model,
			Timeout:  cfg.llmCfg.Timeout,
		},
		Memory: ares_config.MemoryConfig{
			Enabled:     cfg.memCfg.Enabled,
			EnableRAG:   cfg.memCfg.EnableRAG,
			RAGTopK:     cfg.memCfg.RAGTopK,
			RAGMinScore: cfg.memCfg.RAGMinScore,
		},
		Knowledge: ares_config.KnowledgeConfig{
			RetrievalEnabled: cfg.knlCfg.Enabled,
		},
		Evolution: ares_config.EvolutionConfig{
			Enabled: cfg.evoCfg.Enabled,
		},
		MCP: ares_config.MCPConfig{Servers: []ares_config.MCPServerEntry{}},
	}
	if cfg.dbCfg.Host != "" {
		out.Storage = ares_config.StorageConfig{
			Enabled:  true,
			Type:     "postgres",
			Host:     cfg.dbCfg.Host,
			Port:     cfg.dbCfg.Port,
			Username: cfg.dbCfg.User,
			Password: cfg.dbCfg.Password,
			Database: cfg.dbCfg.Database,
			SSLMode:  cfg.dbCfg.SSLMode,
		}
	}
	if cfg.embedCfg.ServiceURL != "" {
		out.Embedding = ares_config.EmbeddingConfig{
			Enabled: true,
			BaseURL: cfg.embedCfg.ServiceURL,
		}
	}
	return out
}

// newBootstrapCore assembles the core components through the Bootstrap kernel
// using the provided lifecycle context, so background goroutines Bootstrap
// starts (distillation subscriber, GA ticker, LLM suggestion ticker) exit when
// the caller cancels it. Returns nil when the config is not Bootstrap-capable
// or assembly fails (the SDK then falls back to its own wiring), so a Bootstrap
// regression never breaks SDK construction.
func newBootstrapCore(ctx context.Context, cfg *config) *ares_bootstrap.Components {
	if !bootstrapCapable(cfg) {
		return nil
	}
	comp, err := ares_bootstrap.Bootstrap(ctx, buildBootstrapConfig(cfg), nil)
	if err != nil {
		slog.Warn("sdk: bootstrap core assembly failed; using SDK wiring", "error", err)
		return nil
	}
	return comp
}
