package evalapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockEvalResultRepository implements EvalResultRepository for testing.
type mockEvalResultRepository struct {
	results     map[string][]*EvalResult
	leaderboard []*LeaderboardEntry
	totalCount  int
	storeErr    error
	getByIDErr  error
}

func newMockRepo() *mockEvalResultRepository {
	return &mockEvalResultRepository{
		results: make(map[string][]*EvalResult),
	}
}

func (m *mockEvalResultRepository) Store(ctx context.Context, result *EvalResult) error {
	if m.storeErr != nil {
		return m.storeErr
	}
	m.results[result.RunID] = append(m.results[result.RunID], result)
	return nil
}

func (m *mockEvalResultRepository) StoreBatch(ctx context.Context, results []*EvalResult) error {
	if m.storeErr != nil {
		return m.storeErr
	}
	for _, r := range results {
		m.results[r.RunID] = append(m.results[r.RunID], r)
	}
	return nil
}

func (m *mockEvalResultRepository) GetByRunID(ctx context.Context, runID string) ([]*EvalResult, error) {
	if m.getByIDErr != nil {
		return nil, m.getByIDErr
	}
	return m.results[runID], nil
}

func (m *mockEvalResultRepository) GetLeaderboard(
	ctx context.Context, limit, offset int,
) ([]*LeaderboardEntry, int, error) {
	return m.leaderboard, m.totalCount, nil
}

func (m *mockEvalResultRepository) GetComparison(
	ctx context.Context, runIDs []string,
) ([]*ComparisonRow, error) {
	rows := []*ComparisonRow{}
	for _, runID := range runIDs {
		results := m.results[runID]
		for _, r := range results {
			row := &ComparisonRow{
				TestCaseID:   r.TestCaseID,
				TestCaseName: r.TestCaseName,
				Results: map[string]ComparisonCell{
					runID + ":" + r.ConfigName: {
						ConfigName: r.ConfigName,
						Score:      r.Score,
						Status:     r.Status,
						DurationMs: r.DurationMs,
					},
				},
			}
			rows = append(rows, row)
		}
	}
	return rows, nil
}

// --- Handler Tests ---

func TestHandleRunEval(t *testing.T) {
	repo := newMockRepo()
	svc, err := NewService(repo)
	require.NoError(t, err)
	h := NewHandler(svc)

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantField  string
	}{
		{
			name:       "empty body returns 400",
			body:       "",
			wantStatus: http.StatusBadRequest,
			wantField:  "error",
		},
		{
			name:       "invalid JSON returns 400",
			body:       `{invalid`,
			wantStatus: http.StatusBadRequest,
			wantField:  "error",
		},
		{
			name:       "empty suite path returns 400",
			body:       `{"suite_path":"","agent_configs":[{"name":"test","model":"gpt-4"}]}`,
			wantStatus: http.StatusBadRequest,
			wantField:  "error",
		},
		{
			name:       "no agent configs returns 400",
			body:       `{"suite_path":"/tmp/suite.yaml","agent_configs":[]}`,
			wantStatus: http.StatusBadRequest,
			wantField:  "error",
		},
		{
			name: "valid request with nonexistent suite returns 202 (async)",
			body: `{
				"suite_path": "/nonexistent/path/suite.yaml",
				"agent_configs": [{"name":"config-a","model":"gpt-4"}]
			}`,
			wantStatus: http.StatusAccepted,
			wantField:  "run_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/eval/run", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			h.HandleRunEval(w, req)

			assert.Equal(t, tt.wantStatus, w.Code)

			var resp map[string]any
			err := json.NewDecoder(w.Body).Decode(&resp)
			require.NoError(t, err)
			assert.Contains(t, resp, tt.wantField)
		})
	}
}

func TestHandleGetResults(t *testing.T) {
	svc, err := NewService(newMockRepo())
	require.NoError(t, err)
	h := NewHandler(svc)

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantError  bool
	}{
		{
			name:       "missing run_id returns 400",
			path:       "/api/v1/eval/results/",
			wantStatus: http.StatusBadRequest,
			wantError:  true,
		},
		{
			name:       "valid run_id with no results returns 200 empty",
			path:       "/api/v1/eval/results/run-abc-123",
			wantStatus: http.StatusOK,
			wantError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()

			h.HandleGetResults(w, req)

			assert.Equal(t, tt.wantStatus, w.Code)

			var resp map[string]any
			err := json.NewDecoder(w.Body).Decode(&resp)
			require.NoError(t, err)

			if tt.wantError {
				assert.Contains(t, resp, "error")
			} else {
				assert.Contains(t, resp, "results")
				assert.Contains(t, resp, "total_count")
			}
		})
	}
}

