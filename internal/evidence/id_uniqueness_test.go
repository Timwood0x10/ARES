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

// TestNewEvidenceIDIsStableForOneValue pins that the ID is not derived from
// the payload content: two separately constructed records with identical
// content must still get distinct IDs. A content digest alone would make
// re-appended duplicates indistinguishable from within-tick collisions and
// break the store's ON CONFLICT (id) DO NOTHING dedup contract.
//
// Stability of the ID across reads of the same value is a property of the
// plain struct field (assigned once at construction in NewEvidence); it has no
// observable failure mode to test here — a lazily recomputed ID would not be
// a field at all and would fail to compile.
func TestNewEvidenceIDIsStableForOneValue(t *testing.T) {
	ev := NewEvidence("memory", KindKnowledge, map[string]any{"k": "v"})

	if again := NewEvidence("memory", KindKnowledge, map[string]any{"k": "v"}); again.ID == ev.ID {
		t.Fatal("two separately constructed records must not share an ID")
	}
}
