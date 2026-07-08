package builtin

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Timwood0x10/ares/internal/tools/resources/base"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// CodeRunner provides code execution capabilities with sandbox constraints.
//
// SECURITY: This tool executes code on the host system. Python is disabled by
// default. Operators must explicitly enable it via EnablePython(true) after
// reviewing the sandbox constraints. The allowlist mode is the primary defense
// — only the modules listed in allowedImports are permitted.
type CodeRunner struct {
	*base.BaseTool
	mu                sync.RWMutex
	enablePython      bool
	enableJS          bool
	timeout           time.Duration
	maxOutputSize     int
	dangerousPatterns []string
	allowedImports    map[string]bool
	strictAllowlist   bool
}

// allowedPythonImports is the default allowlist of modules that may be imported
// in executed Python code. Operators can extend this via AddAllowedImport.
var allowedPythonImports = []string{
	"math", "random", "statistics", "itertools", "functools",
	"collections", "decimal", "fractions", "re", "string",
	"datetime", "time", "calendar",
	"json", "csv",
}

// NewCodeRunner creates a new CodeRunner tool.
//
// By default both Python and JavaScript execution are DISABLED. Operators must
// call EnablePython(true) or EnableJS(true) after evaluating the security
// implications. The strict allowlist mode is enabled by default so that only
// the modules in allowedImports can be used.
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
		BaseTool:        base.NewBaseToolWithCapabilities("code_runner", "Execute Python and JavaScript code with sandbox constraints", core.CategorySystem, []core.Capability{core.CapabilityExternal}, params),
		enablePython:    false,
		enableJS:        false,
		timeout:         30 * time.Second,
		maxOutputSize:   10240,
		strictAllowlist: true,
		allowedImports:  buildAllowedImportsSet(allowedPythonImports),
		dangerousPatterns: []string{
			"__import__", "__builtins__", "compile(",
			"eval(", "exec(", "globals(", "locals(",
			"open(", "system(", "popen", "fork(",
		},
	}
}

// NewCodeRunnerWithOptions creates a new CodeRunner with custom options.
//
// Operators are strongly encouraged to keep enablePython=false unless they
// understand the risks. The strict allowlist remains enabled.
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
		BaseTool:        base.NewBaseToolWithCapabilities("code_runner", "Execute Python and JavaScript code with sandbox constraints", core.CategorySystem, []core.Capability{core.CapabilityExternal}, params),
		enablePython:    enablePython,
		enableJS:        enableJS,
		timeout:         timeout,
		maxOutputSize:   maxOutputSize,
		strictAllowlist: true,
		allowedImports:  buildAllowedImportsSet(allowedPythonImports),
		dangerousPatterns: []string{
			"__import__", "__builtins__", "compile(",
			"eval(", "exec(", "globals(", "locals(",
			"open(", "system(", "popen", "fork(",
		},
	}
}

// buildAllowedImportsSet converts a slice of module names into a set for O(1) lookup.
func buildAllowedImportsSet(modules []string) map[string]bool {
	set := make(map[string]bool, len(modules))
	for _, m := range modules {
		set[m] = true
	}
	return set
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

	// Validate code for potential security issues.
	if err := t.validateCode(code); err != nil {
		return core.NewErrorResult(fmt.Sprintf("code validation failed: %v", err)), nil
	}

	// Get execution parameters.
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

	// Create context with timeout.
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	switch operation {
	case "run_python":
		if !t.IsPythonEnabled() {
			return core.NewErrorResult("Python execution is disabled"), nil
		}
		return t.runPython(execCtx, code, maxOutputSize)
	case "run_js":
		if !t.IsJSEnabled() {
			return core.NewErrorResult("JavaScript execution is disabled"), nil
		}
		return t.runJavaScript(execCtx, code, maxOutputSize)
	default:
		return core.NewErrorResult(fmt.Sprintf("unsupported operation: %s", operation)), nil
	}
}

// importPattern matches import statements with word boundaries.
var importPattern = regexp.MustCompile(`\bimport\s+\w+`)

