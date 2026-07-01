// Package experience provides types for managing evolution experiences.
// This file defines RawExperience and NormalizerConfig used by the normalizer.
package experience

import (
	"log/slog"
	"sort"
	"time"
)

// RawExperience represents unprocessed experience data collected from various sources.
// It contains fields that may be incomplete, have inconsistent types, or require
// normalization before being used in the evolution system.
type RawExperience struct {
	// StrategyID is the unique identifier of the strategy that generated this experience.
	StrategyID string

	// TaskType categorizes the type of task (e.g., "code_generation", "analysis").
	TaskType string

	// Timestamp is when the experience was recorded.
	Timestamp time.Time

	// Score is the fitness score achieved (may be in various ranges).
	Score interface{}

	// Latency is the execution latency (may be in various units).
	Latency interface{}

	// WallTime is the wall-clock execution time (may be in various units).
	WallTime interface{}

	// ErrorRate is the error rate (may be percentage or fraction).
	ErrorRate interface{}

	// Success indicates whether the task was completed successfully.
	Success interface{}

	// Cost is the computational cost incurred.
	Cost interface{}

	// MutationType describes the type of mutation used.
	MutationType string

	// Metadata holds additional unstructured data.
	Metadata map[string]interface{}
}

// NormalizerConfig holds configuration parameters for the normalizer.
type NormalizerConfig struct {
	// MaxLatencyMs is the maximum acceptable latency in milliseconds.
	// Experiences with latency above this threshold are filtered as noise.
	// Default: 10000 (10 seconds).
	MaxLatencyMs int64

	// MaxErrorRate is the maximum acceptable error rate [0, 1].
	// Experiences with error rate above this threshold are filtered as noise.
	// Default: 1.0 (disabled).
	MaxErrorRate float64

	// DedupWindowMinutes is the time window in minutes for deduplication.
	// Experiences with same strategy_id + task_type within this window are duplicates.
	// Default: 1 minute.
	DedupWindowMinutes int

	// DefaultScore is the default score for missing values.
	// Default: 0.0.
	DefaultScore float64

	// DefaultLatencyMs is the default latency for missing values.
	// Default: 0.
	DefaultLatencyMs int64

	// DefaultWallTimeSeconds is the default wall time for missing values.
	// Default: 0.0.
	DefaultWallTimeSeconds float64

	// DefaultErrorRate is the default error rate for missing values.
	// Default: 0.0.
	DefaultErrorRate float64

	// DefaultSuccess is the default success value for missing values.
	// Default: false.
	DefaultSuccess bool

	// DefaultCost is the default cost for missing values.
	// Default: 0.0.
	DefaultCost float64
}

// DefaultNormalizerConfig returns a NormalizerConfig with sensible defaults.
func DefaultNormalizerConfig() *NormalizerConfig {
	return &NormalizerConfig{
		MaxLatencyMs:           10000, // 10 seconds
		MaxErrorRate:           1.0,   // Allow up to 100% error rate (disabled)
		DedupWindowMinutes:     1,
		DefaultScore:           0.0,
		DefaultLatencyMs:       0,
		DefaultWallTimeSeconds: 0.0,
		DefaultErrorRate:       0.0,
		DefaultSuccess:         false,
		DefaultCost:            0.0,
	}
}

// ToolCallRecord represents raw execution data from tool calls.
// It captures detailed metrics about individual tool invocations including
// timing, success/failure status, and result characteristics.
// This is used by the GA/Memory/Tool fusion system to track tool performance.
type ToolCallRecord struct {
	// StrategyID is the identifier of the strategy that initiated this tool call.
	StrategyID string `json:"strategy_id"`

	// TaskType is the type of task being executed (e.g., "code_generation", "analysis").
	TaskType string `json:"task_type"`

	// ToolName is the name of the tool that was invoked.
	ToolName string `json:"tool_name"`

	// InputSummary is a concise summary of the input parameters.
	// This is a truncated or hashed representation to avoid excessive storage.
	InputSummary string `json:"input_summary"`

	// OutputSummary is a concise summary of the output or result.
	// This captures key information without storing full responses.
	OutputSummary string `json:"output_summary"`

	// ErrorCode is the error code if the tool call failed, empty on success.
	// This follows standard error code conventions (e.g., "TIMEOUT", "INVALID_INPUT").
	ErrorCode string `json:"error_code"`

	// LatencyMs is the execution latency in milliseconds.
	LatencyMs int64 `json:"latency_ms"`

	// Success indicates whether the tool call completed successfully.
	Success bool `json:"success"`

	// Timestamp is when the tool call was initiated.
	Timestamp time.Time `json:"timestamp"`

	// RetryCount is the number of retry attempts made for this tool call.
	RetryCount int `json:"retry_count"`

	// ResultSizeBytes is the size of the result in bytes.
	ResultSizeBytes int64 `json:"result_size_bytes"`
}

