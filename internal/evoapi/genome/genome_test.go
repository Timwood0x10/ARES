package genome

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/evoapi/mutation"
)

func newTestStrategy(id string, params map[string]any) *mutation.Strategy {
	return &mutation.Strategy{
		ID:             id,
		Version:        1,
		Score:          0.5,
		PromptTemplate: "prompt-" + id,
		Params:         params,
		MutationType:   mutation.MutationRoot,
	}
}

// ── NewCrosser ──────────────────────────────────────────────────────────────

func TestNewCrosser_AllPromptModes(t *testing.T) {
	modes := []PromptCrossoverMode{PromptInherit, PromptHalfSplit, PromptUniform}
	for _, mode := range modes {
		c, err := NewCrosser(CrosserConfig{PromptMode: mode})
		if err != nil {
			t.Fatalf("NewCrosser(PromptMode=%d): %v", mode, err)
		}
		if c == nil {
			t.Fatal("NewCrosser returned nil")
		}
	}
}

func TestNewCrosser_AllCrossoverTypes(t *testing.T) {
	types := []CrossoverType{CrossoverUniform, CrossoverSinglePoint, CrossoverTwoPoint, CrossoverScattered, ""}
	for _, ct := range types {
		c, err := NewCrosser(CrosserConfig{CrossoverType: ct})
		if err != nil {
			t.Fatalf("NewCrosser(CrossoverType=%q): %v", ct, err)
		}
		if c == nil {
			t.Fatal("NewCrosser returned nil")
		}
	}
}

// ── Crosser.Crossover ───────────────────────────────────────────────────────

func TestCrossover_NilParents(t *testing.T) {
	c, err := NewCrosser(CrosserConfig{})
	if err != nil {
		t.Fatalf("NewCrosser: %v", err)
	}
	ctx := context.Background()

	_, err = c.Crossover(ctx, nil, newTestStrategy("b", map[string]any{"x": 1}))
	if err == nil {
		t.Error("expected error for nil parentA")
	}

	_, err = c.Crossover(ctx, newTestStrategy("a", map[string]any{"x": 1}), nil)
	if err == nil {
		t.Error("expected error for nil parentB")
	}
}

func TestCrossover_UniformDefault(t *testing.T) {
	c, err := NewCrosser(CrosserConfig{CrossoverType: CrossoverUniform})
	if err != nil {
		t.Fatalf("NewCrosser: %v", err)
	}

	a := newTestStrategy("a", map[string]any{"temp": 0.7, "top_k": 40})
	b := newTestStrategy("b", map[string]any{"temp": 0.3, "top_k": 20})

	child, err := c.Crossover(context.Background(), a, b)
	if err != nil {
		t.Fatalf("Crossover: %v", err)
	}
	if child == nil {
		t.Fatal("child is nil")
	}
	if len(child.Params) == 0 {
		t.Error("child params should not be empty")
	}
}

func TestCrossover_SinglePointDefault(t *testing.T) {
	c, err := NewCrosser(CrosserConfig{CrossoverType: CrossoverSinglePoint})
	if err != nil {
		t.Fatalf("NewCrosser: %v", err)
	}

	a := newTestStrategy("a", map[string]any{"k1": 1, "k2": 2, "k3": 3, "k4": 4})
	b := newTestStrategy("b", map[string]any{"k1": 10, "k2": 20, "k3": 30, "k4": 40})

	child, err := c.Crossover(context.Background(), a, b)
	if err != nil {
		t.Fatalf("Crossover: %v", err)
	}
	if child == nil {
		t.Fatal("child is nil")
	}
	if len(child.Params) != 4 {
		t.Errorf("child params len = %d, want 4", len(child.Params))
	}
}

