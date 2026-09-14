package knowledge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
)

// Normalizer converts Raw bytes into Normalized text.
// Stage 1 of the Resolver pipeline: standardize input format.
type Normalizer interface {
	// Name returns the normalizer name for logging.
	Name() string

	// Normalize converts raw bytes to normalized text.
	Normalize(ctx context.Context, obj *KnowledgeObject) (*KnowledgeObject, error)
}

// EntityMatcher attempts to match a KnowledgeObject against existing entities.
// Stage 2 of the Resolver pipeline: resolve aliases and duplicates.
type EntityMatcher interface {
	// Name returns the matcher name for logging.
	Name() string

	// Match tries to match the object to an existing entity.
	// Returns the matched ID and confidence, or ("", 0, nil) for new entities.
	Match(ctx context.Context, obj *KnowledgeObject, candidates []*KnowledgeObject) (*ResolveResult, error)
}

// tokenizeBag splits text into a lowercase word-count bag, matching the
// DefaultEntityMatcher's token semantics (pipeline/normalizer.go's tokenize).
// A local variant exists because hybrid.go's tokenize returns a set
// (map[string]bool); the matcher contract needs counts.
func tokenizeBag(text string) map[string]int {
	tokens := make(map[string]int)
	for _, word := range strings.Fields(strings.ToLower(text)) {
		if word != "" {
			tokens[word]++
		}
	}
	return tokens
}

// TokenAwareMatcher is the tokenize-once fast path for entity matchers.
// Implementations receive the object's token bag and a cache of candidate
// token bags (keyed by candidate ID) computed once per Process call, instead
// of re-tokenizing every candidate inside their own O(n²) pair loop. The
// legacy Match method remains the fallback for matchers that do not opt in.
type TokenAwareMatcher interface {
	EntityMatcher

	// MatchTokens runs the match with pre-computed token bags.
	// objTokens is tokenizeBag(obj.Normalized + " " + obj.Summary);
	// candTokens maps candidate ID to the same computation for each
	// candidate. Bags are read-only shared state — implementations must
	// not mutate them.
	MatchTokens(ctx context.Context, obj *KnowledgeObject, objTokens map[string]int, candidates []*KnowledgeObject, candTokens map[string]map[string]int) (*ResolveResult, error)
}

// ResolveResult is the outcome of entity matching.
type ResolveResult struct {
	MatchedObjectID string  `json:"matched_object_id,omitempty"`
	Confidence      float64 `json:"confidence"`
	IsNew           bool    `json:"is_new"`
}

// Validator checks whether a merge result is consistent.
// Stage 3 of the Resolver pipeline: validate and detect conflicts.
type Validator interface {
	// Name returns the validator name for logging.
	Name() string

	// Validate checks the merged object for conflicts.
	Validate(ctx context.Context, merged *KnowledgeObject, sources []*KnowledgeObject) (*ValidationResult, error)
}

// ValidationResult is the outcome of conflict validation.
type ValidationResult struct {
	Confidence float64    `json:"confidence"`
	Conflicts  []Conflict `json:"conflicts,omitempty"`
}

// Conflict describes a field-level disagreement between sources.
type Conflict struct {
	Field    string `json:"field"`
	ValueA   any    `json:"value_a"`
	ValueB   any    `json:"value_b"`
	Strategy string `json:"strategy"` // "take_newer" / "take_higher_confidence" / "manual"
}

// Summarizer compresses Normalized text into a concise Summary.
type Summarizer interface {
	// Name returns the summarizer name for logging.
	Name() string

	// Summarize generates a token-efficient summary from normalized text.
	Summarize(ctx context.Context, obj *KnowledgeObject) (*KnowledgeObject, error)
}

