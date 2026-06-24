package core

import (
	"encoding/json"
	"testing"
	"time"
)

// TestNewResult tests creating a new result.
func TestNewResult(t *testing.T) {
	tests := []struct {
		name    string
		success bool
		data    interface{}
	}{
		{
			name:    "successful result with data",
			success: true,
			data:    map[string]string{"key": "value"},
		},
		{
			name:    "failed result with data",
			success: false,
			data:    "error data",
		},
		{
			name:    "successful result with nil data",
			success: true,
			data:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NewResult(tt.success, tt.data)
			if result.Success != tt.success {
				t.Errorf("Success = %v, want %v", result.Success, tt.success)
			}
			// For map data, check if data is nil or not
			if tt.data != nil && result.Data == nil {
				t.Errorf("Data should not be nil")
			}
			if tt.data == nil && result.Data != nil {
				t.Errorf("Data should be nil")
			}
		})
	}
}

// TestNewErrorResult tests creating an error result.
func TestNewErrorResult(t *testing.T) {
	tests := []struct {
		name  string
		error string
	}{
		{
			name:  "error with message",
			error: "test error message",
		},
		{
			name:  "empty error message",
			error: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NewErrorResult(tt.error)
			if result.Success {
				t.Error("Success should be false for error result")
			}
			if result.Error != tt.error {
				t.Errorf("Error = %q, want %q", result.Error, tt.error)
			}
		})
	}
}

// TestResultWithMetadata tests adding metadata to result.
func TestResultWithMetadata(t *testing.T) {
	tests := []struct {
		name     string
		result   Result
		key      string
		value    interface{}
		wantSize int
	}{
		{
			name: "add metadata to result with nil metadata",
			result: Result{
				Success: true,
				Data:    "test",
			},
			key:      "key1",
			value:    "value1",
			wantSize: 1,
		},
		{
			name: "add metadata to result with existing metadata",
			result: Result{
				Success: true,
				Data:    "test",
				Metadata: map[string]interface{}{
					"existing": "value",
				},
			},
			key:      "new_key",
			value:    "new_value",
			wantSize: 2,
		},
		{
			name: "overwrite existing metadata key",
			result: Result{
				Success: true,
				Data:    "test",
				Metadata: map[string]interface{}{
					"key": "old_value",
				},
			},
			key:      "key",
			value:    "new_value",
			wantSize: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.result.WithMetadata(tt.key, tt.value)
			if len(tt.result.Metadata) != tt.wantSize {
				t.Errorf("Metadata size = %d, want %d", len(tt.result.Metadata), tt.wantSize)
			}
			if tt.result.Metadata[tt.key] != tt.value {
				t.Errorf("Metadata[%q] = %v, want %v", tt.key, tt.result.Metadata[tt.key], tt.value)
			}
		})
	}
}

