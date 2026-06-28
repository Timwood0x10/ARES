package monitoring

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/monitoring/dag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockDAGPrunable implements DAGPrunable for testing.
type mockDAGPrunable struct {
	mu           sync.Mutex
	nodes        map[string]dag.NodeStatus
	nodeDetails  map[string]*dag.DAGNode
	removed      []string
	trimmed      map[string]int
	trimTimeline map[string]int
}

func newMockDAGPrunable() *mockDAGPrunable {
	return &mockDAGPrunable{
		nodes:        make(map[string]dag.NodeStatus),
		nodeDetails:  make(map[string]*dag.DAGNode),
		trimmed:      make(map[string]int),
		trimTimeline: make(map[string]int),
	}
}

func (m *mockDAGPrunable) Nodes() map[string]dag.NodeStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make(map[string]dag.NodeStatus, len(m.nodes))
	for k, v := range m.nodes {
		cp[k] = v
	}
	return cp
}

func (m *mockDAGPrunable) RemoveNode(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removed = append(m.removed, id)
	delete(m.nodes, id)
	return nil
}

func (m *mockDAGPrunable) TrimTimeline(id string, maxLen int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.trimTimeline[id] = maxLen
	return nil
}

func (m *mockDAGPrunable) GetNode(id string) (*dag.DAGNode, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nodeDetails[id]
	if !ok {
		return nil, false
	}
	cp := *n
	return &cp, true
}

func (m *mockDAGPrunable) removedIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]string, len(m.removed))
	copy(cp, m.removed)
	return cp
}

func (m *mockDAGPrunable) timelineTrims() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make(map[string]int, len(m.trimTimeline))
	for k, v := range m.trimTimeline {
		cp[k] = v
	}
	return cp
}

func TestPruner_NewPruner(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		mp := NewMainPage()
		p := NewPruner(mp, PruneConfig{})
		require.NotNil(t, p)
		assert.Equal(t, 24*time.Hour, p.config.MaxAgentAge)
		assert.Equal(t, 10000, p.config.MaxEvents)
		assert.Equal(t, 100, p.config.MaxTracesPerAgent)
		assert.Equal(t, 500, p.config.MaxTimelineLen)
		assert.Equal(t, 5*time.Minute, p.config.PruneInterval)
	})

	t.Run("custom", func(t *testing.T) {
		mp := NewMainPage()
		cfg := PruneConfig{
			MaxAgentAge:   1 * time.Hour,
			MaxEvents:     500,
			MaxTimelineLen: 50,
			PruneInterval: 30 * time.Second,
		}
		p := NewPruner(mp, cfg)
		require.NotNil(t, p)
		assert.Equal(t, 1*time.Hour, p.config.MaxAgentAge)
		assert.Equal(t, 500, p.config.MaxEvents)
		assert.Equal(t, 50, p.config.MaxTimelineLen)
	})
}

