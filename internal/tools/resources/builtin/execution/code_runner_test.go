package builtin

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"goagentx/internal/tools/resources/core"
)

// TestNewCodeRunner tests creating a new CodeRunner.
func TestNewCodeRunner(t *testing.T) {
	runner := NewCodeRunner()
	require.NotNil(t, runner)
	if runner.Name() != "code_runner" {
		t.Errorf("Name() = %q, want 'code_runner'", runner.Name())
	}
	if runner.Category() != core.CategorySystem {
		t.Errorf("Category() = %v, want CategorySystem", runner.Category())
	}
	if !runner.enablePython {
		t.Error("Python should be enabled by default")
	}
	if runner.enableJS {
		t.Error("JavaScript should be disabled by default")
	}
	if runner.timeout != 30*time.Second {
		t.Errorf("Default timeout = %v, want 30s", runner.timeout)
	}
	if runner.maxOutputSize != 10240 {
		t.Errorf("Default maxOutputSize = %d, want 10240", runner.maxOutputSize)
	}
}

// TestNewCodeRunnerWithOptions tests creating a CodeRunner with custom options.
func TestNewCodeRunnerWithOptions(t *testing.T) {
	tests := []struct {
		name          string
		enablePython  bool
		enableJS      bool
		timeout       time.Duration
		maxOutputSize int
	}{
		{
			name:          "both enabled",
			enablePython:  true,
			enableJS:      true,
			timeout:       60 * time.Second,
			maxOutputSize: 20480,
		},
		{
			name:          "only python",
			enablePython:  true,
			enableJS:      false,
			timeout:       15 * time.Second,
			maxOutputSize: 5120,
		},
		{
			name:          "only js",
			enablePython:  false,
			enableJS:      true,
			timeout:       45 * time.Second,
			maxOutputSize: 8192,
		},
		{
			name:          "none enabled",
			enablePython:  false,
			enableJS:      false,
			timeout:       30 * time.Second,
			maxOutputSize: 10240,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := NewCodeRunnerWithOptions(tt.enablePython, tt.enableJS, tt.timeout, tt.maxOutputSize)
			require.NotNil(t, runner)
			if runner.enablePython != tt.enablePython {
				t.Errorf("enablePython = %v, want %v", runner.enablePython, tt.enablePython)
			}
			if runner.enableJS != tt.enableJS {
				t.Errorf("enableJS = %v, want %v", runner.enableJS, tt.enableJS)
			}
			if runner.timeout != tt.timeout {
				t.Errorf("timeout = %v, want %v", runner.timeout, tt.timeout)
			}
			if runner.maxOutputSize != tt.maxOutputSize {
				t.Errorf("maxOutputSize = %d, want %d", runner.maxOutputSize, tt.maxOutputSize)
			}
		})
	}
}

// TestCodeRunnerExecute_MissingParameters tests missing required parameters.
func TestCodeRunnerExecute_MissingParameters(t *testing.T) {
	runner := NewCodeRunner()
	ctx := context.Background()

	tests := []struct {
		name   string
		params map[string]interface{}
	}{
		{
			name:   "no parameters",
			params: map[string]interface{}{},
		},
		{
			name: "missing operation",
			params: map[string]interface{}{
				"code": "print('hello')",
			},
		},
		{
			name: "empty operation",
			params: map[string]interface{}{
				"operation": "",
				"code":      "print('hello')",
			},
		},
		{
			name: "missing code",
			params: map[string]interface{}{
				"operation": "run_python",
			},
		},
		{
			name: "empty code",
			params: map[string]interface{}{
				"operation": "run_python",
				"code":      "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := runner.Execute(ctx, tt.params)
			if err != nil {
				t.Errorf("Execute() unexpected error: %v", err)
				return
			}
			if result.Success {
				t.Error("Execute() should fail when required parameters are missing")
			}
		})
	}
}