// KnowledgePipeline orchestrates processing of KnowledgeObjects through
// Normalizer → EntityMatcher → Validator → Summarizer stages.
// It accepts KnowledgeObjects and returns processed ones with entity
// resolution (alias matching, conflict detection) applied.
type KnowledgePipeline struct {
	normalizers []Normalizer
	matchers    []EntityMatcher
	validators  []Validator
	summarizers []Summarizer

	// mu protects the resolved-objects pool, which is shared across
	// concurrent Process calls when the runtime loads from multiple
	// providers in parallel. RWMutex: readers grab the published candidate
	// snapshot in O(1) under RLock; only the (rare) pool mutation and
	// compaction take the write lock.
	mu sync.RWMutex
	// resolvedObjects is the bounded pool of fully processed objects used
	// as candidates for entity matching in subsequent calls. It is capped
	// at maxResolvedCandidates (FIFO eviction): without a cap the pool grew
	// with every object ever processed, and the per-Process snapshot of it
	// made the total cost O(n²) over the pipeline's lifetime.
	resolvedObjects map[string]*KnowledgeObject
	// resolvedOrder preserves insertion order (each ID appears at most
	// once) for FIFO eviction; orderHead is the front cursor so pops are
	// O(1) without reslicing on every eviction.
	resolvedOrder []string
	orderHead     int
	// candidates is the published, append-only candidate snapshot served to
	// readers. It is only ever appended to (safe for readers holding an
	// older slice header) or wholesale REPLACED by compactLocked; it is
	// never mutated in place, which is what makes the lock-free O(1)
	// snapshot grab race-free.
	candidates []*KnowledgeObject
	// staleEntries counts pool entries evicted or superseded that the
	// published snapshot still references; compaction runs when it exceeds
	// half the cap, bounding the snapshot overshoot at 1.5× the cap while
	// amortizing the rebuild cost.
	staleEntries int

	// candTokens caches the token bags of the LIVE pool (Phase 6 hot path):
	// matching is O(n²) pairs and re-tokenizing both sides per pair made N
	// objects cost O(n²·m) allocations. The cache is maintained
	// INCREMENTALLY under mu — recordResolved upserts one bag per mutation
	// and evictions delete one — so it stays consistent with the pool
	// without any O(pool) rebuild. Guarded by mu; bags are read-only
	// shared state. A nil cache (before the first mutation) is built
	// lazily on first read.
	candTokens map[string]map[string]int
}

// maxResolvedCandidates caps the entity-matching candidate pool. The pool is
// a "recently resolved" heuristic window, not an authoritative index —
// matchers tolerate a bounded stale tail (the pre-fix behavior was matching
// against ALL history, so any bounded window is a semantic subset).
const maxResolvedCandidates = 1024

// NewKnowledgePipeline creates a KnowledgePipeline with the given processors.
func NewKnowledgePipeline(
	normalizers []Normalizer,
	matchers []EntityMatcher,
	validators []Validator,
	summarizers []Summarizer,
) *KnowledgePipeline {
	return &KnowledgePipeline{
		normalizers:     normalizers,
		matchers:        matchers,
		validators:      validators,
		summarizers:     summarizers,
		resolvedObjects: make(map[string]*KnowledgeObject),
	}
}

