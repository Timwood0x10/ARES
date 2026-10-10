package mutation

import (
	"context"
	"testing"

	internalmutation "github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// ── Strategy.ToInternal ─────────────────────────────────────────────────────

func TestToInternal_Nil(t *testing.T) {
	var s *Strategy
	if got := s.ToInternal(); got != nil {
		t.Errorf("ToInternal(nil) = %v, want nil", got)
	}
}

func TestToInternal_Basic(t *testing.T) {
	s := &Strategy{
		ID:             "s1",
		Version:        3,
		Score:          0.8,
		ParentID:       "parent-1",
		PromptTemplate: "do something",
		Params:         map[string]any{"temp": 0.7},
		MutationType:   MutationParameter,
	}
	internal := s.ToInternal()
	if internal == nil {
		t.Fatal("ToInternal returned nil")
	}
	if internal.ID != "s1" {
		t.Errorf("ID = %q", internal.ID)
	}
	if internal.Version != 3 {
		t.Errorf("Version = %d", internal.Version)
	}
	if internal.Score != 0.8 {
		t.Errorf("Score = %v", internal.Score)
	}
	if internal.ParentID != "parent-1" {
		t.Errorf("ParentID = %q", internal.ParentID)
	}
	if internal.PromptTemplate != "do something" {
		t.Errorf("PromptTemplate = %q", internal.PromptTemplate)
	}
	if internal.Params["temp"] != 0.7 {
		t.Errorf("Params[temp] = %v", internal.Params["temp"])
	}
}

// ── FromInternal ────────────────────────────────────────────────────────────

func TestFromInternal_Nil(t *testing.T) {
	if got := FromInternal(nil); got != nil {
		t.Errorf("FromInternal(nil) = %v, want nil", got)
	}
}

// ── parseMutationType / toMutationType roundtrip ────────────────────────────

func TestParseMutationType(t *testing.T) {
	tests := []struct {
		name string
		give MutationType
	}{
		{"parameter", MutationParameter},
		{"prompt", MutationPrompt},
		{"tool", MutationTool},
		{"crossover", MutationCrossover},
		{"root", MutationRoot},
		{"empty", ""},
		{"unknown", "garbage"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = parseMutationType(tt.give)
		})
	}
}

func TestToMutationType(t *testing.T) {
	tests := []struct {
		name string
		give internalmutation.MutationType
	}{
		{"parameter", internalmutation.MutationParameter},
		{"prompt", internalmutation.MutationPrompt},
		{"tool", internalmutation.MutationTool},
		{"crossover", internalmutation.MutationCrossover},
		{"root", internalmutation.MutationRoot},
		{"unknown", internalmutation.MutationType(99)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = toMutationType(tt.give)
		})
	}
}

// ── MutationType constants ──────────────────────────────────────────────────

func TestMutationTypeConstants(t *testing.T) {
	consts := []struct {
		name string
		val  MutationType
		want string
	}{
		{"parameter", MutationParameter, "parameter"},
		{"prompt", MutationPrompt, "prompt"},
		{"tool", MutationTool, "tool"},
		{"crossover", MutationCrossover, "crossover"},
		{"root", MutationRoot, "root"},
	}
	for _, tt := range consts {
		if string(tt.val) != tt.want {
			t.Errorf("%s = %q, want %q", tt.name, tt.val, tt.want)
		}
	}
}

// ── NewMutator ──────────────────────────────────────────────────────────────

func TestNewMutator_Defaults(t *testing.T) {
	m, err := NewMutator(MutatorConfig{})
	if err != nil {
		t.Fatalf("NewMutator: %v", err)
	}
	if m == nil {
		t.Fatal("NewMutator returned nil")
	}
	if m.paramProb != 0.3 {
		t.Errorf("paramProb = %v, want 0.3", m.paramProb)
	}
	if m.promptProb != 0.3 {
		t.Errorf("promptProb = %v, want 0.3", m.promptProb)
	}
}

func TestNewMutator_CustomConfig(t *testing.T) {
	m, err := NewMutator(MutatorConfig{
		ParamRanges: map[string][]any{
			"temperature": {0.1, 0.5, 0.9},
		},
		PromptPool:         []string{"prompt-a", "prompt-b"},
		ToolPool:           []string{"tool-x", "tool-y"},
		ParamMutationProb:  0.5,
		PromptMutationProb: 0.4,
		ToolMutationProb:   0.6,
	})
	if err != nil {
		t.Fatalf("NewMutator: %v", err)
	}
	if m.paramProb != 0.5 {
		t.Errorf("paramProb = %v, want 0.5", m.paramProb)
	}
	if m.promptProb != 0.4 {
		t.Errorf("promptProb = %v, want 0.4", m.promptProb)
	}
}

