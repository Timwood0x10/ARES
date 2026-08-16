// Package ares_config provides configuration loading and validation for ares.
// This file contains the default value initialization logic for the Config type.
package ares_config

import (
	"time"
)

// Default string constants used across config defaults. Declared as named
// constants (rather than inline literals) so goconst stays quiet and the
// values are grep-able.
const (
	defaultServerHost   = "localhost"
	defaultLLMProvider  = "ollama"
	defaultLLMModel     = "llama3.2"
	defaultOutputFormat = "simple"
	defaultStorageType  = "postgres"
	defaultPGVectorTbl  = "embeddings"
	providerOpenAI      = "openai"
	providerOpenRouter  = "openrouter"
	providerAnthropic   = "anthropic"
	// defaultLeaderID is the leader agent ID assigned by the minimal config
	// path (and shared across configs that reference the leader by ID).
	defaultLeaderID = "leader-1"
)

// DefaultArchiveDir is the default round-archive directory. Exported so the
// minimal service path (api_impl) can reuse the exact same default without
// duplicating the literal, keeping the two wiring paths in sync.
const DefaultArchiveDir = ".context/rounds"

// NewMinimalConfig builds a fully-runnable Config from only the LLM endpoint
// details, so a user does not need a YAML file to start the runtime: everything
// else (agents, memory, tools, storage, kernel policy) falls back to the
// package defaults via setDefaults.
//
// Provider is inferred: a non-empty apiKey selects the OpenAI-compatible
// provider (works for any OpenAI-compatible endpoint); otherwise ollama.
// Memory is force-enabled because the leader agent contract requires a
// MemoryManager (see validateServeConfig) — enabling it here is the only
// non-default choice the minimal path must make.
//
// Args:
//   - baseURL: the LLM endpoint root, e.g. "https://api.openai.com/v1" or
//     "http://localhost:11434/v1". Empty falls back to the provider default.
//   - apiKey: the API key (empty for local ollama).
//   - model: the model name. Empty selects the provider default.
//
// Returns:
//   - *Config: a fully-defaulted, validated config ready for Bootstrap.
func NewMinimalConfig(baseURL, apiKey, model string) *Config {
	cfg := &Config{}
	cfg.LLM.BaseURL = baseURL
	cfg.LLM.APIKey = apiKey
	cfg.LLM.Provider = providerOpenAI
	if apiKey == "" {
		cfg.LLM.Provider = defaultLLMProvider // ollama
	}
	cfg.LLM.Model = model
	// Memory defaults to enabled (nil Enabled field → IsEnabled() == true), so
	// a minimal startup always satisfies the leader agent's Memory requirement.
	cfg.setDefaults()
	if cfg.LLM.Model == "" {
		if cfg.LLM.Provider == providerOpenAI {
			cfg.LLM.Model = "gpt-4o-mini"
		} else {
			cfg.LLM.Model = defaultLLMModel
		}
	}
	// Assemble a default agent team so the runtime is immediately capable of
	// task division (coder / reviewer / researcher), even with no config file.
	// A user who wants different agents supplies a config file instead.
	cfg.Agents.Leader.ID = defaultLeaderID
	cfg.Agents.Sub = defaultSubAgents()
	return cfg
}

// defaultSubAgents returns the standard capability team wired by the minimal
// config path. Types mirror the demo/monitor-live fleet: an analysis/coder, a
// recommendation/reviewer and a research agent, each with the triggers that
// route profile fields to them.
func defaultSubAgents() []SubAgentConfig {
	return []SubAgentConfig{
		{
			ID:       "coder-a",
			Type:     "coder",
			Category: "analysis",
			Triggers: []string{"analysis", "code"},
		},
		{
			ID:       "reviewer-1",
			Type:     "reviewer",
			Category: "recommendation",
			Triggers: []string{"recommendation", "optimization"},
		},
		{
			ID:       "researcher-1",
			Type:     "researcher",
			Category: "research",
			Triggers: []string{"research", "knowledge"},
		},
	}
}

