package services

import (
	"context"
	stderrors "errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	apperrors "github.com/Timwood0x10/ares/internal/errors"
	pgembed "github.com/Timwood0x10/ares/internal/storage/postgres/embedding"
)

// TestSimpleRetrievalServiceNew_DefaultConfig pins the nil-config defaults
// through the production constructor.
func TestSimpleRetrievalServiceNew_DefaultConfig(t *testing.T) {
	svc := NewSimpleRetrievalService(nil, nil, nil)
	cfg := svc.GetConfig()
	require.NotNil(t, cfg)
	require.Equal(t, 5, cfg.TopK)
	require.InDelta(t, 0.6, cfg.MinScore, 1e-9)
	require.Equal(t, "query:", cfg.QueryPrefix)
}

// TestSimpleRetrievalServiceConfigRoundTrip covers SetConfig/GetConfig.
func TestSimpleRetrievalServiceConfigRoundTrip(t *testing.T) {
	svc := NewSimpleRetrievalService(nil, nil, nil)
	want := &SimpleRetrievalConfig{TopK: 9, MinScore: 0.33, QueryPrefix: "passage:"}
	svc.SetConfig(want)
	got := svc.GetConfig()
	require.Equal(t, 9, got.TopK)
	require.InDelta(t, 0.33, got.MinScore, 1e-9)
	require.Equal(t, "passage:", got.QueryPrefix)
}

// TestSimpleRetrievalServiceIsPrecisionMode locks the simple-service routing
// contract: rune-count short-circuit, "=" trigger, digit-adjacent math
// operators; programming symbols and hyphenated words stay non-precision.
func TestSimpleRetrievalServiceIsPrecisionMode(t *testing.T) {
	svc := NewSimpleRetrievalService(nil, nil, nil)
	cases := []struct {
		query string
		want  bool
	}{
		{"go error", true},                   // 8 runes
		{"a=1 and b=2 with more text", true}, // contains "="
		{"compute 3+5 for the answer now", true},
		{"compute 10/3 for the answer now", true},
		{stubQueryLong, false}, // long prose
		{"explain the C++ toolchain story", false},
		{"handle *args and **kwargs safely here", false},
		{"deploy go-agent services today", false},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, svc.isPrecisionMode(tc.query), "query %q", tc.query)
	}
}

// TestSimpleRetrievalServiceSearch_VectorPathFiltersMinScoreAndTopK drives the
// non-precision vector path through sqlmock: similarity extraction, MinScore
// filtering and TopK truncation.
func TestSimpleRetrievalServiceSearch_VectorPathFiltersMinScoreAndTopK(t *testing.T) {
	db, mock := stubSQLMock(t, true)
	mock.ExpectQuery(stubReKBVector).
		WillReturnRows(
			stubKBVectorRows("high chunk", 0.9).
				AddRow("kb-vec-2", stubTenant, "mid chunk", stubEmbeddingText, stubModel, 1,
					"completed", "document", "docs/mid.md", stubEmptyMeta, "doc-2",
					1, "hash-mid", 0, stubNow(), stubNow(), 0.7).
				AddRow("kb-vec-3", stubTenant, "low chunk", stubEmbeddingText, stubModel, 1,
					"completed", "document", "docs/low.md", stubEmptyMeta, "doc-3",
					2, "hash-low", 0, stubNow(), stubNow(), 0.4),
		)

	svc := stubSimpleSvc(stubKBRepo(db), stubNewPipeline([]float64{0.1, 0.2}, nil),
		&SimpleRetrievalConfig{TopK: 2, MinScore: 0.5, QueryPrefix: "query:"})

	results, err := svc.Search(context.Background(), stubTenant, stubQueryLong)
	require.NoError(t, err)
	require.Len(t, results, 2, "0.4-similarity chunk must be filtered by MinScore=0.5; TopK=2 caps the rest")
	require.InDelta(t, 0.9, results[0].Score, 1e-9)
	require.InDelta(t, 0.7, results[1].Score, 1e-9)
	require.Equal(t, "high chunk", results[0].Content)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSimpleRetrievalServiceSearch_EmbedQueryErrorWraps pins the pipeline
// failure contract: wrapped as "embed query", no SQL is touched.
func TestSimpleRetrievalServiceSearch_EmbedQueryErrorWraps(t *testing.T) {
	db, mock := stubSQLMock(t, true)
	embedErr := apperrors.New("stub embed down")
	svc := stubSimpleSvc(stubKBRepo(db), stubNewPipeline(nil, embedErr), nil)

	_, err := svc.Search(context.Background(), stubTenant, stubQueryLong)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "embed query"), "err = %v", err)
	require.ErrorIs(t, err, embedErr)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSimpleRetrievalServiceSearch_VectorRepoErrorWraps pins the repository
