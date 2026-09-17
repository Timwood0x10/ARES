// Public type aliases for external consumers. Go's internal visibility rule
// prevents importing internal packages from outside this module, so every
// internal type that appears in a public sdk/ function signature must be
// re-exported here.
package sdk

import (
	"context"

	apitools "github.com/Timwood0x10/ares/internal/apitools"
	kernel "github.com/Timwood0x10/ares/internal/kernel"
	knowledge "github.com/Timwood0x10/ares/internal/knowledge"
	kprovider "github.com/Timwood0x10/ares/internal/knowledge/provider"
	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
	llmservice "github.com/Timwood0x10/ares/internal/llmservice"
	mcpclient "github.com/Timwood0x10/ares/internal/mcpclient"
	evo "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
	distill "github.com/Timwood0x10/ares/internal/runtime/memory/distillation"
	toolsource "github.com/Timwood0x10/ares/internal/tools/toolsource"
)

// ---------------------------------------------------------------------------
// LLM types (for WithLLMConfig, WithFallbackLLM, FriendlyErr)
// ---------------------------------------------------------------------------

// LLMProvider identifies an LLM backend.
type LLMProvider = llmcore.LLMProvider

// Supported LLM providers.
const (
	LLMProviderOpenRouter LLMProvider = llmcore.LLMProviderOpenRouter
	LLMProviderOllama     LLMProvider = llmcore.LLMProviderOllama
	LLMProviderOpenAI     LLMProvider = llmcore.LLMProviderOpenAI
	LLMProviderAnthropic  LLMProvider = llmcore.LLMProviderAnthropic
)

// LLMConfig configures an LLM connection.
type LLMConfig = llmcore.LLMConfig

// BaseConfig holds shared HTTP client settings (timeout, retries).
type BaseConfig = llmcore.BaseConfig

// ---------------------------------------------------------------------------
// Knowledge quality gate (for WithAKGQualityGate)
// ---------------------------------------------------------------------------

// QualityGateConfig controls the knowledge ingestion quality thresholds.
type QualityGateConfig = knowledge.QualityGateConfig

// DefaultQualityGateConfig returns the built-in quality gate defaults.
func DefaultQualityGateConfig() QualityGateConfig {
	return knowledge.DefaultQualityGateConfig()
}

// ---------------------------------------------------------------------------
// Tool source / selector (for WithToolSource, WithToolSelector)
// ---------------------------------------------------------------------------

// ToolSource provides a dynamic set of tools to an agent.
type ToolSource = toolsource.ToolSource

// ToolSelector filters the available tool set for a given input.
type ToolSelector = toolsource.ToolSelector

// ---------------------------------------------------------------------------
// Knowledge store & providers (advanced)
// ---------------------------------------------------------------------------

// KnowledgeStore persists and retrieves KnowledgeObjects.
// Obtain one from Runtime.KnowledgeStore() or construct via WithSQLiteKnowledgeStore / WithPostgres.
type KnowledgeStore = knowledge.KnowledgeStore

// GraphProvider streams KnowledgeObjects into the knowledge graph.
// Implement this to feed custom data sources into the knowledge pipeline.
type GraphProvider = kprovider.GraphProvider

// ---------------------------------------------------------------------------
// Tool registry (for Runtime.ToolRegistry)
// ---------------------------------------------------------------------------

// Registry holds the set of available tools (built-in + custom + MCP).
type Registry = apitools.Registry

// ---------------------------------------------------------------------------
// Context cleaner (for NewContextCleaner)
// ---------------------------------------------------------------------------

// ContextCleaner compresses message history with role-aware strategies.
type ContextCleaner = llmcore.ContextCleaner

// Message is a single chat message (role + content + optional tool data).
type Message = llmcore.Message

// CleanOptions tunes the context cleaning behavior.
type CleanOptions = llmcore.CleanOptions

// DefaultCleanOptions returns sensible defaults for context cleaning.
func DefaultCleanOptions() CleanOptions {
	return llmcore.DefaultCleanOptions()
}

// CleaningMode controls how aggressively the cleaner strips tool data.
type CleaningMode = llmcore.CleaningMode

// Cleaning mode constants.
const (
	CleaningModeDefault      CleaningMode = llmcore.CleaningModeDefault
	CleaningModeConservative CleaningMode = llmcore.CleaningModeConservative
	CleaningModeAggressive   CleaningMode = llmcore.CleaningModeAggressive
)

// CleanerStats reports cumulative cleaning statistics.
type CleanerStats = llmcore.CleanerStats

// ---------------------------------------------------------------------------
// Runtime snapshot (for Runtime.Snapshot)
// ---------------------------------------------------------------------------

// Snapshot captures the health status of all runtime components.
type Snapshot = kernel.Snapshot

// ComponentStatus is the health status of a single runtime component.
type ComponentStatus = kernel.ComponentStatus

// SnapshotSummary aggregates component counts by state.
type SnapshotSummary = kernel.SnapshotSummary

// ---------------------------------------------------------------------------
// Distillation & Experience (for WithDistillation)
// ---------------------------------------------------------------------------

// DistillationConfig controls memory distillation behavior.
type DistillationConfig = distill.DistillationConfig

// DefaultDistillationConfig returns the built-in distillation defaults.
func DefaultDistillationConfig() *DistillationConfig {
	return distill.DefaultDistillationConfig()
}

// Experience is a distilled lesson learned from agent execution.
type Experience = evo.Experience

// ExperienceRepository persists experiences for later retrieval.
type ExperienceRepository = evo.ExperienceRepository

// Experience type constants.
const (
	ExperienceTypeFailure = evo.TypeFailure
)

// ---------------------------------------------------------------------------
// LLM message & request types (for direct LLM interaction)
// ---------------------------------------------------------------------------

