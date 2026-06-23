package builtin

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Timwood0x10/ares/internal/tools/resources/base"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// CodeRunner provides code execution capabilities with sandbox constraints.
// WARNING: This tool executes code on the host system. Use with caution and ensure proper sandboxing.
type CodeRunner struct {
	*base.BaseTool
	enablePython        bool
	enableJS            bool
	timeout             time.Duration
	maxOutputSize       int
	dangerousPatterns   []string
	obfuscationPatterns []string
}

// NewCodeRunner creates a new CodeRunner tool.
func NewCodeRunner() *CodeRunner {
	params := &core.ParameterSchema{
		Type: "object",
		Properties: map[string]*core.Parameter{
			"operation": {
				Type:        "string",
				Description: "Operation to perform (run_python, run_js)",
				Enum:        []interface{}{"run_python", "run_js"},
			},
			"code": {
				Type:        "string",
				Description: "Code to execute",
			},
			"timeout_seconds": {
				Type:        "integer",
				Description: "Execution timeout in seconds (default: 30, max: 60)",
				Default:     30,
			},
			"max_output_bytes": {
				Type:        "integer",
				Description: "Maximum output size in bytes (default: 10240)",
				Default:     10240,
			},
		},
		Required: []string{"operation", "code"},
	}

	return &CodeRunner{
		BaseTool:      base.NewBaseToolWithCapabilities("code_runner", "Execute Python and JavaScript code with sandbox constraints", core.CategorySystem, []core.Capability{core.CapabilityExternal}, params),
		enablePython:  true,
		enableJS:      false,
		timeout:       30 * time.Second,
		maxOutputSize: 10240,
		dangerousPatterns: []string{
			"import os", "import subprocess", "import shutil",
			"import sys", "import socket", "import pickle",
			"import marshal", "import ctypes", "import multiprocessing",
			"import importlib", "import urllib", "import requests",
			"import http", "import ftplib", "import telnetlib",
			"eval(", "exec(", "open(", "system(", "popen", "fork(",
			"__import__", "__builtins__", "compile(",
		},
		obfuscationPatterns: []string{
			"chr(", "ord(", "\\x", "base64.",
			"getattr", "setattr",
		},
	}
}

// NewCodeRunnerWithOptions creates a new CodeRunner with custom options.
func NewCodeRunnerWithOptions(enablePython, enableJS bool, timeout time.Duration, maxOutputSize int) *CodeRunner {
	params := &core.ParameterSchema{
		Type: "object",
		Properties: map[string]*core.Parameter{
			"operation": {
				Type:        "string",
				Description: "Operation to perform (run_python, run_js)",
				Enum:        []interface{}{"run_python", "run_js"},
			},
			"code": {
				Type:        "string",
				Description: "Code to execute",
			},
			"timeout_seconds": {
				Type:        "integer",
				Description: "Execution timeout in seconds (default: 30, max: 60)",
				Default:     30,
			},
			"max_output_bytes": {
				Type:        "integer",
				Description: "Maximum output size in bytes (default: 10240)",
				Default:     10240,
			},
		},
		Required: []string{"operation", "code"},
	}

	return &CodeRunner{
		BaseTool:      base.NewBaseToolWithCapabilities("code_runner", "Execute Python and JavaScript code with sandbox constraints", core.CategorySystem, []core.Capability{core.CapabilityExternal}, params),
		enablePython:  enablePython,
		enableJS:      enableJS,
		timeout:       timeout,
		maxOutputSize: maxOutputSize,
		dangerousPatterns: []string{
			"import os", "import subprocess", "import shutil",
			"import sys", "import socket", "import pickle",
			"import marshal", "import ctypes", "import multiprocessing",
			"import importlib", "import urllib", "import requests",
			"import http", "import ftplib", "import telnetlib",
			"eval(", "exec(", "open(", "system(", "popen", "fork(",
			"__import__", "__builtins__", "compile(",
		},
		obfuscationPatterns: []string{
			"chr(", "ord(", "\\x", "base64.",
			"getattr", "setattr",
		},
	}
}