func TestHandleGetResultsWithData(t *testing.T) {
	repo := newMockRepo()
	runID := "test-run-with-data"
	now := time.Now()

	repo.results[runID] = []*EvalResult{{
		ID:           "result-1",
		RunID:        runID,
		ConfigName:   "config-a",
		SuiteName:    "test-suite",
		TestCaseID:   "tc-1",
		TestCaseName: "Test Case 1",
		Score:        0.95,
		Dimensions:   map[string]float64{"accuracy": 0.95},
		Status:       "pass",
		DurationMs:   150,
		CreatedAt:    now,
		UpdatedAt:    now,
	}}

	svc, err := NewService(repo)
	require.NoError(t, err)
	h := NewHandler(svc)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/eval/results/"+runID, nil)
	w := httptest.NewRecorder()
	h.HandleGetResults(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp GetResultsResponse
	err = json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, runID, resp.RunID)
	assert.Equal(t, 1, resp.TotalCount)
	require.Len(t, resp.Results, 1)
	assert.Equal(t, "config-a", resp.Results[0].ConfigName)
	assert.Equal(t, "pass", resp.Results[0].Status)
	assert.InDelta(t, 0.95, resp.Results[0].Score, 0.001)
}

func TestHandleGetLeaderboard(t *testing.T) {
	repo := newMockRepo()
	repo.leaderboard = []*LeaderboardEntry{{
		Rank:          1,
		ConfigName:    "config-a",
		OverallScore:  0.92,
		PassRate:      1.0,
		TotalTests:    10,
		AvgDurationMs: 120,
		RunID:         "run-1",
	}}
	repo.totalCount = 1

	svc, err := NewService(repo)
	require.NoError(t, err)
	h := NewHandler(svc)

	tests := []struct {
		name           string
		query          string
		wantStatus     int
		wantEntryCount int
	}{
		{
			name:           "default params returns entries",
			query:          "",
			wantStatus:     http.StatusOK,
			wantEntryCount: 1,
		},
		{
			name:           "with limit param",
			query:          "?limit=5&offset=0",
			wantStatus:     http.StatusOK,
			wantEntryCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/eval/leaderboard"+tt.query, nil)
			w := httptest.NewRecorder()

			h.HandleGetLeaderboard(w, req)

			assert.Equal(t, tt.wantStatus, w.Code)

			var resp LeaderboardResponse
			err := json.NewDecoder(w.Body).Decode(&resp)
			require.NoError(t, err)
			assert.Equal(t, tt.wantEntryCount, len(resp.Entries))
			assert.Equal(t, 1, resp.TotalCount)
		})
	}
}

func TestHandleGetComparison(t *testing.T) {
	svc, err := NewService(newMockRepo())
	require.NoError(t, err)
	h := NewHandler(svc)

	tests := []struct {
		name       string
		query      string
		wantStatus int
		wantError  bool
	}{
		{
			name:       "missing run_ids returns 400",
			query:      "",
			wantStatus: http.StatusBadRequest,
			wantError:  true,
		},
		{
			name:       "empty run_ids returns 400",
			query:      "?run_ids=",
			wantStatus: http.StatusBadRequest,
			wantError:  true,
		},
		{
			name:       "single valid run_id returns 200",
			query:      "?run_ids=run-1",
			wantStatus: http.StatusOK,
			wantError:  false,
		},
		{
			name:       "multiple run_ids returns 200",
			query:      "?run_ids=run-1,run-2,run-3",
			wantStatus: http.StatusOK,
			wantError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/eval/comparison"+tt.query, nil)
			w := httptest.NewRecorder()

			h.HandleGetComparison(w, req)

			assert.Equal(t, tt.wantStatus, w.Code)

			var resp map[string]any
			err := json.NewDecoder(w.Body).Decode(&resp)
			require.NoError(t, err)

			if tt.wantError {
				assert.Contains(t, resp, "error")
			} else {
				assert.Contains(t, resp, "rows")
				assert.Contains(t, resp, "run_ids")
			}
		})
	}
}

func TestHandleGetComparisonWithData(t *testing.T) {
	repo := newMockRepo()
	runID := "comparison-run"
	now := time.Now()

	repo.results[runID] = []*EvalResult{
		{
			ID: "r1", RunID: runID, ConfigName: "cfg-x",
			SuiteName: "suite", TestCaseID: "tc-1", TestCaseName: "Test 1",
			Score: 0.9, Status: "pass", DurationMs: 100,
			CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "r2", RunID: runID, ConfigName: "cfg-y",
			SuiteName: "suite", TestCaseID: "tc-2", TestCaseName: "Test 2",
			Score: 0.7, Status: "fail", DurationMs: 200,
			CreatedAt: now, UpdatedAt: now,
		},
	}

	svc, err := NewService(repo)
	require.NoError(t, err)
	h := NewHandler(svc)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/eval/comparison?run_ids="+runID, nil)
	w := httptest.NewRecorder()
	h.HandleGetComparison(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp ComparisonResponse
	err = json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp.RunIDs, runID)
	assert.Equal(t, 2, resp.TotalTestCases)
	require.Len(t, resp.Rows, 2)
}

