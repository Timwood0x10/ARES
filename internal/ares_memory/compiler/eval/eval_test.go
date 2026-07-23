package eval

import (
	"context"
	"math"
	"testing"
)

// find returns the per-sample result with the given id (empty if absent).
func find(rep *Report, id string) SampleResult {
	for _, r := range rep.PerSample {
		if r.ID == id {
			return r
		}
	}
	return SampleResult{}
}

func almost(v, want float64) bool { return math.Abs(v-want) < 1e-9 }

func TestHarnessBundled(t *testing.T) {
	samples, err := LoadDir("samples")
	if err != nil {
		t.Fatalf("load dir: %v", err)
	}
	if len(samples) != 4 {
		t.Fatalf("expected 4 top-level samples, got %d", len(samples))
	}

	rep, err := DefaultHarnessConfig().Run(context.Background(), samples)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(rep.PerSample) != 4 {
		t.Fatalf("expected 4 per-sample results, got %d", len(rep.PerSample))
	}

	// conv-clean: all 5 candidates structurally valid, all built, precision 1.0.
	clean := find(rep, "conv-clean")
	if clean.StructuralPassRate != 1.0 {
		t.Errorf("conv-clean structural pass rate = %v, want 1.0", clean.StructuralPassRate)
	}
	if clean.ObjectsBuilt != 5 {
		t.Errorf("conv-clean objects built = %d, want 5", clean.ObjectsBuilt)
	}
	if clean.DroppedStructural != 0 || clean.DroppedLowConf != 0 {
		t.Errorf("conv-clean drops = (struct=%d, low=%d), want 0/0", clean.DroppedStructural, clean.DroppedLowConf)
	}
	if !almost(clean.Precision, 1.0) {
		t.Errorf("conv-clean precision = %v, want 1.0", clean.Precision)
	}

	// conv-clean2: 4 clean candidates, all built, precision 1.0.
	clean2 := find(rep, "conv-clean2")
	if clean2.ObjectsBuilt != 4 || !almost(clean2.Precision, 1.0) {
		t.Errorf("conv-clean2 built=%d precision=%v, want 4/1.0", clean2.ObjectsBuilt, clean2.Precision)
	}

	// Default (per-sample) run has no cross-sample dedup, so dedup_hits == 0.
	if rep.Aggregate.DedupHits != 0 {
		t.Errorf("default run dedup_hits = %d, want 0 (isolated stores)", rep.Aggregate.DedupHits)
	}

	// Aggregate gates: all clean -> structure 1.0, gold present -> precision 1.0.
	if !rep.Gates.StructureGate {
		t.Errorf("structure gate failed with rate %v", rep.Gates.StructureRate)
	}
	if !rep.Gates.PrecisionGate {
		t.Errorf("precision gate failed with rate %v", rep.Gates.PrecisionRate)
	}
	if !rep.Gates.ReadyForPhase3 {
		t.Errorf("ReadyForPhase3 = false, want true for an all-clean bundled set")
	}
}

func TestHarnessNoisy(t *testing.T) {
	s, err := LoadSampleFile("samples/noisy/conv_noisy.json")
	if err != nil {
		t.Fatalf("load noisy: %v", err)
	}
	rep, err := DefaultHarnessConfig().Run(context.Background(), []Sample{s})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	r := rep.PerSample[0]
	// 6 candidates. n3 has an empty triple -> rejected by the L1 structural
	// gate. n4 ("the") is NOT a stopword in akgStopwords, so it survives.
	// n2 (conf 0.2) is dropped by the L2 confidence filter. Net: 5/6
	// structurally valid, 1 structural drop, 1 low-conf drop, 4 built.
	if r.StructuralPass != 5 || r.StructuralTotal != 6 {
		t.Errorf("structural pass/total = %d/%d, want 5/6", r.StructuralPass, r.StructuralTotal)
	}
	if r.DroppedStructural != 1 {
		t.Errorf("dropped structural = %d, want 1", r.DroppedStructural)
	}
	if r.DroppedLowConf != 1 {
		t.Errorf("dropped low-conf = %d, want 1", r.DroppedLowConf)
	}
	if r.ObjectsBuilt != 4 {
		t.Errorf("objects built = %d, want 4", r.ObjectsBuilt)
	}
	if !almost(r.Precision, 1.0) {
		t.Errorf("precision = %v, want 1.0 (gold only lists the valid facts)", r.Precision)
	}
}

func TestHarnessSharedStoreDedup(t *testing.T) {
	samples, err := LoadDir("samples")
	if err != nil {
		t.Fatalf("load dir: %v", err)
	}
	cfg := DefaultHarnessConfig()
	cfg.SharedStore = true
	rep, err := cfg.Run(context.Background(), samples)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// conv-dup-a and conv-dup-b share an identical fact; with one shared store
	// the second is deduplicated.
	if rep.Aggregate.DedupHits < 1 {
		t.Errorf("shared-store dedup_hits = %d, want >= 1", rep.Aggregate.DedupHits)
	}
}

func TestHarnessNoGoldSkipsPrecision(t *testing.T) {
	s := Sample{
		ID: "nogold",
		Candidates: []SampleNode{
			{ID: "x1", Type: "entity", Attributes: map[string]any{"name": "ARES"}, Confidence: 0.9},
		},
	}
	rep, err := DefaultHarnessConfig().Run(context.Background(), []Sample{s})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rep.Aggregate.HasGold {
		t.Errorf("HasGold = true, want false")
	}
	if !rep.Gates.PrecisionSkipped {
		t.Errorf("PrecisionSkipped = false, want true when no gold present")
	}
	// Structure is clean, precision skipped -> ready to proceed.
	if !rep.Gates.ReadyForPhase3 {
		t.Errorf("ReadyForPhase3 = false, want true (structure ok, precision skipped)")
	}
}

func TestNormalizeKey(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Rust compiles to native code", "code compiles native rust"},
		{"rust COMPILES native CODE", "code compiles native rust"},
		{"ARES uses AKG graph", "akg ares graph uses"},
		{"", ""},
		{"the", ""},              // pure stopword -> empty
		{"ARES是运行时", "ares是运行时"}, // CJK is one token (no whitespace segmentation)
	}
	for _, c := range cases {
		if got := NormalizeKey(c.in); got != c.want {
			t.Errorf("NormalizeKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
