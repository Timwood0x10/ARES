package ares

import (
	"context"

	"github.com/Timwood0x10/ares/sdk"
)

// ---------------------------------------------------------------------------
// Core types
// ---------------------------------------------------------------------------

// Agent is a named LLM agent with its own instruction, tools, and memory.
type Agent = sdk.Agent

// Task is a unit of work submitted to the runtime's scheduler.
type Task = sdk.Task

// Result is the outcome of an agent run or task submission.
type Result = sdk.Result

// TokenUsage reports prompt/completion token counts for a result.
type TokenUsage = sdk.TokenUsage

// StreamChunk is a single chunk from a streaming agent response.
type StreamChunk = sdk.StreamChunk

// HumanInputFunc is a callback for human-in-the-loop approval gates.
type HumanInputFunc = sdk.HumanInputFunc

// ---------------------------------------------------------------------------
// LLM types
// ---------------------------------------------------------------------------

// LLMProvider identifies an LLM backend.
type LLMProvider = sdk.LLMProvider

// Supported LLM providers.
const (
	LLMProviderOpenRouter = sdk.LLMProviderOpenRouter
	LLMProviderOllama     = sdk.LLMProviderOllama
	LLMProviderOpenAI     = sdk.LLMProviderOpenAI
	LLMProviderAnthropic  = sdk.LLMProviderAnthropic
)

// LLMConfig configures an LLM connection.
type LLMConfig = sdk.LLMConfig

// BaseConfig holds shared HTTP client settings (timeout, retries).
type BaseConfig = sdk.BaseConfig

// LLMMessage is a single message in an LLM conversation.
type LLMMessage = sdk.LLMMessage

// ToolCall represents a tool/function call requested by the LLM.
type ToolCall = sdk.ToolCall

// FunctionCall contains the function name and arguments.
type FunctionCall = sdk.FunctionCall

// GenerateRequest is a request to generate text from an LLM.
type GenerateRequest = sdk.GenerateRequest

// GenerateResponse is the LLM's response to a GenerateRequest.
type GenerateResponse = sdk.GenerateResponse

// EmbeddingRequest is a request to generate an embedding vector.
type EmbeddingRequest = sdk.EmbeddingRequest

// EmbeddingResponse contains the generated embedding vector.
type EmbeddingResponse = sdk.EmbeddingResponse

// FunctionDefinition describes a function the LLM can call.
type FunctionDefinition = sdk.FunctionDefinition

// ---------------------------------------------------------------------------
// Tool types
// ---------------------------------------------------------------------------

// Tool is the interface every custom tool must implement.
type Tool = sdk.Tool

// ToolFunc defines a Tool from plain functions.
type ToolFunc = sdk.ToolFunc

// ToolResult is the outcome of a tool execution.
type ToolResult = sdk.ToolResult

// Registry holds the set of available tools (built-in + custom + MCP).
type Registry = sdk.Registry

// ToolSource provides a dynamic set of tools to an agent.
type ToolSource = sdk.ToolSource

// ToolSelector filters the available tool set for a given input.
type ToolSelector = sdk.ToolSelector

// BuiltinToolsOption configures built-in tool registration.
type BuiltinToolsOption = sdk.BuiltinToolsOption

// NewRegistry creates a tool registry pre-populated with built-in tools.
func NewRegistry() *Registry { return sdk.NewRegistry() }

// NewEmptyRegistry creates an empty tool registry without built-in tools.
func NewEmptyRegistry() *Registry { return sdk.NewEmptyRegistry() }

// WithFileSandboxDir sets the sandbox directory for file tools.
func WithFileSandboxDir(dir string) BuiltinToolsOption { return sdk.WithFileSandboxDir(dir) }

// RegisterBuiltinTools registers all built-in tools into the registry.
func RegisterBuiltinTools(r *Registry, opts ...BuiltinToolsOption) error {
	return sdk.RegisterBuiltinTools(r, opts...)
}

// ---------------------------------------------------------------------------
// Knowledge types
// ---------------------------------------------------------------------------

// QualityGateConfig controls knowledge ingestion quality thresholds.
type QualityGateConfig = sdk.QualityGateConfig

