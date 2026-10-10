package knowledge

import (
	"context"
	"log/slog"
	"math"
	"strings"
	"sync"
	"testing"
)

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name string
		a    []float32
		b    []float32
		want float64
	}{
		{
			name: "identical_vectors_one",
			a:    []float32{1, 0, 0},
			b:    []float32{1, 0, 0},
			want: 1,
		},
		{
			name: "orthogonal_vectors_zero",
			a:    []float32{1, 0},
			b:    []float32{0, 1},
			want: 0,
		},
		{
			name: "opposite_vectors_negative_one",
			a:    []float32{1, 0},
			b:    []float32{-1, 0},
			want: -1,
		},
		{
			name: "empty_first_vector_zero",
			a:    []float32{},
			b:    []float32{1, 2},
			want: 0,
		},
		{
			name: "mismatched_lengths_zero",
			a:    []float32{1, 2, 3},
			b:    []float32{1, 2},
			want: 0,
		},
		{
			name: "zero_magnitude_vector_zero",
			a:    []float32{0, 0, 0},
			b:    []float32{1, 2, 3},
			want: 0,
		},
		{
			name: "parallel_normalized_one",
			a:    []float32{2, 0, 0},
			b:    []float32{5, 0, 0},
			want: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CosineSimilarity(tc.a, tc.b)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("CosineSimilarity(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestLexicalScore(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		content string
		want    float64
	}{
		{
			name:    "full_overlap_one",
			query:   "redis caching",
			content: "redis caching",
			want:    1,
		},
		{
			name:    "no_overlap_zero",
			query:   "redis caching",
			content: "postgres persistence",
			want:    0,
		},
		{
			name:    "partial_overlap_jaccard",
			query:   "redis caching layer",
			content: "redis persistence",
			want:    1.0 / 4.0, // intersection=1 (redis), union=4 (redis,caching,layer,persistence)
		},
		{
			name:    "empty_query_zero",
			query:   "",
			content: "redis caching",
			want:    0,
		},
		{
			name:    "empty_content_zero",
			query:   "redis caching",
			content: "",
			want:    0,
		},
		{
			name:    "case_insensitive_overlap",
			query:   "Redis CACHING",
			content: "redis caching layer",
			want:    2.0 / 3.0, // intersection=2 (redis,caching), union=3
		},
		{
			// B1: before CJK bigrams a whole Chinese sentence was ONE token, so
			// a query sharing a phrase scored 0 and the lexical term was noise.
			// Now: query {缓存,存策,策略} (3), content 13 runes → 12 bigrams,
			// intersection 3 → 3/12.
			name:    "chinese_shared_phrase_overlaps",
			query:   "缓存策略",
			content: "我们决定使用缓存策略来加速",
			want:    3.0 / 12.0,
		},
		{
			name:    "chinese_unrelated_zero",
			query:   "缓存策略",
			content: "完全无关的内容",
			want:    0,
		},
		{
			name:    "chinese_single_rune_exact_match",
			query:   "缓",
			content: "缓",
			want:    1, // one-rune runs fall back to a unigram
		},
		{
			name:    "mixed_ascii_and_chinese_fields",
			query:   "redis 缓存",
			content: "redis 缓存策略",
			want:    2.0 / 4.0, // {redis,缓存} ∩ {redis,缓存,存策,策略}
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := LexicalScore(tc.query, tc.content)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("LexicalScore(%q, %q) = %v, want %v", tc.query, tc.content, got, tc.want)
			}
		})
	}
}

