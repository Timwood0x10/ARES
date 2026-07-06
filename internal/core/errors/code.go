package errors

import "time"

// ErrorCode represents a structured error code.
type ErrorCode struct {
	Code       string        `json:"code"`
	Message    string        `json:"message"`
	Module     string        `json:"module"`
	Retry      bool          `json:"retry"`
	RetryMax   int           `json:"retry_max"`
	Backoff    time.Duration `json:"backoff"`
	HTTPStatus int           `json:"http_status"`
}

// NewErrorCode creates a new ErrorCode.
func NewErrorCode(code, message, module string, retry bool, retryMax int, backoff time.Duration, httpStatus int) *ErrorCode {
	return &ErrorCode{
		Code:       code,
		Message:    message,
		Module:     module,
		Retry:      retry,
		RetryMax:   retryMax,
		Backoff:    backoff,
		HTTPStatus: httpStatus,
	}
}

// AppError represents an application error with context.
type AppError struct {
	Code    *ErrorCode
	Err     error
	Context map[string]any
}

// Error returns the error message.
func (e *AppError) Error() string {
	if e.Code == nil {
		if e.Err != nil {
			return e.Err.Error()
		}
		return "unknown error"
	}
	if e.Err != nil {
		return e.Code.Message + ": " + e.Err.Error()
	}
	return e.Code.Message
}

// Unwrap returns the underlying error.
func (e *AppError) Unwrap() error {
	return e.Err
}

// WithContext adds context to the error.
func (e *AppError) WithContext(key string, value any) *AppError {
	if e.Context == nil {
		e.Context = make(map[string]any)
	}
	e.Context[key] = value
	return e
}

// IsRetryable checks if the error is retryable.
func (e *AppError) IsRetryable() bool {
	if e.Code == nil {
		return false
	}
	return e.Code.Retry
}

// ShouldRetry checks if the error should be retried based on attempt count.
func (e *AppError) ShouldRetry(attempt int) bool {
	if e.Code == nil {
		return false
	}
	if !e.Code.Retry {
		return false
	}
	return attempt < e.Code.RetryMax
}

// Wrap wraps an error with the error code.
func Wrap(err error, code *ErrorCode) *AppError {
	return &AppError{
		Code: code,
		Err:  err,
	}
}

// New creates a new AppError.
func New(code *ErrorCode) *AppError {
	return &AppError{
		Code: code,
	}
}
