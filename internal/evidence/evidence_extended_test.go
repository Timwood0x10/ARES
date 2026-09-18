package evidence

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// ── EvidenceKind constants ──────────────────────────────────────────────────

func TestEvidenceKindConstants(t *testing.T) {
	kinds := []EvidenceKind{
		KindExecutionTrace, KindFailure, KindKnowledge,
		KindInsight, KindFitness, KindDimensionEval,
	}
	seen := make(map[EvidenceKind]bool)
	for _, k := range kinds {
		if string(k) == "" {
			t.Error("empty EvidenceKind constant")
		}
		if seen[k] {
			t.Errorf("duplicate EvidenceKind: %q", k)
		}
		seen[k] = true
	}
}

// ── NewEvidence ─────────────────────────────────────────────────────────────

func TestNewEvidenceBasic(t *testing.T) {
	e := NewEvidence("test-source", KindFitness, map[string]any{"value": 0.8})
	if e.ID == "" {
		t.Error("ID should not be empty")
	}
	if e.Source != "test-source" {
		t.Errorf("Source = %q, want test-source", e.Source)
	}
	if e.Kind != KindFitness {
		t.Errorf("Kind = %q, want %q", e.Kind, KindFitness)
	}
	if e.Timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
	}
	if len(e.Payload) == 0 {
		t.Error("Payload should not be empty")
	}
}

func TestNewEvidenceNilPayload(t *testing.T) {
	e := NewEvidence("src", KindKnowledge, nil)
	if len(e.Payload) != 0 {
		t.Errorf("Payload = %v, want empty for nil input", e.Payload)
	}
}

func TestNewEvidenceUniqueIDs(t *testing.T) {
	e1 := NewEvidence("src", KindFitness, nil)
	e2 := NewEvidence("src", KindFitness, nil)
	if e1.ID == e2.ID {
		t.Errorf("IDs should be unique: %q == %q", e1.ID, e2.ID)
	}
}

func TestNewEvidenceWithMetadata(t *testing.T) {
	e := NewEvidence("src", KindFitness, nil,
		WithMetadata("key1", "val1"),
		WithMetadata("key2", "val2"),
	)
	if e.Metadata["key1"] != "val1" {
		t.Errorf("Metadata[key1] = %q, want val1", e.Metadata["key1"])
	}
	if e.Metadata["key2"] != "val2" {
		t.Errorf("Metadata[key2] = %q, want val2", e.Metadata["key2"])
	}
}

func TestNewEvidenceWithTTL(t *testing.T) {
	ttl := 5 * time.Minute
	e := NewEvidence("src", KindFitness, nil, WithTTL(ttl))
	if e.TTL != ttl {
		t.Errorf("TTL = %v, want %v", e.TTL, ttl)
	}
}

func TestNewEvidenceWithID(t *testing.T) {
	e := NewEvidence("src", KindFitness, nil, WithID("custom-id"))
	if e.ID != "custom-id" {
		t.Errorf("ID = %q, want custom-id", e.ID)
	}
}

func TestNewEvidenceWithEmptyID(t *testing.T) {
	e := NewEvidence("src", KindFitness, nil, WithID(""))
	if e.ID == "" {
		t.Error("empty WithID should not override auto-generated ID")
	}
}

// ── MemoryStore.Append ──────────────────────────────────────────────────────

func TestMemoryStoreAppend(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	e := NewEvidence("src", KindFitness, map[string]any{"value": 1.0})
	if err := s.Append(ctx, e); err != nil {
		t.Fatalf("Append: %v", err)
	}
}