// fromImportPattern matches `from X import Y` statements.
var fromImportPattern = regexp.MustCompile(`\bfrom\s+(\w+)\s+import`)

// stripPythonComments removes single-line comments from Python code.
func stripPythonComments(code string) string {
	lines := strings.Split(code, "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		// Find the first unquoted '#' character.
		inSingleQuote := false
		inDoubleQuote := false
		commentStart := -1
		for i, c := range line {
			switch {
			case c == '\'' && !inDoubleQuote:
				inSingleQuote = !inSingleQuote
			case c == '"' && !inSingleQuote:
				inDoubleQuote = !inDoubleQuote
			case c == '#' && !inSingleQuote && !inDoubleQuote:
				commentStart = i
				goto foundComment
			}
		}
	foundComment:
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
//
// When strictAllowlist is true (the default), only the modules listed in
// allowedImports may be imported. The dangerous-pattern denylist is retained
// as defense-in-depth but is not relied upon as the primary control.
func (t *CodeRunner) validateCode(code string) error {
	t.mu.RLock()
	defer t.mu.RUnlock()

	stripped := stripPythonComments(code)
	lowerCode := strings.ToLower(stripped)

	// Defense-in-depth: reject known dangerous builtins.
	for _, pattern := range t.dangerousPatterns {
		if strings.Contains(lowerCode, strings.ToLower(pattern)) {
			return fmt.Errorf("potentially dangerous pattern detected: %s", pattern)
		}
	}

	if t.strictAllowlist {
		// Validate `import X` statements. Use lowercased code so that
		// case-insensitive variants like "IMPORT OS" are also caught.
		matches := importPattern.FindAllString(lowerCode, -1)
		for _, match := range matches {
			parts := strings.Fields(match)
			if len(parts) >= 2 {
				moduleName := parts[1]
				if !t.allowedImports[moduleName] {
					return fmt.Errorf("import not in allowlist: %s", moduleName)
				}
			}
		}

		// Validate `from X import Y` statements.
		fromMatches := fromImportPattern.FindAllStringSubmatch(lowerCode, -1)
		for _, m := range fromMatches {
			if len(m) >= 2 && !t.allowedImports[m[1]] {
				return fmt.Errorf("import not in allowlist: %s", m[1])
			}
		}
	}

	return nil
}

// limitedWriter wraps a bytes.Buffer and stops writing once maxBytes has been
// reached, preventing unbounded memory growth from malicious output.
type limitedWriter struct {
	buf       *bytes.Buffer
	maxBytes  int
	truncated bool
}

// newLimitedWriter creates a writer that caps output at maxBytes.
func newLimitedWriter(maxBytes int) *limitedWriter {
	return &limitedWriter{
		buf:      &bytes.Buffer{},
		maxBytes: maxBytes,
	}
}

// Write writes bytes to the buffer, stopping once maxBytes is exceeded.
func (w *limitedWriter) Write(p []byte) (int, error) {
	if w.truncated {
		return len(p), nil
	}
	if w.buf.Len()+len(p) > w.maxBytes {
		remaining := w.maxBytes - w.buf.Len()
		if remaining > 0 {
			if _, err := w.buf.Write(p[:remaining]); err != nil {
				return 0, err
			}
		}
		w.truncated = true
		return len(p), nil
	}
	return w.buf.Write(p)
}

// String returns the captured output, with a truncation marker if needed.
func (w *limitedWriter) String() string {
	out := w.buf.String()
	if w.truncated {
		out += "\n... (output truncated)"
	}
	return out
}

// runPython executes Python code.
func (t *CodeRunner) runPython(ctx context.Context, code string, maxOutputSize int) (core.Result, error) {
	cmd := exec.CommandContext(ctx, "python3", "-c", code) // #nosec G204
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	workDir, err := os.MkdirTemp("", "code-runner-*")
	if err != nil {
		return core.NewErrorResult(fmt.Sprintf("failed to create temp dir: %v", err)), nil
	}
	cmd.Dir = workDir
	defer func() {
		if rmErr := os.RemoveAll(workDir); rmErr != nil {
			log.Error("failed to clean up temp dir", "path", workDir, "error", rmErr)
		}
	}()

	stdout := newLimitedWriter(maxOutputSize)
	stderr := newLimitedWriter(maxOutputSize)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	startTime := time.Now()
	runErr := cmd.Run()
	executionTime := time.Since(startTime)

	if runErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return core.NewResult(false, map[string]interface{}{
				"operation":      "run_python",
				"success":        false,
				"error":          "execution timeout",
				"stderr":         stderr.String(),
				"execution_time": executionTime.Milliseconds(),
			}), nil
		}

		return core.NewResult(false, map[string]interface{}{
			"operation":      "run_python",
			"success":        false,
			"error":          runErr.Error(),
			"stderr":         stderr.String(),
			"execution_time": executionTime.Milliseconds(),
		}), nil
	}

	return core.NewResult(true, map[string]interface{}{
		"operation":      "run_python",
		"success":        true,
		"output":         stdout.String(),
		"stderr":         stderr.String(),
		"execution_time": executionTime.Milliseconds(),
	}), nil
}

