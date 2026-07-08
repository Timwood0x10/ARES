package knowledge

import (
	"context"
	"fmt"
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

	// resolvedObjects accumulates objects that have been fully processed,
	// used as candidates for entity matching in subsequent calls.
	resolvedObjects map[string]*KnowledgeObject
}

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
		return nil, fmt.Errorf("pipeline: received nil object")
	}

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
		return nil, fmt.Errorf("pipeline: all normalizers returned nil")
	}

	// Stage 2: Resolve (Normalized → Matched → Validated).
	// Accumulate resolved objects as candidates for future matching.
	if len(p.matchers) > 0 {
		candidates := make([]*KnowledgeObject, 0, len(p.resolvedObjects))
		for _, o := range p.resolvedObjects {
			candidates = append(candidates, o)
		}

		for _, matcher := range p.matchers {
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

		// Add to resolved candidates for future calls.
		p.resolvedObjects[obj.ID] = obj
	}

	// Stage 3: Summarize (Normalized → Summary).
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

	return obj, nil
}

// ProcessStream processes a channel of KnowledgeObjects through the pipeline.
func (p *KnowledgePipeline) ProcessStream(ctx context.Context, in <-chan *KnowledgeObject) <-chan *KnowledgeObject {
	out := make(chan *KnowledgeObject, 64)
	go func() {
		defer close(out)
		for obj := range in {
			if obj == nil {
				log.Warn("pipeline: skipping nil object in stream")
				continue
			}
			processed, err := p.Process(ctx, obj)
			if err != nil {
				log.Warn("pipeline: skipping object", "id", obj.ID, "error", err)
				continue
			}
			if processed != nil {
				out <- processed
			}
		}
	}()
	return out
}

// mergeConfidence combines two confidence scores, preferring higher values
// and boosting when both sources agree.
func mergeConfidence(a, b float64) float64 {
	if a > b {
		return a + (b * 0.1)
	}
	return b + (a * 0.1)
}
