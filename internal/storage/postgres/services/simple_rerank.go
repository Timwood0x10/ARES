package services

import (
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
)

// SimpleRerankWeights are the weights of the rerank terms, relative to each
// other (they are normalized per candidate, so their absolute scale does not
// matter).
type SimpleRerankWeights struct {
	// Similarity weights the vector similarity the candidate was recalled with.
	Similarity float64
	// Lexical weights how much of the query's vocabulary the candidate contains.
	Lexical float64
	// Recency weights the candidate's freshness. It applies only when the
	// producer reported a timestamp (see rerankScore).
	Recency float64
}

// DefaultSimpleRerankWeights returns the shipped weights.
//
// Similarity leads because it is still the best single signal; lexical overlap
// breaks its ties toward text that actually mentions what was asked (this is the
// term that demotes a "假近" neighbour); recency gets a light touch so a stale
// document does not outrank a fresh one on an equal match, without letting a new
// but irrelevant row climb.
func DefaultSimpleRerankWeights() SimpleRerankWeights {
	return SimpleRerankWeights{Similarity: 1.0, Lexical: 0.5, Recency: 0.2}
}

// defaultRerankHalfLife is the age at which the recency term halves.
const defaultRerankHalfLife = 30 * 24 * time.Hour

// rerankCandidate is one recalled row as the reranker sees it.
type rerankCandidate struct {
	// Content is the chunk text, used for the lexical term.
	Content string
	// Source is the chunk's origin, carried through for the caller.
	Source string
	// Similarity is the recall score (cosine similarity, 1 = identical).
	Similarity float64
	// Age is how long ago the row was written, or 0 when the producer did not
	// report a timestamp. 0 means UNKNOWN, not "just now" — see rerankScore.
	Age time.Duration
}

// rerankScore combines the available terms into one score in [0,1].
//
// Terms whose evidence is absent are dropped and the remaining weights are
// renormalized, so a candidate is never penalized for what the producer did not
// send. Recency is the case that matters: defaulting an unknown timestamp to
// "old" would silently bury every row whose metadata lacks a date, and there is
// no way to tell that from the scores.
func rerankScore(query string, c rerankCandidate, weights SimpleRerankWeights, halfLife time.Duration) float64 {
	if halfLife <= 0 {
		halfLife = defaultRerankHalfLife
	}
	total := 0.0
	weightSum := 0.0
	if weights.Similarity > 0 {
		total += weights.Similarity * clampUnit(c.Similarity)
		weightSum += weights.Similarity
	}
	if weights.Lexical > 0 {
		total += weights.Lexical * lexicalOverlap(query, c.Content)
		weightSum += weights.Lexical
	}
	if weights.Recency > 0 && c.Age > 0 {
		total += weights.Recency * recencyFactor(c.Age, halfLife)
		weightSum += weights.Recency
	}
	if weightSum == 0 {
		// Every term disabled or unavailable: fall back to the recall score
		// rather than returning a constant, which would make the order arbitrary.
		return clampUnit(c.Similarity)
	}
	return total / weightSum
}

// rerankCandidates orders candidates by rerankScore (descending) and returns the
// top topK.
//
// The input is the WIDE recall set, and this is the step that makes it worth
// fetching: without it the caller takes the first topK in recall order, which is
// exactly the cosine order — the same rows a LIMIT topK would have returned, so
// the extra candidates cost a query and change nothing (defect R2).
//
// Ties keep their recall order (sort.SliceStable), so the result is deterministic
// for a given candidate set.
func rerankCandidates(query string, candidates []rerankCandidate, topK int, weights SimpleRerankWeights, halfLife time.Duration) []rerankCandidate {
	if topK <= 0 || len(candidates) == 0 {
		return nil
	}
	scored := make([]rerankCandidate, len(candidates))
	copy(scored, candidates)
	scores := make(map[int]float64, len(scored))
	for i := range scored {
		scores[i] = rerankScore(query, scored[i], weights, halfLife)
	}
	order := make([]int, len(scored))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return scores[order[a]] > scores[order[b]]
	})
	if len(order) > topK {
		order = order[:topK]
	}
	out := make([]rerankCandidate, 0, len(order))
	for _, idx := range order {
		out = append(out, scored[idx])
	}
	return out
}

