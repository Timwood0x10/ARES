package research

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ─── MemoryStore Tests (Integration with SQLite) ──────────

func newTestMemoryStore(t *testing.T) *MemoryStore {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test_memory.db")
	store, err := NewMemoryStore(path)
	if err != nil {
		t.Fatalf("create test memory store: %v", err)
	}
	return store
}

func TestMemoryStore_AppendAndGetEntry(t *testing.T) {
	store := newTestMemoryStore(t)
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	entry := &MemoryEntry{
		ID:            "test-uuid-001",
		Symbol:        "AAPL",
		AnalysisDate:  time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC),
		Rating:        RatingBuy,
		Benchmark:     "SPY",
		HoldingDays:   30,
		SourceQuality: "good",
		CreatedAt:     time.Now(),
	}

	err := store.AppendEntry(ctx, entry)
	if err != nil {
		t.Fatalf("append entry failed: %v", err)
	}

	entries, err := store.GetEntries(ctx, "AAPL", 10)
	if err != nil {
		t.Fatalf("get entries failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Symbol != "AAPL" {
		t.Errorf("expected symbol AAPL, got %s", entries[0].Symbol)
	}
	if entries[0].Rating != RatingBuy {
		t.Errorf("expected rating Buy, got %s", entries[0].Rating)
	}
}

func TestMemoryStore_DuplicateSymbolDate(t *testing.T) {
	store := newTestMemoryStore(t)
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	date := time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC)

	entry1 := &MemoryEntry{
		Symbol:       "TSLA",
		AnalysisDate: date,
		Rating:       RatingSell,
	}
	entry2 := &MemoryEntry{
		Symbol:       "TSLA",
		AnalysisDate: date,
		Rating:       RatingHold,
	}

	_ = store.AppendEntry(ctx, entry1)
	err := store.AppendEntry(ctx, entry2) // Should be ignored (duplicate).
	if err != nil {
		t.Fatalf("second append should not error: %v", err)
	}

	entries, _ := store.GetEntries(ctx, "TSLA", 10)
	if len(entries) != 1 {
		t.Errorf("expected 1 entry (dedup), got %d", len(entries))
	}
}

func TestMemoryStore_UpdateOutcome(t *testing.T) {
	store := newTestMemoryStore(t)
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	entry := &MemoryEntry{
		Symbol:       "NVDA",
		AnalysisDate: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		Rating:       RatingOverweight,
		Benchmark:    "SPY",
		HoldingDays:  14,
		Status:       MemoryStatusPending,
	}

	_ = store.AppendEntry(ctx, entry)

	outcome := &Outcome{
		ActualReturn:    12.5,
		BenchmarkReturn: 3.2,
		RealizedAlpha:   9.3,
		Notes:           "AI demand exceeded expectations.",
	}

	err := store.UpdateOutcome(ctx, entry.ID, outcome)
	if err != nil {
		t.Fatalf("update outcome failed: %v", err)
	}

	// Verify status changed to resolved.
	entries, _ := store.GetEntries(ctx, "NVDA", 10)
	if len(entries) == 0 || entries[0].Status != MemoryStatusResolved {
		t.Error("entry should be resolved after update")
	}
}

func TestMemoryStore_GetPendingEntries(t *testing.T) {
	store := newTestMemoryStore(t)
	defer func() { _ = store.Close() }()

	ctx := context.Background()

	// Add a pending entry.
	_ = store.AppendEntry(ctx, &MemoryEntry{
		ID:           "test-pending-001",
		Symbol:       "MSFT",
		Rating:       RatingHold,
		AnalysisDate: time.Now(),
		CreatedAt:    time.Now(),
	})

	pending, err := store.GetPendingEntries(ctx)
	if err != nil {
		t.Fatalf("get pending failed: %v", err)
	}
	if len(pending) < 1 {
		t.Error("should have at least 1 pending entry")
	}
}

func TestMemoryStore_Close(t *testing.T) {
	store := newTestMemoryStore(t)
	err := store.Close()
	if err != nil {
		t.Fatalf("close failed: %v", err)
	}
}

// ─── MemoryLog Tests ──────────────────────────────────────

type mockStore struct {
	entries   []*MemoryEntry
	pending   []*MemoryEntry
	outcomes  map[string]*Outcome
	appendErr error
	getErr    error
	updateErr error
}

func (m *mockStore) AppendEntry(_ context.Context, entry *MemoryEntry) error {
	if m.appendErr != nil {
		return m.appendErr
	}
	m.entries = append(m.entries, entry)
	m.pending = append(m.pending, entry)
	return nil
}

func (m *mockStore) GetEntries(_ context.Context, symbol string, limit int) ([]*MemoryEntry, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	var result []*MemoryEntry
	count := 0
	for _, e := range m.entries {
		if e.Symbol == symbol && count < limit {
			result = append(result, e)
			count++
		}
	}
	return result, nil
}