func TestCrossover_TwoPointDefault(t *testing.T) {
	c, err := NewCrosser(CrosserConfig{CrossoverType: CrossoverTwoPoint})
	if err != nil {
		t.Fatalf("NewCrosser: %v", err)
	}

	a := newTestStrategy("a", map[string]any{"k1": 1, "k2": 2, "k3": 3, "k4": 4, "k5": 5, "k6": 6})
	b := newTestStrategy("b", map[string]any{"k1": 10, "k2": 20, "k3": 30, "k4": 40, "k5": 50, "k6": 60})

	child, err := c.Crossover(context.Background(), a, b)
	if err != nil {
		t.Fatalf("Crossover: %v", err)
	}
	if child == nil {
		t.Fatal("child is nil")
	}
	if len(child.Params) != 6 {
		t.Errorf("child params len = %d, want 6", len(child.Params))
	}
}

// ── Crosser.CrossWithType ───────────────────────────────────────────────────

func TestCrossWithType_AllTypes(t *testing.T) {
	c, err := NewCrosser(CrosserConfig{})
	if err != nil {
		t.Fatalf("NewCrosser: %v", err)
	}

	a := newTestStrategy("a", map[string]any{"p1": 1, "p2": 2, "p3": 3, "p4": 4})
	b := newTestStrategy("b", map[string]any{"p1": 10, "p2": 20, "p3": 30, "p4": 40})
	ctx := context.Background()

	types := []CrossoverType{CrossoverUniform, CrossoverSinglePoint, CrossoverTwoPoint, CrossoverScattered, ""}
	for _, ct := range types {
		child, err := c.CrossWithType(ctx, a, b, ct)
		if err != nil {
			t.Errorf("CrossWithType(%q): %v", ct, err)
			continue
		}
		if child == nil {
			t.Errorf("CrossWithType(%q) returned nil child", ct)
		}
	}
}

func TestCrossWithType_Unsupported(t *testing.T) {
	c, err := NewCrosser(CrosserConfig{})
	if err != nil {
		t.Fatalf("NewCrosser: %v", err)
	}

	a := newTestStrategy("a", map[string]any{"x": 1})
	b := newTestStrategy("b", map[string]any{"x": 2})

	_, err = c.CrossWithType(context.Background(), a, b, "unsupported_type")
	if err == nil {
		t.Error("expected error for unsupported crossover type")
	}
}

func TestCrossWithType_NilParents(t *testing.T) {
	c, err := NewCrosser(CrosserConfig{})
	if err != nil {
		t.Fatalf("NewCrosser: %v", err)
	}

	_, err = c.CrossWithType(context.Background(), nil, nil, CrossoverUniform)
	if err == nil {
		t.Error("expected error for nil parents")
	}
}

// ── singlePointCrossover ────────────────────────────────────────────────────

func TestSinglePointCrossover_EmptyParams(t *testing.T) {
	c, err := NewCrosser(CrosserConfig{})
	if err != nil {
		t.Fatalf("NewCrosser: %v", err)
	}

	a := newTestStrategy("a", nil)
	b := newTestStrategy("b", nil)

	child, err := c.CrossWithType(context.Background(), a, b, CrossoverSinglePoint)
	if err != nil {
		t.Fatalf("CrossWithType: %v", err)
	}
	if child == nil {
		t.Fatal("child is nil")
	}
}

func TestSinglePointCrossover_OverlappingKeys(t *testing.T) {
	c, err := NewCrosser(CrosserConfig{})
	if err != nil {
		t.Fatalf("NewCrosser: %v", err)
	}

	a := newTestStrategy("a", map[string]any{"x": 1, "y": 2})
	b := newTestStrategy("b", map[string]any{"x": 10, "z": 30})

	child, err := c.CrossWithType(context.Background(), a, b, CrossoverSinglePoint)
	if err != nil {
		t.Fatalf("CrossWithType: %v", err)
	}
	if len(child.Params) != 3 {
		t.Errorf("child params len = %d, want 3 (union of keys)", len(child.Params))
	}
}

// ── twoPointCrossover ───────────────────────────────────────────────────────

func TestTwoPointCrossover_FewKeysFallsBack(t *testing.T) {
	c, err := NewCrosser(CrosserConfig{})
	if err != nil {
		t.Fatalf("NewCrosser: %v", err)
	}

	// Fewer than 3 keys — falls back to single-point
	a := newTestStrategy("a", map[string]any{"x": 1})
	b := newTestStrategy("b", map[string]any{"y": 2})

	child, err := c.CrossWithType(context.Background(), a, b, CrossoverTwoPoint)
	if err != nil {
		t.Fatalf("CrossWithType: %v", err)
	}
	if child == nil {
		t.Fatal("child is nil")
	}
}

