// Package experience provides core data structures for the GA/Memory/Tool fusion system.
package experience

import (
	"testing"
	"time"
)

func TestToolCallRecordIsEmpty(t *testing.T) {
	tests := []struct {
		name     string
		record   ToolCallRecord
		expected bool
	}{
		{
			name:     "empty record",
			record:   ToolCallRecord{},
			expected: true,
		},
		{
			name: "record with strategy ID",
			record: ToolCallRecord{
				StrategyID: "strategy-123",
			},
			expected: false,
		},
		{
			name: "fully populated record",
			record: ToolCallRecord{
				StrategyID:      "strategy-123",
				TaskType:        "code_generation",
				ToolName:        "search",
				LatencyMs:       150,
				Success:         true,
				Timestamp:       time.Now(),
				RetryCount:      0,
				ResultSizeBytes: 1024,
			},
			expected: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := test.record.IsEmpty()
			if result != test.expected {
				t.Errorf("IsEmpty() = %v, expected %v", result, test.expected)
			}
		})
	}
}

func TestExecutionExperienceIsEmpty(t *testing.T) {
	tests := []struct {
		name       string
		experience ExecutionExperience
		expected   bool
	}{
		{
			name:       "empty experience",
			experience: ExecutionExperience{},
			expected:   true,
		},
		{
			name: "experience with strategy ID",
			experience: ExecutionExperience{
				StrategyID: "strategy-123",
			},
			expected: false,
		},
		{
			name: "fully populated experience",
			experience: ExecutionExperience{
				StrategyID:    "strategy-789",
				TaskType:      "code_generation",
				Success:       true,
				LatencyMs:     2500,
				RetryCount:    1,
				ErrorRate:     0.05,
				ToolChain:     "search|read|write",
				ResultQuality: 0.85,
				TokenCost:     5000,
				WallTime:      3000,
				Timestamp:     time.Now(),
			},
			expected: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := test.experience.IsEmpty()
			if result != test.expected {
				t.Errorf("IsEmpty() = %v, expected %v", result, test.expected)
			}
		})
	}
}

func TestNormalizedExecutionExperienceIsEmpty(t *testing.T) {
	tests := []struct {
		name       string
		experience NormalizedExecutionExperience
		expected   bool
	}{
		{
			name:       "empty normalized experience",
			experience: NormalizedExecutionExperience{},
			expected:   true,
		},
		{
			name: "normalized experience with strategy ID",
			experience: NormalizedExecutionExperience{
				StrategyID: "strategy-123",
			},
			expected: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := test.experience.IsEmpty()
			if result != test.expected {
				t.Errorf("IsEmpty() = %v, expected %v", result, test.expected)
			}
		})
	}
}

func TestEvidenceIsEmpty(t *testing.T) {
	tests := []struct {
		name     string
		evidence Evidence
		expected bool
	}{
		{
			name:     "empty evidence",
			evidence: Evidence{},
			expected: true,
		},
		{
			name: "evidence with strategy ID",
			evidence: Evidence{
				StrategyID:  "strategy-123",
				SampleCount: 10,
			},
			expected: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := test.evidence.IsEmpty()
			if result != test.expected {
				t.Errorf("IsEmpty() = %v, expected %v", result, test.expected)
			}
		})
	}
}

func TestEvidenceHasSamples(t *testing.T) {
	tests := []struct {
		name     string
		evidence Evidence
		expected bool
	}{
		{
			name:     "zero samples",
			evidence: Evidence{SampleCount: 0},
			expected: false,
		},
		{
			name:     "one sample",
			evidence: Evidence{SampleCount: 1},
			expected: true,
		},
		{
			name:     "many samples",
			evidence: Evidence{SampleCount: 100},
			expected: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := test.evidence.HasSamples()
			if result != test.expected {
				t.Errorf("HasSamples() = %v, expected %v", result, test.expected)
			}
		})
	}
}

