// nolint: errcheck // Test code may ignore return values
package errors

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestErrorCode(t *testing.T) {
	t.Run("create error code", func(t *testing.T) {
		code := NewErrorCode("TEST_CODE", "test message", "test_module", true, 3, time.Second, 500)

		if code.Code != "TEST_CODE" {
			t.Errorf("expected TEST_CODE, got %s", code.Code)
		}
		if code.Message != "test message" {
			t.Errorf("expected test message, got %s", code.Message)
		}
		if code.Module != "test_module" {
			t.Errorf("expected test_module, got %s", code.Module)
		}
		if !code.Retry {
			t.Errorf("expected retry true")
		}
		if code.RetryMax != 3 {
			t.Errorf("expected 3 retries, got %d", code.RetryMax)
		}
		if code.Backoff != time.Second {
			t.Errorf("expected 1 second backoff")
		}
		if code.HTTPStatus != 500 {
			t.Errorf("expected 500, got %d", code.HTTPStatus)
		}
	})
}

func TestAppError(t *testing.T) {
	t.Run("create app error", func(t *testing.T) {
		code := NewErrorCode("TEST", "test", "01-003", false, 0, 0, 400)
		err := New(code)

		if err.Error() != "test" {
			t.Errorf("expected test, got %s", err.Error())
		}
	})

	t.Run("wrap error", func(t *testing.T) {
		inner := errors.New("inner error")
		code := NewErrorCode("TEST", "test", "01-003", false, 0, 0, 400)
		err := Wrap(inner, code)

		if err.Error() != "test: inner error" {
			t.Errorf("expected 'test: inner error', got %s", err.Error())
		}
	})

	t.Run("unwrap", func(t *testing.T) {
		inner := errors.New("inner error")
		code := NewErrorCode("TEST", "test", "01-003", false, 0, 0, 400)
		err := Wrap(inner, code)

		unwrapped := err.Unwrap()
		if unwrapped != inner {
			t.Errorf("expected unwrapped error")
		}
	})

	t.Run("with context", func(t *testing.T) {
		code := NewErrorCode("TEST", "test", "01-003", false, 0, 0, 400)
		err := New(code).WithContext("key", "value")

		if err.Context["key"] != "value" {
			t.Errorf("expected value in context")
		}
	})

	t.Run("is retryable", func(t *testing.T) {
		code := NewErrorCode("TEST", "test", "01-003", true, 0, 0, 400)
		err := New(code)

		if !err.IsRetryable() {
			t.Errorf("expected retryable")
		}
	})

	t.Run("should retry", func(t *testing.T) {
		code := NewErrorCode("TEST", "test", "01-003", true, 3, 0, 400)
		err := New(code)

		if !err.ShouldRetry(1) {
			t.Errorf("expected should retry at attempt 1")
		}
		if err.ShouldRetry(3) {
			t.Errorf("expected should not retry at attempt 3")
		}
	})

	t.Run("should not retry when retry is false", func(t *testing.T) {
		code := NewErrorCode("TEST", "test", "01-003", false, 0, 0, 400)
		err := New(code)

		if err.ShouldRetry(1) {
			t.Errorf("expected should not retry")
		}
	})
}

func TestStrategy(t *testing.T) {
	t.Run("get strategy", func(t *testing.T) {
		strategy := GetStrategy("ErrUserNotFound")

		if strategy.DLQEnabled {
			t.Errorf("expected DLQ false for user not found")
		}
	})

	t.Run("should dlq", func(t *testing.T) {
		if ShouldDLQ("ErrDatabase") {
			t.Errorf("expected false")
		}
	})

	t.Run("should alert", func(t *testing.T) {
		if ShouldAlert("ErrUserNotFound") {
			t.Errorf("expected false for user not found")
		}
	})
}

func TestHandler(t *testing.T) {
	t.Run("create handler", func(t *testing.T) {
		handler := NewHandler(nil, nil)

		if handler == nil {
			t.Errorf("expected handler")
		}
	})

	t.Run("handle error no retry", func(t *testing.T) {
		handler := NewHandler(nil, nil)
		code := NewErrorCode("TEST", "test", "01-003", false, 0, 0, 400)
		err := New(code)

		handler.HandleError(context.Background(), err, 0)
	})

	t.Run("retry with backoff success", func(t *testing.T) {
		handler := NewHandler(nil, nil)
		code := NewErrorCode("TEST", "test", "01-003", true, 3, time.Millisecond, 400)
		err := New(code)

		attempt := 0
		fn := func() error {
			attempt++
			return nil
		}

		resultErr := handler.RetryWithBackoff(context.Background(), err, 0, fn)
		if resultErr != nil {
			t.Errorf("expected no error")
		}
		if attempt != 1 {
			t.Errorf("expected 1 attempt, got %d", attempt)
		}
	})

	t.Run("retry with backoff failure", func(t *testing.T) {
		handler := NewHandler(nil, nil)
		code := NewErrorCode("TEST", "test", "01-003", true, 1, time.Millisecond, 400)
		err := New(code)

		attempt := 0
		fn := func() error {
			attempt++
			return errors.New("failed")
		}

		resultErr := handler.RetryWithBackoff(context.Background(), err, 0, fn)
		if resultErr == nil {
			t.Errorf("expected error")
		}
	})
}

func TestFormatError(t *testing.T) {
	t.Run("format app error", func(t *testing.T) {
		code := NewErrorCode("TEST", "test message", "01-003", false, 0, 0, 400)
		err := New(code)

		formatted := FormatError(err)
		if formatted == "" {
			t.Errorf("expected formatted error")
		}
	})

	t.Run("format standard error", func(t *testing.T) {
		err := errors.New("standard error")

		formatted := FormatError(err)
		if formatted != "standard error" {
			t.Errorf("expected standard error, got %s", formatted)
		}
	})
}