// lexicalOverlap is the share of the query's content words that appear in the
// candidate text, in [0,1].
//
// Coverage rather than Jaccard on purpose: a long chunk that mentions everything
// asked for should score 1 even though the query's words are a tiny fraction of
// its own vocabulary. A shorter chunk that mentions the same words scores the
// same — length is the reranker's job to weigh, not this term's.
func lexicalOverlap(query, content string) float64 {
	queryTerms := contentTerms(query)
	if len(queryTerms) == 0 {
		return 0
	}
	contentSet := make(map[string]struct{}, len(queryTerms)*4)
	for term := range contentTerms(content) {
		contentSet[term] = struct{}{}
	}
	found := 0
	for term := range queryTerms {
		if _, ok := contentSet[term]; ok {
			found++
		}
	}
	return float64(found) / float64(len(queryTerms))
}

// minTermRunes drops one- and two-character tokens from space-separated scripts:
// they are overwhelmingly punctuation-bound leftovers and stopwords, and counting
// them makes every candidate look partially relevant.
const minTermRunes = 3

// contentTerms splits text into the distinct content words used by the lexical
// term: case-folded, and per script — space-separated words for latin scripts, and
// adjacent-character bigrams for scripts that do not separate words at all.
//
// The CJK branch is not a nicety. Chinese text has no spaces, so a whole clause
// arrives as ONE alphanumeric run: kept whole it matches only an identical clause,
// and the lexical term would contribute nothing exactly where this repository needs
// it most (the shipped judge prompts and the evaluation corpus are Chinese). Bigrams
// are the standard cheap stand-in for a word segmenter: no dictionary, no
// dependency, and they do recover partial overlap.
func contentTerms(text string) map[string]struct{} {
	terms := make(map[string]struct{})
	var latin strings.Builder
	var cjk []rune

	flushLatin := func() {
		if len([]rune(latin.String())) >= minTermRunes {
			terms[latin.String()] = struct{}{}
		}
		latin.Reset()
	}
	flushCJK := func() {
		switch len(cjk) {
		case 0:
			// nothing accumulated
		case 1:
			// A lone character is still a term: a single-character query word is a
			// real question in these scripts.
			terms[string(cjk)] = struct{}{}
		default:
			for i := 0; i+1 < len(cjk); i++ {
				terms[string(cjk[i:i+2])] = struct{}{}
			}
		}
		cjk = cjk[:0]
	}

	for _, r := range strings.ToLower(text) {
		switch {
		case isCJK(r):
			flushLatin()
			cjk = append(cjk, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			flushCJK()
			latin.WriteRune(r)
		default:
			// Punctuation and whitespace separate both kinds of run.
			flushLatin()
			flushCJK()
		}
	}
	flushLatin()
	flushCJK()
	return terms
}

// isCJK reports whether r belongs to a script written without spaces between
// words, where the "word" unit has to be reconstructed rather than split.
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

// recencyFactor decays an age into [0,1] with the given half-life: an age of
// halfLife scores 0.5, twice that 0.25, and so on.
func recencyFactor(age, halfLife time.Duration) float64 {
	if age <= 0 {
		return 1
	}
	if halfLife <= 0 {
		halfLife = defaultRerankHalfLife
	}
	return clampUnit(math.Pow(0.5, float64(age)/float64(halfLife)))
}

// chunkAge reads how long ago a chunk was written, from its metadata, or 0 when
// no usable timestamp is present.
//
// The keys are the ones the repositories select alongside the content; the first
// one that parses wins. An absent or unparsable value reports UNKNOWN (0) rather
// than "old", because the recency term drops unknown evidence instead of
// guessing — a guess would silently bury every row whose metadata lacks a date,
// with nothing in the scores to show it happened.
func chunkAge(metadata map[string]any, now time.Time) time.Duration {
	for _, key := range []string{"created_at", "updated_at"} {
		raw, ok := metadata[key]
		if !ok {
			continue
		}
		ts, ok := parseMetadataTime(raw)
		if !ok {
			continue
		}
		age := now.Sub(ts)
		if age < 0 {
			// A future timestamp is a clock skew, not a fresh document: treat it
			// as unknown rather than max-freshness.
			return 0
		}
		return age
	}
	return 0
}

// parseMetadataTime accepts the shapes a driver or JSON round trip can produce
// for a timestamp column.
func parseMetadataTime(raw any) (time.Time, bool) {
	switch v := raw.(type) {
	case time.Time:
		return v, !v.IsZero()
	case string:
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05.999999", "2006-01-02 15:04:05"} {
			if ts, err := time.Parse(layout, v); err == nil {
				return ts, true
			}
		}
	}
	return time.Time{}, false
}

// clampUnit bounds a score into [0,1]. Cosine similarity from pgvector is
// already in that range for the operator the search uses, but a clamped value in
// the scorer means a corrupt one cannot dominate the ranking.
func clampUnit(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