func TestNormalizeExecution(t *testing.T) {
	tests := []struct {
		name            string
		raw             ExecutionExperience
		maxLatencyMs    int64
		maxRetryCount   int
		maxTokenCost    int64
		maxWallTime     int64
		expectedSuccess float64
		expectedLatency float64
	}{
		{
			name: "successful experience",
			raw: ExecutionExperience{
				StrategyID:    "strategy-123",
				TaskType:      "code_generation",
				Success:       true,
				LatencyMs:     500,
				RetryCount:    2,
				ErrorRate:     0.1,
				ToolChain:     "search|read",
				ResultQuality: 0.9,
				TokenCost:     1000,
				WallTime:      600,
				Timestamp:     time.Now(),
			},
			maxLatencyMs:    1000,
			maxRetryCount:   5,
			maxTokenCost:    2000,
			maxWallTime:     1000,
			expectedSuccess: 1.0,
			expectedLatency: 0.5,
		},
		{
			name: "failed experience",
			raw: ExecutionExperience{
				StrategyID:    "strategy-failed",
				TaskType:      "analysis",
				Success:       false,
				LatencyMs:     2000,
				RetryCount:    4,
				ErrorRate:     0.5,
				ToolChain:     "error",
				ResultQuality: 0.2,
				TokenCost:     3000,
				WallTime:      2500,
				Timestamp:     time.Now(),
			},
			maxLatencyMs:    1000,
			maxRetryCount:   5,
			maxTokenCost:    2000,
			maxWallTime:     1000,
			expectedSuccess: 0.0,
			expectedLatency: 0.0,
		},
		{
			name: "zero max values",
			raw: ExecutionExperience{
				StrategyID:    "strategy-edge",
				TaskType:      "test",
				Success:       true,
				LatencyMs:     100,
				RetryCount:    1,
				ErrorRate:     0.0,
				ToolChain:     "tool",
				ResultQuality: 1.0,
				TokenCost:     500,
				WallTime:      100,
				Timestamp:     time.Now(),
			},
			maxLatencyMs:    0,
			maxRetryCount:   0,
			maxTokenCost:    0,
			maxWallTime:     0,
			expectedSuccess: 1.0,
			expectedLatency: 0.0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := NormalizeExecution(
				test.raw,
				test.maxLatencyMs,
				test.maxRetryCount,
				test.maxTokenCost,
				test.maxWallTime,
			)

			if result.Success != test.expectedSuccess {
				t.Errorf("Success = %v, expected %v", result.Success, test.expectedSuccess)
			}
			if result.LatencyMs != test.expectedLatency {
				t.Errorf("LatencyMs = %v, expected %v", result.LatencyMs, test.expectedLatency)
			}

			// Verify normalized values are in [0.0, 1.0] range
			if result.LatencyMs < 0 || result.LatencyMs > 1.0 {
				t.Errorf("LatencyMs out of range [0, 1]: %v", result.LatencyMs)
			}
			if result.RetryCount < 0 || result.RetryCount > 1.0 {
				t.Errorf("RetryCount out of range [0, 1]: %v", result.RetryCount)
			}
			if result.ErrorRate < 0 || result.ErrorRate > 1.0 {
				t.Errorf("ErrorRate out of range [0, 1]: %v", result.ErrorRate)
			}
			if result.TokenCost < 0 || result.TokenCost > 1.0 {
				t.Errorf("TokenCost out of range [0, 1]: %v", result.TokenCost)
			}
			if result.WallTime < 0 || result.WallTime > 1.0 {
				t.Errorf("WallTime out of range [0, 1]: %v", result.WallTime)
			}
		})
	}
}

