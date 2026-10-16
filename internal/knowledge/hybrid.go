package knowledge

import (
	"log/slog"
	"math"
	"strings"
)

// CosineSimilarity returns the cosine similarity between two float32 vectors
// in [-1, 1]. It returns 0 when either vector is empty or has zero magnitude.
func CosineSimilarity(a, b []float32) float64 {
	n := len(a)
	if n == 0 || n != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := 0; i < n; i++ {
		af := float64(a[i])
		bf := float64(b[i])
		dot += af * bf
		na += af * af
		nb += bf * bf
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// LexicalScore returns a normalized keyword-overlap score in [0, 1] between a
// query and content, computed as the Jaccard index of lowercased token sets.
// It returns 0 when either side has no tokens.
func LexicalScore(query, content string) float64 {
	qt := tokenize(query)
	ct := tokenize(content)
	if len(qt) == 0 || len(ct) == 0 {
		return 0
	}
	intersection := 0
	for tok := range qt {
		if ct[tok] {
			intersection++
		}
	}
	if intersection == 0 {
		return 0
	}
	union := len(qt) + len(ct) - intersection
	return float64(intersection) / float64(union)
}

// tokenize splits s into a lowercase token set (map[string]bool) for the
// lexical Jaccard score. ASCII words split on whitespace as before. CJK
// characters have no whitespace boundaries, so a whole Chinese sentence used
// to collapse into ONE token — the Jaccard score degenerated to 0 or 1 and
// the lexical term became noise (hybrid retrieval degraded to pure vector).
// CJK runs are therefore emitted as character bigrams (with a unigram for a
// run of exactly one character) so a query and a document sharing phrases
// overlap without requiring full-sentence equality.
func tokenize(s string) map[string]bool {
	set := make(map[string]bool)
	for _, f := range strings.Fields(s) {
		t := strings.ToLower(f)
		if t == "" {
			continue
		}
		if hasCJK(t) {
			for _, gram := range cjkBigrams(t) {
				set[gram] = true
			}
			continue
		}
		set[t] = true
	}
	return set
}

// hasCJK reports whether s contains at least one CJK rune (the unified
// ideographs and their extensions A/B, plus kana and hangul — any script
// without whitespace word boundaries).
func hasCJK(s string) bool {
	for _, r := range s {
		if (r >= 0x2E80 && r <= 0x9FFF) || // CJK radicals through unified ideographs
			(r >= 0x3400 && r <= 0x4DBF) || // CJK extension A
			(r >= 0xF900 && r <= 0xFAFF) || // CJK compatibility ideographs
			(r >= 0x20000 && r <= 0x2FA1F) || // extensions B..F, compat supplement
			(r >= 0x3040 && r <= 0x30FF) || // hiragana / katakana
			(r >= 0xAC00 && r <= 0xD7AF) { // hangul syllables
			return true
		}
	}
	return false
}

// cjkBigrams renders s as character bigrams (and a unigram when s is a
// single character), lowercased. Non-CJK runes inside the run (ASCII words,
// digits, punctuation) participate as ordinary characters so mixed text
// keeps natural boundaries.
func cjkBigrams(s string) []string {
	runes := []rune(strings.ToLower(s))
	if len(runes) == 1 {
		return []string{string(runes)}
	}
	grams := make([]string, 0, len(runes)-1)
	for i := 0; i+1 < len(runes); i++ {
		grams = append(grams, string(runes[i:i+2]))
	}
	return grams
}

// HybridWeights are the relative weights of the hybrid scoring terms.
//
// They are RELATIVE, not absolute: the blend renormalizes them over whichever
// terms are available for a candidate, so a caller can scale them freely (0.7/0.3
// and 7/3 score identically) and adding a term does not require rebalancing the
// others by hand.
type HybridWeights struct {
	// Vector weights cosine similarity against the query vector.
	Vector float64
	// Lexical weights the query/content token overlap.
	Lexical float64
	// Recency weights the object's freshness (see FreshnessScore).
	Recency float64
}

// DefaultHybridWeights returns the shipped weights: 0.7 vector, 0.3 lexical, and
// ZERO recency.
//
// Recency is zero by default on purpose, and the reason is not that it is
// unimplemented — the term is scored and tested. It is that the store's recall
// query already reads `ORDER BY updated_at DESC LIMIT k`, so the candidate window is
// recency-biased BEFORE scoring, and a recency term in the score counts the same
// signal a second time. Whether that second helping improves ranking is a
// measurement question nobody has run for this corpus, and re-ranking every
// production retrieval on an unmeasured hunch is how a "fix" turns into a
// regression. Callers that have measured it set HybridSearchRequest.Weights.
func DefaultHybridWeights() HybridWeights {
	return HybridWeights{Vector: 0.7, Lexical: 0.3, Recency: 0}
}

// WithRecency returns the weights with the recency term set to w.
//
// It exists so a deployment can enable recency without restating the relevance
// weights: the base ratios stay defined in exactly one place, and a caller cannot
// accidentally rebalance vector against lexical while turning one term on.
func (w HybridWeights) WithRecency(recency float64) HybridWeights {
	w.Recency = recency
	return w
}

// effective reports whether the weights can score anything, and returns the
// defaults when they cannot. All-zero (or all-negative) weights would otherwise
// give every candidate a FinalScore of 0, which reads exactly like "nothing
// matched" — the caller is told instead.
func (w HybridWeights) effective() (HybridWeights, bool) {
	if w.Vector+w.Lexical+w.Recency <= 0 {
		return DefaultHybridWeights(), false
	}
	return w, true
}

// weightedTerm is one (weight, value) pair fed to the blend.
type weightedTerm struct {
	weight float64
	value  float64
}

// blend returns the weighted mean of the terms with a positive weight.
//
// Terms that are unavailable are passed with weight 0 by the caller, so the
// remaining weights carry the whole score: a candidate with no vector is not
// silently damped toward zero, it is scored on what could be measured. The result
// stays in the range of the values given (all terms are in [0,1] except cosine,
// which can be negative).
func (w HybridWeights) blend(terms ...weightedTerm) float64 {
	total, weightSum := 0.0, 0.0
	for _, t := range terms {
		if t.weight <= 0 {
			continue
		}
		total += t.weight * t.value
		weightSum += t.weight
	}
	if weightSum == 0 {
		return 0
	}
	return total / weightSum
}

// ScoreHybrid scores each object by vector cosine similarity (vs queryVec, using the
// representation provided for that object in reps), lexical overlap (vs query) and
// recency, with DefaultHybridWeights. It does NOT filter or sort — callers sort and
// filter by FinalScore.
//
// reps is keyed by object ID; only reps whose Model matches are meaningful, so
// callers should pre-filter reps to the requested model.
func ScoreHybrid(objects []*KnowledgeObject, reps map[string]*Representation, queryVec []float32, query string) []ScoredObject {
	return ScoreHybridWithWeights(objects, reps, queryVec, query, DefaultHybridWeights())
}

// ScoreHybridWithWeights is ScoreHybrid with explicit weights.
//
// When queryVec is nil or no representation exists for an object, the vector term is
// omitted and the score is the blend of what remains — today, lexical alone, which is
// what this function has always returned in that case, with recency joining it only
// when a caller weights it.
func ScoreHybridWithWeights(objects []*KnowledgeObject, reps map[string]*Representation, queryVec []float32, query string, weights HybridWeights) []ScoredObject {
	w, configured := weights.effective()
	if !configured {
		slog.Warn("knowledge: hybrid weights are all zero, using the defaults",
			"vector", weights.Vector, "lexical", weights.Lexical, "recency", weights.Recency)
	}
	scorer := &hybridScorer{reps: reps, queryVec: queryVec, query: query, weights: w, hasVec: len(queryVec) > 0}
	results := make([]ScoredObject, 0, len(objects))
	for _, obj := range objects {
		if obj == nil {
			continue
		}
		results = append(results, scorer.score(obj))
	}
	return results
}

// hybridScorer carries one scoring pass's inputs, so the per-candidate helper takes a
// single argument instead of threading six values (and a mutable warn-once flag)
// through every call.
type hybridScorer struct {
	reps     map[string]*Representation
	queryVec []float32
	query    string
	weights  HybridWeights
	hasVec   bool
	// dimWarned is set by the first dimension mismatch in this pass: the warning is
	// per call, not per object, or one mismatched corpus would flood the log.
	dimWarned bool
}

// score folds in whatever terms are available for one candidate.
//
// The availability rules (is there a query vector? is there a representation? same
// dimension?) are the part that is easy to get wrong, so each case returns early
// instead of nesting.
func (s *hybridScorer) score(obj *KnowledgeObject) ScoredObject {
	lex := LexicalScore(s.query, obj.Summary+" "+obj.Normalized)
	rec := FreshnessScore(obj.EffectiveObservedAt())

	// Without a usable vector the score is the blend of the remaining terms, which keeps
	// a not-yet-embedded object rankable instead of scoring it 0 on every retrieval (the
	// pre-existing behaviour this must preserve).
	if !s.hasVec {
		return s.withoutVector(obj, lex, rec)
	}
	rep, ok := s.reps[obj.ID]
	if !ok || rep == nil || len(rep.Vector) == 0 {
		return s.withoutVector(obj, lex, rec)
	}
	if len(rep.Vector) != len(s.queryVec) {
		// Dimension mismatch: embedding model change or mixed-model representations.
		// CosineSimilarity returns 0 silently, which degrades retrieval to pure lexical —
		// surface it so the operator can act (re-embed, filter reps).
		if !s.dimWarned {
			slog.Warn("knowledge: vector dimension mismatch, vector score silently zeroed",
				"query_dim", len(s.queryVec),
				"rep_dim", len(rep.Vector),
				"rep_model", rep.Model,
				"object_id", obj.ID)
			s.dimWarned = true
		}
		return s.withoutVector(obj, lex, rec)
	}
	vec := CosineSimilarity(s.queryVec, rep.Vector)
	return ScoredObject{Object: obj, VectorScore: vec, LexicalScore: lex, RecencyScore: rec,
		FinalScore: s.weights.blend(
			weightedTerm{s.weights.Vector, vec},
			weightedTerm{s.weights.Lexical, lex},
			weightedTerm{s.weights.Recency, rec})}
}

// withoutVector scores a candidate the vector term cannot speak for.
func (s *hybridScorer) withoutVector(obj *KnowledgeObject, lex, rec float64) ScoredObject {
	return ScoredObject{Object: obj, LexicalScore: lex, RecencyScore: rec,
		FinalScore: s.weights.blend(weightedTerm{s.weights.Lexical, lex}, weightedTerm{s.weights.Recency, rec})}
}

// The recency term scores EffectiveObservedAt — the repo's single definition of "how
// old is this object" (ObservedAt, else UpdatedAt, else CreatedAt) — rather than a
// second fallback chain. It is NOT the same field the store's recall window orders by
// (that is raw updated_at), so with a non-zero recency weight the window's bias and
// the score's differ slightly; that is noted rather than papered over, because
// "which timestamp is recency" is exactly the kind of question two implementations
// answer two ways.