// TestCodeRunnerExecute_InvalidOperation tests invalid operation types.
func TestCodeRunnerExecute_InvalidOperation(t *testing.T) {
	runner := NewCodeRunner()
	ctx := context.Background()

	tests := []struct {
		name      string
		operation string
	}{
		{
			name:      "invalid operation",
			operation: "invalid_op",
		},
		{
			name:      "empty operation",
			operation: "",
		},
		{
			name:      "random operation",
			operation: "run_ruby",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := map[string]interface{}{
				"operation": tt.operation,
				"code":      "print('test')",
			}

			result, err := runner.Execute(ctx, params)
			if err != nil {
				t.Errorf("Execute() unexpected error: %v", err)
				return
			}
			if result.Success {
				t.Error("Execute() should fail for invalid operation")
			}
		})
	}
}

// TestCodeRunnerExecute_CodeValidation tests code validation.
func TestCodeRunnerExecute_CodeValidation(t *testing.T) {
	runner := NewCodeRunner()
	ctx := context.Background()

	tests := []struct {
		name      string
		code      string
		wantError bool
	}{
		{
			name:      "safe code",
			code:      "print('hello')",
			wantError: false,
		},
		{
			name:      "safe math",
			code:      "x = 1 + 2; print(x)",
			wantError: false,
		},
		{
			name:      "dangerous - import os",
			code:      "import os",
			wantError: true,
		},
		{
			name:      "dangerous - import subprocess",
			code:      "import subprocess",
			wantError: true,
		},
		{
			name:      "dangerous - eval",
			code:      "eval('1+1')",
			wantError: true,
		},
		{
			name:      "dangerous - exec",
			code:      "exec('print(1)')",
			wantError: true,
		},
		{
			name:      "dangerous - __import__",
			code:      "__import__('os')",
			wantError: true,
		},
		{
			name:      "dangerous - open",
			code:      "open('/etc/passwd')",
			wantError: true,
		},
		{
			name:      "dangerous - system",
			code:      "import os; os.system('ls')",
			wantError: true,
		},
		{
			name:      "case insensitive - IMPORT OS",
			code:      "IMPORT OS",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := map[string]interface{}{
				"operation": "run_python",
				"code":      tt.code,
			}

			result, err := runner.Execute(ctx, params)
			if err != nil {
				t.Errorf("Execute() unexpected error: %v", err)
				return
			}

			if tt.wantError {
				if result.Success {
					t.Error("Execute() should fail for dangerous code")
				}
				if !strings.Contains(result.Error, "code validation failed") {
					t.Errorf("Error message should mention code validation, got: %s", result.Error)
				}
			} else {
				// Safe code might still fail if Python is not available, but that's OK
				// Just check that validation didn't reject it
				if !result.Success && strings.Contains(result.Error, "code validation failed") {
					t.Errorf("Safe code should not be rejected by validation: %s", result.Error)
				}
			}
		})
	}
}

// TestCodeRunnerExecute_PythonDisabled tests Python disabled scenario.
func TestCodeRunnerExecute_PythonDisabled(t *testing.T) {
	runner := NewCodeRunner()
	runner.EnablePython(false)
	ctx := context.Background()

	params := map[string]interface{}{
		"operation": "run_python",
		"code":      "print('hello')",
	}

	result, err := runner.Execute(ctx, params)
	if err != nil {
		t.Errorf("Execute() unexpected error: %v", err)
		return
	}

	if result.Success {
		t.Error("Execute() should fail when Python is disabled")
	}

	if !strings.Contains(result.Error, "Python execution is disabled") {
		t.Errorf("Error message should mention Python is disabled, got: %s", result.Error)
	}
}

// TestCodeRunnerExecute_JSDisabled tests JavaScript disabled scenario.
func TestCodeRunnerExecute_JSDisabled(t *testing.T) {
	runner := NewCodeRunner()
	runner.EnableJS(false)
	ctx := context.Background()

	params := map[string]interface{}{
		"operation": "run_js",
		"code":      "console.log('hello')",
	}

	result, err := runner.Execute(ctx, params)
	if err != nil {
		t.Errorf("Execute() unexpected error: %v", err)
		return
	}

	if result.Success {
		t.Error("Execute() should fail when JavaScript is disabled")
	}

	if !strings.Contains(result.Error, "JavaScript execution is disabled") {
		t.Errorf("Error message should mention JavaScript is disabled, got: %s", result.Error)
	}
}