func TestMergeEvidence(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name                string
		experiences         []NormalizedExecutionExperience
		expectedEmpty       bool
		expectedSampleCount int64
		expectedSuccessRate float64
	}{
		{
			name:                "empty slice",
			experiences:         []NormalizedExecutionExperience{},
			expectedEmpty:       true,
			expectedSampleCount: 0,
		},
		{
			name: "single experience",
			experiences: []NormalizedExecutionExperience{
				{
					StrategyID:    "strategy-123",
					TaskType:      "code_generation",
					Success:       1.0,
					LatencyMs:     0.8,
					ErrorRate:     0.95,
					ToolChain:     "search|read",
					ResultQuality: 0.9,
					Timestamp:     now,
				},
			},
			expectedEmpty:       false,
			expectedSampleCount: 1,
			expectedSuccessRate: 1.0,
		},
		{
			name: "multiple experiences",
			experiences: []NormalizedExecutionExperience{
				{
					StrategyID:    "strategy-456",
					TaskType:      "analysis",
					Success:       1.0,
					LatencyMs:     0.9,
					ErrorRate:     0.98,
					ToolChain:     "search|analyze",
					ResultQuality: 0.95,
					Timestamp:     now,
				},
				{
					StrategyID:    "strategy-456",
					TaskType:      "analysis",
					Success:       0.0,
					LatencyMs:     0.3,
					ErrorRate:     0.5,
					ToolChain:     "search|analyze",
					ResultQuality: 0.4,
					Timestamp:     now,
				},
			},
			expectedEmpty:       false,
			expectedSampleCount: 2,
			expectedSuccessRate: 0.5,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := MergeEvidence(test.experiences)

			if result.IsEmpty() != test.expectedEmpty {
				t.Errorf("IsEmpty() = %v, expected %v", result.IsEmpty(), test.expectedEmpty)
			}

			if result.SampleCount != test.expectedSampleCount {
				t.Errorf("SampleCount = %v, expected %v", result.SampleCount, test.expectedSampleCount)
			}

			if !test.expectedEmpty && result.SuccessRate != test.expectedSuccessRate {
				t.Errorf("SuccessRate = %v, expected %v", result.SuccessRate, test.expectedSuccessRate)
			}

			if !test.expectedEmpty {
				if result.Confidence < 0 || result.Confidence > 1.0 {
					t.Errorf("Confidence out of range [0, 1]: %v", result.Confidence)
				}
				if result.ErrorRate < 0 || result.ErrorRate > 1.0 {
					t.Errorf("ErrorRate out of range [0, 1]: %v", result.ErrorRate)
				}
			}
		})
	}
}

func TestNormalizeExecutionEdgeCases(t *testing.T) {
	t.Run("negative max values", func(t *testing.T) {
		raw := ExecutionExperience{
			StrategyID: "strategy-test",
			TaskType:   "test",
			Success:    true,
			LatencyMs:  50,
			Timestamp:  time.Now(),
		}

		result := NormalizeExecution(raw, -100, -5, -1000, -50)
		if result.LatencyMs != 0.0 {
			t.Errorf("Expected latency to be clamped to 0 with negative max, got %v", result.LatencyMs)
		}
	})

	t.Run("zero latency", func(t *testing.T) {
		raw := ExecutionExperience{
			StrategyID: "strategy-zero",
			TaskType:   "test",
			Success:    true,
			LatencyMs:  0,
			Timestamp:  time.Now(),
		}

		result := NormalizeExecution(raw, 1000, 5, 1000, 1000)
		if result.LatencyMs != 1.0 {
			t.Errorf("Expected latency to be 1.0 for zero latency, got %v", result.LatencyMs)
		}
	})

	t.Run("full error rate", func(t *testing.T) {
		raw := ExecutionExperience{
			StrategyID: "strategy-error",
			TaskType:   "test",
			Success:    false,
			ErrorRate:  1.0,
			Timestamp:  time.Now(),
		}

		result := NormalizeExecution(raw, 1000, 5, 1000, 1000)
		if result.ErrorRate != 0.0 {
			t.Errorf("Expected error rate to be 0.0 for full error rate, got %v", result.ErrorRate)
		}
	})
}

func TestMergeEvidenceTimestampHandling(t *testing.T) {
	earlier := time.Now().Add(-2 * time.Hour)
	latest := time.Now()

	experiences := []NormalizedExecutionExperience{
		{
			StrategyID: "strategy-time",
			TaskType:   "test",
			Success:    1.0,
			Timestamp:  earlier,
		},
		{
			StrategyID: "strategy-time",
			TaskType:   "test",
			Success:    0.8,
			Timestamp:  latest,
		},
	}

	result := MergeEvidence(experiences)
	if !result.LastUpdated.Equal(latest) {
		t.Errorf("Expected LastUpdated to be %v, got %v", latest, result.LastUpdated)
	}
}