// Package compiler — interfaces for the Compiler pipeline stages.
package compiler

import "context"

// ExtractedEntity is a raw entity extracted from a source message.
type ExtractedEntity struct {
	Name       string            `json:"name"`
	Type       string            `json:"type"`    // concept | tool | module | language
	Aliases    []string          `json:"aliases"` // Alternative names
	Properties map[string]string `json:"properties,omitempty"`
	Confidence float64           `json:"confidence"`
	SourceID   string            `json:"source_id"` // Source message ID
	// Evidence names the extraction signal class that produced this entity
	// (evCamelCase, evStructuralRef, ...). It travels with the entity into
	// the eval harness so per-evidence precision can be measured, which is
	// the statistical basis for confidence calibration.
	Evidence string `json:"evidence,omitempty"`
}

// ExtractedFact is a raw fact extracted from a source message.
// Stored as a structured triple (Subject → Predicate → Object).
type ExtractedFact struct {
	Subject    string  `json:"subject"`
	Predicate  string  `json:"predicate"`
	Object     string  `json:"object"`
	Confidence float64 `json:"confidence"`
	SourceID   string  `json:"source_id"` // Source message ID
}

// ExtractResult holds the raw output of the Extract stage.
type ExtractResult struct {
	Entities []ExtractedEntity `json:"entities"`
	Facts    []ExtractedFact   `json:"facts"`
}

// Extractor extracts entities and facts from raw source messages.
// The default implementation uses AKG's EntityExtractor (zero LLM token cost).
type Extractor interface {
	// Extract extracts entities and facts from source messages.
	//
	// Args:
	//
	//	ctx — context for cancellation and timeout.
	//	messages — source messages to extract from.
	//
	// Returns:
	//
	//	entities — extracted entities, may be empty.
	//	facts — extracted facts, may be empty.
	//	err — non-nil if extraction fails critically.
	Extract(ctx context.Context, messages []SourceMessage) ([]ExtractedEntity, []ExtractedFact, error)

	// Name returns the name of this extractor implementation.
	Name() string
}

// Normalizer canonicalizes extracted entities and facts.
// Responsibilities include: alias resolution, coreference resolution,
// name normalization, and deduplication.
type Normalizer interface {
	// Normalize canonicalizes entities and facts.
	//
	// Args:
	//
	//	ctx — context for cancellation and timeout.
	//	entities — raw extracted entities.
	//	facts — raw extracted facts.
	//
	// Returns:
	//
	//	normalizedEntities — canonicalized entities.
	//	normalizedFacts — canonicalized facts.
	//	err — non-nil if normalization fails critically.
	Normalize(ctx context.Context, entities []ExtractedEntity, facts []ExtractedFact) ([]ExtractedEntity, []ExtractedFact, error)

	// Name returns the name of this normalizer implementation.
	Name() string
}

// CompileConfig is defined in compiler.go (already in the same package).
// Ensure interfaces compile.
var _ Extractor = (*noopExtractor)(nil)
var _ Normalizer = (*noopNormalizer)(nil)

// noopExtractor is a no-op implementation of Extractor for testing.
type noopExtractor struct{}

func (n *noopExtractor) Extract(_ context.Context, _ []SourceMessage) ([]ExtractedEntity, []ExtractedFact, error) {
	return nil, nil, nil
}

func (n *noopExtractor) Name() string { return "noop" }

// noopNormalizer is a no-op implementation of Normalizer for testing.
type noopNormalizer struct{}

func (n *noopNormalizer) Normalize(_ context.Context, entities []ExtractedEntity, facts []ExtractedFact) ([]ExtractedEntity, []ExtractedFact, error) {
	return entities, facts, nil
}

func (n *noopNormalizer) Name() string { return "noop" }

// NewNoopExtractor creates a no-op extractor for testing.
func NewNoopExtractor() Extractor { return &noopExtractor{} }

// NewNoopNormalizer creates a no-op normalizer for testing.
func NewNoopNormalizer() Normalizer { return &noopNormalizer{} }