func TestIsRetryable(t *testing.T) {
	t.Run("retryable app error", func(t *testing.T) {
		code := NewErrorCode("TEST", "test", "01-003", true, 0, 0, 400)
		err := New(code)

		if !IsRetryable(err) {
			t.Errorf("expected retryable")
		}
	})

	t.Run("not retryable", func(t *testing.T) {
		err := errors.New("standard error")

		if IsRetryable(err) {
			t.Errorf("expected not retryable")
		}
	})
}

func TestSentinelErrors(t *testing.T) {
	t.Run("agent errors", func(t *testing.T) {
		if ErrAgentNotFound.Error() != "agent not found: not found" {
			t.Errorf("expected agent not found: not found")
		}
	})

	t.Run("protocol errors", func(t *testing.T) {
		if ErrInvalidMessage.Error() != "invalid message format" {
			t.Errorf("expected invalid message format")
		}
	})

	t.Run("storage errors", func(t *testing.T) {
		if ErrDBConnectionFailed.Error() != "database connection failed" {
			t.Errorf("expected database connection failed")
		}
	})

	t.Run("llm errors", func(t *testing.T) {
		if ErrLLMRequestFailed.Error() != "LLM request failed" {
			t.Errorf("expected LLM request failed")
		}
	})

	t.Run("validation errors", func(t *testing.T) {
		if ErrInvalidUserID.Error() != "invalid user ID" {
			t.Errorf("expected invalid user ID")
		}
	})

	t.Run("workflow errors", func(t *testing.T) {
		if ErrWorkflowNotFound.Error() != "workflow not found: not found" {
			t.Errorf("expected workflow not found: not found")
		}
	})

	t.Run("ratelimit errors", func(t *testing.T) {
		if ErrRateLimitExceeded.Error() != "rate limit exceeded" {
			t.Errorf("expected rate limit exceeded")
		}
	})
}
func TestStrategyFull(t *testing.T) {
	t.Run("get strategy found", func(t *testing.T) {
		strategy := GetStrategy("01-003")
		if strategy.Backoff == 0 {
			t.Errorf("expected backoff to be set")
		}
	})

	t.Run("get strategy not found", func(t *testing.T) {
		strategy := GetStrategy("UNKNOWN")
		if strategy.MaxRetries != 1 {
			t.Errorf("expected default strategy")
		}
	})

	t.Run("should dlq false", func(t *testing.T) {
		if ShouldDLQ("UNKNOWN") {
			t.Errorf("expected false for unknown")
		}
	})

	t.Run("should alert false", func(t *testing.T) {
		if ShouldAlert("UNKNOWN") {
			t.Errorf("expected false for unknown")
		}
	})
}

func TestHandlerFull(t *testing.T) {
	t.Run("handle error with alert", func(t *testing.T) {
		var alerted bool
		handler := NewHandler(nil, func(ctx context.Context, msg string) {
			alerted = true
		})
		code := NewErrorCode("01-003", "test", "01-003", false, 0, 0, 500)
		err := New(code)

		handler.HandleError(context.Background(), err, 0)

		if !alerted {
			t.Errorf("expected alert")
		}
	})

	t.Run("handle error with dlq", func(t *testing.T) {
		var dlqCalled bool
		handler := NewHandler(func(ctx context.Context, msg *DLQMessage) error {
			dlqCalled = true
			return nil
		}, nil)
		// 01-002 has DLQEnabled: true, but we need ShouldRetry to return false
		code := NewErrorCode("01-002", "test", "01-002", false, 0, 0, 500)
		err := New(code)

		handler.HandleError(context.Background(), err, 0)

		if !dlqCalled {
			t.Errorf("expected dlq to be called")
		}
	})

	t.Run("handle error dlq send failure", func(t *testing.T) {
		var dlqCalled bool
		handler := NewHandler(func(ctx context.Context, msg *DLQMessage) error {
			dlqCalled = true
			return errors.New("dlq error")
		}, nil)
		code := NewErrorCode("01-002", "test", "01-002", false, 0, 0, 500)
		err := New(code)

		handler.HandleError(context.Background(), err, 0)

		if !dlqCalled {
			t.Errorf("expected dlq to be called")
		}
	})

	t.Run("retry with backoff success", func(t *testing.T) {
		handler := NewHandler(nil, nil)
		code := NewErrorCode("01-002", "test", "01-002", true, 3, 1*time.Millisecond, 400)
		err := New(code)

		ctx := context.Background()
		fnCalled := false
		fn := func() error {
			fnCalled = true
			return nil
		}

		resultErr := handler.RetryWithBackoff(ctx, err, 0, fn)
		if resultErr != nil {
			t.Errorf("unexpected error: %v", resultErr)
		}
		if !fnCalled {
			t.Errorf("expected function to be called")
		}
	})

	t.Run("retry with backoff exceeds max retries", func(t *testing.T) {
		handler := NewHandler(nil, nil)
		code := NewErrorCode("01-002", "test", "01-002", true, 3, 1*time.Millisecond, 400)
		err := New(code)

		ctx := context.Background()
		fnCalled := false
		fn := func() error {
			fnCalled = true
			return nil
		}

		// attempt >= RetryMax, should return error without calling fn
		resultErr := handler.RetryWithBackoff(ctx, err, 5, fn)
		if resultErr == nil {
			t.Errorf("expected error when exceeding max retries")
		}
		if fnCalled {
			t.Errorf("expected function NOT to be called when exceeding max retries")
		}
	})
}

// nolint: errcheck // Test code may ignore return values
// nolint: errcheck // Test code may ignore return values
