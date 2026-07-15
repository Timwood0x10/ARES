// Package knowledge provides the public API for the ARES Knowledge
// Fabric (AKF) — the Agent Knowledge Graph (AKG).
//
// This package exposes the core AKF types (KnowledgeObject,
// WorkingGraph, Representation, Relation, Evidence) and the pipeline
// interfaces (Normalizer, EntityMatcher, Validator, Summarizer) to
// external modules. The internal implementation lives in
// internal/knowledge; this file re-exports its public contract via
// type aliases so external callers can construct, process, and query
// knowledge graphs without importing internal packages.
//
// Key design principle: storage-agnostic. External modules may back
// the knowledge store with any vector database (PostgreSQL pgvector,
// SQLite-vec, Weaviate, Qdrant, Milvus, etc.) by implementing the
// KnowledgeStore interface.
package knowledge

import (
	"github.com/Timwood0x10/ares/internal/knowledge"
)

// ObjectType identifies the type of a knowledge object.
type ObjectType = knowledge.ObjectType

// Evidence records the provenance of a KnowledgeObject.
type Evidence = knowledge.Evidence

// KnowledgeObject is the universal knowledge representation.
//
// Three-layer data structure:
//   - Raw:        Original bytes from the source, preserved for re-distillation.
//   - Normalized: Cleaned, standardized text for embedding and matching.
//   - Summary:    LLM-friendly summary for token-efficient retrieval.
type KnowledgeObject = knowledge.KnowledgeObject

// Representation stores an embedding vector for a KnowledgeObject.
type Representation = knowledge.Representation

// Relation connects two KnowledgeObjects with a named relationship.
type Relation = knowledge.Relation

// WorkingGraph is a task-specific cognitive graph.
// Lifecycle: Build → Consume → Destroy. Never persisted.
type WorkingGraph = knowledge.WorkingGraph

// Query defines filter criteria for KnowledgeStore queries.
type Query = knowledge.Query

// KnowledgeStore is an optional persistence layer for KnowledgeObjects.
// It serves as Cache, Persistence, and History — not a required hop.
// Provider → Pipeline → KnowledgeRuntime bypasses Store entirely.
type KnowledgeStore = knowledge.KnowledgeStore

// Intent describes what knowledge is needed and within what constraints.
type Intent = knowledge.Intent

// Scope defines the boundaries for knowledge retrieval.
type Scope = knowledge.Scope

// Constraint is a key-value filter with an operator.
type Constraint = knowledge.Constraint

// TokenBudget allocates token usage between graph context and LLM reasoning.
type TokenBudget = knowledge.TokenBudget

// Normalizer converts Raw bytes into Normalized text.
type Normalizer = knowledge.Normalizer

// EntityMatcher attempts to match a KnowledgeObject against existing entities.
type EntityMatcher = knowledge.EntityMatcher

// Validator checks whether a merge result is consistent.
type Validator = knowledge.Validator

// Summarizer compresses Normalized text into a concise Summary.
type Summarizer = knowledge.Summarizer

// ResolveResult is the outcome of entity matching.
type ResolveResult = knowledge.ResolveResult

// ValidationResult is the outcome of conflict validation.
type ValidationResult = knowledge.ValidationResult

// Conflict describes a field-level disagreement between sources.
type Conflict = knowledge.Conflict

// KnowledgePipeline orchestrates processing of KnowledgeObjects through
// Normalizer → EntityMatcher → Validator → Summarizer stages.
type KnowledgePipeline = knowledge.KnowledgePipeline

// Object type constants.
const (
	ObjectMemory       = knowledge.ObjectMemory
	ObjectUser         = knowledge.ObjectUser
	ObjectProject      = knowledge.ObjectProject
	ObjectCode         = knowledge.ObjectCode
	ObjectIssue        = knowledge.ObjectIssue
	ObjectCommit       = knowledge.ObjectCommit
	ObjectDecision     = knowledge.ObjectDecision
	ObjectDocument     = knowledge.ObjectDocument
	ObjectToolResult   = knowledge.ObjectToolResult
	ObjectWorkflow     = knowledge.ObjectWorkflow
	ObjectRuntime      = knowledge.ObjectRuntime
	ObjectArchitecture = knowledge.ObjectArchitecture
)

// Built-in relation names.
const (
	RelDependsOn   = knowledge.RelDependsOn
	RelCalls       = knowledge.RelCalls
	RelCauses      = knowledge.RelCauses
	RelFixes       = knowledge.RelFixes
	RelBelongsTo   = knowledge.RelBelongsTo
	RelUses        = knowledge.RelUses
	RelImplements  = knowledge.RelImplements
	RelSimilarTo   = knowledge.RelSimilarTo
	RelGeneratedBy = knowledge.RelGeneratedBy
	RelDecidedBy   = knowledge.RelDecidedBy
	RelSupersedes  = knowledge.RelSupersedes
	RelLearnsFrom  = knowledge.RelLearnsFrom
)

// NewKnowledgePipeline creates a KnowledgePipeline with the given processors.
var NewKnowledgePipeline = knowledge.NewKnowledgePipeline
