package services

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	apperrors "github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
)

// TestRetrievalServiceSearch_ValidateRejectsNilAndEmptyFields pins Search
// entry validation through the full method (not just validateRequest).
func TestRetrievalServiceSearch_ValidateRejectsNilAndEmptyFields(t *testing.T) {
	svc := stubRetrievalSvc(nil, nil, nil, nil, stubGuard(t))

	_, err := svc.Search(context.Background(), nil)
	require.ErrorIs(t, err, apperrors.ErrInvalidArgument)

	_, err = svc.Search(context.Background(), &SearchRequest{})
	require.ErrorIs(t, err, apperrors.ErrInvalidArgument, "empty query must be rejected")

	_, err = svc.Search(context.Background(), &SearchRequest{Query: stubQueryLong})
	require.ErrorIs(t, err, apperrors.ErrInvalidArgument, "empty tenant must be rejected")

	// TopK defaulting: 0 → 10, cap at 100.
	req := &SearchRequest{Query: stubQueryShort, TenantID: stubTenant, TopK: 0}
	// Precision path with nil kbRepo fails loud AFTER validation — the error
	// text proves TopK validation ran first (validate passed).
	_, err = svc.Search(context.Background(), req)
	require.Error(t, err)
	require.Equal(t, 10, req.TopK, "TopK<=0 must default to 10 in validateRequest")

	reqBig := &SearchRequest{Query: stubQueryShort, TenantID: stubTenant, TopK: 500}
	_, err = svc.Search(context.Background(), reqBig)
	require.Error(t, err)
	require.Equal(t, 100, reqBig.TopK, "TopK must be capped at 100")
}

// TestRetrievalServiceSearch_RateLimitRejected pins the guard gate: a
// zero-burst limiter rejects before any repository work happens.
func TestRetrievalServiceSearch_RateLimitRejected(t *testing.T) {
	db, mock := stubSQLMock(t, true)
	guard := postgres.NewRetrievalGuard(0, 1, time.Second, time.Second)
	t.Cleanup(guard.Close)

	svc := stubRetrievalSvc(stubKBRepo(db), nil, nil, stubNewPipeline([]float64{0.1}, nil), guard)
	_, err := svc.Search(context.Background(), &SearchRequest{
		Query: stubQueryLong, TenantID: stubTenant,
		Plan: &RetrievalPlan{SearchKnowledge: true, EnableQueryRewrite: false},
	})
	require.ErrorIs(t, err, apperrors.ErrRateLimitExceeded)
	require.NoError(t, mock.ExpectationsWereMet(), "rate-limited search must not touch SQL")
}

// TestRetrievalServiceIsPrecisionMode_MatchesRetrievalContract locks the
// hybrid service's routing: it is stricter than Simple — ":" also triggers.
func TestRetrievalServiceIsPrecisionMode_MatchesRetrievalContract(t *testing.T) {
	svc := stubRetrievalSvc(nil, nil, nil, nil, stubGuard(t))
	cases := []struct {
		query string
		want  bool
	}{
		{"go error", true},
		{"key: value in config with more text", true}, // ":" trigger
		{"set timeout=30 for the request path", true},
		{"compute 3+5 for the answer now", true},
		{stubQueryLong, false},
		{"explain the C++ toolchain story", false},
		{"handle **kwargs safely in this function", false},
		{"deploy go-agent services today", false},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, svc.isPrecisionMode(tc.query), "query %q", tc.query)
	}
}

// TestRetrievalServiceSearch_PrecisionExactHitShortCircuits drives precision
// through Search: exact rows surface as knowledge/exact/1.0 and the
// keyword/vector SQL is never queried.
func TestRetrievalServiceSearch_PrecisionExactHitShortCircuits(t *testing.T) {
	db, mock := stubSQLMock(t, true)
	mock.ExpectQuery(stubReKBSubstring).WillReturnRows(stubKBSubstringRows("exact body"))

	svc := stubRetrievalSvc(stubKBRepo(db), nil, nil, nil, stubGuard(t))
	results, err := svc.Search(context.Background(), &SearchRequest{
		Query: stubQueryShort, TenantID: stubTenant, TopK: 5,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.InDelta(t, 1.0, results[0].Score, 1e-9)
	require.Equal(t, "knowledge", results[0].Source)
	require.Equal(t, "exact", results[0].SubSource)
	require.Equal(t, "exact body", results[0].Content)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestRetrievalServiceSearch_PrecisionNilKBFailsLoud pins the precision
// contract through Search: an unassembled kbRepo is a hard error, not empty
// results (contrasts with SimpleRetrievalService's per-stage degrade).
func TestRetrievalServiceSearch_PrecisionNilKBFailsLoud(t *testing.T) {
	svc := stubRetrievalSvc(nil, nil, nil, nil, stubGuard(t))
	_, err := svc.Search(context.Background(), &SearchRequest{
		Query: stubQueryShort, TenantID: stubTenant, TopK: 5,
	})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "knowledge base repository is not configured"),
		"err = %v", err)
}

// TestRetrievalServiceSearch_PrecisionExactErrorPropagates pins abort-on-
// stage-error: RetrievalService does NOT fall through to keyword on exact
// failure.
func TestRetrievalServiceSearch_PrecisionExactErrorPropagates(t *testing.T) {
	db, mock := stubSQLMock(t, true)
	exactErr := apperrors.New("stub exact down")
	mock.ExpectQuery(stubReKBSubstring).WillReturnError(exactErr)

	svc := stubRetrievalSvc(stubKBRepo(db), nil, nil, nil, stubGuard(t))
	_, err := svc.Search(context.Background(), &SearchRequest{
		Query: stubQueryShort, TenantID: stubTenant, TopK: 5,
	})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "exact match search"), "err = %v", err)
	require.ErrorIs(t, err, exactErr)
	require.NoError(t, mock.ExpectationsWereMet(),
		"keyword/vector stages must not run after exact-stage failure")
}

