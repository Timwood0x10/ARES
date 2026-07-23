// Package compiler provides the Conversation Compiler — the core pipeline that
// transforms conversation messages into a structured Knowledge Model (KM) graph.
//
// Pipeline stages:
//  1. Extract — extracts entities, facts, and events from raw conversation
//     using AKG's EntityExtractor (zero LLM token cost).
//  2. Normalize — canonicalizes names, resolves aliases, resolves coreferences.
//  3. Compile — builds the KM graph: deduplicates, merges, links, and prunes.
//
// After Compile, the KM graph is consumed by Selectors (PromptSelector,
// MemorySelector, AKGSelector) which produce SubGraphs for downstream consumers.
package compiler

import (
	"context"
	"fmt"
	"time"
	"unicode"
)

// Stage defines a single stage in the Compiler pipeline.
type Stage int

const (
	StageExtract   Stage = iota + 1 // Extract entities and facts from raw input
	StageNormalize                  // Canonicalize and resolve aliases
	StageCompile                    // Build KM graph: deduplicate, merge, link
)

// String returns the human-readable name of the stage.
func (s Stage) String() string {
	switch s {
	case StageExtract:
		return "extract"
	case StageNormalize:
		return "normalize"
	case StageCompile:
		return "compile"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// SourceMessage represents a single message from a conversation session.
// This is the input to the Compiler pipeline.
type SourceMessage struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`    // user | assistant | system | tool
	Content   string    `json:"content"` // Message text content
	TurnID    string    `json:"turn_id"` // Conversation turn identifier
	ToolCalls []any     `json:"tool_calls,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// CompileConfig holds configuration for a single Compile operation.
type CompileConfig struct {
	// MaxNodes is the maximum number of nodes to retain after pruning.
	// Zero means no limit.
	MaxNodes int

	// MinConfidence is the minimum confidence threshold for node retention.
	MinConfidence float64

	// Incremental indicates whether this is an incremental compile.
	// When true, the compiler merges new nodes into the existing model
	// instead of rebuilding from scratch.
	Incremental bool

	// PreviousModel is the previous KM version for incremental compilation.
	PreviousModel *KnowledgeModel
}

// DefaultCompileConfig returns sensible defaults for CompileConfig.
func DefaultCompileConfig() CompileConfig {
	return CompileConfig{
		MaxNodes:      500,
		MinConfidence: 0.3,
		Incremental:   false,
	}
}

// CompileResult holds the output of a single Compile operation.
type CompileResult struct {
	Model     *KnowledgeModel `json:"model"`
	Stats     CompileStats    `json:"stats"`
	CreatedAt time.Time       `json:"created_at"`
}

// CompileStats holds statistics about a Compile operation.
type CompileStats struct {
	MessagesIn     int                     `json:"messages_in"`
	NodesCreated   int                     `json:"nodes_created"`
	EdgesCreated   int                     `json:"edges_created"`
	NodesPruned    int                     `json:"nodes_pruned"`
	StageDurations map[Stage]time.Duration `json:"stage_durations"`
	TotalDuration  time.Duration           `json:"total_duration"`
}

// Compiler is the main entry point for the conversation compilation pipeline.
// It orchestrates the Extract → Normalize → Compile stages.
type Compiler struct {
	extractor  Extractor
	normalizer Normalizer
	compiler   *GraphCompiler
	config     CompileConfig
}

// NewCompiler creates a new Compiler with the given dependencies.
//
// Args:
//
//	extractor — extracts entities and facts from raw messages (uses AKG).
//	normalizer — canonicalizes names and resolves aliases.
//	config — default CompileConfig (can be overridden per Compile call).
//
// Returns:
//
//	*Compiler — the configured Compiler instance.
func NewCompiler(extractor Extractor, normalizer Normalizer, config CompileConfig) *Compiler {
	return &Compiler{
		extractor:  extractor,
		normalizer: normalizer,
		compiler:   NewGraphCompiler(),
		config:     config,
	}
}

// Compile runs the full Compiler pipeline on a set of source messages.
//
// Pipeline:
//  1. Extract — calls the Extractor to produce raw entities and facts.
//  2. Normalize — calls the Normalizer to canonicalize names and resolve aliases.
//  3. Compile — builds the KM graph: deduplicates, merges, links, and prunes.
//
// Args:
//
//	ctx — context for cancellation and timeout.
//	messages — source messages to compile.
//	opts — optional CompileConfig overrides.
//
// Returns:
//
//	*CompileResult — the compiled knowledge model and stats.
//	error — non-nil if any stage fails.
func (c *Compiler) Compile(ctx context.Context, messages []SourceMessage, opts ...CompileConfig) (*CompileResult, error) {
	if len(messages) == 0 {
		return nil, fmt.Errorf("compiler: no messages to compile")
	}

	cfg := c.config
	if len(opts) > 0 {
		cfg = opts[0]
	}

	start := time.Now()
	stats := CompileStats{
		MessagesIn:     len(messages),
		StageDurations: make(map[Stage]time.Duration),
	}

	// Stage 1: Extract.
	stageStart := time.Now()
	rawEntities, rawFacts, err := c.extractor.Extract(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("compiler: extract: %w", err)
	}
	stats.StageDurations[StageExtract] = time.Since(stageStart)

	// Stage 2: Normalize.
	stageStart = time.Now()
	normalizedEntities, normalizedFacts, err := c.normalizer.Normalize(ctx, rawEntities, rawFacts)
	if err != nil {
		return nil, fmt.Errorf("compiler: normalize: %w", err)
	}
	stats.StageDurations[StageNormalize] = time.Since(stageStart)

	// Stage 3: Compile.
	stageStart = time.Now()
	model, err := c.compiler.Compile(ctx, normalizedEntities, normalizedFacts, cfg)
	if err != nil {
		return nil, fmt.Errorf("compiler: compile: %w", err)
	}
	stats.StageDurations[StageCompile] = time.Since(stageStart)

	// Update metadata.
	model.Metadata.CompileCount++
	model.Metadata.SourceCount += len(messages)

	// Prune if configured.
	if cfg.MaxNodes > 0 {
		pruned := model.Prune(cfg.MaxNodes, cfg.MinConfidence)
		stats.NodesPruned = pruned
	}

	stats.NodesCreated = model.NodeCount()
	stats.EdgesCreated = model.EdgeCount()
	stats.TotalDuration = time.Since(start)

	return &CompileResult{
		Model:     model,
		Stats:     stats,
		CreatedAt: time.Now(),
	}, nil
}

// ShouldCompile checks whether the compiler should be triggered based on
// the token budget threshold.
//
// Args:
//
//	messages — the current messages buffer.
//	windowSize — the total token budget window size.
//	threshold — the trigger threshold as a fraction of windowSize (e.g. 0.7).
//
// Returns:
//
//	bool — true if compilation should be triggered.
func ShouldCompile(messages []SourceMessage, windowSize int, threshold float64) bool {
	if windowSize <= 0 || threshold <= 0 {
		return false
	}
	// Estimate token count. ASCII text is ~4 chars/token; non-ASCII runes (e.g.
	// CJK) are closer to 1 token/char. The previous len(Content)/4 heuristic
	// counted every CJK char (3 UTF-8 bytes) as 0 tokens, so Chinese
	// conversations never reached the threshold — see
	// COMPILER_INTEGRATION_PLAN §3.4.
	totalTokens := 0
	for _, m := range messages {
		totalTokens += estimateContentTokens(m.Content)
	}
	return float64(totalTokens) >= float64(windowSize)*threshold
}

// estimateContentTokens returns a rough token count for s. ASCII runes
// contribute ~1 token per 4 chars; non-ASCII runes (CJK, etc.) contribute ~1
// token each. It is the content-level counterpart to estimateNodeTokens in
// prompt_selector.go.
func estimateContentTokens(s string) int {
	asciiChars := 0
	cjkChars := 0
	for _, r := range s {
		if r <= unicode.MaxASCII {
			asciiChars++
		} else {
			cjkChars++
		}
	}
	return asciiChars/4 + cjkChars
}

// CompileMode returns the appropriate CompileConfig based on the model state.
// For incremental compilation, pass the previous model.
func CompileMode(previous *KnowledgeModel, maxNodes int, minConfidence float64) CompileConfig {
	cfg := DefaultCompileConfig()
	cfg.MaxNodes = maxNodes
	cfg.MinConfidence = minConfidence
	if previous != nil && previous.NodeCount() > 0 {
		cfg.Incremental = true
		cfg.PreviousModel = previous
	}
	return cfg
}

// Ensure default config is used.
var _ = DefaultCompileConfig
