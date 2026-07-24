// Package eval is the Phase 1 / 1.3 evaluation harness for the AKG quality
// gate. It feeds extracted conversation candidates through the REAL compiler
// pipeline (AKGSelector.Select -> AKGBuilder.Build, with the L1 structural
// gate, L2 confidence filter, and L3 dedup), then measures what the gate
// actually did against human/LLM gold annotations.
//
// The two hard gates that decide whether we may proceed to Phase 2 (persistence
// swap) and Phase 3 (auto-retrieval wiring) are:
//
//   - Structure gate:  >= 0.95 of candidate nodes must be structurally valid
//     (pass compiler.ValidateNodeForAKG). This proves the extractor is not
//     producing garbage.
//   - Precision gate:  >= 0.85 micro-precision of built objects vs gold. This
//     proves that what survives the gate is actually correct knowledge, not
//     noise that merely looks structured.
//
// Dedup rate and the confidence histogram are reported for inspection but are
// not gating. A sample needs only candidate nodes to run; gold is optional and
// only required for the precision gate.
package eval

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_memory/compiler"
	"github.com/Timwood0x10/ares/internal/knowledge"
	memorystore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
)

// Gate thresholds (plan 1.3).
const (
	// StructureGateMin is the minimum fraction of candidate nodes that must
	// pass the L1 structural validator to proceed.
	StructureGateMin = 0.95
	// PrecisionGateMin is the minimum micro-precision of built objects vs gold.
	PrecisionGateMin = 0.85
)

// SampleNode mirrors one candidate KM node extracted from a conversation,
// before the quality gate runs. The harness projects these through the real
// pipeline so the measured metrics reflect production behavior.
type SampleNode struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"` // entity|fact|reference|decision|...
	Attributes map[string]any `json:"attributes,omitempty"`
	Confidence float64        `json:"confidence"`
	Source     string         `json:"source,omitempty"`
}

// Gold holds the ground-truth knowledge for a sample, used to compute
// precision/recall. Keys are plain strings; they are canonicalized with
// NormalizeKey before comparison, so phrasing/casing differences do not
// matter. Facts are typically "subject predicate object", entities their names.
type Gold struct {
	Facts      []string `json:"facts,omitempty"`
	Entities   []string `json:"entities,omitempty"`
	References []string `json:"references,omitempty"`
}

// Message is kept for provenance only; the harness does not parse it.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Sample is one evaluated conversation: the extracted candidate nodes plus
// optional gold annotations.
type Sample struct {
	ID         string       `json:"id"`
	Namespace  string       `json:"namespace,omitempty"`
	Messages   []Message    `json:"messages,omitempty"`
	Candidates []SampleNode `json:"candidates"`
	Gold       *Gold        `json:"gold,omitempty"`
}

// HarnessConfig configures an evaluation run.
type HarnessConfig struct {
	// MinConfidence is the AKG selector threshold (L2). Production uses 0.6.
	MinConfidence float64 `json:"min_confidence"`
	// MaxFacts caps returned facts (0 = no cap).
	MaxFacts int `json:"max_facts"`
	// DedupThreshold is the Jaccard cutoff for the L3 resolver (0.85).
	DedupThreshold float64 `json:"dedup_threshold"`
	// EnableQualityGate toggles the L1 structural gate.
	EnableQualityGate bool `json:"enable_quality_gate"`
	// SharedStore makes one KnowledgeStore span every sample in a run,
	// modelling real cross-session collaborative dedup. When false (default)
	// each sample gets an isolated store so precision is measured cleanly.
	SharedStore bool `json:"shared_store"`
}

// DefaultHarnessConfig returns production-aligned defaults.
func DefaultHarnessConfig() HarnessConfig {
	return HarnessConfig{
		MinConfidence:     0.6,
		MaxFacts:          0,
		DedupThreshold:    0.85,
		EnableQualityGate: true,
		SharedStore:       false,
	}
}

// BuiltItem is one object that survived all gates, with the canonical key used
// for precision comparison.
type BuiltItem struct {
	ID         string  `json:"id"`
	NodeType   string  `json:"node_type"`
	ObjectType string  `json:"object_type"`
	Summary    string  `json:"summary"`
	Confidence float64 `json:"confidence"`
	Signal     string  `json:"signal"`
	Key        string  `json:"key"`
	// Evidence is the extraction signal class that produced the candidate
	// (camelcase, structural_ref, recurrence, quoted, ...). It is read from
	// the candidate node attributes and powers per-evidence precision, the
	// statistical basis for confidence calibration.
	Evidence string `json:"evidence,omitempty"`
}