// Execute performs the code execution operation.
func (t *CodeRunner) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	operation, ok := params["operation"].(string)
	if !ok || operation == "" {
		return core.NewErrorResult("operation is required"), nil
	}

	code, ok := params["code"].(string)
	if !ok || code == "" {
		return core.NewErrorResult("code is required"), nil
	}

	if len(code) > 10000 {
		return core.NewErrorResult("code exceeds maximum length of 10000 characters"), nil
	}

	// Validate code for potential security issues
	if err := t.validateCode(code); err != nil {
		return core.NewErrorResult(fmt.Sprintf("code validation failed: %v", err)), nil
	}

	// Get execution parameters
	timeoutSeconds := getInt(params, "timeout_seconds", 30)
	if timeoutSeconds > 60 {
		timeoutSeconds = 60
	}
	if timeoutSeconds < 1 {
		timeoutSeconds = 1
	}

	timeout := time.Duration(timeoutSeconds) * time.Second

	maxOutputSize := getInt(params, "max_output_size", t.maxOutputSize)
	if maxOutputSize < 1024 {
		maxOutputSize = 1024
	}

	// Create context with timeout
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	switch operation {
	case "run_python":
		if !t.enablePython {
			return core.NewErrorResult("Python execution is disabled"), nil
		}
		return t.runPython(execCtx, code, maxOutputSize)
	case "run_js":
		if !t.enableJS {
			return core.NewErrorResult("JavaScript execution is disabled"), nil
		}
		return t.runJavaScript(execCtx, code, maxOutputSize)
	default:
		return core.NewErrorResult(fmt.Sprintf("unsupported operation: %s", operation)), nil
	}
}

// importPattern matches import statements with word boundaries.
var importPattern = regexp.MustCompile(`\bimport\s+\w+`)

