package aresrecovery

import (
	"testing"
)

// TestCombinedFitness verifies the auto+human blend (0.3 auto / 0.7 human).
func TestCombinedFitness(t *testing.T) {
	got := CombinedFitness(0.5, 0.8)
	want := 0.3*0.5 + 0.7*0.8
	if got != want {
		t.Fatalf("CombinedFitness(0.5, 0.8) = %v, want %v", got, want)
	}
}

// TestFeedbackStoreAddAndAll verifies feedback entries are recorded oldest
// first.
func TestFeedbackStoreAddAndAll(t *testing.T) {
	store := NewFeedbackStore()
	store.Add(HumanFeedback{CandidateID: "c1", Rating: 4, Approved: true, Reason: "good"})
	store.Add(HumanFeedback{CandidateID: "c2", Rating: 2, Approved: false, Reason: "unstable"})

	all := store.All()
	if len(all) != 2 {
		t.Fatalf("want 2 entries, got %d", len(all))
	}
	if all[0].CandidateID != "c1" || all[1].CandidateID != "c2" {
		t.Fatalf("entries out of order: %+v", all)
	}
	if store.Count() != 2 {
		t.Fatalf("count = %d, want 2", store.Count())
	}
}

// TestFeedbackStoreLatestWins verifies re-reviewing a candidate replaces the
// previous entry (latest wins).
func TestFeedbackStoreLatestWins(t *testing.T) {
	store := NewFeedbackStore()
	store.Add(HumanFeedback{CandidateID: "c1", Rating: 2, Approved: false})
	store.Add(HumanFeedback{CandidateID: "c1", Rating: 5, Approved: true, Reason: "after retest"})

	if store.Count() != 1 {
		t.Fatalf("re-review must replace, count = %d", store.Count())
	}
	latest := store.ForCandidate("c1")
	if latest == nil || latest.Rating != 5 || !latest.Approved {
		t.Fatalf("latest feedback wrong: %+v", latest)
	}
}

// TestFeedbackStoreForCandidate verifies lookup and nil for unreviewed
// candidates.
func TestFeedbackStoreForCandidate(t *testing.T) {
	store := NewFeedbackStore()
	if store.ForCandidate("missing") != nil {
		t.Fatal("unreviewed candidate must return nil")
	}
	store.Add(HumanFeedback{CandidateID: "c1", Rating: 4.5, Approved: true})
	got := store.ForCandidate("c1")
	if got == nil || got.Rating != 4.5 {
		t.Fatalf("ForCandidate wrong: %+v", got)
	}
}

// TestFeedbackStoreMaxEntries verifies history is capped to the most recent n
// entries.
func TestFeedbackStoreMaxEntries(t *testing.T) {
	store := NewFeedbackStore().WithMaxEntries(2)
	for i := 0; i < 5; i++ {
		store.Add(HumanFeedback{CandidateID: string(rune('a' + i)), Rating: float64(i)})
	}
	all := store.All()
	if len(all) != 2 {
		t.Fatalf("want 2 retained entries, got %d", len(all))
	}
	// Entries a..e added; the last two (d, e) must survive the cap.
	if all[0].CandidateID != string(rune('d')) || all[1].CandidateID != string(rune('e')) {
		t.Fatalf("must keep the most recent entries, got %+v", all)
	}
	// ForCandidate must still resolve the retained entries.
	if got := store.ForCandidate("d"); got == nil || got.Rating != 3 {
		t.Fatalf("ForCandidate(d) must resolve after trimming, got %+v", got)
	}
}