// ExecutionExperience represents raw execution metrics from agent runtime.
// It aggregates data from tool calls and execution traces to provide
// a complete picture of a strategy's performance on a specific task.
// This is used by the GA/Memory/Tool fusion system for strategy evaluation.
type ExecutionExperience struct {
	// StrategyID is the identifier of the strategy used.
	StrategyID string `json:"strategy_id"`

	// TaskType is the type of task being executed.
	TaskType string `json:"task_type"`

	// Success indicates whether the overall task completed successfully.
	Success bool `json:"success"`

	// LatencyMs is the total execution latency in milliseconds.
	LatencyMs int64 `json:"latency_ms"`

	// RetryCount is the total number of retries across all operations.
	RetryCount int `json:"retry_count"`

	// ErrorRate is the proportion of operations that resulted in errors (0.0 to 1.0).
	ErrorRate float64 `json:"error_rate"`

	// ToolChain is a hash string representing the sequence of tools used.
	// This enables grouping experiences by tool usage patterns.
	ToolChain string `json:"tool_chain"`

	// ResultQuality is a quality score of the final result (0.0 to 1.0).
	ResultQuality float64 `json:"result_quality"`

	// TokenCost is the total token cost incurred during execution.
	TokenCost int64 `json:"token_cost"`

	// WallTime is the wall-clock time in milliseconds including waiting.
	WallTime int64 `json:"wall_time"`

	// Timestamp is when this experience was recorded.
	Timestamp time.Time `json:"timestamp"`
}

// NormalizedExecutionExperience represents a normalized version of ExecutionExperience.
// All values are scaled to standard ranges for consistent comparison
// and aggregation across different task types and strategies.
// This is used by the GA/Memory/Tool fusion system for evidence aggregation.
type NormalizedExecutionExperience struct {
	// StrategyID is the identifier of the strategy used.
	StrategyID string `json:"strategy_id"`

	// TaskType is the type of task being executed.
	TaskType string `json:"task_type"`

	// Success is the normalized success indicator (1.0 for success, 0.0 for failure).
	Success float64 `json:"success"`

	// LatencyMs is the normalized latency value (0.0 to 1.0, where 1.0 is best).
	// Higher values indicate better (faster) performance.
	LatencyMs float64 `json:"latency_ms"`

	// RetryCount is the normalized retry count (0.0 to 1.0, where 1.0 is best).
	// Higher values indicate fewer retries (better performance).
	RetryCount float64 `json:"retry_count"`

	// ErrorRate is the inverted and normalized error rate (0.0 to 1.0, where 1.0 is best).
	// Higher values indicate lower error rates.
	ErrorRate float64 `json:"error_rate"`

	// ToolChain is a hash string representing the sequence of tools used.
	ToolChain string `json:"tool_chain"`

	// ResultQuality is the normalized result quality (0.0 to 1.0).
	ResultQuality float64 `json:"result_quality"`

	// TokenCost is the normalized token cost (0.0 to 1.0, where 1.0 is best).
	// Higher values indicate lower cost (better efficiency).
	TokenCost float64 `json:"token_cost"`

	// WallTime is the normalized wall-clock time (0.0 to 1.0, where 1.0 is best).
	// Higher values indicate shorter execution time.
	WallTime float64 `json:"wall_time"`

	// Timestamp is when this experience was recorded.
	Timestamp time.Time `json:"timestamp"`
}