// EvidenceStat aggregates precision per extraction evidence class across all
// gold-annotated samples. This is the empirical measurement that calibrated
// confidence values are derived from (replacing hand-tuned constants).
type EvidenceStat struct {
	Built int `json:"built"`
	Hit   int `json:"hit"`
	// Precision is raw hit/built.
	Precision float64 `json:"precision"`
	// Smoothed is the Laplace-smoothed precision (hit+1)/(built+2), which
	// avoids degenerate 0.0/1.0 estimates from tiny buckets.
	Smoothed float64 `json:"smoothed"`
}

// SampleResult holds the per-sample measurements.
type SampleResult struct {
	ID                string `json:"id"`
	NodesIn           int64  `json:"nodes_in"`
	DroppedLowConf    int64  `json:"dropped_low_conf"`
	DroppedStructural int64  `json:"dropped_structural"`
	DedupHits         int64  `json:"dedup_hits"`
	ObjectsBuilt      int64  `json:"objects_built"`

	StructuralPass     int     `json:"structural_pass"`  // L1 passes
	StructuralTotal    int     `json:"structural_total"` // candidate count
	StructuralPassRate float64 `json:"structural_pass_rate"`

	SurvivalRate float64 `json:"survival_rate"` // objects_built / nodes_in

	HasGold   bool    `json:"has_gold"`
	Precision float64 `json:"precision,omitempty"`
	Recall    float64 `json:"recall,omitempty"`

	// raw counts for aggregation (omitted from JSON).
	PrecHit   int `json:"-"`
	PrecBuilt int `json:"-"`
	RecallHit int `json:"-"` // distinct gold keys covered (recall numerator)
	PrecGold  int `json:"-"`

	Built []BuiltItem `json:"built,omitempty"`
}

// Aggregate summarizes the whole run.
type Aggregate struct {
	NodesIn           int64 `json:"nodes_in"`
	DroppedLowConf    int64 `json:"dropped_low_conf"`
	DroppedStructural int64 `json:"dropped_structural"`
	DedupHits         int64 `json:"dedup_hits"`
	ObjectsBuilt      int64 `json:"objects_built"`

	StructuralPassRate float64 `json:"structural_pass_rate"`
	SurvivalRate       float64 `json:"survival_rate"`
	DedupRate          float64 `json:"dedup_rate"`

	PrecisionMicro float64 `json:"precision_micro"`
	RecallMicro    float64 `json:"recall_micro"`
	HasGold        bool    `json:"has_gold"`

	ConfidenceHistogram map[string]int64 `json:"confidence_histogram"`
	SignalTiers         map[string]int64 `json:"signal_tiers"`

	// EvidencePrecision maps evidence class -> measured precision, only
	// populated when gold annotations exist. Used to calibrate extractor
	// confidence constants against reality instead of intuition.
	EvidencePrecision map[string]*EvidenceStat `json:"evidence_precision,omitempty"`
}

// Gates reports the pass/fail verdict for the two hard gates.
type Gates struct {
	StructureRate    float64 `json:"structure_rate"`
	StructureGate    bool    `json:"structure_gate"` // >= 0.95
	PrecisionRate    float64 `json:"precision_rate"`
	PrecisionGate    bool    `json:"precision_gate"` // >= 0.85 (skipped if no gold)
	PrecisionSkipped bool    `json:"precision_skipped"`
	ReadyForPhase3   bool    `json:"ready_for_phase3"`
}

// Report is the full evaluation output.
type Report struct {
	GeneratedAt time.Time      `json:"generated_at"`
	Config      HarnessConfig  `json:"config"`
	Samples     int            `json:"samples"`
	Aggregate   Aggregate      `json:"aggregate"`
	Gates       Gates          `json:"gates"`
	PerSample   []SampleResult `json:"per_sample"`
}

