package tools

import (
	"context"
	"time"

	builtin_exec "github.com/Timwood0x10/ares/internal/tools/resources/builtin/execution"
	builtin_file "github.com/Timwood0x10/ares/internal/tools/resources/builtin/file"
	builtin_math "github.com/Timwood0x10/ares/internal/tools/resources/builtin/math"
	builtin_network "github.com/Timwood0x10/ares/internal/tools/resources/builtin/network"
	builtin_system "github.com/Timwood0x10/ares/internal/tools/resources/builtin/system"
	builtin_text "github.com/Timwood0x10/ares/internal/tools/resources/builtin/text"
)

// RegisterBuiltinTools registers all built-in tools into the given registry.
func RegisterBuiltinTools(r *Registry) error {
	tools := []Tool{
		NewCalculatorTool(),
		NewDateTimeTool(),
		NewTextProcessorTool(),
		NewWebSearchTool(),
		NewHTTPRequestTool(),
		NewWebScraperTool(),
		NewRegexTool(),
		NewJSONTools(),
		NewDataValidationTool(),
		NewDataTransformTool(),
		NewLogAnalyzerTool(),
		NewIDGeneratorTool(),
		NewFileToolsTool(),
		NewCodeRunnerTool(),
	}
	for _, t := range tools {
		if err := r.Register(t); err != nil {
			return err
		}
	}
	return nil
}

// ── Math ─────────────────────────────────────────────────

type CalculatorTool struct{ inner *builtin_math.Calculator }

func NewCalculatorTool() *CalculatorTool {
	return &CalculatorTool{inner: builtin_math.NewCalculator()}
}
func (t *CalculatorTool) Name() string { return "calculator" }
func (t *CalculatorTool) Description() string {
	return "Mathematical calculator with expression evaluation"
}
func (t *CalculatorTool) Execute(ctx context.Context, params map[string]any) (Result, error) {
	res, err := t.inner.Execute(ctx, params)
	if err != nil {
		return Result{Success: false, Data: err.Error()}, nil
	}
	return Result{Success: res.Success, Data: res.Data}, nil
}

type DateTimeTool struct{ inner *builtin_math.DateTime }

func NewDateTimeTool() *DateTimeTool {
	return &DateTimeTool{inner: builtin_math.NewDateTime()}
}
func (t *DateTimeTool) Name() string { return "datetime" }
func (t *DateTimeTool) Description() string {
	return "Date/time operations, formatting, timezone conversion"
}
func (t *DateTimeTool) Execute(ctx context.Context, params map[string]any) (Result, error) {
	res, err := t.inner.Execute(ctx, params)
	if err != nil {
		return Result{Success: false, Data: err.Error()}, nil
	}
	return Result{Success: res.Success, Data: res.Data}, nil
}

type TextProcessorTool struct{ inner *builtin_math.TextProcessor }

func NewTextProcessorTool() *TextProcessorTool {
	return &TextProcessorTool{inner: builtin_math.NewTextProcessor()}
}
func (t *TextProcessorTool) Name() string { return "text_processor" }
func (t *TextProcessorTool) Description() string {
	return "Text processing: case conversion, trimming, splitting"
}
func (t *TextProcessorTool) Execute(ctx context.Context, params map[string]any) (Result, error) {
	res, err := t.inner.Execute(ctx, params)
	if err != nil {
		return Result{Success: false, Data: err.Error()}, nil
	}
	return Result{Success: res.Success, Data: res.Data}, nil
}

// ── Network ──────────────────────────────────────────────

type WebSearchTool struct{ inner *builtin_network.WebSearch }

func NewWebSearchTool() *WebSearchTool {
	return &WebSearchTool{inner: builtin_network.NewWebSearch()}
}
func (t *WebSearchTool) Name() string { return "web_search" }
func (t *WebSearchTool) Description() string {
	return "Search the web using SearXNG meta search engine"
}
func (t *WebSearchTool) Execute(ctx context.Context, params map[string]any) (Result, error) {
	res, err := t.inner.Execute(ctx, params)
	if err != nil {
		return Result{Success: false, Data: err.Error()}, nil
	}
	return Result{Success: res.Success, Data: res.Data}, nil
}

type HTTPRequestTool struct{ inner *builtin_network.HTTPRequest }

func NewHTTPRequestTool() *HTTPRequestTool {
	return &HTTPRequestTool{inner: builtin_network.NewHTTPRequest()}
}
func (t *HTTPRequestTool) Name() string        { return "http_request" }
func (t *HTTPRequestTool) Description() string { return "Make HTTP requests (GET, POST, PUT, DELETE)" }
func (t *HTTPRequestTool) Execute(ctx context.Context, params map[string]any) (Result, error) {
	res, err := t.inner.Execute(ctx, params)
	if err != nil {
		return Result{Success: false, Data: err.Error()}, nil
	}
	return Result{Success: res.Success, Data: res.Data}, nil
}

type WebScraperTool struct{ inner *builtin_network.WebScraper }

func NewWebScraperTool() *WebScraperTool {
	fetcher := builtin_network.NewWebFetcher(builtin_network.NewDefaultHTTPClient(30 * time.Second))
	return &WebScraperTool{inner: builtin_network.NewWebScraper(fetcher)}
}
func (t *WebScraperTool) Name() string        { return "web_scraper" }
func (t *WebScraperTool) Description() string { return "Scrape and extract content from web pages" }
func (t *WebScraperTool) Execute(ctx context.Context, params map[string]any) (Result, error) {
	res, err := t.inner.Execute(ctx, params)
	if err != nil {
		return Result{Success: false, Data: err.Error()}, nil
	}
	return Result{Success: res.Success, Data: res.Data}, nil
}