// Evidence represents multi-dimensional aggregated statistics.
// It aggregates multiple execution experiences into statistical summaries
// to support strategy evaluation and selection decisions.
// This is used by the GA/Memory/Tool fusion system for strategy comparison.
type Evidence struct {
	// StrategyID is the identifier of the strategy this evidence applies to.
	StrategyID string `json:"strategy_id"`

	// TaskType is the type of task this evidence covers.
	TaskType string `json:"task_type"`

	// SuccessRate is the proportion of successful executions (0.0 to 1.0).
	SuccessRate float64 `json:"success_rate"`

	// LatencyP50 is the 50th percentile (median) latency in milliseconds.
	LatencyP50 int64 `json:"latency_p50"`

	// LatencyP95 is the 95th percentile latency in milliseconds.
	LatencyP95 int64 `json:"latency_p95"`

	// ErrorRate is the overall proportion of errors encountered (0.0 to 1.0).
	ErrorRate float64 `json:"error_rate"`

	// SampleCount is the number of experiences aggregated into this evidence.
	SampleCount int64 `json:"sample_count"`

	// ToolChainHash is a hash representing the most common tool chain pattern.
	ToolChainHash string `json:"tool_chain_hash"`

	// Confidence is the confidence level of this evidence (0.0 to 1.0).
	// Higher values indicate more reliable statistics based on larger sample sizes.
	Confidence float64 `json:"confidence"`

	// LastUpdated is when this evidence was last updated.
	LastUpdated time.Time `json:"last_updated"`
}

// NormalizeExecution converts an ExecutionExperience to a NormalizedExecutionExperience.
// It applies min-max normalization to all numeric fields, scaling them
// to the [0.0, 1.0] range. For metrics where lower is better (latency,
// retry count, error rate, token cost, wall time), the values are inverted
// so that higher normalized values always indicate better performance.
//
// Parameters:
//   - raw: The raw execution experience to normalize
//   - maxLatencyMs: Maximum expected latency for normalization
//   - maxRetryCount: Maximum expected retry count for normalization
//   - maxTokenCost: Maximum expected token cost for normalization
//   - maxWallTime: Maximum expected wall time for normalization
//
// Returns a NormalizedExecutionExperience with all values scaled to [0.0, 1.0].
func NormalizeExecution(
	raw ExecutionExperience,
	maxLatencyMs int64,
	maxRetryCount int,
	maxTokenCost int64,
	maxWallTime int64,
) NormalizedExecutionExperience {
	if maxLatencyMs <= 0 {
		maxLatencyMs = 1
	}
	if maxRetryCount <= 0 {
		maxRetryCount = 1
	}
	if maxTokenCost <= 0 {
		maxTokenCost = 1
	}
	if maxWallTime <= 0 {
		maxWallTime = 1
	}

	normalizedLatency := 1.0 - float64(raw.LatencyMs)/float64(maxLatencyMs)
	if normalizedLatency < 0 {
		normalizedLatency = 0
	}

	normalizedRetry := 1.0 - float64(raw.RetryCount)/float64(maxRetryCount)
	if normalizedRetry < 0 {
		normalizedRetry = 0
	}

	normalizedErrorRate := 1.0 - raw.ErrorRate
	if normalizedErrorRate < 0 {
		normalizedErrorRate = 0
	}

	normalizedTokenCost := 1.0 - float64(raw.TokenCost)/float64(maxTokenCost)
	if normalizedTokenCost < 0 {
		normalizedTokenCost = 0
	}

	normalizedWallTime := 1.0 - float64(raw.WallTime)/float64(maxWallTime)
	if normalizedWallTime < 0 {
		normalizedWallTime = 0
	}

	var successValue float64
	if raw.Success {
		successValue = 1.0
	}

	return NormalizedExecutionExperience{
		StrategyID:    raw.StrategyID,
		TaskType:      raw.TaskType,
		Success:       successValue,
		LatencyMs:     normalizedLatency,
		RetryCount:    normalizedRetry,
		ErrorRate:     normalizedErrorRate,
		ToolChain:     raw.ToolChain,
		ResultQuality: raw.ResultQuality,
		TokenCost:     normalizedTokenCost,
		WallTime:      normalizedWallTime,
		Timestamp:     raw.Timestamp,
	}
}

// IsEmpty returns true if the ToolCallRecord has no meaningful data.
// A record is considered empty if StrategyID and ToolName are both empty.
func (r *ToolCallRecord) IsEmpty() bool {
	return r.StrategyID == "" && r.ToolName == ""
}

// IsEmpty returns true if the ExecutionExperience has no meaningful data.
// An experience is considered empty if StrategyID is empty.
func (e *ExecutionExperience) IsEmpty() bool {
	return e.StrategyID == ""
}

// IsEmpty returns true if the NormalizedExecutionExperience has no meaningful data.
// An experience is considered empty if StrategyID is empty.
func (e *NormalizedExecutionExperience) IsEmpty() bool {
	return e.StrategyID == ""
}

