package flight

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/evidence"
)

// The DEEP_CODE_REVIEW_2026 HIGH regressions for the flight recorder:
// evidence I/O off the hot path (#15), per-agent roots (#16), and the
// bounded genealogy (#17).

// --- #15: evidence I/O off the subscription hot path ---

// blockingEvidenceStore wedges every Append until release closes.
type blockingEvidenceStore struct {
	release chan struct{}
	appends chan struct{} // signals each Append arrival (buffered)
}

func (s *blockingEvidenceStore) Append(ctx context.Context, e evidence.Evidence) error {
	if s.appends != nil {
		s.appends <- struct{}{}
	}
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *blockingEvidenceStore) Query(ctx context.Context, f evidence.Filter) ([]evidence.Evidence, error) {
	return nil, nil
}

func (s *blockingEvidenceStore) Aggregate(ctx context.Context, f evidence.Filter, fn evidence.AggregateFn) (float64, error) {
	return 0, nil
}

// TestCollectorEvidenceEmissionDoesNotBlockHotPath pins #15: with the
// evidence store wedged, processing events must not block on the store I/O —
// the emissions are buffered and flushed in the background, and an
// over-full buffer drops (counted) instead of stalling the loop. The
// regression: EmitWithMeta ran synchronously in processEvent, so a slow
// store stalled event consumption until the publisher's bounded channel
// started silently dropping flight events.
func TestCollectorEvidenceEmissionDoesNotBlockHotPath(t *testing.T) {
	store := &blockingEvidenceStore{release: make(chan struct{})}
	c := NewCollector(CollectorConfig{EvidenceStore: store})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.evidenceFlushLoop(ctx)

	// Enough events to overflow the evidence queue (each task.completed
	// enqueues up to three emissions: execution trace + workflow +
	// scheduler fitness) while the flusher is wedged on the first Append.
	const events = 200
	start := time.Now()
	for i := 0; i < events; i++ {
		c.processEvent(ctx, &ares_events.Event{
			Type:     ares_events.EventTaskCompleted,
			ID:       fmt.Sprintf("evt-%d", i),
			StreamID: "agent-1",
			Payload:  map[string]any{},
		})
	}
	elapsed := time.Since(start)

	require.Less(t, elapsed, time.Second,
		"processing %d events against a wedged evidence store must not block on store I/O (took %s)", events, elapsed)
	require.Greater(t, c.DroppedEvidence(), uint64(0),
		"an overflowing evidence queue must drop-and-count, not block")

	// Unblock and let the flusher drain; Stop is not needed for the
	// assertion, but the goroutine must be able to finish.
	close(store.release)
}

// TestCollectorEvidenceEmittedThroughBackgroundFlusher pins the correctness
// half of #15: buffered evidence still lands in the store via the full
// Start → subscribe → collect → flush pipeline.
func TestCollectorEvidenceEmittedThroughBackgroundFlusher(t *testing.T) {
	evStore := evidence.NewMemoryStore()
	src := ares_events.NewMemoryEventStore()
	c := NewCollector(CollectorConfig{EventStore: src, EvidenceStore: evStore})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, c.Start(ctx))
	defer c.Stop()

	require.NoError(t, src.Append(ctx, "agent-1", []*ares_events.Event{{
		Type:      ares_events.EventTaskCompleted,
		ID:        "evt-done",
		StreamID:  "agent-1",
		Timestamp: time.Now(),
		Payload:   map[string]any{},
	}}, -1))

	// The emission is asynchronous: poll until both the execution trace and
	// the workflow fitness evidence land.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		traces, err := evStore.Query(ctx, evidence.Filter{Source: "flight", Kind: evidence.KindExecutionTrace})
		require.NoError(t, err)
		fitness, err := evStore.Query(ctx, evidence.Filter{Source: "workflow", Kind: evidence.KindFitness})
		require.NoError(t, err)
		if len(traces) >= 1 && len(fitness) >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("buffered evidence never reached the store within the deadline")
}

// --- #16: per-agent roots ---

// TestGraphMultiAgentExportsAllRoots pins #16: with multiple root agents,
// every root's subtree is rendered — the regression kept a single root
// (first or last, depending on the era), so the export silently dropped
// every other agent's subtree.
func TestGraphMultiAgentExportsAllRoots(t *testing.T) {
	g := NewGraph()
	now := time.Now()

	// Two root agents, each with a child.
	g.AddNode(&GraphNode{ID: "agent-1", Type: NodeAgent, Name: "Alpha", StartAt: now})
	g.AddNode(&GraphNode{ID: "tool-1", ParentID: "agent-1", Type: NodeTool, Name: "Search", StartAt: now})
	g.AddNode(&GraphNode{ID: "agent-2", Type: NodeAgent, Name: "Beta", StartAt: now.Add(time.Second)})
	g.AddNode(&GraphNode{ID: "tool-2", ParentID: "agent-2", Type: NodeTool, Name: "Runner", StartAt: now.Add(time.Second)})

	// Root() keeps the first root (backward-compatible accessor).
	if g.Root() == nil || g.Root().Name != "Alpha" {
		t.Fatalf("Root() = %+v, want the first root Alpha", g.Root())
	}

	roots := g.Roots()
	if len(roots) != 2 {
		t.Fatalf("Roots() = %d roots, want 2 (one per agent)", len(roots))
	}

	mermaid := g.ExportMermaid()
	for _, want := range []string{"Alpha", "Beta", "Search", "Runner"} {
		if !strings.Contains(mermaid, want) {
			t.Errorf("ExportMermaid must render agent %q's subtree; output:\n%s", want, mermaid)
		}
	}

	dot := g.ExportDOT()
	for _, want := range []string{"Alpha", "Beta"} {
		if !strings.Contains(dot, want) {
			t.Errorf("ExportDOT must render agent %q's subtree; output:\n%s", want, dot)
		}
	}

	// Depth is the max across roots (1 here: root → tool).
	if d := g.Depth(); d != 1 {
		t.Errorf("Depth() = %d, want 1", d)
	}

	// JSON export covers every root.
	data, err := g.ExportJSON()
	require.NoError(t, err)
	assert.Contains(t, string(data), "Alpha")
	assert.Contains(t, string(data), "Beta")
}

