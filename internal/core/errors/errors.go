package errors

import "errors"

// Sentinel errors for Agent module.
var (
	ErrAgentNotFound       = errors.New("agent not found")
	ErrAgentTimeout        = errors.New("agent execution timeout")
	ErrAgentPanic          = errors.New("agent internal panic")
	ErrTaskQueueFull       = errors.New("task queue is full")
	ErrDependencyCycle     = errors.New("task dependency cycle detected")
	ErrAgentNotReady       = errors.New("agent not ready")
	ErrAgentBusy           = errors.New("agent is busy")
	ErrAgentAlreadyStarted = errors.New("agent already started")
	ErrAgentNotRunning     = errors.New("agent not running")
	ErrQueueNotInitialized = errors.New("message queue not initialized")
	ErrToolNotFound        = errors.New("tool not found")
	ErrMaxStepsExceeded    = errors.New("agent max steps exceeded")
)

// Sentinel errors for Protocol module.
var (
	ErrInvalidMessage  = errors.New("invalid message format")
	ErrMessageTimeout  = errors.New("message send timeout")
	ErrHeartbeatMissed = errors.New("heartbeat missed")
	ErrQueueFull       = errors.New("message queue is full")
	ErrQueueEmpty      = errors.New("message queue is empty")
	ErrQueueClosed     = errors.New("message queue is closed")
)

// Sentinel errors for Storage module.
var (
	ErrDBConnectionFailed = errors.New("database connection failed")
	ErrQueryFailed        = errors.New("query execution failed")
	ErrVectorSearchFailed = errors.New("vector search failed")
	ErrRecordNotFound     = errors.New("record not found")
	ErrTransactionFailed  = errors.New("transaction failed")
	ErrNoTransaction      = errors.New("no active transaction")
	ErrInvalidArgument    = errors.New("invalid argument provided")
	ErrCircuitBreakerOpen = errors.New("circuit breaker is open")
	ErrServiceUnavailable = errors.New("service is temporarily unavailable")
	ErrInvalidState       = errors.New("invalid state")
	ErrSecretExpired      = errors.New("secret has expired")
	ErrNotImplemented     = errors.New("feature not implemented yet")
	ErrBufferFull         = errors.New("write buffer is full")
)

// Sentinel errors for LLM module.
var (
	ErrLLMRequestFailed    = errors.New("LLM request failed")
	ErrLLMTimeout          = errors.New("LLM response timeout")
	ErrLLMQuotaExceeded    = errors.New("LLM quota exceeded")
	ErrLLMInvalidResponse  = errors.New("LLM invalid response")
	ErrLLMParserFailed     = errors.New("LLM output parsing failed")
	ErrLLMValidationFailed = errors.New("LLM output validation failed")
)

// Sentinel errors for Rate Limiting module.
var (
	ErrRateLimitExceeded = errors.New("rate limit exceeded")
	ErrDBTimeout         = errors.New("database operation timeout")
)

// Sentinel errors for Parameter validation.
var (
	ErrInvalidUserID = errors.New("invalid user ID")
	ErrInvalidAge    = errors.New("invalid age")
	ErrInvalidBudget = errors.New("invalid budget range")
	ErrInvalidInput  = errors.New("invalid input parameter")
	ErrNilPointer    = errors.New("nil pointer error")
)

// Sentinel errors for parsing and retry.
var (
	ErrProfileParsingFailed        = errors.New("profile parsing failed")
	ErrProfileValidationFailed     = errors.New("profile validation failed")
	ErrMaxRetriesExceeded          = errors.New("max retries exceeded")
	ErrTaskExecutionFailed         = errors.New("task execution failed")
	ErrPromptRenderFailed          = errors.New("prompt render failed")
	ErrLLMGenerateFailed           = errors.New("LLM generate failed")
	ErrTaskPlannerNotInitialized   = errors.New("task planner not initialized")
	ErrProfileParserNotInitialized = errors.New("profile parser not initialized")
	ErrDispatchNotInitialized      = errors.New("task dispatcher not initialized")
	ErrResultAggNotInitialized     = errors.New("result aggregator not initialized")
	ErrDispatchFailed              = errors.New("task dispatch failed")
)

// Sentinel errors for Workflow module.
var (
	ErrWorkflowNotFound     = errors.New("workflow not found")
	ErrWorkflowLoadFailed   = errors.New("workflow load failed")
	ErrWorkflowCyclicDAG    = errors.New("workflow has cyclic dependency")
	ErrWorkflowInvalidPhase = errors.New("invalid workflow phase")
)

// Sentinel errors for Rate Limiter.
var (
	ErrBackpressureTriggered = errors.New("backpressure triggered")
	ErrTokenBucketExhausted  = errors.New("token bucket exhausted")
)
