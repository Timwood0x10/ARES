package research

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func newTestMemoryLog(t *testing.T) *MemoryLog {
	t.Helper()
	store, err := NewInMemoryStore()
	if err != nil {
		t.Fatalf("create memory store: %v", err)
	}
	return NewMemoryLog(store)
}

func TestPopulateMemoryContext_NoHistory_ReturnsEmptyContext(t *testing.T) {
	memLog := newTestMemoryLog(t)
	ctx := context.Background()

	state := NewResearchState("AAPL", time.Now(), &ResearchConfig{})
	PopulateMemoryContext(ctx, memLog, state)

	if state.MemoryContext == nil {
		// No history = nil context is acceptable
		return
	}
	if len(state.MemoryContext.PastDecisions) > 0 {
		t.Error("expected no past decisions for unknown symbol")
	}
}

func TestPopulateMemoryContext_WithHistory_InjectsPastDecisions(t *testing.T) {
	memLog := newTestMemoryLog(t)
	ctx := context.Background()

	now := time.Now()
	entry := &MemoryEntry{
		Symbol:       "AAPL",
		AnalysisDate: now.AddDate(0, -1, 0),
		Rating:       RatingBuy,
		Benchmark:    "SPY",
		Status:       MemoryStatusPending,
	}
	if err := memLog.Append(ctx, entry); err != nil {
		t.Fatalf("append entry: %v", err)
	}

	state := NewResearchState("AAPL", now, &ResearchConfig{})
	PopulateMemoryContext(ctx, memLog, state)

	if state.MemoryContext == nil {
		t.Fatal("expected non-nil MemoryContext")
	}
	if len(state.MemoryContext.PastDecisions) != 1 {
		t.Fatalf("expected 1 past decision, got %d", len(state.MemoryContext.PastDecisions))
	}
	if state.MemoryContext.PastDecisions[0].Symbol != "AAPL" {
		t.Errorf("expected symbol AAPL, got %s", state.MemoryContext.PastDecisions[0].Symbol)
	}
}

func TestPopulateMemoryContext_MultipleSymbols_OnlyReturnsRequested(t *testing.T) {
	memLog := newTestMemoryLog(t)
	ctx := context.Background()

	now := time.Now()
	_ = memLog.Append(ctx, &MemoryEntry{Symbol: "AAPL", AnalysisDate: now, Status: MemoryStatusPending})
	_ = memLog.Append(ctx, &MemoryEntry{Symbol: "MSFT", AnalysisDate: now, Status: MemoryStatusPending})
	_ = memLog.Append(ctx, &MemoryEntry{Symbol: "AAPL", AnalysisDate: now.AddDate(0, 0, -1), Status: MemoryStatusPending})

	state := NewResearchState("AAPL", now, &ResearchConfig{})
	PopulateMemoryContext(ctx, memLog, state)

	if state.MemoryContext == nil {
		t.Fatal("expected non-nil MemoryContext")
	}
	if len(state.MemoryContext.PastDecisions) != 2 {
		t.Fatalf("expected 2 AAPL decisions, got %d", len(state.MemoryContext.PastDecisions))
	}
	for _, d := range state.MemoryContext.PastDecisions {
		if d.Symbol != "AAPL" {
			t.Errorf("unexpected symbol %s in past decisions", d.Symbol)
		}
	}
}

func TestPopulateMemoryContext_NilLog_DoesNotPanic(t *testing.T) {
	state := NewResearchState("AAPL", time.Now(), &ResearchConfig{})
	PopulateMemoryContext(context.Background(), nil, state)
	if state.MemoryContext != nil {
		t.Error("expected nil MemoryContext when log is nil")
	}
}