// ── Mutator.Mutate ──────────────────────────────────────────────────────────

func TestMutate_NilParent(t *testing.T) {
	m, err := NewMutator(MutatorConfig{})
	if err != nil {
		t.Fatalf("NewMutator: %v", err)
	}
	_, err = m.Mutate(context.Background(), nil)
	if err == nil {
		t.Error("expected error for nil parent")
	}
}

func TestMutate_Success(t *testing.T) {
	m, err := NewMutator(MutatorConfig{
		ParamRanges: map[string][]any{
			"temperature": {0.1, 0.5, 0.9},
		},
	})
	if err != nil {
		t.Fatalf("NewMutator: %v", err)
	}

	parent := &Strategy{
		ID:           "parent",
		Version:      1,
		Score:        0.5,
		Params:       map[string]any{"temperature": 0.5},
		MutationType: MutationRoot,
	}

	child, err := m.Mutate(context.Background(), parent)
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	if child == nil {
		t.Fatal("child is nil")
	}
	if child.ID == "" {
		t.Error("child ID should not be empty")
	}
}

// ── Mutator.MutateN ─────────────────────────────────────────────────────────

func TestMutateN_NilParent(t *testing.T) {
	m, err := NewMutator(MutatorConfig{})
	if err != nil {
		t.Fatalf("NewMutator: %v", err)
	}
	_, err = m.MutateN(context.Background(), nil, 3)
	if err == nil {
		t.Error("expected error for nil parent")
	}
}

func TestMutateN_InvalidCount(t *testing.T) {
	m, err := NewMutator(MutatorConfig{})
	if err != nil {
		t.Fatalf("NewMutator: %v", err)
	}
	parent := &Strategy{ID: "p", Params: map[string]any{"x": 1}}

	for _, n := range []int{0, -1} {
		_, err := m.MutateN(context.Background(), parent, n)
		if err == nil {
			t.Errorf("expected error for n=%d", n)
		}
	}
}

func TestMutateN_Success(t *testing.T) {
	m, err := NewMutator(MutatorConfig{
		ParamRanges: map[string][]any{
			"temperature": {0.1, 0.5, 0.9},
		},
	})
	if err != nil {
		t.Fatalf("NewMutator: %v", err)
	}

	parent := &Strategy{
		ID:           "parent",
		Version:      1,
		Params:       map[string]any{"temperature": 0.5},
		MutationType: MutationRoot,
	}

	children, err := m.MutateN(context.Background(), parent, 3)
	if err != nil {
		t.Fatalf("MutateN: %v", err)
	}
	if len(children) == 0 {
		t.Fatal("no children returned")
	}
	for i, child := range children {
		if child == nil {
			t.Errorf("children[%d] is nil", i)
		}
	}
}

// ── Strategy roundtrip ──────────────────────────────────────────────────────

func TestStrategyRoundtrip(t *testing.T) {
	orig := &Strategy{
		ID:             "rt-1",
		Version:        5,
		Score:          0.95,
		ParentID:       "rt-parent",
		PromptTemplate: "roundtrip prompt",
		Params:         map[string]any{"k1": "v1", "k2": 42.0},
		MutationType:   MutationPrompt,
	}

	internal := orig.ToInternal()
	if internal == nil {
		t.Fatal("ToInternal returned nil")
	}

	back := FromInternal(internal)
	if back == nil {
		t.Fatal("FromInternal returned nil")
	}

	if back.ID != orig.ID {
		t.Errorf("ID = %q, want %q", back.ID, orig.ID)
	}
	if back.Version != orig.Version {
		t.Errorf("Version = %d, want %d", back.Version, orig.Version)
	}
	if back.Score != orig.Score {
		t.Errorf("Score = %v, want %v", back.Score, orig.Score)
	}
	if back.ParentID != orig.ParentID {
		t.Errorf("ParentID = %q, want %q", back.ParentID, orig.ParentID)
	}
	if back.PromptTemplate != orig.PromptTemplate {
		t.Errorf("PromptTemplate = %q, want %q", back.PromptTemplate, orig.PromptTemplate)
	}
	if back.Params["k1"] != "v1" {
		t.Errorf("Params[k1] = %v", back.Params["k1"])
	}
}