// Process runs the full pipeline on a single KnowledgeObject.
func (p *KnowledgePipeline) Process(ctx context.Context, obj *KnowledgeObject) (*KnowledgeObject, error) {
	var err error

	// Early nil guard — prevent panic from nil input or nil returns from
	// upstream pipeline stages.
	if obj == nil {
		return nil, errors.New("pipeline: received nil object")
	}

	// Work on a shallow copy so concurrent Process calls never mutate the
	// caller's KnowledgeObject in place. The normalizer and summarizer write
	// Normalized/Summary, so sharing the original object across parallel
	// provider streams would be a data race. Shallow copy is sufficient
	// because the pipeline stages only read (never mutate) slice/map fields.
	cp := *obj
	obj = &cp

	// Stage 1: Normalize (Raw → Normalized).
	for _, norm := range p.normalizers {
		if obj == nil {
			return nil, fmt.Errorf("pipeline: normalizer %s returned nil object", norm.Name())
		}
		normalized, nErr := norm.Normalize(ctx, obj)
		if nErr != nil {
			log.Warn("normalizer failed (skipping)", "normalizer", norm.Name(), "error", nErr)
			continue
		}
		if normalized == nil {
			log.Warn("normalizer returned nil (skipping)", "normalizer", norm.Name())
			continue
		}
		obj = normalized
	}
	if obj == nil {
		return nil, errors.New("pipeline: all normalizers returned nil")
	}

	// Stage 2: Resolve (Normalized → Matched → Validated).
	// Accumulate resolved objects as candidates for future matching.
	if len(p.matchers) > 0 {
		// Grab the published snapshot in O(1): the slice is append-only
		// between compactions (see the candidates field comment), so the
		// header we hold here stays valid and consistent even while a
		// concurrent insert appends under the write lock. This replaces the
		// per-Process O(n) copy that made N objects cost O(n²) total.
		p.mu.RLock()
		candidates := p.candidates
		p.mu.RUnlock()

		// Tokenize-once fast path (Phase 6, hot-path allocation): matching
		// is O(n²) pairs and each pair used to re-tokenize BOTH sides —
		// N objects cost O(n²·m) tokenizations, each allocating a fresh map
		// (pprof: 82% of retriever allocations). Matchers that implement
		// MatchTokens receive pre-computed bags from a LIFECYCLE cache
		// (p.candTokens, rebuilt lazily after mutations), so a batch of N
		// Processes tokenizes each candidate roughly once instead of once
		// per pair. The legacy Match interface stays for third-party
		// matchers. Candidates are append-only shallow copies whose
		// Normalized/Summary are immutable once published (see
		// recordResolved), so a bag computed at publish time never goes
		// stale; upserts mark the cache dirty and force a rebuild that
		// re-reads the superseding copy.
		var tokenAware []TokenAwareMatcher
		for _, m := range p.matchers {
			if tm, ok := m.(TokenAwareMatcher); ok {
				tokenAware = append(tokenAware, tm)
			}
		}
		if len(tokenAware) > 0 {
			p.matchTokenAware(ctx, obj, candidates, tokenAware)
		}

		for _, matcher := range p.matchers {
			if _, ok := matcher.(TokenAwareMatcher); ok {
				continue // already handled by the fast path above
			}
			result, mErr := matcher.Match(ctx, obj, candidates)
			if mErr != nil {
				log.Warn("entity matcher failed (skipping)", "matcher", matcher.Name(), "error", mErr)
				continue
			}
			if result != nil && !result.IsNew {
				obj.Confidence = mergeConfidence(obj.Confidence, result.Confidence)
				// Run validators on merged object.
				for _, val := range p.validators {
					vResult, vErr := val.Validate(ctx, obj, candidates)
					if vErr != nil {
						log.Warn("validator failed (skipping)", "validator", val.Name(), "error", vErr)
						continue
					}
					if vResult != nil {
						obj.Confidence = vResult.Confidence
					}
				}
			}
		}
	}

	// Stage 3: Summarize (Normalized → Summary) — runs regardless of
	// whether matchers are configured.
	for _, sum := range p.summarizers {
		if obj == nil {
			return nil, fmt.Errorf("pipeline: summarizer %s received nil", sum.Name())
		}
		obj, err = sum.Summarize(ctx, obj)
		if err != nil {
			log.Warn("summarizer failed (skipping)", "summarizer", sum.Name(), "error", err)
			continue
		}
		if obj == nil {
			return nil, fmt.Errorf("pipeline: summarizer %s returned nil", sum.Name())
		}
	}

	// Record this object as a candidate for future resolution passes.
	// Must happen after Summarize so concurrent goroutines in the same
	// pipeline never read a partially-processed object via the pool.
	if len(p.matchers) > 0 {
		p.recordResolved(obj)
	}

	return obj, nil
}

// matchTokenAware runs the tokenize-once fast path for all TokenAwareMatcher
// matchers: candidate token bags come from the pipeline's lifecycle cache
// (incrementally maintained by recordResolved), so the O(n²) pair loop
// allocates nothing. The read lock is held for the WHOLE match: concurrent
// Process calls (loadAndProcess streams providers in parallel) run
// recordResolved's cache writes under the write lock, so a bag map handed
// out unlocked would race. All work under RLock is pure in-memory
// computation — no IO, no re-entrant pipeline calls — so holding it only
// serializes matching against publication, never against execution.
// Explicit unlock (not defer) for symmetry; this helper never takes the
// write lock itself.
func (p *KnowledgePipeline) matchTokenAware(ctx context.Context, obj *KnowledgeObject, candidates []*KnowledgeObject, matchers []TokenAwareMatcher) {
	objTokens := tokenizeBag(obj.Normalized + " " + obj.Summary)
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, tm := range matchers {
		result, mErr := tm.MatchTokens(ctx, obj, objTokens, candidates, p.candTokens)
		if mErr != nil {
			log.Warn("entity matcher failed (skipping)", "matcher", tm.Name(), "error", mErr)
			continue
		}
		if result != nil && !result.IsNew {
			obj.Confidence = mergeConfidence(obj.Confidence, result.Confidence)
			for _, val := range p.validators {
				vResult, vErr := val.Validate(ctx, obj, candidates)
				if vErr != nil {
					log.Warn("validator failed (skipping)", "validator", val.Name(), "error", vErr)
					continue
				}
				if vResult != nil {
					obj.Confidence = vResult.Confidence
				}
			}
		}
	}
}