// TestTokenize_CJKRunsBecomeBigrams locks the B1 tokenizer contract: a CJK
// whitespace-delimited run becomes character bigrams (a unigram when the run is
// a single rune), while non-CJK runes inside such a run stay ordinary
// characters so mixed text keeps natural boundaries. This is the mechanism that
// keeps Chinese lexical overlap from degenerating to 0/1.
func TestTokenize_CJKRunsBecomeBigrams(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "chinese_run_becomes_bigrams", in: "缓存策略", want: []string{"缓存", "存策", "策略"}},
		{name: "two_runes_one_bigram", in: "缓存", want: []string{"缓存"}},
		{name: "single_rune_unigram", in: "缓", want: []string{"缓"}},
		{name: "ascii_words_unchanged", in: "Redis Caching", want: []string{"redis", "caching"}},
		{name: "mixed_run_keeps_ascii_chars", in: "测试abc", want: []string{"测试", "试a", "ab", "bc"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tokenize(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("tokenize(%q) = %v, want exactly %v", tc.in, got, tc.want)
			}
			for _, w := range tc.want {
				if !got[w] {
					t.Errorf("tokenize(%q) missing token %q (got %v)", tc.in, w, got)
				}
			}
		})
	}
}

func TestScoreHybrid(t *testing.T) {
	objs := []*KnowledgeObject{
		{ID: "a", Summary: "redis caching", Normalized: "redis is used for caching"},
		{ID: "b", Summary: "postgres persistence", Normalized: "postgres is used for persistence"},
	}
	queryVec := []float32{1, 0, 0}
	reps := map[string]*Representation{
		"a": {ID: "ra", ObjectID: "a", Model: "m", Vector: []float32{1, 0, 0}}, // identical → vec=1
		"b": {ID: "rb", ObjectID: "b", Model: "m", Vector: []float32{0, 1, 0}}, // orthogonal → vec=0
	}

	t.Run("with_vector", func(t *testing.T) {
		results := ScoreHybrid(objs, reps, queryVec, "redis caching")
		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
		// Find result for object "a" (has vector + lexical overlap).
		var resA, resB ScoredObject
		for _, r := range results {
			switch r.Object.ID {
			case "a":
				resA = r
			case "b":
				resB = r
			}
		}
		if resA.Object == nil {
			t.Fatal("missing result for object a")
		}
		// Vector identical → vec=1. Lexical: query {redis,caching} vs content
		// {redis,caching,is,used,for} → intersection 2, union 5 → lex=0.4.
		// final = 0.7*1 + 0.3*0.4 = 0.82.
		if math.Abs(resA.VectorScore-1) > 1e-9 {
			t.Errorf("object a VectorScore = %v, want 1", resA.VectorScore)
		}
		if math.Abs(resA.LexicalScore-0.4) > 1e-9 {
			t.Errorf("object a LexicalScore = %v, want 0.4", resA.LexicalScore)
		}
		wantFinalA := 0.7*1 + 0.3*0.4
		if math.Abs(resA.FinalScore-wantFinalA) > 1e-9 {
			t.Errorf("object a FinalScore = %v, want %v", resA.FinalScore, wantFinalA)
		}
		// Object b: orthogonal vector (vec=0), no lexical overlap → final = lex = 0.
		if resB.Object == nil {
			t.Fatal("missing result for object b")
		}
		if math.Abs(resB.VectorScore) > 1e-9 {
			t.Errorf("object b VectorScore = %v, want 0", resB.VectorScore)
		}
		if math.Abs(resB.FinalScore) > 1e-9 {
			t.Errorf("object b FinalScore = %v, want 0", resB.FinalScore)
		}
	})

	t.Run("without_vector_lexical_only", func(t *testing.T) {
		results := ScoreHybrid(objs, reps, nil, "redis caching")
		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
		for _, r := range results {
			// Without a query vector, VectorScore must be 0 and FinalScore == LexicalScore.
			if math.Abs(r.VectorScore) > 1e-9 {
				t.Errorf("object %s VectorScore = %v, want 0 (no query vector)", r.Object.ID, r.VectorScore)
			}
			if math.Abs(r.FinalScore-r.LexicalScore) > 1e-9 {
				t.Errorf("object %s FinalScore = %v, want %v (lexical only)", r.Object.ID, r.FinalScore, r.LexicalScore)
			}
		}
	})

	t.Run("missing_representation_lexical_only", func(t *testing.T) {
		// Provide a query vector but no reps; each object should fall back to lexical.
		results := ScoreHybrid(objs, nil, queryVec, "redis caching")
		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
		for _, r := range results {
			if math.Abs(r.VectorScore) > 1e-9 {
				t.Errorf("object %s VectorScore = %v, want 0 (no rep)", r.Object.ID, r.VectorScore)
			}
			if math.Abs(r.FinalScore-r.LexicalScore) > 1e-9 {
				t.Errorf("object %s FinalScore = %v, want %v (lexical fallback)", r.Object.ID, r.FinalScore, r.LexicalScore)
			}
		}
	})

	t.Run("dimension_mismatch_vector_score_zeroed", func(t *testing.T) {
		// B2: a rep whose vector dimension differs from the query vector must
		// score 0 on the vector path and fall back to lexical — AND the
		// degradation must be observable (Warn carrying both dimensions), not
		// a silent zero from CosineSimilarity. The capture below is what makes
		// the second half an assertion rather than a comment.
		logs := captureDefaultLog(t)

		mismatchReps := map[string]*Representation{
			"a": {ID: "ra", ObjectID: "a", Model: "mismatch", Vector: []float32{1, 0, 0, 0}}, // dim 4 vs query dim 3
		}
		results := ScoreHybrid(objs, mismatchReps, queryVec, "redis caching")
		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
		for _, r := range results {
			if math.Abs(r.VectorScore) > 1e-9 {
				t.Errorf("object %s VectorScore = %v, want 0 (dimension mismatch)", r.Object.ID, r.VectorScore)
			}
			if math.Abs(r.FinalScore-r.LexicalScore) > 1e-9 {
				t.Errorf("object %s FinalScore = %v, want %v (lexical fallback)",
					r.Object.ID, r.FinalScore, r.LexicalScore)
			}
		}

		rec, ok := logs.findWarn("dimension mismatch")
		if !ok {
			t.Fatal("dimension mismatch must be logged at WARN (B2), not silently zeroed")
		}
		if got := attrInt(rec, "query_dim"); got != 3 {
			t.Errorf("warn query_dim = %d, want 3", got)
		}
		if got := attrInt(rec, "rep_dim"); got != 4 {
			t.Errorf("warn rep_dim = %d, want 4", got)
		}
	})
}