// DefaultQualityGateConfig returns the built-in quality gate defaults.
func DefaultQualityGateConfig() QualityGateConfig {
	return sdk.DefaultQualityGateConfig()
}

// KnowledgeStore persists and retrieves knowledge objects.
type KnowledgeStore = sdk.KnowledgeStore

// GraphProvider streams knowledge objects into the knowledge graph.
type GraphProvider = sdk.GraphProvider

// ObjectType classifies a knowledge object.
type ObjectType = sdk.ObjectType

// Evidence links a knowledge object to its source.
type Evidence = sdk.Evidence

// KnowledgeObject is a single node in the knowledge graph.
type KnowledgeObject = sdk.KnowledgeObject

// Representation stores an embedding vector for a knowledge object.
type Representation = sdk.Representation

// Relation is a directed edge between knowledge objects.
type Relation = sdk.Relation

// KnowledgeQuery filters knowledge objects.
type KnowledgeQuery = sdk.KnowledgeQuery

// Intent describes what knowledge retrieval should find.
type Intent = sdk.Intent

// Scope constrains knowledge retrieval.
type Scope = sdk.Scope

// ---------------------------------------------------------------------------
// Context cleaner types
// ---------------------------------------------------------------------------

// ContextCleaner compresses message history with role-aware strategies.
type ContextCleaner = sdk.ContextCleaner

// Message is a single chat message.
type Message = sdk.Message

// CleanOptions tunes the context cleaning behavior.
type CleanOptions = sdk.CleanOptions

// DefaultCleanOptions returns sensible defaults for context cleaning.
func DefaultCleanOptions() CleanOptions {
	return sdk.DefaultCleanOptions()
}

// CleaningMode controls how aggressively the cleaner strips tool data.
type CleaningMode = sdk.CleaningMode

// Cleaning mode constants.
const (
	CleaningModeDefault      = sdk.CleaningModeDefault
	CleaningModeConservative = sdk.CleaningModeConservative
	CleaningModeAggressive   = sdk.CleaningModeAggressive
)

// CleanerStats reports cumulative cleaning statistics.
type CleanerStats = sdk.CleanerStats

// NewContextCleaner returns a turn-aware context cleaner.
func NewContextCleaner() ContextCleaner {
	return sdk.NewContextCleaner()
}

// ---------------------------------------------------------------------------
// Graph types
// ---------------------------------------------------------------------------

// Graph is a DAG of nodes for multi-step workflows.
type Graph = sdk.Graph

// GraphResult carries per-node outputs and the final shared state.
type GraphResult = sdk.GraphResult

// NewGraph creates a new empty graph with the given ID.
func NewGraph(id string) *Graph {
	return sdk.NewGraph(id)
}

// ---------------------------------------------------------------------------
// Runtime introspection types
// ---------------------------------------------------------------------------

// Snapshot captures the health status of all runtime components.
type Snapshot = sdk.Snapshot

// ComponentStatus is the health status of a single runtime component.
type ComponentStatus = sdk.ComponentStatus

// SnapshotSummary aggregates component counts by state.
type SnapshotSummary = sdk.SnapshotSummary

// ---------------------------------------------------------------------------
// Config file types
// ---------------------------------------------------------------------------

// ConfigFile is the deserialized ares.yaml configuration.
type ConfigFile = sdk.ConfigFile

// MemoryFileConfig is the memory section of ares.yaml.
type MemoryFileConfig = sdk.MemoryFileConfig

// DatabaseFileConfig is the database section of ares.yaml.
type DatabaseFileConfig = sdk.DatabaseFileConfig

// EmbeddingFileConfig is the embedding section of ares.yaml.
type EmbeddingFileConfig = sdk.EmbeddingFileConfig

// KnowledgeFileConfig is the knowledge section of ares.yaml.
type KnowledgeFileConfig = sdk.KnowledgeFileConfig

// QualityGateFileConfig is the knowledge quality gate section of ares.yaml.
type QualityGateFileConfig = sdk.QualityGateFileConfig