//nolint:gocyclo // Complex default value initialization for multiple config sections
func (c *Config) setDefaults() {
	if c.Server.Host == "" {
		c.Server.Host = defaultServerHost
	}
	if c.Server.Port == 0 {
		c.Server.Port = 8080
	}
	if c.LLM.Provider == "" {
		c.LLM.Provider = defaultLLMProvider
	}
	if c.LLM.Model == "" {
		c.LLM.Model = defaultLLMModel
	}
	if c.LLM.Timeout == 0 {
		c.LLM.Timeout = 60
	}
	if c.LLM.MaxTokens == 0 {
		c.LLM.MaxTokens = 4096
	}
	if c.LLM.ScorerAPIRate == 0 {
		c.LLM.ScorerAPIRate = 10
	}
	if c.LLM.ScorerAPIBurst == 0 {
		c.LLM.ScorerAPIBurst = 20
	}
	if c.Agents.Leader.MaxSteps == 0 {
		c.Agents.Leader.MaxSteps = 10
	}
	if c.Agents.Leader.MaxParallelTasks == 0 {
		c.Agents.Leader.MaxParallelTasks = 5
	}
	if c.Agents.Leader.MaxValidationRetry == 0 {
		c.Agents.Leader.MaxValidationRetry = 3
	}
	if c.Output.Format == "" {
		c.Output.Format = defaultOutputFormat
	}
	if c.Output.ItemTemplate == "" {
		c.Output.ItemTemplate = "{{.ItemID}}: {{.Name}} ({{.Price}})"
	}
	if c.Output.SummaryTemplate == "" {
		c.Output.SummaryTemplate = "Got {{.Count}} recommendations"
	}
	// Storage defaults
	if c.Storage.Type == "" {
		c.Storage.Type = defaultStorageType
	}
	if c.Storage.Port == 0 {
		c.Storage.Port = 5432
	}
	if c.Storage.PGVector.Dimension == 0 {
		c.Storage.PGVector.Dimension = 1536
	}
	if c.Storage.PGVector.TableName == "" {
		c.Storage.PGVector.TableName = defaultPGVectorTbl
	}
	// Memory defaults
	if c.Memory.SessionMemory.MaxHistory == 0 {
		c.Memory.SessionMemory.MaxHistory = 50
	}
	if c.Memory.UserProfile.Storage == "" {
		c.Memory.UserProfile.Storage = "memory"
	}
	if c.Memory.TaskDistillation.Prompt == "" {
		c.Memory.TaskDistillation.Prompt = DefaultTaskDistillationPrompt
	}
	// Closed-loop memory defaults. MaxHistory defaults to 10 when zero — this
	// is the closed-loop context window, distinct from SessionMemory.MaxHistory.
	if c.Memory.MaxHistory == 0 {
		c.Memory.MaxHistory = 10
	}
	// Distillation defaults: only apply threshold default when distillation
	// is opted in. When EnableDistillation is false, leave threshold at zero
	// so the closed loop treats it as "do not distill".
	if c.Memory.EnableDistillation && c.Memory.DistillationThreshold == 0 {
		c.Memory.DistillationThreshold = 3
	}
	// RAG defaults: only apply TopK/MinScore defaults when RAG is opted in.
	// When EnableRAG is false, leave them at zero so retrieval stays inert.
	if c.Memory.EnableRAG {
		if c.Memory.RAGTopK == 0 {
			c.Memory.RAGTopK = 5
		}
		if c.Memory.RAGMinScore == 0 {
			c.Memory.RAGMinScore = 0.4
		}
	}
	// Archive defaults: dir and max_rounds apply regardless so the values are
	// always valid; Enabled is *bool so its default-on semantics need no setting here.
	if c.Memory.Archive.Dir == "" {
		c.Memory.Archive.Dir = DefaultArchiveDir
	}
	if c.Memory.Archive.MaxRounds == 0 {
		c.Memory.Archive.MaxRounds = 200
	}
	// Knowledge (AKG) defaults: only apply TopK/MinScore defaults when
	// retrieval is opted in. When RetrievalEnabled is false, leave them at
	// zero so AKG retrieval stays inert.
	if c.Knowledge.RetrievalEnabled {
		if c.Knowledge.TopK == 0 {
			c.Knowledge.TopK = 5
		}
		if c.Knowledge.MinScore == 0 {
			c.Knowledge.MinScore = 0.4
		}
	}
	// Validation defaults
	if c.Validation.SchemaType == "" {
		c.Validation.SchemaType = "default" // "default", "travel", "custom"
	}
	if c.Validation.MaxRetries == 0 {
		c.Validation.MaxRetries = 3
	}
	// Workflow defaults
	if c.Workflow.ReloadInterval == 0 && c.Workflow.AutoReload {
		c.Workflow.ReloadInterval = 30 // seconds
	}
	// MCP defaults
	for i := range c.MCP.Servers {
		if c.MCP.Servers[i].Timeout == 0 {
			c.MCP.Servers[i].Timeout = 30
		}
	}
	// Dashboard defaults
	if c.Dashboard.Addr == "" {
		c.Dashboard.Addr = ":8090"
	}
	if c.Dashboard.WSPingInterval == 0 {
		c.Dashboard.WSPingInterval = 30
	}
	// Evolution defaults
	if c.Evolution.PopulationSize == 0 {
		c.Evolution.PopulationSize = 20
	}
	if c.Evolution.EliteCount == 0 {
		c.Evolution.EliteCount = 2
	}
	if c.Evolution.SurvivalRate == 0 {
		c.Evolution.SurvivalRate = 0.6
	}
	if c.Evolution.MutationRate == 0 {
		c.Evolution.MutationRate = 0.2
	}
	if c.Evolution.MinMutationRate == 0 {
		c.Evolution.MinMutationRate = 0.05
	}
	if c.Evolution.MaxMutationRate == 0 {
		c.Evolution.MaxMutationRate = 0.5
	}
	if c.Evolution.Generations == 0 {
		c.Evolution.Generations = 15
	}
	if c.Evolution.BreedingPoolRatio == 0 {
		c.Evolution.BreedingPoolRatio = 0.5
	}
	if c.Evolution.MinInterval == "" {
		c.Evolution.MinInterval = "5m"
	}
	if c.Evolution.SelectionStrategy == "" {
		c.Evolution.SelectionStrategy = "tournament"
	}
	if c.Evolution.TournamentSize == 0 {
		c.Evolution.TournamentSize = 3
	}
	if c.Evolution.CrossoverType == "" {
		c.Evolution.CrossoverType = "uniform"
	}
	// LLM scoring defaults — MaxCallsPerGeneration caps LLM API cost per
	// generation. When zero, use 100 (matches the tiered scorer default).
	if c.Evolution.LLMScoring.MaxCallsPerGeneration == 0 {
		c.Evolution.LLMScoring.MaxCallsPerGeneration = 100
	}
	// Discovery defaults — opt-in via Enabled (default false). When enabled
	// but Interval is unset, default to 5 minutes between discovery cycles.
	if c.Discovery.Interval == 0 {
		c.Discovery.Interval = 5 * time.Minute
	}
}