// capturedLog records every slog entry emitted through the default logger so a
// test can assert on the OBSERVABLE signal of a degradation path (B2: a
// dimension mismatch must warn). Guarded by a mutex because the default logger
// is process-wide and sibling tests may log concurrently.
type capturedLog struct {
	mu      sync.Mutex
	records []slog.Record
}

// Enabled reports that every level is captured.
func (c *capturedLog) Enabled(context.Context, slog.Level) bool { return true }

// Handle appends a copy of the record (the record is only valid during the call).
func (c *capturedLog) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, r.Clone())
	return nil
}

// WithAttrs returns the handler unchanged; attribute grouping is irrelevant here.
func (c *capturedLog) WithAttrs([]slog.Attr) slog.Handler { return c }

// WithGroup returns the handler unchanged; group nesting is irrelevant here.
func (c *capturedLog) WithGroup(string) slog.Handler { return c }

// findWarn returns the first WARN record whose message contains substr.
func (c *capturedLog) findWarn(substr string) (slog.Record, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.records {
		if r.Level == slog.LevelWarn && strings.Contains(r.Message, substr) {
			return r, true
		}
	}
	return slog.Record{}, false
}

// captureDefaultLog installs a capturing handler as the default slog logger and
// restores the previous logger when the test ends.
func captureDefaultLog(t *testing.T) *capturedLog {
	t.Helper()
	c := &capturedLog{}
	prev := slog.Default()
	slog.SetDefault(slog.New(c))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return c
}

// attrInt returns the int64 value of the named attribute, or -1 when the
// attribute is absent or not an integer (avoids the slog.Value panics).
func attrInt(r slog.Record, key string) int {
	got := -1
	r.Attrs(func(a slog.Attr) bool {
		if a.Key != key {
			return true
		}
		if a.Value.Kind() == slog.KindInt64 {
			got = int(a.Value.Int64())
		}
		return false
	})
	return got
}
