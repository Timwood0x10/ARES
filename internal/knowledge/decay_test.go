package knowledge_test

import (
	"context"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/knowledge"
	memstore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
)

// TestAKG_Decay_DownweightsStale_NeverDeletes pins the core M4 decay
// contract: stale objects are archived (StatusSuperseded) but NOT deleted
// — they stay in the store and can still be retrieved.
func TestAKG_Decay_DownweightsStale_NeverDeletes(t *testing.T) {
	t.Parallel()

	store := memstore.New()
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	// Seed: one fresh, one stale, one very old.
	objects := []*knowledge.KnowledgeObject{
		{ID: "fresh", Namespace: "ns", Summary: "fresh", Status: knowledge.StatusActive,
			CreatedAt: now.Add(-1 * time.Hour), Quality: &knowledge.Quality{FreshnessScore: 1.0}},
		{ID: "stale", Namespace: "ns", Summary: "stale", Status: knowledge.StatusActive,
			CreatedAt: now.Add(-48 * time.Hour), Quality: &knowledge.Quality{FreshnessScore: 1.0}},
		{ID: "ancient", Namespace: "ns", Summary: "ancient", Status: knowledge.StatusActive,
			CreatedAt: now.Add(-200 * 24 * time.Hour), Quality: &knowledge.Quality{FreshnessScore: 1.0}},
	}
	knowledge.StampWriteSchema(objects...)
	if err := store.Save(context.Background(), objects...); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cfg := knowledge.DecayConfig{
		ArchiveAfter: 100 * 24 * time.Hour, // 100 days
	}

	report, err := knowledge.ApplyDecay(context.Background(), store, "ns", cfg, now)
	if err != nil {
		t.Fatalf("ApplyDecay: %v", err)
	}

	if report.Archived == 0 {
		t.Error("expected at least one archived object (the ancient one)")
	}

	// The ancient object should be archived (StatusSuperseded).
	ancient, _ := store.Get(context.Background(), "ns", "ancient")
	if ancient.Status != knowledge.StatusSuperseded {
		t.Errorf("ancient object status = %q, want %q (archived, not deleted)",
			ancient.Status, knowledge.StatusSuperseded)
	}

	// The fresh and stale objects should still be active.
	fresh, _ := store.Get(context.Background(), "ns", "fresh")
	if fresh.Status != knowledge.StatusActive {
		t.Errorf("fresh object status = %q, want %q", fresh.Status, knowledge.StatusActive)
	}
	stale, _ := store.Get(context.Background(), "ns", "stale")
	if stale.Status != knowledge.StatusActive {
		t.Errorf("stale object status = %q, want %q (still active, not archived yet)",
			stale.Status, knowledge.StatusActive)
	}
}

// TestAKG_Decay_NoDecayByDefault pins that the default DecayConfig (zero
// durations) is a no-op — preserving pre-0.3.4 behaviour.
func TestAKG_Decay_NoDecayByDefault(t *testing.T) {
	t.Parallel()

	store := memstore.New()
	obj := &knowledge.KnowledgeObject{
		ID: "obj", Namespace: "ns", Summary: "test", Status: knowledge.StatusActive,
		CreatedAt: time.Now().Add(-365 * 24 * time.Hour),
		Quality:   &knowledge.Quality{FreshnessScore: 1.0},
	}
	knowledge.StampWriteSchema(obj)
	if err := store.Save(context.Background(), obj); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cfg := knowledge.DefaultDecayConfig()
	report, err := knowledge.ApplyDecay(context.Background(), store, "ns", cfg, time.Now())
	if err != nil {
		t.Fatalf("ApplyDecay: %v", err)
	}
	if report.Archived != 0 || report.Errors != 0 {
		t.Errorf("default config should be a no-op, got archived=%d errors=%d",
			report.Archived, report.Errors)
	}
}

// TestAKG_BuildReport_ExposesCounters pins the FIELD SET only.
//
// It constructs a literal and asserts the fields it set, so it proves the struct carries
// the counters M4 asks for — and nothing about a producer. The producer is covered where it
// lives: `TestDistillConversationWithReport_ProducesCounters` in
// internal/knowledge/adapter drives a real ingest and checks the numbers against the run.
// The old doc claimed this test pinned observability, which was exactly the kind of
// self-certifying assertion that let a report with no producer pass for finished work.
func TestAKG_BuildReport_ExposesCounters(t *testing.T) {
	t.Parallel()

	report := knowledge.BuildReport{
		ReadCount:    100,
		NewEntities:  80,
		NewRelations: 30,
		Deduped:      10,
		Rejected:     5,
		Conflicts:    2,
		Duration:     250 * time.Millisecond,
	}

	if report.ReadCount != 100 {
		t.Errorf("ReadCount = %d, want 100", report.ReadCount)
	}
	if report.NewEntities != 80 {
		t.Errorf("NewEntities = %d, want 80", report.NewEntities)
	}
	if report.NewRelations != 30 {
		t.Errorf("NewRelations = %d, want 30", report.NewRelations)
	}
	if report.Deduped != 10 {
		t.Errorf("Deduped = %d, want 10", report.Deduped)
	}
	if report.Rejected != 5 {
		t.Errorf("Rejected = %d, want 5", report.Rejected)
	}
	if report.Conflicts != 2 {
		t.Errorf("Conflicts = %d, want 2", report.Conflicts)
	}
	if report.Duration != 250*time.Millisecond {
		t.Errorf("Duration = %v, want 250ms", report.Duration)
	}
}

// TestAKG_ContentFingerprint pins that the fingerprint is deterministic for
// the same content and differs when content changes — the basis for
// incremental builds (M4).
func TestAKG_ContentFingerprint(t *testing.T) {
	t.Parallel()

	obj1 := &knowledge.KnowledgeObject{
		ID: "a", Type: knowledge.ObjectMemory, Summary: "hello world", Normalized: "hello world",
	}
	obj2 := &knowledge.KnowledgeObject{
		ID: "b", Type: knowledge.ObjectMemory, Summary: "hello world", Normalized: "hello world",
	}
	obj3 := &knowledge.KnowledgeObject{
		ID: "c", Type: knowledge.ObjectMemory, Summary: "different", Normalized: "different",
	}

	fp1 := knowledge.ContentFingerprint(obj1)
	fp2 := knowledge.ContentFingerprint(obj2)
	fp3 := knowledge.ContentFingerprint(obj3)

	if fp1 != fp2 {
		t.Errorf("same content should have same fingerprint: %q vs %q", fp1, fp2)
	}
	if fp1 == fp3 {
		t.Error("different content should have different fingerprints")
	}
	if fp1 == "" {
		t.Error("fingerprint should not be empty for non-nil object")
	}
}
