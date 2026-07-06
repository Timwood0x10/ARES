package errors

import "time"

// ErrorStrategy defines retry and handling strategy for error codes.
//
// Example configuration:
//
//	{
//	  "code": "01-002",
//	  "backoff": "5s",
//	  "maxRetries": 3,
//	  "dlqEnabled": true,
//	  "alertEnabled": false,
//	  "alertMessage": ""
//	}
//
// Fields:
//   - Backoff: Wait duration before retry
//   - MaxRetries: Maximum number of retry attempts (0 = no retry)
//   - DLQEnabled: Send to Dead Letter Queue on final failure
//   - AlertEnabled: Trigger alert notification
//   - AlertMessage: Custom alert message (optional)
type ErrorStrategy struct {
	Backoff      time.Duration
	MaxRetries   int
	DLQEnabled   bool
	AlertEnabled bool
	AlertMessage string
}

// ShouldDLQ checks if the error should be sent to DLQ.
// Uses the configurable strategy registry.
func ShouldDLQ(code string) bool {
	return GetStrategy(code).DLQEnabled
}

// ShouldAlert checks if the error should trigger an alert.
// Uses the configurable strategy registry.
func ShouldAlert(code string) bool {
	return GetStrategy(code).AlertEnabled
}

// GetAlertMessage returns the alert message for the error code.
// Uses the configurable strategy registry.
func GetAlertMessage(code string) string {
	return GetStrategy(code).AlertMessage
}