// IsEmpty returns true if the Evidence has no meaningful data.
// Evidence is considered empty if StrategyID is empty.
func (e *Evidence) IsEmpty() bool {
	return e.StrategyID == ""
}

// HasSamples returns true if the Evidence has at least one sample.
func (e *Evidence) HasSamples() bool {
	return e.SampleCount > 0
}

// MergeEvidence combines multiple NormalizedExecutionExperience values into a single Evidence.
// This aggregates normalized metrics and computes statistical summaries.
//
// Parameters:
//   - experiences: Slice of normalized execution experiences to merge
//
// Returns an Evidence struct containing aggregated statistics.
// Returns an empty Evidence if the input slice is empty.
func MergeEvidence(experiences []NormalizedExecutionExperience) Evidence {
	if len(experiences) == 0 {
		return Evidence{}
	}

	var (
		totalSuccess      float64
		totalLatencyNorm  float64
		totalErrorRate    float64
		totalQuality      float64
		strategyID        string
		taskType          string
		toolChainHash     string
		lastTimestamp     time.Time
		latencyNormValues []float64
	)

	for _, exp := range experiences {
		if exp.StrategyID != "" {
			if strategyID == "" {
				strategyID = exp.StrategyID
			} else if strategyID != exp.StrategyID {
				slog.Warn("MergeEvidence: mixed strategy IDs in batch",
					"first", strategyID, "encountered", exp.StrategyID)
			}
		}
		if exp.TaskType != "" {
			if taskType == "" {
				taskType = exp.TaskType
			} else if taskType != exp.TaskType {
				slog.Warn("MergeEvidence: mixed task types in batch",
					"first", taskType, "encountered", exp.TaskType)
			}
		}
		if exp.ToolChain != "" && toolChainHash == "" {
			toolChainHash = exp.ToolChain
		}
		if exp.Timestamp.After(lastTimestamp) {
			lastTimestamp = exp.Timestamp
		}

		totalSuccess += exp.Success
		totalLatencyNorm += exp.LatencyMs
		totalErrorRate += exp.ErrorRate
		totalQuality += exp.ResultQuality
		latencyNormValues = append(latencyNormValues, exp.LatencyMs)
	}

	count := float64(len(experiences))
	successRate := totalSuccess / count
	avgErrorRate := totalErrorRate / count

	confidence := calculateEvidenceConfidence(int64(len(experiences)))

	latencyP50, latencyP95 := calculateLatencyPercentilesFromNormalized(latencyNormValues)

	return Evidence{
		StrategyID:    strategyID,
		TaskType:      taskType,
		SuccessRate:   successRate,
		LatencyP50:    latencyP50,
		LatencyP95:    latencyP95,
		ErrorRate:     avgErrorRate,
		SampleCount:   int64(len(experiences)),
		ToolChainHash: toolChainHash,
		Confidence:    confidence,
		LastUpdated:   lastTimestamp,
	}
}

// calculateEvidenceConfidence computes the confidence level based on sample count.
// Uses a linear scale: confidence = (sampleCount + 1) / 15, capped at 1.0.
// Minimum confidence is 0.1 for a single sample.
func calculateEvidenceConfidence(sampleCount int64) float64 {
	if sampleCount <= 0 {
		return 0
	}
	confidence := float64(sampleCount+1) / 15.0
	if confidence > 1.0 {
		confidence = 1.0
	}
	if confidence < 0.1 {
		confidence = 0.1
	}
	return confidence
}

// calculateLatencyPercentilesFromNormalized computes p50 and p95 latency percentiles
// from a slice of normalized latency values.
// Since these are normalized (higher is better), we convert back to
// approximate millisecond values using a standard reference.
func calculateLatencyPercentilesFromNormalized(values []float64) (p50 int64, p95 int64) {
	if len(values) == 0 {
		return 0, 0
	}

	sorted := make([]float64, len(values))
	copy(sorted, values)
	sort.Float64s(sorted)

	p50Index := len(sorted) / 2
	p50Norm := sorted[p50Index]

	p95Index := int(float64(len(sorted)) * 0.95)
	if p95Index >= len(sorted) {
		p95Index = len(sorted) - 1
	}
	p95Norm := sorted[p95Index]

	const maxLatencyMs = 10000.0
	p50 = int64((1.0 - p50Norm) * maxLatencyMs)
	p95 = int64((1.0 - p95Norm) * maxLatencyMs)

	return p50, p95
}