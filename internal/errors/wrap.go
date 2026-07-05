// Package errors provides high-performance error wrapping utilities
// and all sentinel error definitions for the application.

package errors

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors for common domain conditions.
var (
	// ErrNotFound indicates the requested entity does not exist.
	ErrNotFound = errors.New("not found")

	// ErrInvalidConfig is returned when config is nil or invalid.
	ErrInvalidConfig = errors.New("invalid config")

	// ErrAlreadyExists is returned when a resource already exists.
	ErrAlreadyExists = errors.New("resource already exists")

	// ErrAccessDenied is returned when access to a resource is denied.
	ErrAccessDenied = errors.New("access denied")

	// ErrTimeout is returned when an operation times out.
	ErrTimeout = errors.New("operation timeout")

	// ErrInternal is returned for internal server errors.
	ErrInternal = errors.New("internal server error")

	// ErrNoMarketData indicates no market data is available for the requested symbol.
	ErrNoMarketData = errors.New("no market data available")
)

// Sentinel errors for Agent module.
var (
	ErrAgentNotFound       = fmt.Errorf("agent not found: %w", ErrNotFound)
	ErrAgentTimeout        = errors.New("agent execution timeout")
	ErrAgentPanic          = errors.New("agent internal panic")
	ErrTaskQueueFull       = errors.New("task queue is full")
	ErrDependencyCycle     = errors.New("task dependency cycle detected")
	ErrAgentNotReady       = errors.New("agent not ready")
	ErrAgentBusy           = errors.New("agent is busy")
	ErrAgentAlreadyStarted = errors.New("agent already started")
	ErrAgentNotRunning     = errors.New("agent not running")
	ErrQueueNotInitialized = errors.New("message queue not initialized")
	ErrToolNotFound        = fmt.Errorf("tool not found: %w", ErrNotFound)
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
	ErrRecordNotFound     = fmt.Errorf("record not found: %w", ErrNotFound)
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
	ErrWorkflowNotFound     = fmt.Errorf("workflow not found: %w", ErrNotFound)
	ErrWorkflowLoadFailed   = errors.New("workflow load failed")
	ErrWorkflowCyclicDAG    = errors.New("workflow has cyclic dependency")
	ErrWorkflowInvalidPhase = errors.New("invalid workflow phase")
)

// Sentinel errors for Rate Limiter.
var (
	ErrBackpressureTriggered = errors.New("backpressure triggered")
	ErrTokenBucketExhausted  = errors.New("token bucket exhausted")
)

// New creates a new error with the given message.
func New(message string) error {
	return &wrappedError{
		msg: message,
		err: nil,
	}
}

// Newf creates a new error with a formatted message.
func Newf(format string, args ...any) error {
	return &wrappedError{
		msg: fmt.Sprintf(format, args...),
		err: nil,
	}
}

// Wrap wraps an error with a context message without format string parsing.
// This is more efficient than fmt.Errorf for high-frequency error paths.
//
// Usage:
//
//	return Wrap(err, "operation name")
//	return Wrap(err, "operation name: additional context")
func Wrap(err error, message string) error {
	if err == nil {
		return nil
	}
	if message == "" {
		return err
	}
	return &wrappedError{
		msg: message,
		err: err,
	}
}

// WrapError wraps an error with another error (for %w: %w pattern).
// This is used when you want to chain two errors together.
func WrapError(baseErr, wrapErr error) error {
	if wrapErr == nil {
		return baseErr
	}
	if baseErr == nil {
		return wrapErr
	}
	return &wrappedError{
		msg: baseErr.Error(),
		err: wrapErr,
	}
}

// Wrapf wraps an error with a formatted message (use sparingly).
// This should only be used when format string is necessary.
// For simple concatenation, use Wrap instead.
func Wrapf(err error, format string, args ...any) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf(format+": %w", append(args, err)...)
}

// FormatError creates a new error with a formatted message using %w for error wrapping.
// This is used when you want to format an error with additional context.
func FormatError(baseErr error, format string, args ...any) error {
	if baseErr == nil {
		return nil
	}
	// Check if format string contains %w
	if strings.Contains(format, "%w") {
		// Replace %w with %s for formatting, then wrap the error
		formatWithoutW := strings.ReplaceAll(format, "%w", "%s")
		message := fmt.Sprintf(formatWithoutW, append(args, baseErr.Error())...)
		return &wrappedError{
			msg: message,
			err: baseErr,
		}
	}
	// If no %w, just format the message
	message := fmt.Sprintf(format, args...)
	return &wrappedError{
		msg: message,
		err: baseErr,
	}
}

// wrappedError is a lightweight error wrapper.
type wrappedError struct {
	msg string
	err error
}

func (w *wrappedError) Error() string {
	if w.err == nil {
		return w.msg
	}
	var b strings.Builder
	b.Grow(len(w.msg) + 2 + len(w.err.Error()))
	b.WriteString(w.msg)
	b.WriteString(": ")
	b.WriteString(w.err.Error())
	return b.String()
}

func (w *wrappedError) Unwrap() error {
	return w.err
}
