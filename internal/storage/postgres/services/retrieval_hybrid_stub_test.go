package services

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	memembed "github.com/Timwood0x10/ares/internal/runtime/memory/embedding"
	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
)

// stubKnowledgeOnlyPlan returns a plan that exercises the hybrid path with a
// single SQL source pair (kb vector + kb keyword), rewrite disabled so
// buildQueries yields one weighted query.
func stubKnowledgeOnlyPlan(topK int, minScore float64) *SearchRequest {
	return &SearchRequest{
		Query:       stubQueryLong,
		TenantID:    stubTenant,
		TopK:        topK,
		MinScore:    minScore,
		EnableTrace: true,
		Plan: &RetrievalPlan{
			SearchKnowledge:     true,
			SearchExperience:    false,
			SearchTools:         false,
			SearchTaskResults:   false,
			KnowledgeWeight:     0.4,
			EnableQueryRewrite:  false,
			EnableKeywordSearch: true,
			EnableTimeDecay:     false,
			TopK:                topK,
		},
	}
}

// TestRetrievalServiceSearch_HybridKnowledgeOnlyMergesTraces drives the full
// hybrid Search: unordered sqlmock absorbs the parallel vector∥keyword pair
// inside searchSingleQuery; rewrite disabled keeps exactly one query so each
// SQL pattern fires at most once.
func TestRetrievalServiceSearch_HybridKnowledgeOnlyMergesTraces(t *testing.T) {
	db, mock := stubSQLMock(t, false)
	mock.ExpectQuery(stubReKBVector).WillReturnRows(stubKBVectorRows("vec hit", 0.9))
	mock.ExpectQuery(stubReKBKeyword).WillReturnRows(stubKBKeywordRows("kw hit", 0.6))

	svc := stubHybridSvc(stubKBRepo(db), stubNewPipeline([]float64{0.2, 0.3}, nil), stubGuard(t))
	req := stubKnowledgeOnlyPlan(5, 0.01)

	results, err := svc.Search(context.Background(), req)
	require.NoError(t, err)
	require.NotEmpty(t, results)

	require.NotNil(t, req.Trace)
	require.Equal(t, len(results), req.Trace.FinalResults)
	require.False(t, req.Trace.RewriteUsed, "rewrite disabled ⇒ single weighted query")
	require.NotZero(t, req.Trace.SearchBreakdown["knowledge"],
		"trace breakdown must count knowledge results")
	for _, r := range results {
		require.Equal(t, stubQueryLong, r.Query)
		require.InDelta(t, 1.0, r.QueryWeight, 1e-9, "original query weight is 1.0")
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestRetrievalServiceSearch_HybridTopKAndMinScoreApplied pins the post-merge
// caps on the hybrid path.
func TestRetrievalServiceSearch_HybridTopKAndMinScoreApplied(t *testing.T) {
	db, mock := stubSQLMock(t, false)
	mock.ExpectQuery(stubReKBVector).WillReturnRows(
		stubKBVectorRows("high", 0.95).
			AddRow("kb-vec-2", stubTenant, "mid", stubEmbeddingText, stubModel, 1,
				"completed", "document", "docs/mid.md", stubEmptyMeta, "doc-2",
				1, "h2", 0, stubNow(), stubNow(), 0.75).
			AddRow("kb-vec-3", stubTenant, "low", stubEmbeddingText, stubModel, 1,
				"completed", "document", "docs/low.md", stubEmptyMeta, "doc-3",
				2, "h3", 0, stubNow(), stubNow(), 0.2),
	)
	mock.ExpectQuery(stubReKBKeyword).WillReturnRows(stubKBKeywordRows("kw low", 0.1))

	svc := stubHybridSvc(stubKBRepo(db), stubNewPipeline([]float64{0.2}, nil), stubGuard(t))
	req := stubKnowledgeOnlyPlan(2, 0.4)

	results, err := svc.Search(context.Background(), req)
	require.NoError(t, err)
	require.LessOrEqual(t, len(results), 2, "TopK=2 must cap the merged set")
	for _, r := range results {
		require.GreaterOrEqual(t, r.Score, 0.4, "MinScore=0.4 must filter the merged set")
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestRetrievalServiceSearchAllVectorSources_PlanGatesAndNilRepos covers the
// vector fan-out gates directly (package-private, serial — ordered mock).
func TestRetrievalServiceSearchAllVectorSources_PlanGatesAndNilRepos(t *testing.T) {
	db, mock := stubSQLMock(t, true)
	svc := stubRetrievalSvc(stubKBRepo(db), nil, nil, stubNewPipeline(nil, nil), stubGuard(t))

	// All flags off: zero results, zero SQL.
	offReq := &SearchRequest{TenantID: stubTenant,
		Plan: &RetrievalPlan{TopK: 5}}
	got := svc.searchAllVectorSources(context.Background(), []float64{0.1}, stubQueryLong, offReq)
	require.Empty(t, got)
	require.NoError(t, mock.ExpectationsWereMet())

	// Knowledge on, exp/tool repos nil (degrade, no panic, no SQL for them).
	mock.ExpectQuery(stubReKBVector).WillReturnRows(stubKBVectorRows("kb only", 0.8))
	onReq := &SearchRequest{TenantID: stubTenant,
		Plan: &RetrievalPlan{SearchKnowledge: true, SearchExperience: true, SearchTools: true, TopK: 5}}
	got = svc.searchAllVectorSources(context.Background(), []float64{0.1}, stubQueryLong, onReq)
	require.Len(t, got, 1)
	require.Equal(t, "knowledge", got[0].Type)
	require.InDelta(t, 0.8, got[0].Score, 1e-9)
	require.Equal(t, "vector", got[0].SubSource)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestRetrievalServiceSearchAllVectorSources_RepoErrorDegradesToEmpty pins
// the hybrid contract: repository errors inside the fan-out degrade to empty
// results instead of bubbling up.
func TestRetrievalServiceSearchAllVectorSources_RepoErrorDegradesToEmpty(t *testing.T) {
	db, mock := stubSQLMock(t, false)
	mock.ExpectQuery(stubReKBVector).WillReturnError(stubErrDB())
	mock.ExpectQuery(stubReEXPVector).WillReturnError(stubErrDB())
	mock.ExpectQuery(stubReTOOLVector).WillReturnError(stubErrDB())

	svc := stubRetrievalSvc(stubKBRepo(db), stubExpRepo(db), stubToolRepo(db),
		stubNewPipeline([]float64{0.1}, nil), stubGuard(t))
	req := &SearchRequest{TenantID: stubTenant, Plan: &RetrievalPlan{
		SearchKnowledge: true, SearchExperience: true, SearchTools: true,
		ExperienceTopK: 5, TopK: 5,
	}}
	got := svc.searchAllVectorSources(context.Background(), []float64{0.1}, stubQueryLong, req)
	require.Empty(t, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestRetrievalServiceSearchAllVectorSources_MapsScores covers kb+exp+tool
// result mapping from sqlmock rows.
func TestRetrievalServiceSearchAllVectorSources_MapsScores(t *testing.T) {
	db, mock := stubSQLMock(t, false)
	mock.ExpectQuery(stubReKBVector).WillReturnRows(stubKBVectorRows("kb map", 0.81))
	mock.ExpectQuery(stubReEXPVector).WillReturnRows(stubEXPVectorRows(stubEXPOutput, 0.72))
	mock.ExpectQuery(stubReTOOLVector).WillReturnRows(stubTOOLVectorRows(stubToolDescription, 0.63))

	svc := stubRetrievalSvc(stubKBRepo(db), stubExpRepo(db), stubToolRepo(db),
		stubNewPipeline([]float64{0.1}, nil), stubGuard(t))
	req := &SearchRequest{TenantID: stubTenant, Plan: &RetrievalPlan{
		SearchKnowledge: true, SearchExperience: true, SearchTools: true,
		ExperienceTopK: 5, TopK: 5,
		// Ranking off: convertExperiencesToResults path (Content=Output).
		ExperienceRankingEnabled: false,
	}}
	got := svc.searchAllVectorSources(context.Background(), []float64{0.1}, stubQueryLong, req)
	require.Len(t, got, 3)

	byType := map[string]*SearchResult{}
	for _, r := range got {
		byType[r.Type] = r
	}
	require.NotNil(t, byType["knowledge"])
	require.InDelta(t, 0.81, byType["knowledge"].Score, 1e-9)
	require.NotNil(t, byType["experience"])
	require.Equal(t, stubEXPOutput, byType["experience"].Content,
		"experience Content must map from Output")
	require.InDelta(t, 0.72, byType["experience"].Score, 1e-9)
	require.NotNil(t, byType["tool"])
	require.InDelta(t, 0.63, byType["tool"].Score, 1e-9)
	require.Equal(t, stubToolDescription, byType["tool"].Content)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestRetrievalServiceSearchAllKeywordSources_ThreeReposAndNilDegrade covers
// the serial keyword fan-out across kb/exp/tool plus the nil-repo variant.
func TestRetrievalServiceSearchAllKeywordSources_ThreeReposAndNilDegrade(t *testing.T) {
	db, mock := stubSQLMock(t, true)
	mock.ExpectQuery(stubReKBKeyword).WillReturnRows(stubKBKeywordRows("kb kw", 0.5))
	mock.ExpectQuery(stubReEXPKeyword).WillReturnRows(stubEXPKeywordRows(stubEXPInput, 0.6))
	mock.ExpectQuery(stubReTOOLKeyword).WillReturnRows(stubTOOLKeywordRows(stubToolName, 0))

	svc := stubRetrievalSvc(stubKBRepo(db), stubExpRepo(db), stubToolRepo(db), nil, stubGuard(t))
	got := svc.searchAllKeywordSources(context.Background(), stubQueryLong, stubTenant, 5)
	require.Len(t, got, 3)
	sources := map[string]bool{}
	for _, r := range got {
		sources[r.Source] = true
		require.Equal(t, "keyword", r.SubSource)
	}
	require.True(t, sources["knowledge"])
	require.True(t, sources["experience"])
	require.True(t, sources["tool"])
	require.NoError(t, mock.ExpectationsWereMet())

	// Nil repos: empty result, no SQL.
	bare := stubRetrievalSvc(nil, nil, nil, nil, stubGuard(t))
	empty := bare.searchAllKeywordSources(context.Background(), stubQueryLong, stubTenant, 5)
	require.Empty(t, empty)
}

// TestRetrievalServiceBm25Search_ErrorPathsDegrade pins per-source keyword
// error degradation.
func TestRetrievalServiceBm25Search_ErrorPathsDegrade(t *testing.T) {
	db, mock := stubSQLMock(t, false)
	mock.ExpectQuery(stubReKBKeyword).WillReturnError(stubErrDB())
	mock.ExpectQuery(stubReEXPKeyword).WillReturnError(stubErrDB())
	mock.ExpectQuery(stubReTOOLKeyword).WillReturnError(stubErrDB())

	svc := stubRetrievalSvc(stubKBRepo(db), stubExpRepo(db), stubToolRepo(db), nil, stubGuard(t))
	got := svc.searchAllKeywordSources(context.Background(), stubQueryLong, stubTenant, 5)
	require.Empty(t, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestRetrievalServiceApplySourceSignals_ScoringMatrix pins the signal
// multipliers on the pure scoring path.
func TestRetrievalServiceApplySourceSignals_ScoringMatrix(t *testing.T) {
	svc := stubRetrievalSvc(nil, nil, nil, nil, stubGuard(t))
	cases := []struct {
		name   string
		result *SearchResult
		base   float64
		want   float64
	}{
		{"experience success boost",
			&SearchResult{Source: "experience", Metadata: map[string]any{"success": true}}, 1.0, 1.2},
		{"experience failure penalty",
			&SearchResult{Source: "experience", Metadata: map[string]any{"success": false}}, 1.0, 0.7},
		{"experience fast exec boost",
			&SearchResult{Source: "experience", Metadata: map[string]any{"execution_time": 0.4}}, 1.0, 1.2},
		{"experience slow exec penalty",
			&SearchResult{Source: "experience", Metadata: map[string]any{"execution_time": 8.0}}, 1.0, 0.8},
		{"experience reuse boost",
			&SearchResult{Source: "experience", Metadata: map[string]any{"reuse_count": 5}}, 1.0, 1.1},
		{"experience lessons boost",
			&SearchResult{Source: "experience", Metadata: map[string]any{"lessons": "retry with backoff"}}, 1.0, 1.05},
		{"tool requires auth penalty",
			&SearchResult{Source: "tool", Metadata: map[string]any{"requires_auth": true}}, 1.0, 0.9},
		{"tool low success penalty",
			&SearchResult{Source: "tool", Metadata: map[string]any{"success_rate": 0.2}}, 1.0, 0.8},
		{"tool high success boost",
			&SearchResult{Source: "tool", Metadata: map[string]any{"success_rate": 0.95}}, 1.0, 1.1},
		{"knowledge untouched",
			&SearchResult{Source: "knowledge", Metadata: map[string]any{}}, 1.0, 1.0},
	}
	for _, tc := range cases {
		got := svc.applySourceSignals(tc.base, tc.result)
		require.InDelta(t, tc.want, got, 1e-9, tc.name)
	}
}

// TestRetrievalServiceConvertExperiencesToResults_MapsFields pins the storage
// Experience → SearchResult mapping on the ranking-off path.
func TestRetrievalServiceConvertExperiencesToResults_MapsFields(t *testing.T) {
	svc := stubRetrievalSvc(nil, nil, nil, nil, stubGuard(t))
	exp := &storage_models.Experience{
		ID:        "exp-map-1",
		TenantID:  stubTenant,
		Type:      "success",
		Input:     stubEXPInput,
		Output:    stubEXPOutput,
		CreatedAt: stubNow(),
		Metadata:  map[string]any{"similarity": 0.66},
	}
	results := svc.convertExperiencesToResults([]*storage_models.Experience{exp})
	require.Len(t, results, 1)
	require.Equal(t, "exp-map-1", results[0].ID)
	require.Equal(t, stubEXPOutput, results[0].Content)
	require.Equal(t, "experience", results[0].Source)
	require.Equal(t, "vector", results[0].SubSource)
	require.Equal(t, "experience", results[0].Type)
	require.InDelta(t, 0.66, results[0].Score, 1e-9, "similarity metadata must drive Score")

	// Nil metadata path: Score stays zero, no panic.
	bare := svc.convertExperiencesToResults([]*storage_models.Experience{{ID: "exp-nil-meta"}})
	require.Len(t, bare, 1)
	require.InDelta(t, 0.0, bare[0].Score, 1e-9)
}

// TestRetrievalServiceSearchAllVectorSources_EmptyEmbedding pins the
// empty-embedding short-circuit shared by all three vector sources.
func TestRetrievalServiceSearchAllVectorSources_EmptyEmbedding(t *testing.T) {
	db, mock := stubSQLMock(t, true)
	svc := stubRetrievalSvc(stubKBRepo(db), stubExpRepo(db), stubToolRepo(db), nil, stubGuard(t))
	req := &SearchRequest{TenantID: stubTenant, Plan: &RetrievalPlan{
		SearchKnowledge: true, SearchExperience: true, SearchTools: true, TopK: 5,
	}}
	got := svc.searchAllVectorSources(context.Background(), nil, stubQueryLong, req)
	require.Empty(t, got)
	require.NoError(t, mock.ExpectationsWereMet(), "empty embedding must not hit SQL")
}

// compile-time guard: stubPipeline satisfies the pipeline interface used by
// the hybrid vector gate.
var _ memembed.EmbeddingPipeline = (*stubPipeline)(nil)