func TestTwoPointCrossover_EqualSplitPoints(t *testing.T) {
	c, err := NewCrosser(CrosserConfig{})
	if err != nil {
		t.Fatalf("NewCrosser: %v", err)
	}

	// Exactly 3 keys: pt1=1, pt2=2 — exercises the pt1==pt2 guard edge
	a := newTestStrategy("a", map[string]any{"k1": 1, "k2": 2, "k3": 3})
	b := newTestStrategy("b", map[string]any{"k1": 10, "k2": 20, "k3": 30})

	child, err := c.CrossWithType(context.Background(), a, b, CrossoverTwoPoint)
	if err != nil {
		t.Fatalf("CrossWithType: %v", err)
	}
	if len(child.Params) != 3 {
		t.Errorf("child params len = %d, want 3", len(child.Params))
	}
}

// ── collectSortedKeys ───────────────────────────────────────────────────────

func TestCollectSortedKeys(t *testing.T) {
	tests := []struct {
		name string
		a, b map[string]any
		want int
	}{
		{"both_empty", nil, nil, 0},
		{"a_only", map[string]any{"x": 1}, nil, 1},
		{"b_only", nil, map[string]any{"y": 2}, 1},
		{"disjoint", map[string]any{"x": 1}, map[string]any{"y": 2}, 2},
		{"overlap", map[string]any{"x": 1, "z": 3}, map[string]any{"x": 2, "y": 2}, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keys := collectSortedKeys(tt.a, tt.b)
			if len(keys) != tt.want {
				t.Errorf("len = %d, want %d", len(keys), tt.want)
			}
		})
	}
}

func TestCollectSortedKeys_Sorted(t *testing.T) {
	a := map[string]any{"zebra": 1, "alpha": 2, "middle": 3}
	b := map[string]any{"beta": 4, "alpha": 5}
	keys := collectSortedKeys(a, b)
	for i := 1; i < len(keys); i++ {
		if keys[i-1] > keys[i] {
			t.Errorf("keys not sorted: %v", keys)
		}
	}
}

// ── sortStrings / max ───────────────────────────────────────────────────────

func TestSortStrings(t *testing.T) {
	s := []string{"c", "a", "b", "e", "d"}
	sortStrings(s)
	want := []string{"a", "b", "c", "d", "e"}
	for i, v := range want {
		if s[i] != v {
			t.Errorf("s[%d] = %q, want %q", i, s[i], v)
		}
	}
}

func TestMax(t *testing.T) {
	tests := []struct{ a, b, want int }{
		{1, 2, 2},
		{5, 3, 5},
		{4, 4, 4},
		{-1, -2, -1},
	}
	for _, tt := range tests {
		if got := max(tt.a, tt.b); got != tt.want {
			t.Errorf("max(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

// ── CrossoverType / PromptCrossoverMode constants ───────────────────────────

func TestCrossoverTypeConstants(t *testing.T) {
	types := []CrossoverType{CrossoverUniform, CrossoverSinglePoint, CrossoverTwoPoint, CrossoverScattered}
	seen := make(map[CrossoverType]bool)
	for _, ct := range types {
		if string(ct) == "" {
			t.Error("empty CrossoverType constant")
		}
		if seen[ct] {
			t.Errorf("duplicate CrossoverType: %q", ct)
		}
		seen[ct] = true
	}
}

func TestPromptCrossoverModeConstants(t *testing.T) {
	if PromptInherit != 0 {
		t.Errorf("PromptInherit = %d, want 0", PromptInherit)
	}
	if PromptHalfSplit != 1 {
		t.Errorf("PromptHalfSplit = %d, want 1", PromptHalfSplit)
	}
	if PromptUniform != 2 {
		t.Errorf("PromptUniform = %d, want 2", PromptUniform)
	}
}