// ── Text ─────────────────────────────────────────────────

type RegexTool struct{ inner *builtin_text.RegexTool }

func NewRegexTool() *RegexTool {
	return &RegexTool{inner: builtin_text.NewRegexTool()}
}
func (t *RegexTool) Name() string        { return "regex" }
func (t *RegexTool) Description() string { return "Regex matching, extraction, and replacement" }
func (t *RegexTool) Execute(ctx context.Context, params map[string]any) (Result, error) {
	res, err := t.inner.Execute(ctx, params)
	if err != nil {
		return Result{Success: false, Data: err.Error()}, nil
	}
	return Result{Success: res.Success, Data: res.Data}, nil
}

type JSONTools struct{ inner *builtin_text.JSONTools }

func NewJSONTools() *JSONTools {
	return &JSONTools{inner: builtin_text.NewJSONTools()}
}
func (t *JSONTools) Name() string        { return "json_tools" }
func (t *JSONTools) Description() string { return "JSON parse, transform, and validation" }
func (t *JSONTools) Execute(ctx context.Context, params map[string]any) (Result, error) {
	res, err := t.inner.Execute(ctx, params)
	if err != nil {
		return Result{Success: false, Data: err.Error()}, nil
	}
	return Result{Success: res.Success, Data: res.Data}, nil
}

type DataValidationTool struct{ inner *builtin_text.DataValidation }

func NewDataValidationTool() *DataValidationTool {
	return &DataValidationTool{inner: builtin_text.NewDataValidation()}
}
func (t *DataValidationTool) Name() string { return "data_validation" }
func (t *DataValidationTool) Description() string {
	return "Data validation: email, URL, phone, format checks"
}
func (t *DataValidationTool) Execute(ctx context.Context, params map[string]any) (Result, error) {
	res, err := t.inner.Execute(ctx, params)
	if err != nil {
		return Result{Success: false, Data: err.Error()}, nil
	}
	return Result{Success: res.Success, Data: res.Data}, nil
}

type DataTransformTool struct{ inner *builtin_text.DataTransform }

func NewDataTransformTool() *DataTransformTool {
	return &DataTransformTool{inner: builtin_text.NewDataTransform()}
}
func (t *DataTransformTool) Name() string { return "data_transform" }
func (t *DataTransformTool) Description() string {
	return "Data transformation: CSV, XML, YAML conversion"
}
func (t *DataTransformTool) Execute(ctx context.Context, params map[string]any) (Result, error) {
	res, err := t.inner.Execute(ctx, params)
	if err != nil {
		return Result{Success: false, Data: err.Error()}, nil
	}
	return Result{Success: res.Success, Data: res.Data}, nil
}

type LogAnalyzerTool struct{ inner *builtin_text.LogAnalyzer }

func NewLogAnalyzerTool() *LogAnalyzerTool {
	return &LogAnalyzerTool{inner: builtin_text.NewLogAnalyzer()}
}
func (t *LogAnalyzerTool) Name() string { return "log_analyzer" }
func (t *LogAnalyzerTool) Description() string {
	return "Log analysis: parse, filter, aggregate log entries"
}
func (t *LogAnalyzerTool) Execute(ctx context.Context, params map[string]any) (Result, error) {
	res, err := t.inner.Execute(ctx, params)
	if err != nil {
		return Result{Success: false, Data: err.Error()}, nil
	}
	return Result{Success: res.Success, Data: res.Data}, nil
}

// ── System ───────────────────────────────────────────────

type IDGeneratorTool struct{ inner *builtin_system.IDGenerator }

func NewIDGeneratorTool() *IDGeneratorTool {
	return &IDGeneratorTool{inner: builtin_system.NewIDGenerator()}
}
func (t *IDGeneratorTool) Name() string        { return "id_generator" }
func (t *IDGeneratorTool) Description() string { return "Generate UUIDs, nanoids, short IDs" }
func (t *IDGeneratorTool) Execute(ctx context.Context, params map[string]any) (Result, error) {
	res, err := t.inner.Execute(ctx, params)
	if err != nil {
		return Result{Success: false, Data: err.Error()}, nil
	}
	return Result{Success: res.Success, Data: res.Data}, nil
}

// ── File ─────────────────────────────────────────────────

type FileToolsTool struct{ inner *builtin_file.FileTools }

func NewFileToolsTool() *FileToolsTool {
	return &FileToolsTool{inner: builtin_file.NewFileTools()}
}
func (t *FileToolsTool) Name() string { return "file_tools" }
func (t *FileToolsTool) Description() string {
	return "File operations: read, write, list, exists, delete"
}
func (t *FileToolsTool) Execute(ctx context.Context, params map[string]any) (Result, error) {
	res, err := t.inner.Execute(ctx, params)
	if err != nil {
		return Result{Success: false, Data: err.Error()}, nil
	}
	return Result{Success: res.Success, Data: res.Data}, nil
}

// ── Execution ────────────────────────────────────────────

type CodeRunnerTool struct{ inner *builtin_exec.CodeRunner }

func NewCodeRunnerTool() *CodeRunnerTool {
	return &CodeRunnerTool{inner: builtin_exec.NewCodeRunner()}
}
func (t *CodeRunnerTool) Name() string { return "code_runner" }
func (t *CodeRunnerTool) Description() string {
	return "Run Python/JavaScript code snippets in a sandbox"
}
func (t *CodeRunnerTool) Execute(ctx context.Context, params map[string]any) (Result, error) {
	res, err := t.inner.Execute(ctx, params)
	if err != nil {
		return Result{Success: false, Data: err.Error()}, nil
	}
	return Result{Success: res.Success, Data: res.Data}, nil
}