// stripPythonComments removes single-line comments from Python code.
func stripPythonComments(code string) string {
	lines := strings.Split(code, "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		// Find the first unquoted '#' character
		inSingleQuote := false
		inDoubleQuote := false
		commentStart := -1
		for i, c := range line {
			if c == '\'' && !inDoubleQuote {
				inSingleQuote = !inSingleQuote
			} else if c == '"' && !inSingleQuote {
				inDoubleQuote = !inDoubleQuote
			} else if c == '#' && !inSingleQuote && !inDoubleQuote {
				commentStart = i
				break
			}
		}
		if commentStart >= 0 {
			line = line[:commentStart]
		}
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}

// validateCode checks code for potential security issues.
func (t *CodeRunner) validateCode(code string) error {
	// Strip comments to prevent bypass via comment-based splitting
	stripped := stripPythonComments(code)
	lowerCode := strings.ToLower(stripped)

	// Check dangerous patterns (substring match)
	for _, pattern := range t.dangerousPatterns {
		if strings.Contains(lowerCode, pattern) {
			return fmt.Errorf("potentially dangerous pattern detected: %s", pattern)
		}
	}

	// Check import statements with word boundaries using regex
	matches := importPattern.FindAllString(lowerCode, -1)
	for _, match := range matches {
		parts := strings.Fields(match)
		if len(parts) >= 2 {
			moduleName := parts[1]
			// Check if the imported module is in the dangerous patterns list
			for _, pattern := range t.dangerousPatterns {
				if pattern == "import "+moduleName {
					return fmt.Errorf("potentially dangerous import detected: %s", match)
				}
			}
		}
	}

	// Check obfuscation patterns
	for _, pattern := range t.obfuscationPatterns {
		if strings.Contains(lowerCode, pattern) {
			return fmt.Errorf("potential code obfuscation detected: %s", pattern)
		}
	}

	return nil
}

// runPython executes Python code.
func (t *CodeRunner) runPython(ctx context.Context, code string, maxOutputSize int) (core.Result, error) {
	cmd := exec.CommandContext(ctx, "python3", "-c", code) // #nosec G204
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	workDir, _ := os.MkdirTemp("", "code-runner-*")
	if workDir != "" {
		cmd.Dir = workDir
		defer func() {
			if err := os.RemoveAll(workDir); err != nil {
				slog.Error("failed to clean up temp dir", "path", workDir, "error", err)
			}
		}()
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startTime := time.Now()
	err := cmd.Run()
	executionTime := time.Since(startTime)

	// Truncate output if necessary
	output := stdout.String()
	if len(output) > maxOutputSize {
		output = output[:maxOutputSize] + "\n... (output truncated)"
	}

	errorOutput := stderr.String()
	if len(errorOutput) > maxOutputSize {
		errorOutput = errorOutput[:maxOutputSize] + "\n... (error truncated)"
	}

	if err != nil {
		// Check if it was a timeout
		if ctx.Err() == context.DeadlineExceeded {
			return core.NewResult(false, map[string]interface{}{
				"operation":      "run_python",
				"success":        false,
				"error":          "execution timeout",
				"stderr":         errorOutput,
				"execution_time": executionTime.Milliseconds(),
			}), nil
		}

		return core.NewResult(false, map[string]interface{}{
			"operation":      "run_python",
			"success":        false,
			"error":          err.Error(),
			"stderr":         errorOutput,
			"execution_time": executionTime.Milliseconds(),
		}), nil
	}

	return core.NewResult(true, map[string]interface{}{
		"operation":      "run_python",
		"success":        true,
		"output":         output,
		"stderr":         errorOutput,
		"execution_time": executionTime.Milliseconds(),
	}), nil
}

// runJavaScript executes JavaScript code.
func (t *CodeRunner) runJavaScript(ctx context.Context, code string, maxOutputSize int) (core.Result, error) {
	cmd := exec.CommandContext(ctx, "node", "-e", code) // #nosec G204
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	workDir, _ := os.MkdirTemp("", "code-runner-*")
	if workDir != "" {
		cmd.Dir = workDir
		defer func() {
			if err := os.RemoveAll(workDir); err != nil {
				slog.Error("failed to clean up temp dir", "path", workDir, "error", err)
			}
		}()
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startTime := time.Now()
	err := cmd.Run()
	executionTime := time.Since(startTime)

	// Truncate output if necessary
	output := stdout.String()
	if len(output) > maxOutputSize {
		output = output[:maxOutputSize] + "\n... (output truncated)"
	}

	errorOutput := stderr.String()
	if len(errorOutput) > maxOutputSize {
		errorOutput = errorOutput[:maxOutputSize] + "\n... (error truncated)"
	}

	if err != nil {
		// Check if it was a timeout
		if ctx.Err() == context.DeadlineExceeded {
			return core.NewResult(false, map[string]interface{}{
				"operation":      "run_js",
				"success":        false,
				"error":          "execution timeout",
				"stderr":         errorOutput,
				"execution_time": executionTime.Milliseconds(),
			}), nil
		}

		return core.NewResult(false, map[string]interface{}{
			"operation":      "run_js",
			"success":        false,
			"error":          err.Error(),
			"stderr":         errorOutput,
			"execution_time": executionTime.Milliseconds(),
		}), nil
	}

	return core.NewResult(true, map[string]interface{}{
		"operation":      "run_js",
		"success":        true,
		"output":         output,
		"stderr":         errorOutput,
		"execution_time": executionTime.Milliseconds(),
	}), nil
}

// EnablePython enables or disables Python execution.
func (t *CodeRunner) EnablePython(enabled bool) {
	t.enablePython = enabled
}

// EnableJS enables or disables JavaScript execution.
func (t *CodeRunner) EnableJS(enabled bool) {
	t.enableJS = enabled
}

// SetTimeout sets the execution timeout.
func (t *CodeRunner) SetTimeout(timeout time.Duration) {
	t.timeout = timeout
}

// SetMaxOutputSize sets the maximum output size.
func (t *CodeRunner) SetMaxOutputSize(size int) {
	t.maxOutputSize = size
}

// IsPythonEnabled returns whether Python execution is enabled.
func (t *CodeRunner) IsPythonEnabled() bool {
	return t.enablePython
}

// IsJSEnabled returns whether JavaScript execution is enabled.
func (t *CodeRunner) IsJSEnabled() bool {
	return t.enableJS
}

// AddDangerousPattern adds a custom dangerous pattern to validate against.
func (t *CodeRunner) AddDangerousPattern(pattern string) {
	t.dangerousPatterns = append(t.dangerousPatterns, pattern)
}

// GetSupportedLanguages returns the list of supported languages.
func (t *CodeRunner) GetSupportedLanguages() []string {
	languages := []string{}
	if t.enablePython {
		languages = append(languages, "python")
	}
	if t.enableJS {
		languages = append(languages, "javascript")
	}
	return languages
}

// Helper functions.
func getInt(params map[string]interface{}, key string, defaultVal int) int {
	switch v := params[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return defaultVal
}
