// Package errors provides structured application error types and error
// strategy configuration. Sentinel error variables are re-exported from
// internal/errors so that errors.Is works across both packages.
package errors

import (
	apperrors "github.com/Timwood0x10/ares/internal/errors"
)

// Sentinel errors re-exported from internal/errors. Both packages share the
// same error identity so errors.Is(core_errors.X, internal_errors.X) is true.
// Do NOT redeclare these as new errors.New values — that breaks cross-package
// matching.
var (
	// ErrNotFound indicates the requested entity does not exist.
	ErrNotFound = apperrors.ErrNotFound

	// ErrInvalidConfig is returned when config is nil or invalid.
	ErrInvalidConfig = apperrors.ErrInvalidConfig

	// ErrAlreadyExists is returned when a resource already exists.
	ErrAlreadyExists = apperrors.ErrAlreadyExists

	// ErrAccessDenied is returned when access to a resource is denied.
	ErrAccessDenied = apperrors.ErrAccessDenied

	// ErrTimeout is returned when an operation times out.
	ErrTimeout = apperrors.ErrTimeout

	// ErrInternal is returned for internal server errors.
	ErrInternal = apperrors.ErrInternal

	// ErrNoMarketData indicates no market data is available for the requested symbol.
	ErrNoMarketData = apperrors.ErrNoMarketData
)

// Sentinel errors for Agent module.
var (
	ErrAgentNotFound       = apperrors.ErrAgentNotFound
	ErrAgentTimeout        = apperrors.ErrAgentTimeout
	ErrAgentPanic          = apperrors.ErrAgentPanic
	ErrTaskQueueFull       = apperrors.ErrTaskQueueFull
	ErrDependencyCycle     = apperrors.ErrDependencyCycle
	ErrAgentNotReady       = apperrors.ErrAgentNotReady
	ErrAgentBusy           = apperrors.ErrAgentBusy
	ErrAgentAlreadyStarted = apperrors.ErrAgentAlreadyStarted
	ErrAgentNotRunning     = apperrors.ErrAgentNotRunning
	ErrQueueNotInitialized = apperrors.ErrQueueNotInitialized
	ErrToolNotFound        = apperrors.ErrToolNotFound
	ErrMaxStepsExceeded    = apperrors.ErrMaxStepsExceeded
)

// Sentinel errors for Protocol module.
var (
	ErrInvalidMessage  = apperrors.ErrInvalidMessage
	ErrMessageTimeout  = apperrors.ErrMessageTimeout
	ErrHeartbeatMissed = apperrors.ErrHeartbeatMissed
	ErrQueueFull       = apperrors.ErrQueueFull
	ErrQueueEmpty      = apperrors.ErrQueueEmpty
	ErrQueueClosed     = apperrors.ErrQueueClosed
)

// Sentinel errors for Storage module.
var (
	ErrDBConnectionFailed = apperrors.ErrDBConnectionFailed
	ErrQueryFailed        = apperrors.ErrQueryFailed
	ErrVectorSearchFailed = apperrors.ErrVectorSearchFailed
	ErrRecordNotFound     = apperrors.ErrRecordNotFound
	ErrTransactionFailed  = apperrors.ErrTransactionFailed
	ErrNoTransaction      = apperrors.ErrNoTransaction
	ErrInvalidArgument    = apperrors.ErrInvalidArgument
	ErrCircuitBreakerOpen = apperrors.ErrCircuitBreakerOpen
	ErrServiceUnavailable = apperrors.ErrServiceUnavailable
	ErrInvalidState       = apperrors.ErrInvalidState
	ErrSecretExpired      = apperrors.ErrSecretExpired
	ErrNotImplemented     = apperrors.ErrNotImplemented
	ErrBufferFull         = apperrors.ErrBufferFull
)

// Sentinel errors for LLM module.
var (
	ErrLLMRequestFailed    = apperrors.ErrLLMRequestFailed
	ErrLLMTimeout          = apperrors.ErrLLMTimeout
	ErrLLMQuotaExceeded    = apperrors.ErrLLMQuotaExceeded
	ErrLLMInvalidResponse  = apperrors.ErrLLMInvalidResponse
	ErrLLMParserFailed     = apperrors.ErrLLMParserFailed
	ErrLLMValidationFailed = apperrors.ErrLLMValidationFailed
)

// Sentinel errors for Rate Limiting module.
var (
	ErrRateLimitExceeded = apperrors.ErrRateLimitExceeded
	ErrDBTimeout         = apperrors.ErrDBTimeout
)

// Sentinel errors for Parameter validation.
var (
	ErrInvalidUserID = apperrors.ErrInvalidUserID
	ErrInvalidAge    = apperrors.ErrInvalidAge
	ErrInvalidBudget = apperrors.ErrInvalidBudget
	ErrInvalidInput  = apperrors.ErrInvalidInput
	ErrNilPointer    = apperrors.ErrNilPointer
)

// Sentinel errors for parsing and retry.
var (
	ErrProfileParsingFailed        = apperrors.ErrProfileParsingFailed
	ErrProfileValidationFailed     = apperrors.ErrProfileValidationFailed
	ErrMaxRetriesExceeded          = apperrors.ErrMaxRetriesExceeded
	ErrTaskExecutionFailed         = apperrors.ErrTaskExecutionFailed
	ErrPromptRenderFailed          = apperrors.ErrPromptRenderFailed
	ErrLLMGenerateFailed           = apperrors.ErrLLMGenerateFailed
	ErrTaskPlannerNotInitialized   = apperrors.ErrTaskPlannerNotInitialized
	ErrProfileParserNotInitialized = apperrors.ErrProfileParserNotInitialized
	ErrDispatchNotInitialized      = apperrors.ErrDispatchNotInitialized
	ErrResultAggNotInitialized     = apperrors.ErrResultAggNotInitialized
	ErrDispatchFailed              = apperrors.ErrDispatchFailed
)

// Sentinel errors for Workflow module.
var (
	ErrWorkflowNotFound     = apperrors.ErrWorkflowNotFound
	ErrWorkflowLoadFailed   = apperrors.ErrWorkflowLoadFailed
	ErrWorkflowCyclicDAG    = apperrors.ErrWorkflowCyclicDAG
	ErrWorkflowInvalidPhase = apperrors.ErrWorkflowInvalidPhase
)

// Sentinel errors for Rate Limiter.
var (
	ErrBackpressureTriggered = apperrors.ErrBackpressureTriggered
	ErrTokenBucketExhausted  = apperrors.ErrTokenBucketExhausted
)
