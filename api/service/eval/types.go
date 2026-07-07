// Package evaluation provides public types for agent output evaluation.
package evaluation

import "time"

// TestCase represents a single evaluation test case.
type TestCase struct {
	ID             string                 `json:"id" yaml:"id"`
	Name           string                 `json:"name" yaml:"name"`
	Input          string                 `json:"input" yaml:"input"`
	ExpectedOutput string                 `json:"expected_output,omitempty" yaml:"expected_output,omitempty"`
	ExpectedTools  []string               `json:"expected_tools,omitempty" yaml:"expected_tools,omitempty"`
	Timeout        time.Duration          `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	Tags           []string               `json:"tags,omitempty" yaml:"tags,omitempty"`
}

// TestResult represents the result of executing a test case.
type TestResult struct {
	TestCaseID   string             `json:"test_case_id"`
	ActualOutput string             `json:"actual_output"`
	ToolsUsed    []string           `json:"tools_used"`
	Duration     time.Duration      `json:"duration"`
	TokensUsed   int                `json:"tokens_used"`
	Error        string             `json:"error,omitempty"`
	Metrics      map[string]float64 `json:"metrics"`
	Timestamp    time.Time          `json:"timestamp"`
}

// EvalScore represents a single evaluation metric score.
type EvalScore struct {
	Metric  string  `json:"metric"`
	Score   float64 `json:"score"`
	Details string  `json:"details,omitempty"`
}