func TestMemoryStoreAppendRingCap(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	// Append more than the cap.
	for i := 0; i < maxMemoryStoreRecords+100; i++ {
		e := NewEvidence("src", KindFitness, map[string]any{"i": i})
		if err := s.Append(ctx, e); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	results, err := s.Query(ctx, Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) > maxMemoryStoreRecords {
		t.Errorf("results len = %d, want <= %d", len(results), maxMemoryStoreRecords)
	}
}

// ── MemoryStore.Query ───────────────────────────────────────────────────────

func TestMemoryStoreQueryEmpty(t *testing.T) {
	s := NewMemoryStore()
	results, err := s.Query(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("results len = %d, want 0", len(results))
	}
}

func TestMemoryStoreQueryBySource(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	_ = s.Append(ctx, NewEvidence("src-a", KindFitness, nil))
	_ = s.Append(ctx, NewEvidence("src-b", KindFitness, nil))
	_ = s.Append(ctx, NewEvidence("src-a", KindKnowledge, nil))

	results, err := s.Query(ctx, Filter{Source: "src-a"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("results len = %d, want 2", len(results))
	}
}

func TestMemoryStoreQueryByKind(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	_ = s.Append(ctx, NewEvidence("src", KindFitness, nil))
	_ = s.Append(ctx, NewEvidence("src", KindKnowledge, nil))

	results, err := s.Query(ctx, Filter{Kind: KindFitness})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("results len = %d, want 1", len(results))
	}
}

func TestMemoryStoreQueryTimeBounds(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	now := time.Now()
	e1 := NewEvidence("src", KindFitness, nil)
	e1.Timestamp = now.Add(-2 * time.Hour)
	e2 := NewEvidence("src", KindFitness, nil)
	e2.Timestamp = now
	e3 := NewEvidence("src", KindFitness, nil)
	e3.Timestamp = now.Add(2 * time.Hour)

	_ = s.Append(ctx, e1)
	_ = s.Append(ctx, e2)
	_ = s.Append(ctx, e3)

	// Query within [now-1h, now+1h] — only e2 matches.
	results, err := s.Query(ctx, Filter{
		Since: now.Add(-1 * time.Hour),
		Until: now.Add(1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("results len = %d, want 1", len(results))
	}
}

func TestMemoryStoreQueryTTLExpiry(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	// Evidence with very short TTL.
	e := NewEvidence("src", KindFitness, nil, WithTTL(1*time.Nanosecond))
	e.Timestamp = time.Now().Add(-1 * time.Hour) // already expired
	_ = s.Append(ctx, e)

	results, err := s.Query(ctx, Filter{Source: "src"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("results len = %d, want 0 (expired evidence excluded)", len(results))
	}
}

func TestMemoryStoreQueryTTLNotExpired(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	e := NewEvidence("src", KindFitness, nil, WithTTL(24*time.Hour))
	_ = s.Append(ctx, e)

	results, err := s.Query(ctx, Filter{Source: "src"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("results len = %d, want 1 (not expired)", len(results))
	}
}

func TestMemoryStoreQueryLimit(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		_ = s.Append(ctx, NewEvidence("src", KindFitness, map[string]any{"i": i}))
	}

	results, err := s.Query(ctx, Filter{Source: "src", Limit: 3})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("results len = %d, want 3", len(results))
	}
}

func TestMemoryStoreQueryOrderByTimestampDesc(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	now := time.Now()
	for i := 0; i < 5; i++ {
		e := NewEvidence("src", KindFitness, nil)
		e.Timestamp = now.Add(time.Duration(i) * time.Minute)
		_ = s.Append(ctx, e)
	}

	results, err := s.Query(ctx, Filter{Source: "src"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	for i := 1; i < len(results); i++ {
		if results[i].Timestamp.After(results[i-1].Timestamp) {
			t.Errorf("results not sorted descending at index %d", i)
		}
	}
}

func TestMemoryStoreQueryPayloadFilter(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	_ = s.Append(ctx, NewEvidence("src", KindFitness, map[string]any{"strategy_id": "s1", "value": 0.8}))
	_ = s.Append(ctx, NewEvidence("src", KindFitness, map[string]any{"strategy_id": "s2", "value": 0.9}))

	results, err := s.Query(ctx, Filter{
		Source:        "src",
		PayloadFilter: map[string]any{"strategy_id": "s1"},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("results len = %d, want 1", len(results))
	}
}

func TestMemoryStoreQueryPayloadFilterNoMatch(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	_ = s.Append(ctx, NewEvidence("src", KindFitness, map[string]any{"key": "val"}))

	results, err := s.Query(ctx, Filter{
		PayloadFilter: map[string]any{"nonexistent": "x"},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("results len = %d, want 0", len(results))
	}
}

// ── MemoryStore.Aggregate ───────────────────────────────────────────────────

func TestMemoryStoreAggregate(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	// Aggregate extracts float64 directly from payload — use bare numbers.
	e1 := NewEvidence("src", KindFitness, nil)
	e1.Payload = json.RawMessage(`1.0`)
	e2 := NewEvidence("src", KindFitness, nil)
	e2.Payload = json.RawMessage(`2.0`)
	e3 := NewEvidence("src", KindFitness, nil)
	e3.Payload = json.RawMessage(`3.0`)
	_ = s.Append(ctx, e1)
	_ = s.Append(ctx, e2)
	_ = s.Append(ctx, e3)

	sum := func(values []float64) float64 {
		total := 0.0
		for _, v := range values {
			total += v
		}
		return total
	}

	result, err := s.Aggregate(ctx, Filter{Source: "src"}, sum)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if result != 6.0 {
		t.Errorf("Aggregate = %v, want 6.0", result)
	}
}

func TestMemoryStoreAggregateEmpty(t *testing.T) {
	s := NewMemoryStore()
	fn := func(values []float64) float64 { return 42.0 }
	result, err := s.Aggregate(context.Background(), Filter{}, fn)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if result != 0 {
		t.Errorf("Aggregate on empty = %v, want 0", result)
	}
}

func TestMemoryStoreAggregateNonNumericPayload(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	_ = s.Append(ctx, NewEvidence("src", KindFitness, map[string]any{"text": "not a number"}))

	fn := func(values []float64) float64 { return 99.0 }
	result, err := s.Aggregate(ctx, Filter{Source: "src"}, fn)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	// Non-numeric payloads are skipped, so values is empty → return 0.
	if result != 0 {
		t.Errorf("Aggregate = %v, want 0 for non-numeric payloads", result)
	}
}

// ── payloadMatches ──────────────────────────────────────────────────────────

func TestPayloadMatches(t *testing.T) {
	payload, marshalErr := json.Marshal(map[string]any{"key": "val", "num": 42})
	if marshalErr != nil {
		t.Fatalf("json.Marshal: %v", marshalErr)
	}
	tests := []struct {
		name   string
		filter map[string]any
		want   bool
	}{
		{"match_string", map[string]any{"key": "val"}, true},
		{"match_number", map[string]any{"num": 42}, true},
		{"match_multiple", map[string]any{"key": "val", "num": 42}, true},
		{"no_match_wrong_value", map[string]any{"key": "wrong"}, false},
		{"no_match_missing_key", map[string]any{"missing": "x"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := payloadMatches(payload, tt.filter); got != tt.want {
				t.Errorf("payloadMatches(%v) = %v, want %v", tt.filter, got, tt.want)
			}
		})
	}
}

func TestPayloadMatchesEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		payload json.RawMessage
		filter  map[string]any
		want    bool
	}{
		{"empty_payload", json.RawMessage{}, map[string]any{"k": "v"}, false},
		{"non_object_payload", json.RawMessage(`"string"`), map[string]any{"k": "v"}, false},
		{"null_payload", json.RawMessage(`null`), map[string]any{"k": "v"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := payloadMatches(tt.payload, tt.filter); got != tt.want {
				t.Errorf("payloadMatches = %v, want %v", got, tt.want)
			}
		})
	}
}

// ── Collector ───────────────────────────────────────────────────────────────

func TestNewCollector(t *testing.T) {
	store := NewMemoryStore()
	c := NewCollector(store, "test-source")
	if c == nil {
		t.Fatal("NewCollector returned nil")
	}
	if c.source != "test-source" {
		t.Errorf("source = %q, want test-source", c.source)
	}
}

func TestCollectorEmit(t *testing.T) {
	store := NewMemoryStore()
	c := NewCollector(store, "test-source")
	ctx := context.Background()

	err := c.Emit(ctx, KindFitness, map[string]any{"value": 0.9})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}

	results, queryErr := store.Query(ctx, Filter{Source: "test-source"})
	if queryErr != nil {
		t.Fatalf("Query: %v", queryErr)
	}
	if len(results) != 1 {
		t.Errorf("results len = %d, want 1", len(results))
	}
}

func TestCollectorEmitNilStore(t *testing.T) {
	c := NewCollector(nil, "test-source")
	err := c.Emit(context.Background(), KindFitness, nil)
	if err != nil {
		t.Errorf("Emit with nil store should be no-op, got %v", err)
	}
}

func TestCollectorEmitWithMeta(t *testing.T) {
	store := NewMemoryStore()
	c := NewCollector(store, "test-source")
	ctx := context.Background()

	err := c.EmitWithMeta(ctx, KindFitness, map[string]any{"value": 0.5},
		"task_id", "t1", "agent_id", "a1")
	if err != nil {
		t.Fatalf("EmitWithMeta: %v", err)
	}

	results, queryErr := store.Query(ctx, Filter{Source: "test-source"})
	if queryErr != nil {
		t.Fatalf("Query: %v", queryErr)
	}
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	if results[0].Metadata["task_id"] != "t1" {
		t.Errorf("Metadata[task_id] = %q, want t1", results[0].Metadata["task_id"])
	}
	if results[0].Metadata["agent_id"] != "a1" {
		t.Errorf("Metadata[agent_id] = %q, want a1", results[0].Metadata["agent_id"])
	}
}

func TestCollectorEmitWithMetaNilStore(t *testing.T) {
	c := NewCollector(nil, "test-source")
	err := c.EmitWithMeta(context.Background(), KindFitness, nil, "k", "v")
	if err != nil {
		t.Errorf("EmitWithMeta with nil store should be no-op, got %v", err)
	}
}

func TestCollectorEmitWithMetaOddArgs(t *testing.T) {
	store := NewMemoryStore()
	c := NewCollector(store, "test-source")
	ctx := context.Background()

	// Odd number of args — last one is ignored.
	err := c.EmitWithMeta(ctx, KindFitness, nil, "key1", "val1", "dangling")
	if err != nil {
		t.Fatalf("EmitWithMeta: %v", err)
	}

	results, queryErr := store.Query(ctx, Filter{Source: "test-source"})
	if queryErr != nil {
		t.Fatalf("Query: %v", queryErr)
	}
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	if results[0].Metadata["key1"] != "val1" {
		t.Errorf("Metadata[key1] = %q, want val1", results[0].Metadata["key1"])
	}
	if _, exists := results[0].Metadata["dangling"]; exists {
		t.Error("dangling key should not be in metadata")
	}
}
