// Package compiler — MemoryEmitter is the Consumer layer (design §5.3) that
// projects a KM SubGraph into distillation.Memory records and persists them
// via a pluggable MemoryStore. It is entirely rule-based: it reuses the
// distillation package's MemoryClassifier and ImportanceScorer, so it makes
// ZERO LLM calls. The Emitter only writes; it never selects or scores
// candidates for retrieval.
package compiler

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_memory/distillation"
)

// MemoryStore is the storage-agnostic trait for persisting distilled memories.
// Implementations may wrap LanceDB, SQLite, Postgres, Redis, or an in-memory
// store. The Emitter only writes; it never selects or scores.
type MemoryStore interface {
	// Save persists memories. Returns the count actually stored.
	Save(ctx context.Context, memories []distillation.Memory) (int, error)
	// Name returns the store's identifier (e.g. "in-memory", "lancedb").
	Name() string
}

// InMemoryMemoryStore is a default non-persistent MemoryStore for tests and
// graceful degradation when no backing store is configured. It is safe for
// concurrent use.
type InMemoryMemoryStore struct {
	mu       sync.Mutex
	memories []distillation.Memory
}

// NewInMemoryMemoryStore creates an empty InMemoryMemoryStore.
func NewInMemoryMemoryStore() *InMemoryMemoryStore {
	return &InMemoryMemoryStore{}
}

// Save appends the memories and returns the count stored. It honors context
// cancellation.
//
// Args:
//
//	ctx       — context for cancellation and timeout.
//	memories  — memories to persist; nil/empty returns 0.
//
// Returns:
//
//	int   — number of memories stored.
//	error — non-nil if the context is cancelled or nil.
func (s *InMemoryMemoryStore) Save(ctx context.Context, memories []distillation.Memory) (int, error) {
	if ctx == nil {
		return 0, fmt.Errorf("in-memory memory store: context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("in-memory memory store: context cancelled: %w", err)
	}
	if len(memories) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.memories = append(s.memories, memories...)
	return len(memories), nil
}

// Name returns "in-memory".
func (s *InMemoryMemoryStore) Name() string { return "in-memory" }

// All returns a copy of the stored memories. Intended as a test helper; the
// returned slice may be modified without affecting the store.
func (s *InMemoryMemoryStore) All() []distillation.Memory {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]distillation.Memory, len(s.memories))
	copy(out, s.memories)
	return out
}

// MemoryEmitter converts a KM SubGraph into distillation.Memory records and
// writes them via a MemoryStore. It reuses the distillation classifier+scorer
// (rule-based, zero LLM) to assign type and importance. It does NOT select.
type MemoryEmitter struct {
	store      MemoryStore
	classifier *distillation.MemoryClassifier
	scorer     *distillation.ImportanceScorer
}

// NewMemoryEmitter creates a MemoryEmitter backed by the given store. The
// store may be nil for construction, but Emit will then return an error.
//
// Args:
//
//	store — the MemoryStore used to persist memories; may be nil.
//
// Returns:
//
//	*MemoryEmitter — the configured emitter. Always non-nil.
func NewMemoryEmitter(store MemoryStore) *MemoryEmitter {
	return &MemoryEmitter{
		store:      store,
		classifier: distillation.NewMemoryClassifier(),
		scorer:     distillation.NewImportanceScorer(),
	}
}