// runJavaScript executes JavaScript code.
func (t *CodeRunner) runJavaScript(ctx context.Context, code string, maxOutputSize int) (core.Result, error) {
	cmd := exec.CommandContext(ctx, "node", "-e", code) // #nosec G204
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	workDir, err := os.MkdirTemp("", "code-runner-*")
	if err != nil {
		return core.NewErrorResult(fmt.Sprintf("failed to create temp dir: %v", err)), nil
	}
	cmd.Dir = workDir
	defer func() {
		if rmErr := os.RemoveAll(workDir); rmErr != nil {
			log.Error("failed to clean up temp dir", "path", workDir, "error", rmErr)
		}
	}()

	stdout := newLimitedWriter(maxOutputSize)
	stderr := newLimitedWriter(maxOutputSize)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	startTime := time.Now()
	runErr := cmd.Run()
	executionTime := time.Since(startTime)

	if runErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return core.NewResult(false, map[string]interface{}{
				"operation":      "run_js",
				"success":        false,
				"error":          "execution timeout",
				"stderr":         stderr.String(),
				"execution_time": executionTime.Milliseconds(),
			}), nil
		}

		return core.NewResult(false, map[string]interface{}{
			"operation":      "run_js",
			"success":        false,
			"error":          runErr.Error(),
			"stderr":         stderr.String(),
			"execution_time": executionTime.Milliseconds(),
		}), nil
	}

	return core.NewResult(true, map[string]interface{}{
		"operation":      "run_js",
		"success":        true,
		"output":         stdout.String(),
		"stderr":         stderr.String(),
		"execution_time": executionTime.Milliseconds(),
	}), nil
}

// EnablePython enables or disables Python execution.
func (t *CodeRunner) EnablePython(enabled bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.enablePython = enabled
}

// EnableJS enables or disables JavaScript execution.
func (t *CodeRunner) EnableJS(enabled bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.enableJS = enabled
}

// SetTimeout sets the execution timeout.
func (t *CodeRunner) SetTimeout(timeout time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.timeout = timeout
}

// SetMaxOutputSize sets the maximum output size.
func (t *CodeRunner) SetMaxOutputSize(size int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.maxOutputSize = size
}

// IsPythonEnabled returns whether Python execution is enabled.
func (t *CodeRunner) IsPythonEnabled() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.enablePython
}

// IsJSEnabled returns whether JavaScript execution is enabled.
func (t *CodeRunner) IsJSEnabled() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.enableJS
}

// AddAllowedImport adds a module name to the Python import allowlist.
func (t *CodeRunner) AddAllowedImport(module string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.allowedImports[module] = true
}

// AddDangerousPattern adds a custom dangerous pattern to validate against.
func (t *CodeRunner) AddDangerousPattern(pattern string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.dangerousPatterns = append(t.dangerousPatterns, pattern)
}

// GetSupportedLanguages returns the list of supported languages.
func (t *CodeRunner) GetSupportedLanguages() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
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

// io is imported to ensure the limitedWriter satisfies io.Writer at compile time.
var _ io.Writer = (*limitedWriter)(nil)