func (m *mockStore) GetPendingEntries(_ context.Context) ([]*MemoryEntry, error) {
	return m.pending, nil
}

func (m *mockStore) UpdateOutcome(_ context.Context, id string, outcome *Outcome) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.outcomes[id] = outcome
	for _, e := range m.entries {
		if e.ID == id {
			e.Status = MemoryStatusResolved
			break
		}
	}
	return nil
}

func (m *mockStore) GetAllResolvedEntries(_ context.Context, limit int) ([]*MemoryEntry, error) {
	var result []*MemoryEntry
	for _, e := range m.entries {
		if e.Status == MemoryStatusResolved && e.Reflection != "" {
			result = append(result, e)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (m *mockStore) UpdateReflection(_ context.Context, id string, reflection string) error {
	for _, e := range m.entries {
		if e.ID == id {
			e.Reflection = reflection
			return nil
		}
	}
	return fmt.Errorf("entry %s not found", id)
}

func (m *mockStore) Close() error { return nil }

func TestMemoryLog_Append(t *testing.T) {
	ms := &mockStore{entries: make([]*MemoryEntry, 0), outcomes: make(map[string]*Outcome)}
	log := NewMemoryLog(ms)

	ctx := context.Background()
	entry := &MemoryEntry{
		Symbol:       "AAPL",
		AnalysisDate: time.Now(),
		Rating:       RatingBuy,
		Benchmark:    "SPY",
	}

	err := log.Append(ctx, entry)
	if err != nil {
		t.Fatalf("append failed: %v", err)
	}
	if entry.ID == "" {
		t.Error("ID should be generated on append")
	}
	if entry.Status != MemoryStatusPending {
		t.Errorf("status should be pending, got %s", entry.Status)
	}
}

func TestMemoryLog_ResolvePending(t *testing.T) {
	ms := &mockStore{entries: make([]*MemoryEntry, 0), outcomes: make(map[string]*Outcome)}
	log := NewMemoryLog(ms)

	ctx := context.Background()
	entry := &MemoryEntry{
		Symbol: "GOOGL", Rating: RatingBuy, AnalysisDate: time.Now(),
	}
	_ = log.Append(ctx, entry)

	outcomes := map[string]*Outcome{
		entry.ID: {ActualReturn: 8.5, BenchmarkReturn: 2.0, RealizedAlpha: 6.5},
	}

	count, err := log.ResolvePending(ctx, outcomes)
	if err != nil {
		t.Fatalf("resolve pending failed: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 resolved, got %d", count)
	}
}

func TestMemoryLog_GetSymbolHistory(t *testing.T) {
	ms := &mockStore{entries: make([]*MemoryEntry, 0), outcomes: make(map[string]*Outcome)}
	log := NewMemoryLog(ms)

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_ = log.Append(ctx, &MemoryEntry{
			Symbol: "AMD", Rating: RatingHold, AnalysisDate: time.Now().AddDate(0, 0, -i),
		})
	}

	history, err := log.GetSymbolHistory(ctx, "AMD", 5)
	if err != nil {
		t.Fatalf("get history failed: %v", err)
	}
	if len(history) != 3 {
		t.Errorf("expected 3 entries, got %d", len(history))
	}
}

func TestMemoryLog_GenerateContext(t *testing.T) {
	ms := &mockStore{entries: make([]*MemoryEntry, 0), outcomes: make(map[string]*Outcome)}
	log := NewMemoryLog(ms)

	ctx := context.Background()
	_ = log.Append(ctx, &MemoryEntry{
		Symbol: "META", Rating: RatingOverweight, AnalysisDate: time.Now(),
	})

	pmCtx, err := log.GenerateContext(ctx, "META")
	if err != nil {
		t.Fatalf("generate context failed: %v", err)
	}
	if pmCtx == nil {
		t.Fatal("PM context should not be nil")
	}
	if len(pmCtx.PastDecisions) != 1 {
		t.Errorf("expected 1 past decision, got %d", len(pmCtx.PastDecisions))
	}
}

// ─── Filesystem and Cleanup Tests ─────────────────────────

func TestNewMemoryStore_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new_test.db")

	store, err := NewMemoryStore(path)
	if err != nil {
		t.Fatalf("new memory store: %v", err)
	}
	_ = store.Close()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("database file should exist after creation")
	}
}

func TestMemoryStatus_String(t *testing.T) {
	tests := []struct {
		status   MemoryStatus
		expected string
	}{
		{MemoryStatusPending, "pending"},
		{MemoryStatusResolved, "resolved"},
	}
	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if tt.status.String() != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, tt.status.String())
			}
		})
	}
}