// Run evaluates every sample and produces an aggregated Report with gate
// verdicts. A nil context is treated as context.Background.
func (h HarnessConfig) Run(ctx context.Context, samples []Sample) (*Report, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	rep := &Report{GeneratedAt: time.Now(), Config: h, Samples: len(samples)}
	agg := Aggregate{
		ConfidenceHistogram: map[string]int64{},
		SignalTiers:         map[string]int64{},
	}
	var (
		structPass, structTotal                 int
		precHit, precBuilt, recallHit, precGold int
		hasGold                                 bool
		shared                                  knowledge.KnowledgeStore
	)
	evStats := make(map[string]*EvidenceStat)
	if h.SharedStore {
		shared = memorystore.New()
	}
	for _, s := range samples {
		r, err := h.runSample(ctx, s, shared)
		if err != nil {
			return nil, err
		}
		rep.PerSample = append(rep.PerSample, r)
		agg.NodesIn += r.NodesIn
		agg.DroppedLowConf += r.DroppedLowConf
		agg.DroppedStructural += r.DroppedStructural
		agg.DedupHits += r.DedupHits
		agg.ObjectsBuilt += r.ObjectsBuilt
		structPass += r.StructuralPass
		structTotal += r.StructuralTotal
		if r.HasGold {
			hasGold = true
			precHit += r.PrecHit
			precBuilt += r.PrecBuilt
			recallHit += r.RecallHit
			precGold += r.PrecGold
			accumulateEvidence(evStats, r, s.Gold)
		}
		for _, b := range r.Built {
			agg.ConfidenceHistogram[confBucket(b.Confidence)]++
			agg.SignalTiers[b.Signal]++
		}
	}
	agg.StructuralPassRate = safeDivInt64(int64(structPass), int64(structTotal))
	agg.SurvivalRate = safeDivInt64(agg.ObjectsBuilt, agg.NodesIn)
	agg.DedupRate = safeDivInt64(agg.DedupHits, agg.NodesIn)
	agg.HasGold = hasGold
	if hasGold {
		agg.PrecisionMicro = safeDivInt64(int64(precHit), int64(precBuilt))
		agg.RecallMicro = safeDivInt64(int64(recallHit), int64(precGold))
		for _, st := range evStats {
			st.Precision = safeDivInt64(int64(st.Hit), int64(st.Built))
			st.Smoothed = float64(st.Hit+1) / float64(st.Built+2)
		}
		agg.EvidencePrecision = evStats
	}
	rep.Aggregate = agg

	g := Gates{
		StructureRate: agg.StructuralPassRate,
		StructureGate: agg.StructuralPassRate >= StructureGateMin,
	}
	if hasGold {
		g.PrecisionRate = agg.PrecisionMicro
		g.PrecisionGate = agg.PrecisionMicro >= PrecisionGateMin
	} else {
		g.PrecisionSkipped = true
	}
	g.ReadyForPhase3 = g.StructureGate && (g.PrecisionGate || g.PrecisionSkipped)
	rep.Gates = g
	return rep, nil
}

// runSample evaluates a single sample with an isolated or shared store.
func (h HarnessConfig) runSample(ctx context.Context, s Sample, shared knowledge.KnowledgeStore) (SampleResult, error) {
	km, err := toKnowledgeModel(s.Candidates)
	if err != nil {
		return SampleResult{}, err
	}
	ns := s.Namespace
	if ns == "" {
		ns = "eval." + s.ID
	}

	store := shared
	if store == nil {
		store = memorystore.New()
	}
	m := compiler.NewAKGMetrics()
	sel := compiler.NewAKGSelector(h.MinConfidence, h.MaxFacts).WithAKGMetrics(m)
	b := compiler.NewAKGBuilder(store).
		WithResolver(compiler.NewResolver(store, h.DedupThreshold).WithMetrics(m)).
		WithQualityGate(h.EnableQualityGate).
		WithMetrics(m)

	sub := sel.Select(km)
	res, err := b.Build(ctx, sub, ns)
	if err != nil {
		return SampleResult{}, fmt.Errorf("eval: build sample %q: %w", s.ID, err)
	}
	snap := m.Snapshot()

	// Candidate ID -> evidence class, so built objects (which keep the
	// candidate node ID) can be bucketed by the signal that produced them.
	evidenceByID := make(map[string]string, len(s.Candidates))
	for _, c := range s.Candidates {
		if v, ok := c.Attributes["evidence"]; ok {
			if ev, ok := v.(string); ok && ev != "" {
				evidenceByID[c.ID] = ev
			}
		}
	}

	built := make([]BuiltItem, 0, len(res.Objects))
	for _, o := range res.Objects {
		var nt, sig string
		if v, ok := o.Metadata["node_type"]; ok {
			nt, _ = v.(string)
		}
		if v, ok := o.Metadata["source_signal"]; ok {
			sig, _ = v.(string)
		}
		built = append(built, BuiltItem{
			ID:         o.ID,
			NodeType:   nt,
			ObjectType: string(o.Type),
			Summary:    o.Summary,
			Confidence: o.Confidence,
			Signal:     sig,
			Key:        NormalizeKey(o.Summary),
			Evidence:   evidenceByID[o.ID],
		})
	}

	var pass, total int
	for _, c := range s.Candidates {
		total++
		if ok, _ := compiler.ValidateNodeForAKG(toNode(c)); ok {
			pass++
		}
	}

	r := SampleResult{
		ID:                 s.ID,
		NodesIn:            snap.NodesIn,
		DroppedLowConf:     snap.DroppedLowConf,
		DroppedStructural:  snap.DroppedStructural,
		DedupHits:          snap.DedupHits,
		ObjectsBuilt:       snap.ObjectsBuilt,
		StructuralPass:     pass,
		StructuralTotal:    total,
		StructuralPassRate: safeDivInt64(int64(pass), int64(total)),
		SurvivalRate:       safeDivInt64(snap.ObjectsBuilt, snap.NodesIn),
		Built:              built,
	}
	if s.Gold != nil {
		hit, builtN, goldHit, goldN := precisionCounts(built, s.Gold)
		r.HasGold = true
		r.PrecHit = hit
		r.PrecBuilt = builtN
		r.RecallHit = goldHit
		r.PrecGold = goldN
		r.Precision = safeDivInt64(int64(hit), int64(builtN))
		r.Recall = safeDivInt64(int64(goldHit), int64(goldN))
	}
	return r, nil
}

