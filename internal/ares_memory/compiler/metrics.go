// Package compiler — AKGMetrics is the observability collector for the AKG
// quality gate (Phase 1 L3). It accumulates per-run counters and a confidence
// histogram across the three quality-gate stages so operators and the 1.3
// evaluation harness can see what the gate actually did, instead of trusting a
// threshold alone.
//
// Design notes:
//   - The compiler package has NO dependency on any external metrics system.
//     AKGMetrics is a plain in-process collector; the observability layer
//     (internal/ares_observability) bridges a Snapshot() to Prometheus
//     without coupling this low-level library to that dependency.
//   - A single *AKGMetrics instance is shared by the selector, builder, and
//     resolver of one compiler pipeline, so a run produces one coherent
//     snapshot. It is safe for concurrent use, so concurrent sessions may
//     each own their own instance.
package compiler

import (
	"sync"
	"sync/atomic"
)

// akgConfBuckets partition the [0,1] confidence range into human-readable
// buckets that line up with the L2 signal tiers (<0.4 weak, 0.4-0.7 weak,
// 0.7-0.9 medium, 0.9-1.0 strong). The histogram lets the evaluation harness
// confirm the distribution is not piled onto a single value (plan 1.3 gate).
var akgConfBuckets = []struct {
	lo, hi float64
	label  string
}{
	{0.0, 0.4, "<0.4"},
	{0.4, 0.7, "0.4-0.7"},
	{0.7, 0.9, "0.7-0.9"},
	{0.9, 1.001, "0.9-1.0"},
}

// signalTierOf maps a confidence score to its L2 signal tier label. It mirrors
// the mapping in nodeToKnowledgeObject so the histogram tier and the
// object's source_signal metadata never disagree.
func signalTierOf(c float64) string {
	switch {
	case c >= confStrong:
		return "strong"
	case c >= confMedium:
		return "medium"
	default:
		return "weak"
	}
}

// AKGSnapshot is an immutable, non-atomic copy of the accumulated quality-gate
// metrics. Read it after a Compile/Run completes; safe to inspect concurrently.
type AKGSnapshot struct {
	// NodesIn is the number of candidate nodes (entities + facts +
	// references) the AKG selector considered — i.e. "akg_objects_in".
	NodesIn int64
	// DroppedLowConf is the number of nodes the selector excluded because their
	// confidence fell below AKGMinConfidence (L2 gate).
	DroppedLowConf int64
	// DroppedStructural is the number of nodes the builder dropped because they
	// failed ValidateNodeForAKG (L1 structural gate).
	DroppedStructural int64
	// DedupHits is the number of near-duplicate objects the resolver discarded
	// (Jaccard >= threshold against already-persisted knowledge).
	DedupHits int64
	// ObjectsBuilt is the number of objects that survived all gates and were
	// projected into the AKG store.
	ObjectsBuilt int64
	// ConfidenceHistogram counts surviving objects per confidence bucket.
	ConfidenceHistogram map[string]int64
	// SignalTiers counts surviving objects per L2 signal tier.
	SignalTiers map[string]int64
}

// AKGMetrics accumulates the quality-gate observability for one compiler
// pipeline run. Zero value is not usable; construct with NewAKGMetrics.
type AKGMetrics struct {
	nodesIn           atomic.Int64
	droppedLowConf    atomic.Int64
	droppedStructural atomic.Int64
	dedupHits         atomic.Int64
	objectsBuilt      atomic.Int64

	mu         sync.Mutex
	confHist   map[string]int64
	signalTier map[string]int64
}

// NewAKGMetrics creates an empty quality-gate metrics collector.
func NewAKGMetrics() *AKGMetrics {
	return &AKGMetrics{
		confHist:   make(map[string]int64),
		signalTier: make(map[string]int64),
	}
}

// Reset zeroes all counters and distributions. Use it to reuse one instance
// across multiple independent runs while keeping per-run snapshots.
func (m *AKGMetrics) Reset() {
	if m == nil {
		return
	}
	m.nodesIn.Store(0)
	m.droppedLowConf.Store(0)
	m.droppedStructural.Store(0)
	m.dedupHits.Store(0)
	m.objectsBuilt.Store(0)
	m.mu.Lock()
	m.confHist = make(map[string]int64)
	m.signalTier = make(map[string]int64)
	m.mu.Unlock()
}

// RecordInput records the number of candidate nodes handed to the AKG
// selector (entities + facts + references).
func (m *AKGMetrics) RecordInput(n int64) {
	if m == nil || n <= 0 {
		return
	}
	m.nodesIn.Add(n)
}

// RecordLowConfDrop records a node the selector excluded for low confidence.
func (m *AKGMetrics) RecordLowConfDrop() {
	if m == nil {
		return
	}
	m.droppedLowConf.Add(1)
}

// RecordStructuralDrop records a node the builder dropped for failing the
// structural quality gate.
func (m *AKGMetrics) RecordStructuralDrop() {
	if m == nil {
		return
	}
	m.droppedStructural.Add(1)
}

// RecordDedupHit records a near-duplicate object the resolver discarded.
func (m *AKGMetrics) RecordDedupHit() {
	if m == nil {
		return
	}
	m.dedupHits.Add(1)
}

// RecordObjectBuilt records a surviving object and its confidence/signal tier,
// feeding the confidence histogram and signal-tier counts.
func (m *AKGMetrics) RecordObjectBuilt(confidence float64) {
	if m == nil {
		return
	}
	m.objectsBuilt.Add(1)
	tier := signalTierOf(confidence)
	bucket := bucketLabel(confidence)
	m.mu.Lock()
	m.confHist[bucket]++
	m.signalTier[tier]++
	m.mu.Unlock()
}

// bucketLabel returns the confidence-bucket label for c. Values outside
// [0,1] are clamped into the first/last bucket so a stray score never
// produces an empty label.
func bucketLabel(c float64) string {
	for _, b := range akgConfBuckets {
		if c >= b.lo && c < b.hi {
			return b.label
		}
	}
	if c < 0 {
		return akgConfBuckets[0].label
	}
	return akgConfBuckets[len(akgConfBuckets)-1].label
}

// Snapshot returns an immutable copy of the current metrics. Safe to call
// concurrently with recording.
func (m *AKGMetrics) Snapshot() AKGSnapshot {
	if m == nil {
		return AKGSnapshot{}
	}
	m.mu.Lock()
	conf := make(map[string]int64, len(m.confHist))
	for k, v := range m.confHist {
		conf[k] = v
	}
	sig := make(map[string]int64, len(m.signalTier))
	for k, v := range m.signalTier {
		sig[k] = v
	}
	m.mu.Unlock()
	return AKGSnapshot{
		NodesIn:             m.nodesIn.Load(),
		DroppedLowConf:      m.droppedLowConf.Load(),
		DroppedStructural:   m.droppedStructural.Load(),
		DedupHits:           m.dedupHits.Load(),
		ObjectsBuilt:        m.objectsBuilt.Load(),
		ConfidenceHistogram: conf,
		SignalTiers:         sig,
	}
}