// --- Service Tests ---

func TestNewService(t *testing.T) {
	t.Run("nil repository returns error", func(t *testing.T) {
		svc, err := NewService(nil)
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrNilRepository)
		assert.Nil(t, svc)
	})

	t.Run("valid repository creates service", func(t *testing.T) {
		repo := newMockRepo()
		svc, err := NewService(repo)
		assert.NoError(t, err)
		assert.NotNil(t, svc)
	})
}

func TestServiceValidation(t *testing.T) {
	repo := newMockRepo()
	svc, _ := NewService(repo)
	ctx := context.Background()

	t.Run("nil request returns error", func(t *testing.T) {
		resp, err := svc.RunEval(ctx, nil)
		assert.ErrorIs(t, err, ErrNilServiceConfig)
		assert.Nil(t, resp)
	})

	t.Run("empty suite path returns error", func(t *testing.T) {
		resp, err := svc.RunEval(ctx, &RunEvalRequest{
			SuitePath:    "",
			AgentConfigs: []AgentConfigRef{{Name: "a", Model: "gpt-4"}},
		})
		assert.ErrorIs(t, err, ErrEmptySuitePath)
		assert.Nil(t, resp)
	})

	t.Run("empty agent configs returns error", func(t *testing.T) {
		resp, err := svc.RunEval(ctx, &RunEvalRequest{
			SuitePath:    "/some/path.yaml",
			AgentConfigs: []AgentConfigRef{},
		})
		assert.ErrorIs(t, err, ErrEmptyAgentConfigs)
		assert.Nil(t, resp)
	})

	t.Run("invalid run ID for get results", func(t *testing.T) {
		resp, err := svc.GetResults(ctx, "")
		assert.ErrorIs(t, err, ErrInvalidRunID)
		assert.Nil(t, resp)
	})

	t.Run("empty run IDs for comparison", func(t *testing.T) {
		resp, err := svc.GetComparison(ctx, []string{})
		assert.ErrorIs(t, err, ErrEmptyRunIDs)
		assert.Nil(t, resp)
	})
}

// --- Helper function tests ---

func TestExtractPathValue(t *testing.T) {
	tests := []struct {
		input  string
		prefix string
		want   string
	}{
		{"/api/v1/eval/results/abc-123", "/api/v1/eval/results/", "abc-123"},
		{"/api/v1/eval/results/", "/api/v1/eval/results/", ""},
		{"/api/v1/eval/results", "/api/v1/eval/results/", ""},
		{"/other/path", "/api/v1/eval/results/", ""},
		{"/api/v1/eval/results/abc-123/", "/api/v1/eval/results/", "abc-123"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := extractPathValue(tt.input, tt.prefix)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSplitCommaSeparated(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"a,b,c", []string{"a", "b", "c"}},
		{"a, b , c ", []string{"a", "b", "c"}},
		{"single", []string{"single"}},
		{",,,", []string{}},
		{"", []string{}},
		{"  a  ,  b  ", []string{"a", "b"}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := splitCommaSeparated(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestHelperFunctions(t *testing.T) {
	t.Run("strPtr", func(t *testing.T) {
		s := "hello"
		p := strPtr(s)
		require.NotNil(t, p)
		assert.Equal(t, s, *p)
	})

	t.Run("maxMetric empty", func(t *testing.T) {
		assert.Equal(t, 0.0, maxMetric(nil))
		assert.Equal(t, 0.0, maxMetric(map[string]float64{}))
	})

	t.Run("maxMetric with values", func(t *testing.T) {
		assert.Equal(t, 0.9, maxMetric(map[string]float64{"a": 0.5, "b": 0.9, "c": 0.3}))
	})

	t.Run("copyMetrics", func(t *testing.T) {
		original := map[string]float64{"a": 1.0, "b": 2.0}
		copied := copyMetrics(original)
		assert.Equal(t, original, copied)
		// Verify it's a copy.
		copied["c"] = 3.0
		assert.NotContains(t, original, "c")
	})

	t.Run("copyMetrics nil", func(t *testing.T) {
		result := copyMetrics(nil)
		assert.NotNil(t, result)
		assert.Empty(t, result)
	})

	t.Run("isHardError detects hard errors", func(t *testing.T) {
		assert.True(t, isHardError("context deadline exceeded"))
		assert.True(t, isHardError("connection refused"))
		assert.True(t, isHardError("request timeout"))
		assert.False(t, isHardError("output did not match expected"))
		assert.False(t, isHardError(""))
	})
}
