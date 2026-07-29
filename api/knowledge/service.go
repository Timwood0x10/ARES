// Package knowledge provides the public API for the ARES Knowledge
// Fabric (AKF) — the Agent Knowledge Graph (AKG).
//
// This package exposes the KnowledgeService interface, allowing external
// modules (including AI assistants) to build, query, and compile knowledge
// graphs without importing internal packages.
package knowledge

import (
	"context"
	"errors"
)

// KnowledgeService is the public API for the AKG.
// It exposes the four core operations of the Knowledge Fabric.
type KnowledgeService interface {
	// BuildGraph constructs a WorkingGraph for the given intent.
	BuildGraph(ctx context.Context, intent Intent) (*WorkingGraph, error)

	// CompileContext compresses a WorkingGraph into a token-efficient
	// representation for LLM consumption.
	CompileContext(ctx context.Context, graph *WorkingGraph) (string, error)

	// Query searches the knowledge store for objects matching the query.
	Query(ctx context.Context, query Query) ([]*KnowledgeObject, error)

	// Distill converts raw memory into structured KnowledgeObjects.
	Distill(ctx context.Context, rawMemory []byte, tenantID string) ([]*KnowledgeObject, error)
}

// Sentinel errors for the knowledge service.
var (
	ErrNilIntent     = errors.New("knowledge: intent goal is empty")
	ErrEmptyTenantID = errors.New("knowledge: tenant ID is empty")
	ErrNilGraph      = errors.New("knowledge: graph is nil")
)