// toKnowledgeModel builds a KM from candidate nodes.
func toKnowledgeModel(cands []SampleNode) (*compiler.KnowledgeModel, error) {
	km := compiler.NewKnowledgeModel()
	for _, c := range cands {
		if c.ID == "" {
			return nil, fmt.Errorf("eval: candidate missing id")
		}
		if err := km.AddNode(toNode(c)); err != nil {
			return nil, fmt.Errorf("eval: %w", err)
		}
	}
	return km, nil
}

// toNode converts a SampleNode into a KM Node.
func toNode(c SampleNode) *compiler.Node {
	return &compiler.Node{
		ID:         c.ID,
		Type:       compiler.NodeType(c.Type),
		Attributes: c.Attributes,
		Confidence: c.Confidence,
		Source:     c.Source,
	}
}

// precisionCounts compares built keys to gold (canonicalized).
//
// Returns:
//
//	hit     — built items whose key matched gold (precision numerator).
//	builtN  — built items with a usable key (precision denominator).
//	goldHit — DISTINCT gold keys covered by at least one built item (recall
//	          numerator). Counting distinct keys instead of per-built matches
//	          keeps recall <= 1 when several built items collapse onto the
//	          same gold key.
//	goldN   — distinct gold keys (recall denominator).
func precisionCounts(built []BuiltItem, gold *Gold) (hit, builtN, goldHit, goldN int) {
	goldSet := goldKeySet(gold)
	goldN = len(goldSet)
	covered := make(map[string]struct{})
	for _, b := range built {
		if b.Key == "" {
			continue
		}
		builtN++
		if _, ok := goldSet[b.Key]; ok {
			hit++
			covered[b.Key] = struct{}{}
		}
	}
	return hit, builtN, len(covered), goldN
}

// goldKeySet canonicalizes every gold annotation (facts, entities,
// references) into a set of NormalizeKey keys.
func goldKeySet(gold *Gold) map[string]struct{} {
	set := make(map[string]struct{})
	for _, group := range [][]string{gold.Facts, gold.Entities, gold.References} {
		for _, g := range group {
			if k := NormalizeKey(g); k != "" {
				set[k] = struct{}{}
			}
		}
	}
	return set
}

// accumulateEvidence tallies per-evidence-class built/hit counts for one
// gold-annotated sample into stats. Items without an evidence label are
// grouped under "unlabeled" so the report never silently drops them.
func accumulateEvidence(stats map[string]*EvidenceStat, r SampleResult, gold *Gold) {
	goldSet := goldKeySet(gold)
	for _, b := range r.Built {
		if b.Key == "" {
			continue
		}
		ev := b.Evidence
		if ev == "" {
			ev = "unlabeled"
		}
		st := stats[ev]
		if st == nil {
			st = &EvidenceStat{}
			stats[ev] = st
		}
		st.Built++
		if _, ok := goldSet[b.Key]; ok {
			st.Hit++
		}
	}
}

// confBucket maps a confidence score to its reporting bucket label.
func confBucket(c float64) string {
	switch {
	case c < 0.4:
		return "<0.4"
	case c < 0.7:
		return "0.4-0.7"
	case c < 0.9:
		return "0.7-0.9"
	default:
		return "0.9-1.0"
	}
}

// safeDivInt64 returns num/den, or 0 when den <= 0.
func safeDivInt64(num, den int64) float64 {
	if den <= 0 {
		return 0
	}
	return float64(num) / float64(den)
}
