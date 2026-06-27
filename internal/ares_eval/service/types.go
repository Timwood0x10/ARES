package evalapi

import "time"

// EvalResult represents a single persisted evaluation result row.
type EvalResult struct {
	// ID is the unique identifier of this result record.
	ID string `json:"id"`

	// RunID groups results from the same evaluation run.
	RunID string `json:"run_id"`

	// ConfigName identifies the agent configuration being evaluated.
	ConfigName string `json:"config_name"`

	// SuiteName is the name of the test suite used.
	SuiteName string `json:"suite_name"`

	// TestCaseID is the identifier of the test case.
	TestCaseID string `json:"test_case_id"`

	// TestCaseName is the human-readable name of the test case.
	TestCaseName string `json:"test_case_name"`

	// Score is the overall score for this test case (0.0 - 1.0).
	Score float64 `json:"score"`

	// Dimensions holds per-dimension scores (e.g., {"accuracy": 0.9, "speed": 0.8}).
	Dimensions map[string]float64 `json:"dimensions"`

	// Status is the result status: "pass", "fail", or "error".
	Status string `json:"status"`

	// ErrorMessage contains error details when status is "error".
	ErrorMessage *string `json:"error_message,omitempty"`

	// DurationMs is the execution duration in milliseconds.
	DurationMs int `json:"duration_ms"`

	// CreatedAt is when this result was created.
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is when this result was last modified.
	UpdatedAt time.Time `json:"updated_at"`
}

// LeaderboardEntry represents a ranked configuration entry on the leaderboard.
type LeaderboardEntry struct {
	// Rank is the position on the leaderboard (1-based).
	Rank int `json:"rank"`

	// ConfigName identifies the agent configuration.
	ConfigName string `json:"config_name"`

	// OverallScore is the aggregated average score across all test cases.
	OverallScore float64 `json:"overall_score"`

	// PassRate is the fraction of tests that passed (0.0 - 1.0).
	PassRate float64 `json:"pass_rate"`

	// TotalTests is the total number of test cases for this config.
	TotalTests int `json:"total_tests"`

	// AvgDurationMs is the average execution duration in milliseconds.
	AvgDurationMs int `json:"avg_duration_ms"`

	// RunID references the latest evaluation run for this entry.
	RunID string `json:"run_id"`
}

// ComparisonRow represents a single test case's results across multiple runs
// for side-by-side comparison.
type ComparisonRow struct {
	// TestCaseID is the identifier of the test case.
	TestCaseID string `json:"test_case_id"`

	// TestCaseName is the human-readable name of the test case.
	TestCaseName string `json:"test_case_name"`

	// Results maps run_id -> result data for this test case.
	Results map[string]ComparisonCell `json:"results"`
}

// ComparisonCell holds a single configuration's result within a comparison row.
type ComparisonCell struct {
	// ConfigName identifies the agent configuration.
	ConfigName string `json:"config_name"`

	// Score is the overall score for this test case.
	Score float64 `json:"score"`

	// Status is the result status: "pass", "fail", or "error".
	Status string `json:"status"`

	// DurationMs is the execution duration in milliseconds.
	DurationMs int `json:"duration_ms"`
}

// RunEvalRequest is the request body for POST /api/v1/eval/run.
type RunEvalRequest struct {
	// RunID is an optional pre-generated run ID. If empty, the service will generate one.
	RunID string `json:"run_id,omitempty"`

	// SuitePath is the file path to the test suite YAML file.
	SuitePath string `json:"suite_path"`

	// AgentConfigs lists agent configurations to evaluate.
	AgentConfigs []AgentConfigRef `json:"agent_configs"`
}

// AgentConfigRef is a simplified reference to an agent configuration
// used in API requests (avoids exposing full internal AgentConfig).
type AgentConfigRef struct {
	// Name is the unique name for this configuration.
	Name string `json:"name"`

	// Model is the LLM model identifier.
	Model string `json:"model"`

	// Parameters are optional model parameters.
	Parameters map[string]any `json:"parameters,omitempty"`

	// SystemPrompt is the optional system prompt override.
	SystemPrompt string `json:"system_prompt,omitempty"`
}

// RunEvalResponse is the response body for POST /api/v1/eval/run.
type RunEvalResponse struct {
	// RunID uniquely identifies this evaluation run.
	RunID string `json:"run_id"`

	// Status is the run status: "running", "completed", "failed".
	Status string `json:"status"`

	// TotalConfigs is the number of configurations being evaluated.
	TotalConfigs int `json:"total_configs"`

	// TotalTestCases is the total number of test cases across all configs.
	TotalTestCases int `json:"total_test_cases"`
}

// GetResultsResponse is the response body for GET /api/v1/eval/results/:run_id.
type GetResultsResponse struct {
	// RunID is the evaluation run identifier.
	RunID string `json:"run_id"`

	// Results contains all eval results for this run.
	Results []EvalResult `json:"results"`

	// TotalCount is the total number of result records.
	TotalCount int `json:"total_count"`
}

// LeaderboardResponse is the response body for GET /api/v1/eval/leaderboard.
type LeaderboardResponse struct {
	// Entries contains the ranked leaderboard entries.
	Entries []LeaderboardEntry `json:"entries"`

	// TotalCount is the total number of entries.
	TotalCount int `json:"total_count"`
}

// ComparisonResponse is the response body for GET /api/v1/eval/comparison.
type ComparisonResponse struct {
	// RunIDs included in this comparison.
	RunIDs []string `json:"run_ids"`

	// Rows contains per-test-case side-by-side results.
	Rows []ComparisonRow `json:"rows"`

	// TotalTestCases is the number of test cases in the comparison.
	TotalTestCases int `json:"total_test_cases"`
}
