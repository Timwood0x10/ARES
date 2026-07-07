package evolution

import (
	"context"
	"sync"
	"testing"
	"time"

	ares_evolution "github.com/Timwood0x10/ares/internal/ares_evolution"
	"github.com/Timwood0x10/ares/internal/knowledge"
)

// mockStrategyStore implements StrategyStore for tests.
type mockStrategyStore struct {
	mu      sync.Mutex
	active  *ares_evolution.Strategy
	history []*ares_evolution.Strategy
}

func (m *mockStrategyStore) GetActive(_ context.Context) (*ares_evolution.Strategy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active, nil
}

func (m *mockStrategyStore) GetHistory(_ context.Context, _ string, n int) ([]*ares_evolution.Strategy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n > len(m.history) {
		n = len(m.history)
	}
	return m.history[:n], nil
}

func TestNew(t *testing.T) {
	p := New("evo", &mockStrategyStore{})
	if p.Name() != "evo" {
		t.Fatalf("expected name 'evo', got %q", p.Name())
	}
}

func TestNewEmptyName(t *testing.T) {
	p := New("", &mockStrategyStore{})
	if p.Name() != "" {
		t.Fatalf("expected empty name, got %q", p.Name())
	}
}

func TestIntentMatch(t *testing.T) {
	p := New("test", &mockStrategyStore{})

	tests := []struct {
		goal    string
		wantGeq float64
	}{
		{"what was the decision", 0.9},
		{"evolution history", 0.9},
		{"strategy for optimization", 0.9},
		{"why did we choose this", 0.9},
		{"improve performance", 0.9},
		{"hello world", 0.3},
		{"", 0.3},
	}

	for _, tt := range tests {
		t.Run(tt.goal, func(t *testing.T) {
			got := p.IntentMatch(knowledge.Intent{Goal: tt.goal})
			if got < tt.wantGeq {
				t.Errorf("IntentMatch(%q) = %.2f, want >= %.2f", tt.goal, got, tt.wantGeq)
			}
		})
	}
}

func TestStreamNoActiveStrategy(t *testing.T) {
	store := &mockStrategyStore{}
	p := New("test", store)

	objCh, errCh := p.Stream(context.Background(), knowledge.Intent{Scope: knowledge.Scope{MaxObjects: 10}})
	var objs []*knowledge.KnowledgeObject
	for obj := range objCh {
		objs = append(objs, obj)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	default:
	}

	if len(objs) != 0 {
		t.Fatalf("expected 0 objects for no active strategy, got %d", len(objs))
	}
}

func TestStreamWithActiveStrategy(t *testing.T) {
	now := time.Now()
	store := &mockStrategyStore{
		active: &ares_evolution.Strategy{
			ID:        "s1",
			Version:   5,
			Score:     3.0,
			Params:    map[string]any{"temp": 0.7},
			CreatedAt: now,
		},
		history: []*ares_evolution.Strategy{
			{ID: "s1", Version: 4, Score: 2.5, CreatedAt: now.Add(-24 * time.Hour)},
			{ID: "s1", Version: 3, Score: 2.0, CreatedAt: now.Add(-48 * time.Hour)},
		},
	}
	p := New("evo-test", store)

	objCh, errCh := p.Stream(context.Background(), knowledge.Intent{Scope: knowledge.Scope{MaxObjects: 10}})
	var objs []*knowledge.KnowledgeObject
	for obj := range objCh {
		objs = append(objs, obj)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	default:
	}

	if len(objs) != 3 {
		t.Fatalf("expected 3 objects (1 active + 2 history), got %d", len(objs))
	}

	// First object should be the active strategy (v5).
	if objs[0].Metadata["version"] != 5 {
		t.Errorf("expected first object to be v5 (active), got version %v", objs[0].Metadata["version"])
	}
}

func TestStreamMaxResults(t *testing.T) {
	now := time.Now()
	store := &mockStrategyStore{
		active: &ares_evolution.Strategy{
			ID: "s1", Version: 10, Score: 3.0, CreatedAt: now,
		},
		history: []*ares_evolution.Strategy{
			{ID: "s1", Version: 9, Score: 2.9, CreatedAt: now.Add(-1 * time.Hour)},
			{ID: "s1", Version: 8, Score: 2.8, CreatedAt: now.Add(-2 * time.Hour)},
		},
	}
	p := New("test", store)

	// MaxObjects=1 should only return the active strategy.
	objCh, _ := p.Stream(context.Background(), knowledge.Intent{Scope: knowledge.Scope{MaxObjects: 1}})
	var objs []*knowledge.KnowledgeObject
	for obj := range objCh {
		objs = append(objs, obj)
	}
	if len(objs) != 1 {
		t.Fatalf("expected 1 object (MaxObjects=1), got %d", len(objs))
	}
}

func TestStreamCancelContext(t *testing.T) {
	store := &mockStrategyStore{
		active: &ares_evolution.Strategy{
			ID: "s1", Version: 1, Score: 1.0, CreatedAt: time.Now(),
		},
	}
	p := New("test", store)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	objCh, _ := p.Stream(ctx, knowledge.Intent{Scope: knowledge.Scope{MaxObjects: 10}})
	var objs []*knowledge.KnowledgeObject
	for obj := range objCh {
		objs = append(objs, obj)
	}
	if len(objs) != 0 {
		t.Fatalf("expected 0 objects with cancelled context, got %d", len(objs))
	}
}
