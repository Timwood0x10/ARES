package errors

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Handler handles errors with retry and DLQ logic.
type Handler struct {
	dlq       DLQFunc
	alertFunc AlertFunc
}

// DLQFunc defines the function to send error to DLQ.
type DLQFunc func(ctx context.Context, msg *DLQMessage) error

// AlertFunc defines the function to send alert.
type AlertFunc func(ctx context.Context, message string)

// DLQMessage represents a message for dead letter queue.
type DLQMessage struct {
	ErrorCode  string
	Error      error
	Context    map[string]any
	Timestamp  time.Time
	RetryCount int
}

// NewHandler creates a new error handler.
func NewHandler(dlq DLQFunc, alertFunc AlertFunc) *Handler {
	return &Handler{
		dlq:       dlq,
		alertFunc: alertFunc,
	}
}

// HandleError handles an error with retry logic. Uses errors.As internally to
// handle both bare *AppError and wrapped errors (P1-11).
func (h *Handler) HandleError(ctx context.Context, err error, retryCount int) {
	// Guard against nil.
	if err == nil {
		return
	}
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code == nil {
		return
	}

	code := appErr.Code.Code

	// Check if should alert
	if ShouldAlert(code) && h.alertFunc != nil {
		h.alertFunc(ctx, GetAlertMessage(code))
	}

	// Check if should send to DLQ
	if !appErr.ShouldRetry(retryCount) && ShouldDLQ(code) && h.dlq != nil {
		dlqMsg := &DLQMessage{
			ErrorCode:  code,
			Error:      appErr,
			Context:    appErr.Context,
			Timestamp:  time.Now(),
			RetryCount: retryCount,
		}
		if err := h.dlq(ctx, dlqMsg); err != nil {
			log.Error("Failed to send to DLQ", "error_code", code, "error", err)
		}
	}
}

// RetryWithBackoff performs a single retry attempt with exponential backoff.
// The caller is expected to invoke this in a loop, incrementing attempt each
// iteration, until it returns nil or a non-retryable error. This function does
// NOT loop internally; it applies the backoff delay (for attempt > 0) and then
// calls fn exactly once.
func (h *Handler) RetryWithBackoff(ctx context.Context, appErr *AppError, attempt int, fn func() error) error {
	if appErr == nil || appErr.Code == nil {
		return appErr
	}
	if !appErr.ShouldRetry(attempt) {
		return appErr
	}

	strategy := GetStrategy(appErr.Code.Code)

	// Only apply backoff on retry attempts (attempt > 0), not on first attempt
	if attempt > 0 {
		// Exponential backoff: base * 2^(attempt-1)
		// Cap at maxBackoff to prevent excessive waiting
		maxBackoff := 30 * time.Second
		backoff := strategy.Backoff * time.Duration(1<<(attempt-1))
		if backoff > maxBackoff {
			backoff = maxBackoff
		}

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
			// Continue to next attempt
		}
	}

	return fn()
}

// FormatError formats an error for logging or display.
func FormatError(err error) string {
	if err == nil {
		return "<nil>"
	}
	var appErr *AppError
	if errors.As(err, &appErr) {
		// Guard against nil Code which would panic when accessing Code.Code.
		if appErr.Code == nil {
			return appErr.Error()
		}
		var sb strings.Builder
		sb.WriteString("[")
		sb.WriteString(appErr.Code.Code)
		sb.WriteString("] ")
		sb.WriteString(appErr.Error())
		return sb.String()
	}
	return err.Error()
}

// IsRetryable checks if an error is retryable by traversing the error chain
// with errors.As. Handles both bare *AppError and wrapped errors (P1-11).
func IsRetryable(err error) bool {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.IsRetryable()
	}
	return false
}