// TestRetrievalServiceSearch_PrecisionKeywordFallback covers stage two with
// score clamping on the hybrid service.
func TestRetrievalServiceSearch_PrecisionKeywordFallback(t *testing.T) {
	db, mock := stubSQLMock(t, true)
	mock.ExpectQuery(stubReKBSubstring).WillReturnRows(stubKBSubstringRowsEmpty())
	mock.ExpectQuery(stubReKBKeyword).WillReturnRows(stubKBKeywordRows("keyword body", 0.82))

	svc := stubRetrievalSvc(stubKBRepo(db), nil, nil, nil, stubGuard(t))
	results, err := svc.Search(context.Background(), &SearchRequest{
		Query: stubQueryShort, TenantID: stubTenant, TopK: 5,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.InDelta(t, 0.82, results[0].Score, 1e-9)
	require.Equal(t, "keyword", results[0].SubSource)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestRetrievalServiceSearch_PrecisionVectorFallbackCachesQuery covers stage
// three: pipeline-backed vector fallback + markQueryCached observability.
func TestRetrievalServiceSearch_PrecisionVectorFallbackCachesQuery(t *testing.T) {
	db, mock := stubSQLMock(t, true)
	mock.ExpectQuery(stubReKBSubstring).WillReturnRows(stubKBSubstringRowsEmpty())
	mock.ExpectQuery(stubReKBKeyword).WillReturnRows(stubKBKeywordRowsEmpty())
	mock.ExpectQuery(stubReKBVector).WillReturnRows(stubKBVectorRows("vec body", 0.77))

	svc := stubRetrievalSvc(stubKBRepo(db), nil, nil,
		stubNewPipeline([]float64{0.3, 0.4}, nil), stubGuard(t))
	results, err := svc.Search(context.Background(), &SearchRequest{
		Query: stubQueryShort, TenantID: stubTenant, TopK: 5,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.InDelta(t, 0.77, results[0].Score, 1e-9)
	require.Equal(t, "vector", results[0].SubSource)
	require.True(t, svc.isQueryInCache(stubQueryShort),
		"precision vector fallback must mark the query cached")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestRetrievalServiceSearch_PrecisionVectorErrorPropagates pins the
// vector-stage error contract.
func TestRetrievalServiceSearch_PrecisionVectorErrorPropagates(t *testing.T) {
	db, mock := stubSQLMock(t, true)
	vectorErr := apperrors.New("stub vector down")
	mock.ExpectQuery(stubReKBSubstring).WillReturnRows(stubKBSubstringRowsEmpty())
	mock.ExpectQuery(stubReKBKeyword).WillReturnRows(stubKBKeywordRowsEmpty())
	mock.ExpectQuery(stubReKBVector).WillReturnError(vectorErr)

	svc := stubRetrievalSvc(stubKBRepo(db), nil, nil,
		stubNewPipeline([]float64{0.3}, nil), stubGuard(t))
	_, err := svc.Search(context.Background(), &SearchRequest{
		Query: stubQueryShort, TenantID: stubTenant, TopK: 5,
	})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "vector search"), "err = %v", err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestRetrievalServiceSetEmbeddingPipeline_UsedByGetEmbeddingCached covers
// SetEmbeddingPipeline + the embedding cache: two identical queries embed once.
func TestRetrievalServiceSetEmbeddingPipeline_UsedByGetEmbeddingCached(t *testing.T) {
	pipe := stubNewPipeline([]float64{0.5, 0.6}, nil)
	svc := stubRetrievalSvc(nil, nil, nil, pipe, stubGuard(t))

	first := svc.getEmbeddingCached(context.Background(), "cache me once")
	require.Equal(t, []float64{0.5, 0.6}, first)
	second := svc.getEmbeddingCached(context.Background(), "cache me once")
	require.Equal(t, first, second)

	_, embeds := pipe.calls()
	require.Equal(t, int64(1), embeds, "second identical query must hit the cache, not the pipeline")
}

// TestRetrievalServiceLlmBasedRewrite_NilClientReturnsEmpty pins the nil-LLM
// short-circuit on the real rewrite path.
func TestRetrievalServiceLlmBasedRewrite_NilClientReturnsEmpty(t *testing.T) {
	svc := stubRetrievalSvc(nil, nil, nil, nil, stubGuard(t))
	rewrites, err := svc.llmBasedRewrite(context.Background(), stubQueryLong)
	require.NoError(t, err)
	require.Empty(t, rewrites)
}
