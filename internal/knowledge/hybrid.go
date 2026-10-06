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
	queryDim := len(queryVec)
	dimMismatchLogged := false // log once per call, not once per object
	for _, obj := range objects {
		if obj == nil {
			continue
		}
		lex := LexicalScore(query, obj.Summary+" "+obj.Normalized)
		var vec, final float64
		if hasVec {
			if rep, ok := reps[obj.ID]; ok && rep != nil && len(rep.Vector) > 0 {
				if len(rep.Vector) != queryDim {
					// Dimension mismatch: embedding model change or mixed-model
					// representations. CosineSimilarity returns 0 silently,
					// which degrades retrieval to pure lexical — surface it
					// so the operator can act (re-embed, filter reps).
					if !dimMismatchLogged {
						slog.Warn("knowledge: vector dimension mismatch, vector score silently zeroed",
							"query_dim", queryDim,
							"rep_dim", len(rep.Vector),
							"rep_model", rep.Model,
							"object_id", obj.ID)
						dimMismatchLogged = true
					}
					final = lex
					results = append(results, ScoredObject{
						Object:       obj,
						VectorScore:  0,
						LexicalScore: lex,
						FinalScore:   final,
					})
					continue
				}
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