// recordResolved inserts obj into the bounded candidate pool, maintaining the
// FIFO eviction order and the published candidate snapshot. Caller behavior:
// inserts are O(1) amortized — the snapshot is appended to in place and only
// rebuilt when stale (evicted/superseded) entries exceed half the cap.
func (p *KnowledgePipeline) recordResolved(obj *KnowledgeObject) {
	// Store a shallow copy: the pool publishes this pointer to concurrent
	// matchers, and the caller keeps the object Process returned — Distill-
	// Bridge-style post-processing writes Relations/Quality/Confidence on
	// the returned object, which would otherwise race the published
	// snapshot. Slice/map fields are shared by design (stages and consumers
	// treat them as read-only; the input-side copy above protects writes).
	cp := *obj
	obj = &cp
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.resolvedObjects[obj.ID]; exists {
		// Upsert: supersede the stored version. The old pointer remains in
		// the published snapshot until compaction, so matchers may briefly
		// see the older version — bounded by the compaction threshold.
		p.resolvedObjects[obj.ID] = obj
		p.candidates = append(p.candidates, obj)
		p.staleEntries++
	} else {
		p.resolvedObjects[obj.ID] = obj
		p.resolvedOrder = append(p.resolvedOrder, obj.ID)
		p.candidates = append(p.candidates, obj)
		// FIFO eviction at the cap keeps the pool (and thus the snapshot's
		// live portion) bounded.
		for len(p.resolvedObjects) > maxResolvedCandidates {
			evictID := p.resolvedOrder[p.orderHead]
			p.orderHead++
			delete(p.resolvedObjects, evictID)
			delete(p.candTokens, evictID)
			p.staleEntries++
		}
	}
	// Incremental token-cache maintenance: one bag per mutation keeps the
	// cache pool-consistent at O(m) cost — no O(pool) rebuilds on the hot
	// path. Allocated here on first use (a nil cache would otherwise make
	// every MatchTokens call fall back to per-candidate tokenization).
	if p.candTokens == nil {
		p.candTokens = make(map[string]map[string]int, maxResolvedCandidates)
	}
	p.candTokens[obj.ID] = tokenizeBag(obj.Normalized + " " + obj.Summary)

	if p.staleEntries > maxResolvedCandidates/2 {
		p.compactCandidatesLocked()
	}

	// The order slice's consumed prefix would otherwise grow without bound
	// (the backing array holds every ID ever inserted). Once the prefix
	// exceeds the window size, shift the live tail to the front — each
	// element is moved at most once per maxResolvedCandidates pops.
	if p.orderHead > maxResolvedCandidates {
		p.resolvedOrder = append(p.resolvedOrder[:0], p.resolvedOrder[p.orderHead:]...)
		p.orderHead = 0
	}
}

// compactCandidatesLocked rebuilds the published snapshot from the live pool,
// dropping evicted/superseded entries. The rebuild (not in-place filtering)
// is what keeps concurrent readers' older snapshot headers valid. Caller must
// hold the write lock.
func (p *KnowledgePipeline) compactCandidatesLocked() {
	fresh := make([]*KnowledgeObject, 0, len(p.resolvedObjects))
	for _, o := range p.resolvedObjects {
		fresh = append(fresh, o)
	}
	p.candidates = fresh
	p.staleEntries = 0
}

// ProcessStream processes a channel of KnowledgeObjects through the pipeline.
// The returned channel is closed when the input channel is closed or ctx is cancelled.
func (p *KnowledgePipeline) ProcessStream(ctx context.Context, in <-chan *KnowledgeObject) <-chan *KnowledgeObject {
	out := make(chan *KnowledgeObject, 64)
	// Use errgroup for structured concurrency so the goroutine is
	// ctx-cancelable and exits deterministically on either in-channel close
	// or ctx.Done(). The errgroup is not waited on here; callers observe
	// completion via the output channel being closed.
	g, gCtx := errgroup.WithContext(ctx)
	g.Go(func() error {
		defer close(out)
		for {
			select {
			case <-gCtx.Done():
				return nil
			case obj, ok := <-in:
				if !ok {
					return nil
				}
				if obj == nil {
					log.Warn("pipeline: skipping nil object in stream")
					continue
				}
				processed, err := p.Process(gCtx, obj)
				if err != nil {
					log.Warn("pipeline: skipping object", "id", obj.ID, "error", err)
					continue
				}
				if processed != nil {
					select {
					case out <- processed:
					case <-gCtx.Done():
						return nil
					}
				}
			}
		}
	})
	return out
}

// mergeConfidence combines two confidence scores, preferring higher values
// and boosting when both sources agree. The result is clamped to the [0,1]
// range: the previous formula could exceed 1.0 (e.g. 1.0 merged with 1.0
// yielded 1.1), violating the confidence contract and skewing downstream
// ranking and filtering.
func mergeConfidence(a, b float64) float64 {
	var v float64
	if a > b {
		v = a + (b * 0.1)
	} else {
		v = b + (a * 0.1)
	}
	if v > 1 {
		return 1
	}
	if v < 0 {
		return 0
	}
	return v
}
