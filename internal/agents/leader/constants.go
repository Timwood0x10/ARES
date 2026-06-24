package leader

import "time"

// Default configuration constants for LeaderAgent.
const (
	// DefaultMaxSteps is the default maximum number of steps allowed per request.
	DefaultMaxSteps = 10

	// DefaultMaxParallelTasks is the default maximum number of tasks that can run in parallel.
	DefaultMaxParallelTasks = 10

	// DefaultRetryAttempts is the default number of retry attempts for failed operations.
	DefaultRetryAttempts = 3

	// DefaultSimilarTasksLimit is the default limit for similar task search results.
	DefaultSimilarTasksLimit = 3

	// DefaultSimilarityThreshold is the default similarity threshold for task matching.
	// Tasks with similarity below this value will be filtered out.
	DefaultSimilarityThreshold = 0.5

	// DefaultContextHistoryLength is the default maximum number of messages to keep in context.
	DefaultContextHistoryLength = 10

	// DefaultSummaryLength is the default maximum length for result summaries in characters.
	DefaultSummaryLength = 200
)

// Planner defaults.
const (
	// DefaultMaxTasks is the default maximum number of tasks per planning phase.
	DefaultMaxTasks = 5
)

// Dispatcher defaults.
const (
	// DefaultMaxParallel is the default maximum number of parallel task executions.
	DefaultMaxParallel = 10

	// DefaultDispatcherTimeoutSeconds is the default timeout in seconds for dispatch operations.
	DefaultDispatcherTimeoutSeconds = 300
)

// Aggregator defaults.
const (
	// DefaultMaxItems is the default maximum number of items in an aggregated result.
	DefaultMaxItems = 20
)

// Loop defaults for LeaderAgentConfig.
const (
	// DefaultMaxIterations is the default maximum number of loop iterations.
	DefaultMaxIterations = 3

	// DefaultQualityThreshold is the default minimum quality score to accept result.
	DefaultQualityThreshold = 0.7

	// DefaultMaxTotalLLMCalls is the default maximum total LLM calls across all iterations.
	DefaultMaxTotalLLMCalls = 50

	// DefaultMaxLoopDuration is the default maximum duration for the entire loop.
	DefaultMaxLoopDuration = 10 * time.Minute
)

// Channel defaults.
const (
	// DefaultEventChanSize is the default buffer size for agent event channels.
	DefaultEventChanSize = 64
)

// Distillation defaults.
const (
	// DefaultDistillTimeout is the default timeout for distillation operations.
	DefaultDistillTimeout = 2 * time.Minute
)

// Timeout constants for LeaderAgent operations.
const (
	// DefaultTaskTimeout is the default timeout for task execution.
	DefaultTaskTimeout = 5 * time.Minute

	// DefaultDispatchTimeoutDuration is the default timeout for task dispatch operations.
	DefaultDispatchTimeoutDuration = 2 * time.Minute

	// DefaultAggregationTimeout is the default timeout for result aggregation.
	DefaultAggregationTimeout = 1 * time.Minute
)