// LLMMessage is a single message in an LLM conversation.
type LLMMessage = llmcore.LLMMessage

// ToolCall represents a tool/function call requested by the LLM.
type ToolCall = llmcore.ToolCall

// FunctionCall contains the function name and arguments.
type FunctionCall = llmcore.FunctionCall

// GenerateRequest is a request to generate text from an LLM.
type GenerateRequest = llmcore.GenerateRequest

// GenerateResponse is the LLM's response to a GenerateRequest.
type GenerateResponse = llmcore.GenerateResponse

// TokenUsage reports prompt/completion token counts.
type TokenUsageLLM = llmcore.TokenUsage

// EmbeddingRequest is a request to generate an embedding vector.
type EmbeddingRequest = llmcore.EmbeddingRequest

// EmbeddingResponse contains the generated embedding vector.
type EmbeddingResponse = llmcore.EmbeddingResponse

// FunctionDefinition describes a function the LLM can call.
type FunctionDefinition = llmcore.FunctionDefinition

// ---------------------------------------------------------------------------
// Knowledge object types (for KnowledgeStore interaction)
// ---------------------------------------------------------------------------

// ObjectType classifies a knowledge object (fact, concept, procedure, etc.).
type ObjectType = knowledge.ObjectType

// Evidence links a knowledge object to its source.
type Evidence = knowledge.Evidence

// KnowledgeObject is a single node in the knowledge graph.
type KnowledgeObject = knowledge.KnowledgeObject

// Representation stores an embedding vector for a knowledge object.
type Representation = knowledge.Representation

// Relation is a directed edge between knowledge objects.
type Relation = knowledge.Relation

// Query filters knowledge objects by type, namespace, tags, etc.
type KnowledgeQuery = knowledge.Query

// Intent describes what the knowledge retrieval should find.
type Intent = knowledge.Intent

// Scope constrains knowledge retrieval to specific namespaces/types.
type Scope = knowledge.Scope

// ---------------------------------------------------------------------------
// Evolution types (for WithEvolution)
// ---------------------------------------------------------------------------

// Strategy is an evolvable agent decision strategy.
type Strategy = evo.Strategy

// DreamCycleConfig configures the autonomous evolution loop.
type DreamCycleConfig = evo.DreamCycleConfig

// DefaultDreamCycleConfig returns the built-in dream cycle defaults.
func DefaultDreamCycleConfig() DreamCycleConfig {
	return evo.DefaultDreamCycleConfig()
}

// EvolutionMode selects the evolution algorithm.
type EvolutionMode = evo.EvolutionMode

// Evolution mode constants.
const (
	// ModeEvolutionStrategy uses (1+λ) evolution strategy.
	ModeEvolutionStrategy = evo.ModeEvolutionStrategy
	// ModeGeneticAlgorithm uses the full GA pipeline.
	ModeGeneticAlgorithm = evo.ModeGeneticAlgorithm
)

// DreamCycle orchestrates the full autonomous evolution loop.
type DreamCycle = evo.DreamCycle

// ---------------------------------------------------------------------------
// Tool registry helpers
// ---------------------------------------------------------------------------

// NewRegistry creates a tool registry pre-populated with built-in tools.
func NewRegistry() *Registry {
	return apitools.NewRegistry()
}

// NewEmptyRegistry creates an empty tool registry without built-in tools.
func NewEmptyRegistry() *Registry {
	return apitools.NewEmptyRegistry()
}

// BuiltinToolsOption configures built-in tool registration.
type BuiltinToolsOption = apitools.BuiltinToolsOption

// WithFileSandboxDir sets the sandbox directory for file tools.
func WithFileSandboxDir(dir string) BuiltinToolsOption {
	return apitools.WithFileSandboxDir(dir)
}

// RegisterBuiltinTools registers all built-in tools into the registry.
func RegisterBuiltinTools(r *Registry, opts ...BuiltinToolsOption) error {
	return apitools.RegisterBuiltinTools(r, opts...)
}

// ---------------------------------------------------------------------------
// MCP client types (for direct MCP server interaction)
// ---------------------------------------------------------------------------

// MCPClient is a client for a single MCP server.
type MCPClient = mcpclient.Client

// MCPServerConfig describes how to connect to an MCP server.
type MCPServerConfig = mcpclient.ServerConfig

// ConnectMCPFromConfig connects to an MCP server using a config.
func ConnectMCPFromConfig(ctx context.Context, cfg MCPServerConfig) (*MCPClient, error) {
	return mcpclient.ConnectFromConfig(ctx, cfg)
}

// ConnectMCPSSE connects to an MCP server over SSE.
func ConnectMCPSSE(ctx context.Context, name, url string) (*MCPClient, error) {
	return mcpclient.ConnectSSE(ctx, name, url)
}

// ConnectMCPStdio connects to an MCP server over stdio.
func ConnectMCPStdio(ctx context.Context, name, command string, args []string) (*MCPClient, error) {
	return mcpclient.ConnectStdio(ctx, name, command, args)
}

// DiscoverMCPServers finds MCP server configs in a project directory.
func DiscoverMCPServers(projectDir string) []MCPServerConfig {
	return mcpclient.DiscoverServers(projectDir)
}

// ---------------------------------------------------------------------------
// LLM service types (for direct LLM access outside agent loop)
// ---------------------------------------------------------------------------

// LLMService provides direct LLM access (Generate, GenerateSimple, Embedding).
type LLMService = llmservice.Service

// LLMServiceConfig configures an LLMService.
type LLMServiceConfig = llmservice.Config

// NewLLMService creates a standalone LLM service for direct use.
func NewLLMService(cfg *LLMServiceConfig) (*LLMService, error) {
	return llmservice.NewService(cfg)
}
