package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/Timwood0x10/ares/internal/knowledge/runtime"
	apiknowledge "github.com/Timwood0x10/ares/internal/knowledgeapi"
)

// ServiceAdapter implements apiknowledge.KnowledgeService by wrapping
// an internal KnowledgeRuntime.
//
// Design rationale: the internal runtime exposes Execute() which runs
// the full Plan → Load → Link → Reduce pipeline. This adapter maps the
// four public methods onto the runtime's real capabilities:
//
//   - BuildGraph     → runtime.Execute(goal, budget)
//   - CompileContext → graph.Nodes summarized into a markdown block
//   - Query          → filter graph.Nodes by Query criteria (stateless for now)
//   - Distill        → convert raw bytes into a KnowledgeObject
//
// Args:
//   - rt - the internal KnowledgeRuntime (must not be nil).
//
// Returns:
//   - *ServiceAdapter - the adapted service.
//   - error           - non-nil if rt is nil.
func NewServiceAdapter(rt *runtime.KnowledgeRuntime) (*ServiceAdapter, error) {
	if rt == nil {
		return nil, errors.New("knowledge service: KnowledgeRuntime is nil")
	}
	return &ServiceAdapter{rt: rt}, nil
}

// ServiceAdapter bridges internal KnowledgeRuntime to public KnowledgeService.
type ServiceAdapter struct {
	rt *runtime.KnowledgeRuntime
}

// BuildGraph constructs a WorkingGraph for the given intent.
// It delegates to KnowledgeRuntime.Execute with the intent's goal and budget.
func (a *ServiceAdapter) BuildGraph(ctx context.Context, intent apiknowledge.Intent) (*apiknowledge.WorkingGraph, error) {
	if intent.Goal == "" {
		return nil, apiknowledge.ErrNilIntent
	}
	graph, err := a.rt.Execute(ctx, intent.Goal, intent.Budget, nil)
	if err != nil {
		return nil, fmt.Errorf("knowledge service: build graph: %w", err)
	}
	// WorkingGraph is the same struct via type alias, safe to return directly.
	return graph, nil
}

// CompileContext compresses a WorkingGraph into a token-efficient
// markdown representation for LLM consumption.
//
// Format: one bullet per node, containing the node's summary.
// This is intentionally simple — production callers may substitute a
// richer compiler.
func (a *ServiceAdapter) CompileContext(_ context.Context, graph *apiknowledge.WorkingGraph) (string, error) {
	if graph == nil {
		return "", apiknowledge.ErrNilGraph
	}
	var b strings.Builder
	for id, node := range graph.Nodes {
		summary := node.Summary
		if summary == "" {
			summary = node.Normalized
		}
		_, _ = fmt.Fprintf(&b, "- %s (%s): %s\n", id, node.Type, summary)
	}
	return b.String(), nil
}

// Query searches the knowledge store for objects matching the query.
//
// NOT IMPLEMENTED: this adapter is stateless (it wraps a WorkingGraph, not a
// store), so it cannot answer queries. Per the no-fake-implementation rule
// it fails loud with ErrQueryUnsupported instead of returning an empty slice
// that looks like "no results" — a caller filtering on an empty result
// silently degrades instead of learning the capability is absent.
func (a *ServiceAdapter) Query(_ context.Context, _ apiknowledge.Query) ([]*apiknowledge.KnowledgeObject, error) {
	return nil, apiknowledge.ErrQueryUnsupported
}

// Distill converts raw memory into structured KnowledgeObjects.
//
// Current implementation: returns a single KnowledgeObject wrapping the
// raw bytes. A future version will run the full Normalizer →
// EntityMatcher → Validator → Summarizer pipeline.
//
// The object ID is a content hash (sha256 over tenant + raw bytes), NOT the
// byte length: two different memories of equal length collided on a
// length-derived ID, so the second one silently overwrote the first wherever
// IDs are primary keys. Hashing the tenant into the ID also keeps two tenants
// distilling identical content from sharing one object — the store's ID space
// is global, not per-namespace. The same content re-distilled reproduces the
// same ID, preserving the idempotent upsert the length scheme accidentally
// provided.
func (a *ServiceAdapter) Distill(_ context.Context, rawMemory []byte, tenantID string) ([]*apiknowledge.KnowledgeObject, error) {
	if tenantID == "" {
		return nil, apiknowledge.ErrEmptyTenantID
	}
	if len(rawMemory) == 0 {
		return nil, nil
	}
	h := sha256.New()
	_, _ = h.Write([]byte(tenantID))
	_, _ = h.Write([]byte{0}) // delimiter: tenant "a" + content "bc" ≠ tenant "ab" + content "c"
	_, _ = h.Write(rawMemory)
	obj := &apiknowledge.KnowledgeObject{
		ID:        "distilled-" + hex.EncodeToString(h.Sum(nil)[:16]),
		Type:      apiknowledge.ObjectMemory,
		Namespace: tenantID,
		Raw:       rawMemory,
	}
	return []*apiknowledge.KnowledgeObject{obj}, nil
}

// Ensure ServiceAdapter implements the public KnowledgeService interface.
var _ apiknowledge.KnowledgeService = (*ServiceAdapter)(nil)