// TestResultString tests String method.
func TestResultString(t *testing.T) {
	tests := []struct {
		name   string
		result Result
		want   string
	}{
		{
			name: "successful result",
			result: Result{
				Success: true,
				Data:    "test",
			},
			want: "Success",
		},
		{
			name: "failed result",
			result: Result{
				Success: false,
				Error:   "test error",
			},
			want: "Error: test error",
		},
		{
			name: "failed result with empty error",
			result: Result{
				Success: false,
				Error:   "",
			},
			want: "Error: ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.result.String()
			if got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestResultToJSON tests ToJSON method.
func TestResultToJSON(t *testing.T) {
	tests := []struct {
		name    string
		result  Result
		wantErr bool
	}{
		{
			name: "successful result",
			result: Result{
				Success: true,
				Data:    "test data",
			},
			wantErr: false,
		},
		{
			name: "result with complex data",
			result: Result{
				Success: true,
				Data: map[string]interface{}{
					"key": "value",
					"num": 123,
				},
			},
			wantErr: false,
		},
		{
			name: "result with metadata",
			result: Result{
				Success: true,
				Data:    "test",
				Metadata: map[string]interface{}{
					"meta": "data",
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jsonStr, err := tt.result.ToJSON()
			if (err != nil) != tt.wantErr {
				t.Errorf("ToJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				var parsed Result
				if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
					t.Errorf("Failed to unmarshal JSON: %v", err)
				}
				if parsed.Success != tt.result.Success {
					t.Errorf("Parsed Success = %v, want %v", parsed.Success, tt.result.Success)
				}
			}
		})
	}
}

// TestResultWithTiming tests adding timing information to result.
func TestResultWithTiming(t *testing.T) {
	tests := []struct {
		name     string
		result   Result
		duration time.Duration
	}{
		{
			name:     "add timing to result with nil metadata",
			result:   Result{Success: true, Data: "test"},
			duration: 100 * time.Millisecond,
		},
		{
			name: "add timing to result with existing metadata",
			result: Result{
				Success: true,
				Data:    "test",
				Metadata: map[string]interface{}{
					"existing": "value",
				},
			},
			duration: 50 * time.Millisecond,
		},
		{
			name:     "zero duration",
			result:   Result{Success: true, Data: "test"},
			duration: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := time.Now()
			result := ResultWithTiming(tt.result, tt.duration)
			after := time.Now()

			if result.Metadata == nil {
				t.Error("Metadata should not be nil after adding timing")
			}

			durationMs, ok := result.Metadata["duration_ms"].(int64)
			if !ok {
				t.Error("duration_ms should be an int64")
			}
			if durationMs != tt.duration.Milliseconds() {
				t.Errorf("duration_ms = %d, want %d", durationMs, tt.duration.Milliseconds())
			}

			executedAt, ok := result.Metadata["executed_at"].(int64)
			if !ok {
				t.Error("executed_at should be an int64")
			}
			if executedAt < before.Unix() || executedAt > after.Unix() {
				t.Errorf("executed_at %d is outside expected range [%d, %d]", executedAt, before.Unix(), after.Unix())
			}
		})
	}
}

// TestResultList tests ResultList operations.
func TestResultList(t *testing.T) {
	list := NewResultList()

	if list.Total != 0 {
		t.Errorf("Initial Total should be 0, got %d", list.Total)
	}
	if list.Success != 0 {
		t.Errorf("Initial Success should be 0, got %d", list.Success)
	}
	if list.Failed != 0 {
		t.Errorf("Initial Failed should be 0, got %d", list.Failed)
	}

	// Add successful result
	list.Add(Result{Success: true, Data: "success1"})
	if list.Total != 1 {
		t.Errorf("Total should be 1, got %d", list.Total)
	}
	if list.Success != 1 {
		t.Errorf("Success should be 1, got %d", list.Success)
	}

	// Add failed result
	list.Add(Result{Success: false, Error: "error1"})
	if list.Total != 2 {
		t.Errorf("Total should be 2, got %d", list.Total)
	}
	if list.Failed != 1 {
		t.Errorf("Failed should be 1, got %d", list.Failed)
	}

	// Add another successful result
	list.Add(Result{Success: true, Data: "success2"})
	if list.Total != 3 {
		t.Errorf("Total should be 3, got %d", list.Total)
	}
	if list.Success != 2 {
		t.Errorf("Success should be 2, got %d", list.Success)
	}

	if len(list.Results) != 3 {
		t.Errorf("Results length should be 3, got %d", len(list.Results))
	}
}

// TestErrorResult tests ErrorResult operations.
func TestErrorResult(t *testing.T) {
	tests := []struct {
		name    string
		err     *ErrorResult
		wantMsg string
	}{
		{
			name:    "error with code and message",
			err:     NewErrorResultWithCode("ERR001", "Test error"),
			wantMsg: "Test error",
		},
		{
			name:    "error with empty message",
			err:     NewErrorResultWithCode("ERR002", ""),
			wantMsg: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Code == "" {
				t.Error("Code should not be empty")
			}
			if tt.err.Error() != tt.wantMsg {
				t.Errorf("Error() = %q, want %q", tt.err.Error(), tt.wantMsg)
			}

			result := tt.err.ToResult()
			if result.Success {
				t.Error("ToResult() should return failed result")
			}
			if result.Error != tt.err.Message {
				t.Errorf("ToResult().Error = %q, want %q", result.Error, tt.err.Message)
			}
		})
	}
}

// TestErrorResultWithDetails tests ErrorResult with details.
func TestErrorResultWithDetails(t *testing.T) {
	err := NewErrorResultWithCode("ERR001", "Test error")
	details := map[string]interface{}{
		"field":   "username",
		"attempt": 3,
	}
	_ = err.WithDetails(details)

	if err.Details == nil {
		t.Error("Details should not be nil after WithDetails")
	}
	if err.Details["field"] != "username" {
		t.Errorf("Details[\"field\"] = %v, want %v", err.Details["field"], "username")
	}
	if err.Details["attempt"] != 3 {
		t.Errorf("Details[\"attempt\"] = %v, want %v", err.Details["attempt"], 3)
	}

	result := err.ToResult()
	if result.Metadata == nil {
		t.Error("ToResult() should include metadata")
	}
	if result.Metadata["code"] != "ERR001" {
		t.Errorf("Metadata[\"code\"] = %v, want %v", result.Metadata["code"], "ERR001")
	}
}

// TestValidationError tests ValidationError operations.
func TestValidationError(t *testing.T) {
	tests := []struct {
		name    string
		err     *ValidationError
		field   string
		message string
		wantMsg string
	}{
		{
			name:    "validation error",
			err:     NewValidationError("username", "Username is required"),
			field:   "username",
			message: "Username is required",
			wantMsg: "username: Username is required",
		},
		{
			name:    "validation error with empty message",
			err:     NewValidationError("email", ""),
			field:   "email",
			message: "",
			wantMsg: "email: ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Field != tt.field {
				t.Errorf("Field = %q, want %q", tt.err.Field, tt.field)
			}
			if tt.err.Message != tt.message {
				t.Errorf("Message = %q, want %q", tt.err.Message, tt.message)
			}
			if tt.err.Error() != tt.wantMsg {
				t.Errorf("Error() = %q, want %q", tt.err.Error(), tt.wantMsg)
			}
		})
	}
}

// TestResultChaining tests chaining result operations.
func TestResultChaining(t *testing.T) {
	result := NewResult(true, "test data")
	result.WithMetadata("key1", "value1")
	result.WithMetadata("key2", 42)
	result.WithMetadata("key3", true)

	if len(result.Metadata) != 3 {
		t.Errorf("Expected 3 metadata entries, got %d", len(result.Metadata))
	}
	if result.Metadata["key1"] != "value1" {
		t.Errorf("Metadata[\"key1\"] = %v, want %v", result.Metadata["key1"], "value1")
	}
	if result.Metadata["key2"] != 42 {
		t.Errorf("Metadata[\"key2\"] = %v, want %v", result.Metadata["key2"], 42)
	}
	if result.Metadata["key3"] != true {
		t.Errorf("Metadata[\"key3\"] = %v, want %v", result.Metadata["key3"], true)
	}
}
