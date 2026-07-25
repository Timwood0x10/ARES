// Package ares_config provides configuration loading and validation for ares.
// This file contains the default value initialization logic for the Config type.
package ares_config

import (
	"time"
)

//nolint:gocyclo // Complex default value initialization for multiple config sections
func (c *Config) setDefaults() {
	if c.Server.Host == "" {
		c.Server.Host = "localhost"
	}
	if c.Server.Port == 0 {
		c.Server.Port = 8080
	}
	if c.LLM.Provider == "" {
		c.LLM.Provider = "ollama"
	}
	if c.LLM.Model == "" {
		c.LLM.Model = "llama3.2"
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
		c.Output.Format = "simple"
	}
	if c.Output.ItemTemplate == "" {
		c.Output.ItemTemplate = "{{.ItemID}}: {{.Name}} ({{.Price}})"
	}
	if c.Output.SummaryTemplate == "" {
		c.Output.SummaryTemplate = "Got {{.Count}} recommendations"
	}
	// Storage defaults
	if c.Storage.Type == "" {
		c.Storage.Type = "postgres"
	}
	if c.Storage.Port == 0 {
		c.Storage.Port = 5432
	}
	if c.Storage.PGVector.Dimension == 0 {
		c.Storage.PGVector.Dimension = 1536
	}
	if c.Storage.PGVector.TableName == "" {
		c.Storage.PGVector.TableName = "embeddings"
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