// failure contract on the non-precision path.
func TestSimpleRetrievalServiceSearch_VectorRepoErrorWraps(t *testing.T) {
	db, mock := stubSQLMock(t, true)
	repoErr := apperrors.New("stub db unavailable")
	mock.ExpectQuery(stubReKBVector).WillReturnError(repoErr)

	svc := stubSimpleSvc(stubKBRepo(db), stubNewPipeline([]float64{0.1}, nil), nil)
	_, err := svc.Search(context.Background(), stubTenant, stubQueryLong)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "vector search"), "err = %v", err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSimpleRetrievalServiceSearch_EmbeddingClientFallback covers the
// pipeline-less path: a real EmbeddingClient against httptest serves the
// query embedding.
func TestSimpleRetrievalServiceSearch_EmbeddingClientFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/embed", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, writeErr := w.Write([]byte(`{"embedding":[0.8,0.2],"dimension":2}`))
		require.NoError(t, writeErr)
	}))
	defer srv.Close()

	db, mock := stubSQLMock(t, true)
	mock.ExpectQuery(stubReKBVector).WillReturnRows(stubKBVectorRows("client chunk", 0.85))

	client := pgembed.NewEmbeddingClient(srv.URL, stubModel, nil, 0)
	svc := NewSimpleRetrievalService(stubKBRepo(db), client,
		&SimpleRetrievalConfig{TopK: 3, MinScore: 0.1, QueryPrefix: "query:"})

	results, err := svc.Search(context.Background(), stubTenant, stubQueryLong)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.InDelta(t, 0.85, results[0].Score, 1e-9)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSimpleRetrievalServiceSearchPrecision_ExactHitShortCircuits pins the
// precision chain's first stage: exact rows are returned with Score=1.0 and
// keyword/vector SQL is never queried (proven by ExpectationsWereMet with
// only the substring expectation armed).
func TestSimpleRetrievalServiceSearchPrecision_ExactHitShortCircuits(t *testing.T) {
	db, mock := stubSQLMock(t, true)
	mock.ExpectQuery(stubReKBSubstring).WillReturnRows(stubKBSubstringRows("exact body"))

	svc := stubSimpleSvc(stubKBRepo(db), stubNewPipeline([]float64{0.1}, nil), nil)
	results, err := svc.Search(context.Background(), stubTenant, stubQueryShort)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.InDelta(t, 1.0, results[0].Score, 1e-9)
	require.Equal(t, "exact body", results[0].Content)
	require.NoError(t, mock.ExpectationsWereMet(),
		"keyword/vector stages must not run when exact match hits")
}