func TestSaveDecisionToMemory_PersistsPortfolioDecision(t *testing.T) {
	memLog := newTestMemoryLog(t)
	ctx := context.Background()

	now := time.Now()
	priceTarget := 250.0
	state := NewResearchState("AAPL", now, &ResearchConfig{})
	state.PortfolioDecision = &PortfolioDecision{
		Rating:           RatingOverweight,
		ExecutiveSummary: "Strong buy based on AI momentum.",
		InvestmentThesis: "AI-driven growth justifies premium.",
		PriceTarget:      &priceTarget,
		TimeHorizon:      "12 months",
	}

	SaveDecisionToMemory(ctx, memLog, state)

	entries, err := memLog.GetSymbolHistory(ctx, "AAPL", 10)
	if err != nil {
		t.Fatalf("get history: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Rating != RatingOverweight {
		t.Errorf("expected rating Overweight, got %s", entries[0].Rating)
	}
	if entries[0].Benchmark != "SPY" {
		t.Errorf("expected benchmark SPY, got %s", entries[0].Benchmark)
	}
}

func TestSaveDecisionToMemory_NilDecision_DoesNothing(t *testing.T) {
	memLog := newTestMemoryLog(t)
	ctx := context.Background()

	state := NewResearchState("AAPL", time.Now(), &ResearchConfig{})
	SaveDecisionToMemory(ctx, memLog, state)

	entries, err := memLog.GetSymbolHistory(ctx, "AAPL", 10)
	if err != nil {
		t.Fatalf("get history: %v", err)
	}
	if len(entries) != 0 {
		t.Error("expected no entries when PortfolioDecision is nil")
	}
}

func TestSaveDecisionToMemory_NilLog_DoesNotPanic(t *testing.T) {
	state := NewResearchState("AAPL", time.Now(), &ResearchConfig{})
	state.PortfolioDecision = &PortfolioDecision{Rating: RatingBuy}
	SaveDecisionToMemory(context.Background(), nil, state)
}

func TestMemoryIntegration_SaveThenLoad_ClosedLoop(t *testing.T) {
	memLog := newTestMemoryLog(t)
	ctx := context.Background()

	now := time.Now()
	priceTarget := 250.0

	// First run: save decision.
	state1 := NewResearchState("AAPL", now, &ResearchConfig{})
	state1.PortfolioDecision = &PortfolioDecision{
		Rating:           RatingOverweight,
		ExecutiveSummary: "Buy AAPL",
		InvestmentThesis: "AI growth",
		PriceTarget:      &priceTarget,
	}
	SaveDecisionToMemory(ctx, memLog, state1)

	// Second run: load memory.
	state2 := NewResearchState("AAPL", now.AddDate(0, 0, 7), &ResearchConfig{})
	PopulateMemoryContext(ctx, memLog, state2)

	if state2.MemoryContext == nil {
		t.Fatal("expected non-nil MemoryContext from saved decision")
	}
	if len(state2.MemoryContext.PastDecisions) != 1 {
		t.Fatalf("expected 1 past decision, got %d", len(state2.MemoryContext.PastDecisions))
	}
	if state2.MemoryContext.PastDecisions[0].Rating != RatingOverweight {
		t.Errorf("expected RatingOverweight, got %s", state2.MemoryContext.PastDecisions[0].Rating)
	}
}

func TestEnsureMemoryStore_EmptyPath_ReturnsInMemory(t *testing.T) {
	store, err := EnsureMemoryStore("")
	if err != nil {
		t.Fatalf("EnsureMemoryStore with empty path: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Error("close store error:", err)
		}
	}()
	if store == nil {
		t.Fatal("expected non-nil store")
	}
}

func TestMemoryBridge_ErrorDegradation_DoesNotPanic(t *testing.T) {
	// Verify that memory failures in SaveDecisionToMemory produce warnings
	// but do not panic or propagate errors.
	badStore := &brokenStore{}
	memLog := NewMemoryLog(badStore)
	ctx := context.Background()

	state := NewResearchState("AAPL", time.Now(), &ResearchConfig{})
	state.PortfolioDecision = &PortfolioDecision{Rating: RatingBuy}
	SaveDecisionToMemory(ctx, memLog, state)

	// Should not panic even with broken store.
}

type brokenStore struct{}

func (s *brokenStore) AppendEntry(_ context.Context, _ *MemoryEntry) error {
	return fmt.Errorf("broken store")
}
func (s *brokenStore) GetEntries(_ context.Context, _ string, _ int) ([]*MemoryEntry, error) {
	return nil, fmt.Errorf("broken store")
}
func (s *brokenStore) GetPendingEntries(_ context.Context) ([]*MemoryEntry, error) {
	return nil, fmt.Errorf("broken store")
}
func (s *brokenStore) GetAllResolvedEntries(_ context.Context, _ int) ([]*MemoryEntry, error) {
	return nil, fmt.Errorf("broken store")
}
func (s *brokenStore) UpdateOutcome(_ context.Context, _ string, _ *Outcome) error {
	return fmt.Errorf("broken store")
}
func (s *brokenStore) UpdateReflection(_ context.Context, _ string, _ string) error {
	return fmt.Errorf("broken store")
}
func (s *brokenStore) Close() error { return nil }