// KnowledgeEmbeddingFileConfig is the knowledge embedding section of ares.yaml.
type KnowledgeEmbeddingFileConfig = sdk.KnowledgeEmbeddingFileConfig

// LLMFileConfig is the LLM section of ares.yaml.
type LLMFileConfig = sdk.LLMFileConfig

// MCPConn describes an MCP server connection.
type MCPConn = sdk.MCPConn

// LoadConfigFile reads and parses an ares.yaml file.
func LoadConfigFile(path string) (*ConfigFile, error) {
	return sdk.LoadConfigFile(path)
}

// ---------------------------------------------------------------------------
// Distillation & Experience types
// ---------------------------------------------------------------------------

// DistillationConfig controls memory distillation behavior.
type DistillationConfig = sdk.DistillationConfig

// DefaultDistillationConfig returns the built-in distillation defaults.
func DefaultDistillationConfig() *DistillationConfig {
	return sdk.DefaultDistillationConfig()
}

// Experience is a distilled lesson learned from agent execution.
type Experience = sdk.Experience

// ExperienceRepository persists experiences for later retrieval.
type ExperienceRepository = sdk.ExperienceRepository

// Experience type constants.
const (
	ExperienceTypeFailure = sdk.ExperienceTypeFailure
)

// ---------------------------------------------------------------------------
// Evolution types
// ---------------------------------------------------------------------------

// Strategy is an evolvable agent decision strategy.
type Strategy = sdk.Strategy

// DreamCycleConfig configures the autonomous evolution loop.
type DreamCycleConfig = sdk.DreamCycleConfig

// DefaultDreamCycleConfig returns the built-in dream cycle defaults.
func DefaultDreamCycleConfig() DreamCycleConfig {
	return sdk.DefaultDreamCycleConfig()
}

// EvolutionMode selects the evolution algorithm.
type EvolutionMode = sdk.EvolutionMode

// Evolution mode constants.
const (
	// ModeEvolutionStrategy uses (1+λ) evolution strategy.
	ModeEvolutionStrategy = sdk.ModeEvolutionStrategy
	// ModeGeneticAlgorithm uses the full GA pipeline.
	ModeGeneticAlgorithm = sdk.ModeGeneticAlgorithm
)

// DreamCycle orchestrates the full autonomous evolution loop.
type DreamCycle = sdk.DreamCycle

// ---------------------------------------------------------------------------
// MCP client (for direct MCP server interaction)
// ---------------------------------------------------------------------------

// MCPClient is a client for a single MCP server.
type MCPClient = sdk.MCPClient

// MCPServerConfig describes how to connect to an MCP server.
type MCPServerConfig = sdk.MCPServerConfig

// ConnectMCPFromConfig connects to an MCP server using a config.
func ConnectMCPFromConfig(ctx context.Context, cfg MCPServerConfig) (*MCPClient, error) {
	return sdk.ConnectMCPFromConfig(ctx, cfg)
}

// ConnectMCPSSE connects to an MCP server over SSE.
func ConnectMCPSSE(ctx context.Context, name, url string) (*MCPClient, error) {
	return sdk.ConnectMCPSSE(ctx, name, url)
}

// ConnectMCPStdio connects to an MCP server over stdio.
func ConnectMCPStdio(ctx context.Context, name, command string, args []string) (*MCPClient, error) {
	return sdk.ConnectMCPStdio(ctx, name, command, args)
}

// DiscoverMCPServers finds MCP server configs in a project directory.
func DiscoverMCPServers(projectDir string) []MCPServerConfig {
	return sdk.DiscoverMCPServers(projectDir)
}

// ---------------------------------------------------------------------------
// LLM service (for direct LLM access outside agent loop)
// ---------------------------------------------------------------------------

// LLMService provides direct LLM access (Generate, GenerateSimple, Embedding).
type LLMService = sdk.LLMService

// LLMServiceConfig configures an LLMService.
type LLMServiceConfig = sdk.LLMServiceConfig

// NewLLMService creates a standalone LLM service for direct use.
func NewLLMService(cfg *LLMServiceConfig) (*LLMService, error) {
	return sdk.NewLLMService(cfg)
}
