package evidence

import (
	"testing"
)

// TestNewEvidenceIDsAreUniqueWithinATick pins V-1.
//
// Regression (docs/reviews/0.3.1-final-deep-review.md §5.4 V-1): NewEvidence
// derived its ID from UnixNano alone ("ev_%x"). On a coarse clock, or under
// enough concurrency, two records created in the same tick produced the SAME
// ID — and PostgresStore.Append writes with ON CONFLICT (id) DO NOTHING, so the
// second record was dropped with no error and no log. The digest-based
// generatedEvidenceID never ran because these IDs are pre-assigned, which is
// what made the store's collision safety unreachable.
//
// The loop is tight on purpose: uniqueness must hold for back-to-back calls,
// not just for calls separated by real work.
func TestNewEvidenceIDsAreUniqueWithinATick(t *testing.T) {
	const calls = 20000

	seen := make(map[string]struct{}, calls)
	for i := 0; i < calls; i++ {
		ev := NewEvidence("memory", KindKnowledge, map[string]any{"i": i})
		if ev.ID == "" {
			t.Fatal("NewEvidence must produce a non-empty ID")
		}
		if _, dup := seen[ev.ID]; dup {
			t.Fatalf("duplicate evidence ID %q at call %d: the store's "+
				"ON CONFLICT DO NOTHING would drop this record silently", ev.ID, i)
		}
		seen[ev.ID] = struct{}{}
	}
}

// TestNewEvidenceIDIsStableForOneValue locks the idempotency half of the
// contract: the ID is computed once at construction, so re-appending the SAME
// Evidence value is still deduplicated by the store rather than duplicated.
func TestNewEvidenceIDIsStableForOneValue(t *testing.T) {
	ev := NewEvidence("memory", KindKnowledge, map[string]any{"k": "v"})

	if ev.ID != ev.ID {
		t.Fatal("the ID stored on an Evidence value must not change")
	}
	if again := NewEvidence("memory", KindKnowledge, map[string]any{"k": "v"}); again.ID == ev.ID {
		t.Fatal("two separately constructed records must not share an ID")
	}
}
