package evalapi

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/Timwood0x10/ares/internal/ares_eval"
)

// Service orchestrates evaluation runs: loading suites, running tests via
// internal/ares_eval runners, and persisting results through the repository.
type Service struct {
	repo   EvalResultRepository
	loader *ares_eval.Loader
}

// NewService creates a new ares_eval service instance.
//
// Args:
//
//	repo - ares_eval result repository for persistence (must not be nil).
//
// Returns:
//
//	*Service - the initialized service instance.
//	error - ErrNilRepository if repo is nil.
func NewService(repo EvalResultRepository) (*Service, error) {
	if repo == nil {
		return nil, ErrNilRepository
	}
	return &Service{
		repo:   repo,
		loader: ares_eval.NewLoader(),
	}, nil
}

// RunEval starts an asynchronous evaluation run. It loads the test suite,
// runs each agent configuration concurrently using errgroup, and stores
// all results to the repository.
//
// The operation respects context cancellation: if ctx is cancelled,
// all in-flight evaluations are aborted and partial results are still stored.
//
// Args:
//
//	ctx - operation context for cancellation and timeout.
//	req - evaluation run request with suite path and agent configs.
//
// Returns:
//
//	*RunEvalResponse - contains the run_id and status summary.
//	error - non-nil on validation or critical failure.
func (s *Service) RunEval(ctx context.Context, req *RunEvalRequest) (*RunEvalResponse, error) {
	if req == nil {
		return nil, ErrNilServiceConfig
	}
	if req.SuitePath == "" {
		return nil, ErrEmptySuitePath
	}
	if len(req.AgentConfigs) == 0 {
		return nil, ErrEmptyAgentConfigs
	}

	runID := req.RunID
	if runID == "" {
		runID = uuid.New().String()
	}
	now := time.Now()

	// Load test suite.
	suite, err := s.loader.Load(req.SuitePath)
	if err != nil {
		return nil, fmt.Errorf("load suite from %q: %w", req.SuitePath, err)
	}

	totalTestCases := len(suite.TestCases)

	slog.Info("evaluation run started",
		"run_id", runID,
		"suite", suite.Name,
		"configs", len(req.AgentConfigs),
		"test_cases", totalTestCases,
	)

	// Run each agent configuration concurrently via errgroup.
	eg, egCtx := errgroup.WithContext(ctx)
	resultsCh := make(chan []*EvalResult, len(req.AgentConfigs))

	for _, cfgRef := range req.AgentConfigs {
		cfgRef := cfgRef // capture loop variable
		eg.Go(func() error {
			configResults := s.runSingleConfig(egCtx, runID, suite, cfgRef, now)
			select {
			case resultsCh <- configResults:
			case <-egCtx.Done():
			}
			return nil
		})
	}

	// Wait for all configurations to finish.
	if err := eg.Wait(); err != nil {
		slog.Error("evaluation run encountered error",
			"run_id", runID,
			"error", err,
		)
	}

	// Collect and store all results.
	close(resultsCh)
	var allResults []*EvalResult
	for batch := range resultsCh {
		allResults = append(allResults, batch...)
	}

	if len(allResults) > 0 {
		if storeErr := s.repo.StoreBatch(ctx, allResults); storeErr != nil {
			slog.Error("failed to store ares_eval results",
				"run_id", runID,
				"error", storeErr,
			)
			// Return error but include run_id so caller can check partial results.
			return &RunEvalResponse{
				RunID:          runID,
				Status:         "failed",
				TotalConfigs:   len(req.AgentConfigs),
				TotalTestCases: totalTestCases,
			}, fmt.Errorf("store results: %w", storeErr)
		}
	}

	slog.Info("evaluation run completed",
		"run_id", runID,
		"results_stored", len(allResults),
	)

	return &RunEvalResponse{
		RunID:          runID,
		Status:         "completed",
		TotalConfigs:   len(req.AgentConfigs),
		TotalTestCases: totalTestCases,
	}, nil
}

// GetResults retrieves stored results for a given run ID.
func (s *Service) GetResults(ctx context.Context, runID string) (*GetResultsResponse, error) {
	if runID == "" {
		return nil, ErrInvalidRunID
	}

	results, err := s.repo.GetByRunID(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("get results: %w", err)
	}

	// Convert pointers to values for response.
	resultVals := make([]EvalResult, len(results))
	for i, r := range results {
		resultVals[i] = *r
	}

	return &GetResultsResponse{
		RunID:      runID,
		Results:    resultVals,
		TotalCount: len(resultVals),
	}, nil
}

// GetLeaderboard returns the ranked leaderboard of agent configurations.
func (s *Service) GetLeaderboard(ctx context.Context, limit, offset int) (*LeaderboardResponse, error) {
	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	entries, totalCount, err := s.repo.GetLeaderboard(ctx, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("get leaderboard: %w", err)
	}

	entryVals := make([]LeaderboardEntry, len(entries))
	for i, e := range entries {
		entryVals[i] = *e
	}

	return &LeaderboardResponse{
		Entries:    entryVals,
		TotalCount: totalCount,
	}, nil
}

// GetComparison returns side-by-side comparison data for specified run IDs.
func (s *Service) GetComparison(ctx context.Context, runIDs []string) (*ComparisonResponse, error) {
	if len(runIDs) == 0 {
		return nil, ErrEmptyRunIDs
	}

	rows, err := s.repo.GetComparison(ctx, runIDs)
	if err != nil {
		return nil, fmt.Errorf("get comparison: %w", err)
	}

	rowVals := make([]ComparisonRow, len(rows))
	for i, r := range rows {
		rowVals[i] = *r
	}

	return &ComparisonResponse{
		RunIDs:         runIDs,
		Rows:           rowVals,
		TotalTestCases: len(rowVals),
	}, nil
}

