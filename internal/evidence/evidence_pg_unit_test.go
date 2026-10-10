package evidence

import (
	"strings"
	"testing"
	"time"
)

// ── ttlSeconds ──────────────────────────────────────────────────────────────

func TestTTLSeconds(t *testing.T) {
	tests := []struct {
		name string
		ttl  time.Duration
		want int64
	}{
		{"zero", 0, 0},
		{"negative", -5 * time.Second, 0},
		{"exact_second", 1 * time.Second, 1},
		{"exact_two_seconds", 2 * time.Second, 2},
		{"sub_second_rounds_up", 500 * time.Millisecond, 1},
		{"one_and_half_rounds_up", 1500 * time.Millisecond, 2},
		{"sub_millisecond_rounds_up", 1 * time.Millisecond, 1},
		{"one_hour", time.Hour, 3600},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ttlSeconds(tt.ttl)
			if got != tt.want {
				t.Errorf("ttlSeconds(%v) = %d, want %d", tt.ttl, got, tt.want)
			}
		})
	}
}

// ── NewPostgresStore nil pool ───────────────────────────────────────────────

func TestNewPostgresStore_NilPool(t *testing.T) {
	_, err := NewPostgresStore(nil)
	if err == nil {
		t.Error("expected error for nil pool")
	}
}

// ── PostgresStore.Close with nil db ────────────────────────────────────────

func TestPostgresStore_Close_NilDB(t *testing.T) {
	s := &PostgresStore{db: nil}
	if err := s.Close(); err != nil {
		t.Errorf("Close on nil db should return nil, got %v", err)
	}
}

// ── generatedEvidenceID determinism and uniqueness ─────────────────────────

func TestGeneratedEvidenceID_Deterministic(t *testing.T) {
	e := Evidence{
		Source:    "test-source",
		Timestamp: time.Unix(1700000000, 123456789),
		Payload:   []byte(`{"value":1}`),
	}
	id1 := generatedEvidenceID(e)
	id2 := generatedEvidenceID(e)
	if id1 != id2 {
		t.Errorf("generatedEvidenceID not deterministic: %q != %q", id1, id2)
	}
}

func TestGeneratedEvidenceID_UniqueOnDifferentPayload(t *testing.T) {
	ts := time.Unix(1700000000, 123456789)
	e1 := Evidence{Source: "src", Timestamp: ts, Payload: []byte(`{"value":1}`)}
	e2 := Evidence{Source: "src", Timestamp: ts, Payload: []byte(`{"value":2}`)}

	id1 := generatedEvidenceID(e1)
	id2 := generatedEvidenceID(e2)
	if id1 == id2 {
		t.Errorf("IDs should differ for different payloads: %q == %q", id1, id2)
	}
}

func TestGeneratedEvidenceID_UniqueOnDifferentSource(t *testing.T) {
	ts := time.Unix(1700000000, 123456789)
	payload := []byte(`{"v":1}`)
	e1 := Evidence{Source: "srcA", Timestamp: ts, Payload: payload}
	e2 := Evidence{Source: "srcB", Timestamp: ts, Payload: payload}

	id1 := generatedEvidenceID(e1)
	id2 := generatedEvidenceID(e2)
	if id1 == id2 {
		t.Errorf("IDs should differ for different sources: %q == %q", id1, id2)
	}
}

func TestGeneratedEvidenceID_UniqueOnDifferentTimestamp(t *testing.T) {
	payload := []byte(`{"v":1}`)
	e1 := Evidence{Source: "src", Timestamp: time.Unix(1700000000, 1), Payload: payload}
	e2 := Evidence{Source: "src", Timestamp: time.Unix(1700000000, 2), Payload: payload}

	id1 := generatedEvidenceID(e1)
	id2 := generatedEvidenceID(e2)
	if id1 == id2 {
		t.Errorf("IDs should differ for different timestamps: %q == %q", id1, id2)
	}
}

func TestGeneratedEvidenceID_ContainsSource(t *testing.T) {
	e := Evidence{
		Source:    "my-source",
		Timestamp: time.Unix(1700000000, 0),
		Payload:   []byte(`{}`),
	}
	id := generatedEvidenceID(e)
	if id == "" {
		t.Fatal("generatedEvidenceID returned empty")
	}
	if !strings.Contains(id, "my-source") {
		t.Errorf("ID %q should contain source name", id)
	}
}

// ── Store interface compliance ──────────────────────────────────────────────

func TestMemoryStore_ImplementsStore(t *testing.T) {
	var _ Store = NewMemoryStore()
}

// ── MemoryStore concurrent access ──────────────────────────────────────────

func TestMemoryStore_ConcurrentAccess(t *testing.T) {
	s := NewMemoryStore()
	ctx := t.Context()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			e := NewEvidence("src", KindFitness, map[string]any{"i": i})
			_ = s.Append(ctx, e)
		}
	}()

	for i := 0; i < 100; i++ {
		_, _ = s.Query(ctx, Filter{Source: "src"})
	}

	<-done

	results, err := s.Query(ctx, Filter{Source: "src"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 100 {
		t.Errorf("results len = %d, want 100", len(results))
	}
}

// ── Filter zero value ───────────────────────────────────────────────────────

func TestFilter_ZeroValue(t *testing.T) {
	f := Filter{}
	if f.Source != "" {
		t.Error("Source should be empty")
	}
	if f.Kind != "" {
		t.Error("Kind should be empty")
	}
	if !f.Since.IsZero() {
		t.Error("Since should be zero")
	}
	if !f.Until.IsZero() {
		t.Error("Until should be zero")
	}
	if f.Limit != 0 {
		t.Error("Limit should be 0")
	}
	if f.PayloadFilter != nil {
		t.Error("PayloadFilter should be nil")
	}
}
