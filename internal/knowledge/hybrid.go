package knowledge

import (
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

// tokenize splits s on whitespace and lowercases each token, returning a set
// (map[string]bool) of the non-empty tokens.
func tokenize(s string) map[string]bool {
	set := make(map[string]bool)
	for _, f := range strings.Fields(s) {
		if t := strings.ToLower(f); t != "" {
			set[t] = true
		}
	}
	return set
}

// ScoreHybrid scores each object by vector cosine similarity (vs queryVec,
// using the representation provided for that object in reps) and lexical
// overlap (vs query). It does NOT filter or sort — callers sort and filter by
// FinalScore. When queryVec is nil or no representation exists for an object,
// its VectorScore is 0. FinalScore = 0.7*VectorScore + 0.3*LexicalScore when a
// vector is available, otherwise FinalScore = LexicalScore.
//
// reps is keyed by object ID; only reps whose Model matches are meaningful, so
// callers should pre-filter reps to the requested model.
func ScoreHybrid(objects []*KnowledgeObject, reps map[string]*Representation, queryVec []float32, query string) []ScoredObject {
	results := make([]ScoredObject, 0, len(objects))
	hasVec := len(queryVec) > 0
	for _, obj := range objects {
		if obj == nil {
			continue
		}
		lex := LexicalScore(query, obj.Summary+" "+obj.Normalized)
		var vec, final float64
		if hasVec {
			if rep, ok := reps[obj.ID]; ok && rep != nil && len(rep.Vector) > 0 {
				vec = CosineSimilarity(queryVec, rep.Vector)
				final = 0.7*vec + 0.3*lex
			} else {
				final = lex
			}
		} else {
			final = lex
		}
		results = append(results, ScoredObject{
			Object:       obj,
			VectorScore:  vec,
			LexicalScore: lex,
			FinalScore:   final,
		})
	}
	return results
}
