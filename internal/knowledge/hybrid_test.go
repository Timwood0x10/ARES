package knowledge

import (
	"math"
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
}