// TestSimpleRetrievalServiceSearchPrecision_KeywordFallbackClampsScore covers
// stage two: empty exact falls through to keyword, and keyword_score above 1.0
// is clamped by math.Min.
func TestSimpleRetrievalServiceSearchPrecision_KeywordFallbackClampsScore(t *testing.T) {
	db, mock := stubSQLMock(t, true)
	mock.ExpectQuery(stubReKBSubstring).WillReturnRows(stubKBSubstringRowsEmpty())
	mock.ExpectQuery(stubReKBKeyword).WillReturnRows(stubKBKeywordRows("keyword body", 2.0))

	svc := stubSimpleSvc(stubKBRepo(db), nil, nil)
	results, err := svc.Search(context.Background(), stubTenant, stubQueryShort)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.InDelta(t, 1.0, results[0].Score, 1e-9, "keyword_score 2.0 must clamp to 1.0")
	require.Equal(t, "keyword body", results[0].Content)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSimpleRetrievalServiceSearchPrecision_VectorFallbackAppliesMinScore
// covers stage three: empty exact+keyword fall through to vector, where the
// MinScore filter applies.
func TestSimpleRetrievalServiceSearchPrecision_VectorFallbackAppliesMinScore(t *testing.T) {
	db, mock := stubSQLMock(t, true)
	mock.ExpectQuery(stubReKBSubstring).WillReturnRows(stubKBSubstringRowsEmpty())
	mock.ExpectQuery(stubReKBKeyword).WillReturnRows(stubKBKeywordRowsEmpty())
	mock.ExpectQuery(stubReKBVector).WillReturnRows(
		stubKBVectorRows("vec keep", 0.9).
			AddRow("kb-vec-9", stubTenant, "vec drop", stubEmbeddingText, stubModel, 1,
				"completed", "document", "docs/drop.md", stubEmptyMeta, "doc-9",
				3, "hash-9", 0, stubNow(), stubNow(), 0.3),
	)

	svc := stubSimpleSvc(stubKBRepo(db), stubNewPipeline([]float64{0.4, 0.5}, nil),
		&SimpleRetrievalConfig{TopK: 5, MinScore: 0.5, QueryPrefix: "query:"})
	results, err := svc.Search(context.Background(), stubTenant, stubQueryShort)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.InDelta(t, 0.9, results[0].Score, 1e-9)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSimpleRetrievalServiceSearchPrecision_AllStagesFailedJoinsErrors pins
// the degrade contract: every stage erroring yields an errors.Join wrapped as
// "precision search: all stages failed", with all three stage errors present.
func TestSimpleRetrievalServiceSearchPrecision_AllStagesFailedJoinsErrors(t *testing.T) {
	db, mock := stubSQLMock(t, true)
	exactErr := apperrors.New("stub exact down")
	keywordErr := apperrors.New("stub keyword down")
	vectorErr := apperrors.New("stub vector down")
	mock.ExpectQuery(stubReKBSubstring).WillReturnError(exactErr)
	mock.ExpectQuery(stubReKBKeyword).WillReturnError(keywordErr)
	mock.ExpectQuery(stubReKBVector).WillReturnError(vectorErr)

	svc := stubSimpleSvc(stubKBRepo(db), stubNewPipeline([]float64{0.1}, nil), nil)
	_, err := svc.Search(context.Background(), stubTenant, stubQueryShort)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "precision search: all stages failed"), "err = %v", err)

	joined := stderrors.Unwrap(err)
	require.NotNil(t, joined, "wrapped value must be the joined stage errors")
	joinedErrs, ok := joined.(interface{ Unwrap() []error })
	require.True(t, ok, "wrapped value must support multi-unwrap, got %T", joined)
	stages := joinedErrs.Unwrap()
	require.Len(t, stages, 3)
	require.ErrorIs(t, stages[0], exactErr)
	require.ErrorIs(t, stages[1], keywordErr)
	require.ErrorIs(t, stages[2], vectorErr)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestSimpleRetrievalServiceSearch_EmbeddingNotConfigured pins the nil-nil
// embed path: no pipeline and no client yields a clear error, not a panic.
func TestSimpleRetrievalServiceSearch_EmbeddingNotConfigured(t *testing.T) {
	svc := NewSimpleRetrievalService(nil, nil, &SimpleRetrievalConfig{TopK: 1, MinScore: 0.1})
	_, err := svc.Search(context.Background(), stubTenant, stubQueryLong)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "embedding client is not configured"), "err = %v", err)
}

// TestSimpleRetrievalServiceSearchPrecision_KeywordScoreFromMetadata keeps the
// math.Min clamp honest for in-range scores as well.
func TestSimpleRetrievalServiceSearchPrecision_KeywordScoreFromMetadata(t *testing.T) {
	db, mock := stubSQLMock(t, true)
	mock.ExpectQuery(stubReKBSubstring).WillReturnRows(stubKBSubstringRowsEmpty())
	mock.ExpectQuery(stubReKBKeyword).WillReturnRows(stubKBKeywordRows("scored body", 0.42))

	svc := stubSimpleSvc(stubKBRepo(db), nil, nil)
	results, err := svc.Search(context.Background(), stubTenant, stubQueryShort)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.InDelta(t, 0.42, results[0].Score, 1e-9)
	require.InDelta(t, 0.42, math.Min(0.42, 1.0), 1e-9)
	require.NoError(t, mock.ExpectationsWereMet())
}