func TestPruner_StartStop(t *testing.T) {
	mp := NewMainPage()
	p := NewPruner(mp, PruneConfig{PruneInterval: 100 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	p.Start(ctx)
	cancel()
	p.Stop()
	// Should not panic or deadlock.
}

func TestPruner_StopIdempotent(t *testing.T) {
	mp := NewMainPage()
	p := NewPruner(mp, PruneConfig{})
	p.Stop()
	p.Stop()
}

func TestPruner_pruneDAG(t *testing.T) {
	tests := []struct {
		name       string
		nodes      map[string]dag.NodeStatus
		details    map[string]*dag.DAGNode
		maxAge     time.Duration
		wantRemove []string
	}{
		{
			name:  "no nodes",
			nodes: map[string]dag.NodeStatus{},
		},
		{
			name: "removes old dead node",
			nodes: map[string]dag.NodeStatus{
				"n1": dag.StatusDead,
				"n2": dag.StatusRunning,
			},
			details: map[string]*dag.DAGNode{
				"n1": {ID: "n1", Status: dag.StatusDead, UpdatedAt: time.Now().Add(-25 * time.Hour)},
				"n2": {ID: "n2", Status: dag.StatusRunning, UpdatedAt: time.Now()},
			},
			maxAge:     24 * time.Hour,
			wantRemove: []string{"n1"},
		},
		{
			name: "removes old completed node",
			nodes: map[string]dag.NodeStatus{
				"n1": dag.StatusCompleted,
			},
			details: map[string]*dag.DAGNode{
				"n1": {ID: "n1", Status: dag.StatusCompleted, UpdatedAt: time.Now().Add(-2 * time.Hour)},
			},
			maxAge:     1 * time.Hour,
			wantRemove: []string{"n1"},
		},
		{
			name: "keeps recent dead node",
			nodes: map[string]dag.NodeStatus{
				"n1": dag.StatusDead,
			},
			details: map[string]*dag.DAGNode{
				"n1": {ID: "n1", Status: dag.StatusDead, UpdatedAt: time.Now()},
			},
			maxAge:     24 * time.Hour,
			wantRemove: nil,
		},
		{
			name: "keeps running node regardless of age",
			nodes: map[string]dag.NodeStatus{
				"n1": dag.StatusRunning,
			},
			details: map[string]*dag.DAGNode{
				"n1": {ID: "n1", Status: dag.StatusRunning, UpdatedAt: time.Now().Add(-48 * time.Hour)},
			},
			maxAge:     24 * time.Hour,
			wantRemove: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := newMockDAGPrunable()
			mock.nodes = tt.nodes
			mock.nodeDetails = tt.details

			mp := NewMainPage()
			p := NewPruner(mp, PruneConfig{
				MaxAgentAge:    tt.maxAge,
				MaxTimelineLen: 500,
			}, WithPrunerDAG(mock))

			p.pruneDAG(context.Background())

			removed := mock.removedIDs()
			assert.ElementsMatch(t, tt.wantRemove, removed)
		})
	}
}

func TestPruner_pruneDAG_timeline(t *testing.T) {
	mock := newMockDAGPrunable()
	mock.nodes = map[string]dag.NodeStatus{
		"n1": dag.StatusRunning,
		"n2": dag.StatusCompleted,
	}
	mock.nodeDetails = map[string]*dag.DAGNode{
		"n1": {ID: "n1", Status: dag.StatusRunning, UpdatedAt: time.Now()},
		"n2": {ID: "n2", Status: dag.StatusCompleted, UpdatedAt: time.Now()},
	}

	mp := NewMainPage()
	p := NewPruner(mp, PruneConfig{
		MaxAgentAge:    24 * time.Hour,
		MaxTimelineLen: 100,
	}, WithPrunerDAG(mock))

	p.pruneDAG(context.Background())

	trims := mock.timelineTrims()
	assert.Equal(t, 100, trims["n1"])
	assert.Equal(t, 100, trims["n2"])
}

func TestPruner_pruneDAG_nilDAG(t *testing.T) {
	mp := NewMainPage()
	p := NewPruner(mp, PruneConfig{})
	// Should not panic when DAG is nil.
	p.pruneDAG(context.Background())
}

// mockTrimableTab implements TrimableTab for testing.
type mockTrimableTab struct {
	name    string
	count   int
	trimmed int
}

func (t *mockTrimableTab) Name() string                       { return t.name }
func (t *mockTrimableTab) Label() string                      { return t.name }
func (t *mockTrimableTab) HandleEvent(_ *ares_events.Event)   {}
func (t *mockTrimableTab) Snapshot() any                      { return nil }
func (t *mockTrimableTab) Trim(maxLen int) {
	if t.count > maxLen {
		t.trimmed = t.count - maxLen
		t.count = maxLen
	}
}

func TestPruner_pruneTabs(t *testing.T) {
	t.Run("nil main page", func(t *testing.T) {
		p := &Pruner{config: PruneConfig{MaxEvents: 10000}}
		p.mainPage = nil
		// Should not panic.
		p.pruneTabs()
	})

	t.Run("trims trimable tabs", func(t *testing.T) {
		tab := &mockTrimableTab{name: "events", count: 20000}
		mp := &MainPage{
			tabs: map[string]Tab{"events": tab},
		}
		p := NewPruner(mp, PruneConfig{MaxEvents: 10000})
		p.pruneTabs()
		assert.Equal(t, 10000, tab.count)
		assert.Equal(t, 10000, tab.trimmed)
	})

	t.Run("skips non-trimable tabs", func(t *testing.T) {
		mp := &MainPage{
			tabs: map[string]Tab{"basic": &mockTrimableTab{name: "basic", count: 5}},
		}
		p := NewPruner(mp, PruneConfig{MaxEvents: 10000})
		p.pruneTabs()
		// Should not panic.
	})
}

func TestPruner_prune_canceled(t *testing.T) {
	mock := newMockDAGPrunable()
	mock.nodes = map[string]dag.NodeStatus{
		"n1": dag.StatusDead,
	}
	mock.nodeDetails = map[string]*dag.DAGNode{
		"n1": {ID: "n1", Status: dag.StatusDead, UpdatedAt: time.Now().Add(-48 * time.Hour)},
	}

	mp := NewMainPage()
	p := NewPruner(mp, PruneConfig{
		MaxAgentAge:    1 * time.Hour,
		MaxTimelineLen: 100,
	}, WithPrunerDAG(mock))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Already canceled.

	p.prune(ctx)

	// Should not remove because context is canceled.
	removed := mock.removedIDs()
	assert.Empty(t, removed)
}