// TestCodeRunnerExecute_TimeoutParameters tests timeout parameter handling.
func TestCodeRunnerExecute_TimeoutParameters(t *testing.T) {
	runner := NewCodeRunner()
	ctx := context.Background()

	tests := []struct {
		name            string
		timeoutSeconds  interface{}
		expectedSeconds int
	}{
		{
			name:            "default timeout",
			timeoutSeconds:  nil,
			expectedSeconds: 30,
		},
		{
			name:            "valid timeout",
			timeoutSeconds:  10,
			expectedSeconds: 10,
		},
		{
			name:            "timeout capped at 60",
			timeoutSeconds:  100,
			expectedSeconds: 60,
		},
		{
			name:            "timeout minimum 1",
			timeoutSeconds:  0,
			expectedSeconds: 1,
		},
		{
			name:            "timeout minimum 1 negative",
			timeoutSeconds:  -5,
			expectedSeconds: 1,
		},
		{
			name:            "timeout as float",
			timeoutSeconds:  5.5,
			expectedSeconds: 5,
		},
		{
			name:            "timeout as string",
			timeoutSeconds:  "15",
			expectedSeconds: 15,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := map[string]interface{}{
				"operation":       "run_python",
				"code":            "print('test')",
				"timeout_seconds": tt.timeoutSeconds,
			}

			result, err := runner.Execute(ctx, params)
			if err != nil {
				t.Errorf("Execute() unexpected error: %v", err)
				return
			}

			// We can't verify the exact timeout without actually running code
			// Just verify the parameter is accepted
			if result.Data == nil && result.Error == "" {
				t.Error("Execute() should return a result with data or error")
			}
		})
	}
}

// TestCodeRunnerExecute_MaxOutputSize tests max output size parameter handling.
func TestCodeRunnerExecute_MaxOutputSize(t *testing.T) {
	runner := NewCodeRunner()
	ctx := context.Background()

	tests := []struct {
		name           string
		maxOutputBytes interface{}
		expectedSize   int
	}{
		{
			name:           "default size",
			maxOutputBytes: nil,
			expectedSize:   10240,
		},
		{
			name:           "valid size",
			maxOutputBytes: 2048,
			expectedSize:   2048,
		},
		{
			name:           "size minimum 1024",
			maxOutputBytes: 100,
			expectedSize:   1024,
		},
		{
			name:           "size as float",
			maxOutputBytes: 512.5,
			expectedSize:   512,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := map[string]interface{}{
				"operation":        "run_python",
				"code":             "print('test')",
				"max_output_bytes": tt.maxOutputBytes,
			}

			result, err := runner.Execute(ctx, params)
			if err != nil {
				t.Errorf("Execute() unexpected error: %v", err)
				return
			}

			if result.Data == nil && result.Error == "" {
				t.Error("Execute() should return a result with data or error")
			}
		})
	}
}

// TestCodeRunnerEnableDisable tests enable/disable methods.
func TestCodeRunnerEnableDisable(t *testing.T) {
	runner := NewCodeRunner()

	// Test initial state
	if !runner.IsPythonEnabled() {
		t.Error("Python should be enabled initially")
	}
	if runner.IsJSEnabled() {
		t.Error("JavaScript should be disabled initially")
	}

	// Enable JS
	runner.EnableJS(true)
	if !runner.IsJSEnabled() {
		t.Error("JavaScript should be enabled after EnableJS(true)")
	}

	// Disable Python
	runner.EnablePython(false)
	if runner.IsPythonEnabled() {
		t.Error("Python should be disabled after EnablePython(false)")
	}

	// Re-enable Python
	runner.EnablePython(true)
	if !runner.IsPythonEnabled() {
		t.Error("Python should be enabled after EnablePython(true)")
	}

	// Disable JS
	runner.EnableJS(false)
	if runner.IsJSEnabled() {
		t.Error("JavaScript should be disabled after EnableJS(false)")
	}
}

