package research

import (
	"context"
	"testing"
	"time"
)

// ─── Rule-Based Reflection Tests ──────────────────────────

func TestReflectByRules_StrongCorrect(t *testing.T) {
	reflection := reflectByRules(7.5)
	if !containsStr(reflection, "Strong correct") {
		t.Errorf("alpha > 5%% should say 'Strong correct', got: %s", reflection)
	}
	if !containsStr(reflection, "7.50") {
		t.Error("reflection should contain alpha value")
	}
}

func TestReflectByRules_MarginallyCorrect(t *testing.T) {
	reflection := reflectByRules(3.2)
	if !containsStr(reflection, "Marginally correct") {
		t.Errorf("alpha 0-5%% should say 'Marginally correct', got: %s", reflection)
	}
}

func TestReflectByRules_MarginallyWrong(t *testing.T) {
	reflection := reflectByRules(-2.5)
	if !containsStr(reflection, "Marginally wrong") {
		t.Errorf("alpha -5%% to 0 should say 'Marginally wrong', got: %s", reflection)
	}
}

func TestReflectByRules_WrongDirection(t *testing.T) {
	reflection := reflectByRules(-8.0)
	if !containsStr(reflection, "Wrong direction") {
		t.Errorf("alpha < -5%% should say 'Wrong direction', got: %s", reflection)
	}
	if !containsStr(reflection, "Critical errors") {
		t.Error("wrong direction should mention critical errors")
	}
}

func TestReflectByRules_BoundaryAlphaZero(t *testing.T) {
	reflection := reflectByRules(0.0)
	if !containsStr(reflection, "Marginally wrong") {
		t.Errorf("alpha=0 should fall in marginally wrong bucket, got: %s", reflection)
	}
}

func TestReflectByRules_BoundaryAlphaFive(t *testing.T) {
	reflection := reflectByRules(5.0)
	if !containsStr(reflection, "Marginally correct") {
		t.Errorf("alpha=5.0 should fall in marginally correct bucket, got: %s", reflection)
	}
}

func TestReflectByRules_BoundaryAlphaNegativeFive(t *testing.T) {
	reflection := reflectByRules(-5.0)
	if !containsStr(reflection, "Marginally wrong") {
		t.Errorf("alpha=-5.0 should fall in marginally wrong bucket, got: %s", reflection)
	}
}

// ─── Reflector Tests ───────────────────────────────────────

func TestReflector_NilEntry_ReturnsError(t *testing.T) {
	r := NewReflector(nil, nil)
	_, err := r.Reflect(context.Background(), nil)
	if err == nil {
		t.Error("reflecting on nil entry should return error")
	}
}

func TestReflector_RuleBasedReflection(t *testing.T) {
	mockLog := NewMemoryLog(&noopStore{})
	r := NewReflector(mockLog, nil) // No LLM -> rule-based.

	entry := &MemoryEntry{
		Symbol:       "AAPL",
		Rating:       RatingBuy,
		AnalysisDate: time.Now().AddDate(0, 0, -30),
		HoldingDays:  30,
		RawReturn:    ptrFloat(12.5),
		AlphaReturn:  ptrFloat(9.3),
		Status:       MemoryStatusResolved,
	}

	reflection, err := r.Reflect(context.Background(), entry)
	if err != nil {
		t.Fatalf("reflect failed: %v", err)
	}
	if reflection == "" {
		t.Error("reflection should not be empty")
	}
	if !containsStr(reflection, "correct") {
		t.Errorf("positive alpha reflection should mention correctness, got: %s", reflection)
	}
}

func TestReflector_BatchReflect_ProcessesAll(t *testing.T) {
	ns := &noopStore{}
	mockLog := NewMemoryLog(ns)
	r := NewReflector(mockLog, nil)

	ctx := context.Background()
	count, err := r.BatchReflect(ctx)
	if err != nil {
		t.Fatalf("batch reflect failed: %v", err)
	}
	// noopStore returns no pending entries, so count should be 0.
	if count != 0 {
		t.Errorf("expected 0 reflections for empty store, got %d", count)
	}
}

// ─── Outcome/Alpha Calculation Tests ──────────────────────

func TestComputeAlpha_FromAlphaReturn(t *testing.T) {
	alpha := 5.5
	entry := &MemoryEntry{
		AlphaReturn: &alpha,
		RawReturn:   ptrFloat(10.0),
	}
	result := computeAlpha(entry)
	if result != 5.5 {
		t.Errorf("expected alpha 5.5 from AlphaReturn field, got %f", result)
	}
}

func TestComputeAlpha_FallbackToRawReturn(t *testing.T) {
	raw := 8.0
	entry := &MemoryEntry{
		AlphaReturn: nil,
		RawReturn:   &raw,
	}
	result := computeAlpha(entry)
	if result != 8.0 {
		t.Errorf("expected alpha fallback to raw return 8.0, got %f", result)
	}
}

func TestComputeAlpha_ZeroWhenNoData(t *testing.T) {
	entry := &MemoryEntry{
		AlphaReturn: nil,
		RawReturn:   nil,
	}
	result := computeAlpha(entry)
	if result != 0 {
		t.Errorf("expected zero alpha when no data, got %f", result)
	}
}

// ─── Helper Types and Functions ───────────────────────────

type noopStore struct{}

func (n *noopStore) AppendEntry(_ context.Context, _ *MemoryEntry) error { return nil }
func (n *noopStore) GetEntries(_ context.Context, _ string, _ int) ([]*MemoryEntry, error) {
	return nil, nil
}
func (n *noopStore) GetPendingEntries(_ context.Context) ([]*MemoryEntry, error) { return nil, nil }
func (n *noopStore) GetAllResolvedEntries(_ context.Context, _ int) ([]*MemoryEntry, error) {
	return nil, nil
}
func (n *noopStore) UpdateOutcome(_ context.Context, _ string, _ *Outcome) error  { return nil }
func (n *noopStore) UpdateReflection(_ context.Context, _ string, _ string) error { return nil }
func (n *noopStore) Close() error                                                 { return nil }

func ptrFloat(v float64) *float64 { return &v }

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
