package core

import (
	"encoding/json"
	"time"
)

// Result represents the result of a tool execution.
type Result struct {
	Success  bool                   `json:"success"`
	Data     interface{}            `json:"data,omitempty"`
	Error    string                 `json:"error,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// NewResult creates a new Result.
func NewResult(success bool, data interface{}) Result {
	return Result{
		Success: success,
		Data:    data,
	}
}

// NewErrorResult creates a new error Result.
func NewErrorResult(err string) Result {
	return Result{
		Success: false,
		Error:   err,
	}
}

// WithMetadata adds metadata to the result.
func (r *Result) WithMetadata(key string, value interface{}) *Result {
	if r.Metadata == nil {
		r.Metadata = make(map[string]interface{})
	}
	r.Metadata[key] = value
	return r
}

// String returns a string representation.
func (r *Result) String() string {
	if r.Success {
		return "Success"
	}
	return "Error: " + r.Error
}

// ToJSON returns JSON representation.
func (r *Result) ToJSON() (string, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ResultWithTiming adds timing information to result.
func ResultWithTiming(result Result, duration time.Duration) Result {
	if result.Metadata == nil {
		result.Metadata = make(map[string]interface{})
	}
	result.Metadata["duration_ms"] = duration.Milliseconds()
	result.Metadata["executed_at"] = time.Now().Unix()
	return result
}

// ResultList holds multiple results.
type ResultList struct {
	Results []Result `json:"results"`
	Total   int      `json:"total"`
	Success int      `json:"success"`
	Failed  int      `json:"failed"`
}

// NewResultList creates a new ResultList.
func NewResultList() *ResultList {
	return &ResultList{
		Results: make([]Result, 0),
	}
}

// Add adds a result to the list.
func (l *ResultList) Add(result Result) {
	l.Results = append(l.Results, result)
	l.Total++
	if result.Success {
		l.Success++
	} else {
		l.Failed++
	}
}

// ErrorResult represents an error with code.
type ErrorResult struct {
	Code    string                 `json:"code"`
	Message string                 `json:"message"`
	Details map[string]interface{} `json:"details,omitempty"`
}

// NewErrorResultWithCode creates a new ErrorResult.
func NewErrorResultWithCode(code, message string) *ErrorResult {
	return &ErrorResult{
		Code:    code,
		Message: message,
	}
}

// WithDetails adds details to the error.
func (e *ErrorResult) WithDetails(details map[string]interface{}) *ErrorResult {
	e.Details = details
	return e
}

// Error returns the error message.
func (e *ErrorResult) Error() string {
	return e.Message
}

// ToResult converts to standard Result.
func (e *ErrorResult) ToResult() Result {
	return Result{
		Success: false,
		Error:   e.Message,
		Metadata: map[string]interface{}{
			"code": e.Code,
		},
	}
}

// ValidationError represents a validation error.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// NewValidationError creates a new ValidationError.
func NewValidationError(field, message string) *ValidationError {
	return &ValidationError{
		Field:   field,
		Message: message,
	}
}

// Error returns the error message.
func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Message
}