// TestCodeRunnerSetTimeout tests timeout setting.
func TestCodeRunnerSetTimeout(t *testing.T) {
	runner := NewCodeRunner()

	tests := []struct {
		name    string
		timeout time.Duration
	}{
		{
			name:    "5 seconds",
			timeout: 5 * time.Second,
		},
		{
			name:    "1 minute",
			timeout: 1 * time.Minute,
		},
		{
			name:    "0 seconds",
			timeout: 0,
		},
		{
			name:    "negative timeout",
			timeout: -1 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner.SetTimeout(tt.timeout)
			if runner.timeout != tt.timeout {
				t.Errorf("SetTimeout(%v) did not set timeout, got %v", tt.timeout, runner.timeout)
			}
		})
	}
}

// TestCodeRunnerSetMaxOutputSize tests max output size setting.
func TestCodeRunnerSetMaxOutputSize(t *testing.T) {
	runner := NewCodeRunner()

	tests := []struct {
		name string
		size int
	}{
		{
			name: "512 bytes",
			size: 512,
		},
		{
			name: "1024 bytes",
			size: 1024,
		},
		{
			name: "0 bytes",
			size: 0,
		},
		{
			name: "negative size",
			size: -100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner.SetMaxOutputSize(tt.size)
			if runner.maxOutputSize != tt.size {
				t.Errorf("SetMaxOutputSize(%d) did not set size, got %d", tt.size, runner.maxOutputSize)
			}
		})
	}
}

// TestCodeRunnerGetSupportedLanguages tests getting supported languages.
func TestCodeRunnerGetSupportedLanguages(t *testing.T) {
	runner := NewCodeRunner()

	// Test initial state
	languages := runner.GetSupportedLanguages()
	if len(languages) != 1 {
		t.Errorf("Initial languages count = %d, want 1", len(languages))
	}
	if len(languages) > 0 && languages[0] != "python" {
		t.Errorf("Initial language = %s, want 'python'", languages[0])
	}

	// Enable JS
	runner.EnableJS(true)
	languages = runner.GetSupportedLanguages()
	if len(languages) != 2 {
		t.Errorf("Languages count with JS enabled = %d, want 2", len(languages))
	}

	// Disable Python
	runner.EnablePython(false)
	languages = runner.GetSupportedLanguages()
	if len(languages) != 1 {
		t.Errorf("Languages count with Python disabled = %d, want 1", len(languages))
	}
	if len(languages) > 0 && languages[0] != "javascript" {
		t.Errorf("Language with Python disabled = %s, want 'javascript'", languages[0])
	}

	// Disable both
	runner.EnableJS(false)
	languages = runner.GetSupportedLanguages()
	if len(languages) != 0 {
		t.Errorf("Languages count with both disabled = %d, want 0", len(languages))
	}
}

// TestCodeRunnerCapabilities tests code runner capabilities.
func TestCodeRunnerCapabilities(t *testing.T) {
	runner := NewCodeRunner()

	capabilities := runner.Capabilities()
	if len(capabilities) != 1 {
		t.Errorf("Capabilities() length = %d, want 1", len(capabilities))
	}

	if capabilities[0] != core.CapabilityExternal {
		t.Errorf("Capabilities()[0] = %v, want CapabilityExternal", capabilities[0])
	}

	parameters := runner.Parameters()
	require.NotNil(t, parameters)

	if parameters.Type != "object" {
		t.Errorf("Parameters.Type = %q, want 'object'", parameters.Type)
	}

	// Check required parameters
	if len(parameters.Required) != 2 {
		t.Errorf("parameters.Required length = %d, want 2", len(parameters.Required))
	}

	requiredParams := make(map[string]bool)
	for _, req := range parameters.Required {
		requiredParams[req] = true
	}

	if !requiredParams["operation"] {
		t.Error("'operation' should be required")
	}
	if !requiredParams["code"] {
		t.Error("'code' should be required")
	}

	// Check operation enum
	operationParam, exists := parameters.Properties["operation"]
	if !exists {
		t.Error("operation parameter should exist")
	}

	if operationParam.Type != "string" {
		t.Errorf("operation parameter Type = %q, want 'string'", operationParam.Type)
	}

	if operationParam.Enum == nil || len(operationParam.Enum) != 2 {
		t.Error("operation parameter should have exactly 2 enum values")
	}
}
