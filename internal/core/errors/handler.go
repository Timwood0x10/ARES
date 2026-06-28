package errors

import (
	"context"
	"log/slog"
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

// HandleError handles an error with retry logic.
func (h *Handler) HandleError(ctx context.Context, appErr *AppError, retryCount int) {
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
			slog.Error("Failed to send to DLQ", "error_code", code, "error", err)
		}
	}
}

// RetryWithBackoff performs retry with exponential backoff.
func (h *Handler) RetryWithBackoff(ctx context.Context, appErr *AppError, attempt int, fn func() error) error {
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
	if appErr, ok := err.(*AppError); ok {
		var sb strings.Builder
		sb.WriteString("[")
		sb.WriteString(appErr.Code.Code)
		sb.WriteString("] ")
		sb.WriteString(appErr.Error())
		return sb.String()
	}
	return err.Error()
}

// IsRetryable checks if an error is retryable.
func IsRetryable(err error) bool {
	if appErr, ok := err.(*AppError); ok {
		return appErr.IsRetryable()
	}
	return false
}