// TestGraphRootReplacementOnAgentRestart pins the restart half of #16: a
// re-added root id (agent restart reuses the agent id) replaces its earlier
// root entry instead of duplicating it.
func TestGraphRootReplacementOnAgentRestart(t *testing.T) {
	g := NewGraph()
	now := time.Now()

	g.AddNode(&GraphNode{ID: "agent-1", Type: NodeAgent, Name: "before-restart", StartAt: now})
	g.AddNode(&GraphNode{ID: "agent-1", Type: NodeAgent, Name: "after-restart", StartAt: now.Add(time.Second)})

	roots := g.Roots()
	if len(roots) != 1 {
		t.Fatalf("re-added root must replace, not duplicate: got %d roots", len(roots))
	}
	assert.Equal(t, "after-restart", roots[0].Name)
}

// --- #17: bounded genealogy ---

// TestGenealogyBoundedByRootEviction pins #17: with many independent root
// lineages, the oldest root subtrees are retired once the node cap is hit —
// the genealogy no longer grows without bound under agent rotation.
func TestGenealogyBoundedByRootEviction(t *testing.T) {
	orig := maxGenealogyNodes
	maxGenealogyNodes = 10
	t.Cleanup(func() { maxGenealogyNodes = orig })

	g := NewGenealogy()
	// 20 root agents, each with one child: 40 nodes total, cap 10.
	for i := 0; i < 20; i++ {
		g.RecordSpawn("", fmt.Sprintf("root-%02d", i), "sub", nil)
		g.RecordSpawn(fmt.Sprintf("root-%02d", i), fmt.Sprintf("child-%02d", i), "sub", nil)
	}

	nodes := g.AllNodes()
	require.LessOrEqual(t, len(nodes), maxGenealogyNodes,
		"genealogy must be bounded by the node cap, got %d", len(nodes))

	// The surviving roots are the NEWEST lineages (oldest retired first);
	// with a 10-node cap each lineage costs 2 nodes, so the newest five
	// survive and everything older is gone.
	roots := g.Roots()
	require.NotEmpty(t, roots)
	sawNewest := false
	for _, r := range roots {
		var num int
		if _, err := fmt.Sscanf(r.ID, "root-%d", &num); err != nil {
			t.Fatalf("unexpected root id %q: %v", r.ID, err)
		}
		if num >= 15 {
			sawNewest = true
		} else {
			t.Errorf("old lineage %s must have been evicted (only the newest roots may survive)", r.ID)
		}
	}
	assert.True(t, sawNewest, "the newest root lineage must survive")
	if _, ok := g.GetNode("root-00"); ok {
		t.Error("the oldest root must be evicted")
	}
	if _, ok := g.GetNode("child-00"); ok {
		t.Error("the oldest root's subtree must be evicted with it")
	}
}

// TestGenealogyBoundedByDeadLeafPruning pins the single-subtree half of #17:
// one long-lived root with many dead children gets its oldest dead leaves
// pruned, while live children are never evicted.
func TestGenealogyBoundedByDeadLeafPruning(t *testing.T) {
	orig := maxGenealogyNodes
	maxGenealogyNodes = 10
	t.Cleanup(func() { maxGenealogyNodes = orig })

	g := NewGenealogy()
	g.RecordRoot("long-lived", "sub", nil)

	// 20 dead children + 2 live ones.
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("dead-%02d", i)
		g.RecordSpawn("long-lived", id, "sub", nil)
		g.RecordDeath(id)
	}
	g.RecordSpawn("long-lived", "live-a", "sub", nil)
	g.RecordSpawn("long-lived", "live-b", "sub", nil)

	nodes := g.AllNodes()
	require.LessOrEqual(t, len(nodes), maxGenealogyNodes,
		"single-subtree genealogy must be bounded by dead-leaf pruning, got %d", len(nodes))

	// Live agents are never pruned.
	assert.True(t, g.IsAlive("long-lived"))
	assert.True(t, g.IsAlive("live-a"))
	assert.True(t, g.IsAlive("live-b"))

	// Structure stays consistent: every tracked node is either a root or has
	// its parent tracked (no orphans).
	for _, n := range nodes {
		if n.ParentID == "" {
			continue
		}
		_, ok := g.GetNode(n.ParentID)
		assert.True(t, ok, "node %s's parent %s must still be tracked (no orphans)", n.ID, n.ParentID)
	}
}