// runSingleConfig executes a single agent configuration against the test suite
// and converts internal TestResult objects into persisted EvalResult records.
func (s *Service) runSingleConfig(
	ctx context.Context,
	runID string,
	suite *ares_eval.TestSuite,
	cfgRef AgentConfigRef,
	runStart time.Time,
) []*EvalResult {
	// Convert API AgentConfigRef to internal AgentConfig.
	internalCfg := ares_eval.AgentConfig{
		Name:         cfgRef.Name,
		Model:        cfgRef.Model,
		Parameters:   cfgRef.Parameters,
		SystemPrompt: cfgRef.SystemPrompt,
	}
	_ = internalCfg // reserved for future use with real runner factory

	// Create a basic test runner that produces placeholder results.
	// In production, this would use a real AgentExecutor.
	runner := &placeholderRunner{configName: cfgRef.Name}

	results, err := runner.RunSuite(ctx, *suite)
	if err != nil {
		slog.Error("config run failed",
			"run_id", runID,
			"config", cfgRef.Name,
			"error", err,
		)
		// Return a single error result for this config.
		now := time.Now()
		return []*EvalResult{{
			ID:           uuid.New().String(),
			RunID:        runID,
			ConfigName:   cfgRef.Name,
			SuiteName:    suite.Name,
			TestCaseID:   "unknown",
			TestCaseName: "unknown",
			Score:        0,
			Dimensions:   map[string]float64{},
			Status:       "error",
			ErrorMessage: strPtr(err.Error()),
			DurationMs:   0,
			CreatedAt:    now,
			UpdatedAt:    now,
		}}
	}

	// Convert internal TestResult to EvalResult.
	evalResults := make([]*EvalResult, len(results))
	now := time.Now()
	for i, tr := range results {
		status := "pass"
		errMsg := (*string)(nil)
		if tr.Error != "" {
			status = "fail"
			if isHardError(tr.Error) {
				status = "error"
				errMsg = &tr.Error
			}
		}

		evalResults[i] = &EvalResult{
			ID:           uuid.New().String(),
			RunID:        runID,
			ConfigName:   cfgRef.Name,
			SuiteName:    suite.Name,
			TestCaseID:   tr.TestCaseID,
			TestCaseName: lookupTestCaseName(suite, tr.TestCaseID),
			Score:        maxMetric(tr.Metrics),
			Dimensions:   copyMetrics(tr.Metrics),
			Status:       status,
			ErrorMessage: errMsg,
			DurationMs:   int(tr.Duration.Milliseconds()),
			CreatedAt:    now,
			UpdatedAt:    now,
		}
	}

	return evalResults
}

// placeholderRunner implements ares_eval.TestRunner for API-layer evaluation.
// In production, this would be replaced by a real runner backed by an AgentExecutor.
type placeholderRunner struct {
	configName string
}

// RunSuite runs all test cases sequentially, producing placeholder results.
// Each test case gets a default pass result with zero metrics.
func (r *placeholderRunner) RunSuite(ctx context.Context, suite ares_eval.TestSuite) ([]ares_eval.TestResult, error) {
	results := make([]ares_eval.TestResult, 0, len(suite.TestCases))
	for _, tc := range suite.TestCases {
		select {
		case <-ctx.Done():
			return results, ctx.Err()
		default:
		}

		results = append(results, ares_eval.TestResult{
			TestCaseID: tc.ID,
			Timestamp:  time.Now(),
			Metrics:    make(map[string]float64),
			Duration:   0,
		})
	}
	return results, nil
}

// RunSingle runs a single test case with a placeholder result.
func (r *placeholderRunner) RunSingle(ctx context.Context, testCase ares_eval.TestCase) (ares_eval.TestResult, error) {
	return ares_eval.TestResult{
		TestCaseID: testCase.ID,
		Timestamp:  time.Now(),
		Metrics:    make(map[string]float64),
		Duration:   0,
	}, nil
}

// Ensure compile-time interface check.
var _ ares_eval.TestRunner = (*placeholderRunner)(nil)

// --- Helper functions ---

// strPtr returns a pointer to the given string.
func strPtr(s string) *string { return &s }

// lookupTestCaseName finds the name of a test case by ID within a suite.
func lookupTestCaseName(suite *ares_eval.TestSuite, testCaseID string) string {
	for _, tc := range suite.TestCases {
		if tc.ID == testCaseID {
			return tc.Name
		}
	}
	return testCaseID
}

// maxMetric returns the maximum metric value from a metrics map.
func maxMetric(metrics map[string]float64) float64 {
	max := 0.0
	for _, v := range metrics {
		if v > max {
			max = v
		}
	}
	return max
}

// copyMetrics creates a shallow copy of a metrics map.
func copyMetrics(m map[string]float64) map[string]float64 {
	if m == nil {
		return make(map[string]float64)
	}
	cpy := make(map[string]float64, len(m))
	for k, v := range m {
		cpy[k] = v
	}
	return cpy
}

// isHardError determines whether an error message represents a hard failure
// (infrastructure / execution error) vs a soft failure (wrong answer).
func isHardError(msg string) bool {
	hardKeywords := []string{"timeout", "cancelled", "context", "connection", "EOF"}
	lower := strings.ToLower(msg)
	for _, kw := range hardKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