// Emit converts sub.Nodes to memories and persists them. NodeMemory nodes ARE
// the memory representation and are emitted like any other node. Nodes whose
// computed importance is below the scorer's threshold are skipped. Returns the
// count actually stored.
//
// Args:
//
//	ctx      — context for cancellation and timeout.
//	sub      — the SubGraph to emit; nil returns (0, nil).
//	tenantID — tenant identifier recorded in each memory's metadata.
//	userID   — user identifier recorded in each memory's metadata.
//
// Returns:
//
//	int   — number of memories persisted.
//	error — non-nil if the store is nil or persistence fails.
func (e *MemoryEmitter) Emit(ctx context.Context, sub *SubGraph, tenantID, userID string) (int, error) {
	if e.store == nil {
		return 0, fmt.Errorf("memory emitter: store is not configured")
	}
	if ctx == nil {
		return 0, fmt.Errorf("memory emitter: context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("memory emitter: context cancelled: %w", err)
	}
	if sub == nil || len(sub.Nodes) == 0 {
		return 0, nil
	}

	memories := make([]distillation.Memory, 0, len(sub.Nodes))
	for _, n := range sub.Nodes {
		if n == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return 0, fmt.Errorf("memory emitter: context cancelled: %w", err)
		}

		problem := "Node " + string(n.Type)
		solution := nodeSummary(n)

		memType := e.classifier.ClassifyMemory(&distillation.Experience{
			Problem:  problem,
			Solution: solution,
		})
		score := e.scorer.ScoreMemory(memType, problem, solution)

		// NodeMemory nodes are already distilled (they passed the distiller's
		// threshold), so emit them unconditionally using their stored confidence
		// as importance. Non-memory nodes are still gated by the scorer.
		if n.Type == NodeMemory {
			if summary := attrString(n, "summary"); summary != "" {
				solution = summary
			}
			score = n.Confidence
		} else if !e.scorer.ShouldKeep(score) {
			continue
		}

		memories = append(memories, buildMemory(n, memType, solution, score, tenantID, userID))
	}

	if len(memories) == 0 {
		return 0, nil
	}

	stored, err := e.store.Save(ctx, memories)
	if err != nil {
		return 0, fmt.Errorf("memory emitter: save: %w", err)
	}
	return stored, nil
}

// buildMemory assembles a distillation.Memory from a node and its computed
// classification. Metadata carries the provenance needed to trace a memory
// back to its source node, tenant, and user.
func buildMemory(n *Node, memType distillation.MemoryType, solution string, score float64, tenantID, userID string) distillation.Memory {
	return distillation.Memory{
		ID:         n.ID,
		Type:       memType,
		Content:    solution,
		Importance: score,
		Source:     n.Source,
		CreatedAt:  time.Now(),
		Metadata: map[string]interface{}{
			"node_id":     n.ID,
			"node_type":   string(n.Type),
			"memory_type": memType.String(),
			"tenant_id":   tenantID,
			"user_id":     userID,
			"confidence":  n.Confidence,
		},
	}
}

// nodeSummary derives a human-readable summary of a node from its attributes.
// It inspects type-specific fields in priority order so the consumer layer
// (MemoryEmitter, AKGBuilder) gets structured text without any LLM call:
//
//   - decisions: "choice" (with optional " (rejected: <rejection>)")
//   - facts:     "subject predicate object" triple
//   - goals:     "objective"
//   - all others: "description" / "text" / "name"
//
// Falls back to the node ID when no suitable attribute is present.
func nodeSummary(n *Node) string {
	if n == nil {
		return ""
	}
	switch n.Type {
	case NodeDecision:
		if choice := attrString(n, "choice"); choice != "" {
			if rej := attrString(n, "rejection"); rej != "" {
				return choice + " (rejected: " + rej + ")"
			}
			return choice
		}
	case NodeFact:
		subj := attrString(n, "subject")
		pred := attrString(n, "predicate")
		obj := attrString(n, "object")
		if subj != "" || pred != "" || obj != "" {
			return strings.TrimSpace(subj + " " + pred + " " + obj)
		}
	case NodeGoal:
		if obj := attrString(n, "objective"); obj != "" {
			return obj
		}
	}
	for _, key := range []string{attrDescription, "text", attrName} {
		if s := attrString(n, key); s != "" {
			return s
		}
	}
	return n.ID
}

// attrString reads a string-typed attribute. Returns "" when the attribute is
// absent or not a string.
func attrString(n *Node, key string) string {
	if n == nil || n.Attributes == nil {
		return ""
	}
	v, ok := n.Attributes[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}
