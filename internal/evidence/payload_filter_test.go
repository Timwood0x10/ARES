package evidence

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// TestMemoryStoreQuery_PayloadFilter locks the payload-containment filter
// (REVIEW 2.4#26 support): it must apply BEFORE the Limit so a
// payload-scoped window contains the most recent N MATCHING records, and
// records missing the key must not match (they cannot be attributed).
func TestMemoryStoreQuery_PayloadFilter(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	append := func(id, strategyID string, ts time.Time) {
		payload, _ := json.Marshal(map[string]any{"value": 0.5, "strategy_id": strategyID})
		if err := store.Append(ctx, Evidence{
			ID: id, Source: "strategy", Kind: KindFitness, Payload: payload, Timestamp: ts,
		}); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
	}
	// One record without a strategy_id (unattributable) interleaved with
	// attributed ones.
	base := time.Now().Add(-time.Hour)
	append("no-attrib", "", base.Add(1*time.Second))
	for i := 0; i < 3; i++ {
		append("other-"+string(rune('a'+i)), "other", base.Add(time.Duration(10+i)*time.Second))
	}
	for i := 0; i < 5; i++ {
		append("target-"+string(rune('a'+i)), "target", base.Add(time.Duration(20+i)*time.Second))
	}

	evs, err := store.Query(ctx, Filter{
		Source:        "strategy",
		Kind:          KindFitness,
		PayloadFilter: map[string]any{"strategy_id": "target"},
		Limit:         3,
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(evs) != 3 {
		t.Fatalf("expected 3 (limit-bound) target records, got %d", len(evs))
	}
	for _, e := range evs {
		var payload struct {
			StrategyID string `json:"strategy_id"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if payload.StrategyID != "target" {
			t.Errorf("non-matching record %s leaked into the filtered window (strategy_id=%q)",
				e.ID, payload.StrategyID)
		}
	}

	// Without PayloadFilter the same limit returns the 3 NEWEST overall
	// (the target records — they are newest here), proving the limit and
	// the filter compose rather than the filter widening the result.
	all, err := store.Query(ctx, Filter{Source: "strategy", Limit: 100})
	if err != nil {
		t.Fatalf("query all: %v", err)
	}
	if len(all) != 9 {
		t.Fatalf("expected 9 records total, got %d", len(all))
	}
}

// TestMemoryStoreQuery_PayloadFilterNumberMatching verifies numeric
// equivalence across JSON unmarshal types: a payload value of 3 (float64
// after unmarshal) must match a filter value of int 3.
func TestMemoryStoreQuery_PayloadFilterNumberMatching(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	payload, _ := json.Marshal(map[string]any{"value": 0.5, "n": 3})
	if err := store.Append(ctx, Evidence{
		ID: "num", Source: "s", Payload: payload, Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	evs, err := store.Query(ctx, Filter{PayloadFilter: map[string]any{"n": 3}})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(evs) != 1 {
		t.Fatalf("int filter value must match the JSON number 3, got %d records", len(evs))
	}
}
