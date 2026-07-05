package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	builtin_hash "github.com/Timwood0x10/ares/internal/tools/resources/builtin/hash"
	builtin_math "github.com/Timwood0x10/ares/internal/tools/resources/builtin/math"
	builtin_pdf "github.com/Timwood0x10/ares/internal/tools/resources/builtin/pdf"
	builtin_stringutils "github.com/Timwood0x10/ares/internal/tools/resources/builtin/stringutils"
	builtin_system "github.com/Timwood0x10/ares/internal/tools/resources/builtin/system"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

const (
	toolCalculator = "calculator"
	keyExpression  = "expression"
	keyResult      = "result"
)

// RegisterBuiltinTools registers all built-in tools into the given registry.
//
// You normally do NOT need to call this function manually. NewRegistry()
// already calls it automatically, so built-in tools are ready on creation.
//
// This function exists for two scenarios:
//  1. Re-registration after NewEmptyRegistry() — if you called
//     NewEmptyRegistry() and later decide you want the built-in tools.
//  2. Custom registry setup — if you're building your own registry
//     instance and want to populate it with built-in tools.
//
// Registered built-in tools:
//   - calculator, hash_tool, string_utils, pdf_tool, id_generator
//   - regex, json_tools, web_search, file_tools
func RegisterBuiltinTools(r *Registry) error {
	// First register the internal-powered tools.
	internalTools := []core.Tool{
		builtin_math.NewCalculator(),
		builtin_hash.NewHashTool(),
		builtin_stringutils.NewStringUtils(),
		builtin_pdf.NewPDFTool(),
		builtin_system.NewIDGenerator(),
	}
	for _, t := range internalTools {
		if err := r.Register(fromCore(t)); err != nil {
			return err
		}
	}
	// Then register the self-contained legacy tools.
	legacyTools := []Tool{
		&regexTool{},
		&jsonTool{},
		&webSearchTool{client: &http.Client{Timeout: 10 * time.Second}},
		&fileTool{},
	}
	for _, t := range legacyTools {
		if err := r.Register(t); err != nil {
			return err
		}
	}
	return nil
}

// fromCore adapts a core.Tool to the public tools.Tool interface.
func fromCore(t core.Tool) Tool {
	return &coreAdapter{inner: t}
}

// coreAdapter wraps a core.Tool so it implements tools.Tool.
type coreAdapter struct {
	inner core.Tool
}

func (a *coreAdapter) Name() string        { return a.inner.Name() }
func (a *coreAdapter) Description() string { return a.inner.Description() }
func (a *coreAdapter) Capabilities() []string {
	caps := a.inner.Capabilities()
	if len(caps) == 0 {
		return nil
	}
	names := make([]string, len(caps))
	for i, c := range caps {
		names[i] = string(c)
	}
	return names
}
func (a *coreAdapter) Execute(ctx context.Context, params map[string]any) (Result, error) {
	cr, err := a.inner.Execute(ctx, params)
	if err != nil {
		return Result{Success: false, Data: err.Error()}, nil
	}
	return Result{Success: cr.Success, Data: cr.Data}, nil
}

// Compile-time check.
var _ Tool = (*coreAdapter)(nil)

// ── Calculator ───────────────────────────────────────────

type calculatorTool struct{}

func (t *calculatorTool) Name() string { return toolCalculator }
func (t *calculatorTool) Description() string {
	return "Mathematical calculator with expression evaluation"
}
func (t *calculatorTool) Capabilities() []string { return nil }

func (t *calculatorTool) Execute(_ context.Context, params map[string]any) (Result, error) {
	expr, _ := params[keyExpression].(string)
	if expr == "" {
		return Result{Success: false, Data: "expression is required"}, nil
	}
	result, err := evalExpr(expr)
	if err != nil {
		return Result{Success: false, Data: err.Error()}, nil
	}
	return Result{Success: true, Data: map[string]any{
		keyExpression: expr,
		keyResult:     result,
	}}, nil
}

func evalExpr(expr string) (float64, error) {
	tokens := tokenize(expr)
	result, _, err := parseExpr(tokens, 0)
	return result, err
}

func tokenize(expr string) []string {
	var tokens []string
	var current strings.Builder
	for _, ch := range expr {
		switch ch {
		case ' ', '\t':
			continue
		case '+', '-', '*', '/', '(', ')':
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			tokens = append(tokens, string(ch))
		default:
			current.WriteRune(ch)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

func parseExpr(tokens []string, pos int) (float64, int, error) {
	left, pos, err := parseMulDiv(tokens, pos)
	if err != nil {
		return 0, 0, err
	}
	for pos < len(tokens) {
		op := tokens[pos]
		if op != "+" && op != "-" {
			break
		}
		right, newPos, err := parseMulDiv(tokens, pos+1)
		if err != nil {
			return 0, 0, err
		}
		if op == "+" {
			left += right
		} else {
			left -= right
		}
		pos = newPos
	}
	return left, pos, nil
}

func parseMulDiv(tokens []string, pos int) (float64, int, error) {
	left, pos, err := parseUnary(tokens, pos)
	if err != nil {
		return 0, 0, err
	}
	for pos < len(tokens) {
		op := tokens[pos]
		if op != "*" && op != "/" {
			break
		}
		right, newPos, err := parseUnary(tokens, pos+1)
		if err != nil {
			return 0, 0, err
		}
		if op == "*" {
			left *= right
		} else {
			if right == 0 {
				return 0, 0, fmt.Errorf("division by zero")
			}
			left /= right
		}
		pos = newPos
	}
	return left, pos, nil
}

func parseUnary(tokens []string, pos int) (float64, int, error) {
	if pos >= len(tokens) {
		return 0, 0, fmt.Errorf("unexpected end of expression")
	}
	if tokens[pos] == "-" {
		val, newPos, err := parsePrimary(tokens, pos+1)
		return -val, newPos, err
	}
	if tokens[pos] == "+" {
		return parsePrimary(tokens, pos+1)
	}
	return parsePrimary(tokens, pos)
}

func parsePrimary(tokens []string, pos int) (float64, int, error) {
	if pos >= len(tokens) {
		return 0, 0, fmt.Errorf("unexpected end of expression")
	}
	token := tokens[pos]
	if token == "(" {
		val, newPos, err := parseExpr(tokens, pos+1)
		if err != nil {
			return 0, 0, err
		}
		if newPos >= len(tokens) || tokens[newPos] != ")" {
			return 0, 0, fmt.Errorf("missing closing parenthesis")
		}
		return val, newPos + 1, nil
	}
	val, err := strconv.ParseFloat(token, 64)
	if err != nil {
		switch strings.ToLower(token) {
		case "pi":
			return math.Pi, pos + 1, nil
		case "e":
			return math.E, pos + 1, nil
		}
		return 0, 0, fmt.Errorf("invalid number: %s", token)
	}
	return val, pos + 1, nil
}

// ── Regex ────────────────────────────────────────────────

type regexTool struct{}

func (t *regexTool) Name() string           { return "regex" }
func (t *regexTool) Description() string    { return "Regex matching, extraction, and replacement" }
func (t *regexTool) Capabilities() []string { return nil }

func (t *regexTool) Execute(_ context.Context, params map[string]any) (Result, error) {
	operation, _ := params["operation"].(string)
	pattern, _ := params["pattern"].(string)
	text, _ := params["text"].(string)

	if pattern == "" {
		return Result{Success: false, Data: "pattern is required"}, nil
	}
	if text == "" {
		return Result{Success: false, Data: "text is required"}, nil
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return Result{Success: false, Data: fmt.Sprintf("invalid regex: %v", err)}, nil
	}

	switch operation {
	case "match", "":
		matches := re.FindAllString(text, -1)
		return Result{Success: true, Data: map[string]any{
			"matched": len(matches) > 0, "match_count": len(matches), "matches": matches,
			"pattern": pattern, "operation": "match",
		}}, nil
	case "extract":
		matches := re.FindAllStringSubmatch(text, -1)
		return Result{Success: true, Data: map[string]any{
			"matched": len(matches) > 0, "match_count": len(matches), "groups": matches,
			"pattern": pattern, "operation": "extract",
		}}, nil
	case "replace":
		replacement, _ := params["replacement"].(string)
		result := re.ReplaceAllString(text, replacement)
		return Result{Success: true, Data: map[string]any{
			"original": text, "result": result,
			"pattern": pattern, "replacement": replacement, "operation": "replace",
		}}, nil
	default:
		return Result{Success: false, Data: fmt.Sprintf("unknown operation: %s", operation)}, nil
	}
}

// ── JSON Tools ───────────────────────────────────────────

type jsonTool struct{}

func (t *jsonTool) Name() string           { return "json_tools" }
func (t *jsonTool) Description() string    { return "JSON parse, transform, and validation" }
func (t *jsonTool) Capabilities() []string { return nil }

func (t *jsonTool) Execute(_ context.Context, params map[string]any) (Result, error) {
	operation, _ := params["operation"].(string)
	input, _ := params["input"].(string)

	if input == "" {
		return Result{Success: false, Data: "input is required"}, nil
	}

	switch operation {
	case "parse", "":
		var parsed any
		if err := json.Unmarshal([]byte(input), &parsed); err != nil {
			return Result{Success: false, Data: fmt.Sprintf("invalid JSON: %v", err)}, nil
		}
		return Result{Success: true, Data: map[string]any{"parsed": parsed, "operation": "parse"}}, nil
	case "validate":
		var parsed any
		if err := json.Unmarshal([]byte(input), &parsed); err != nil {
			return Result{Success: true, Data: map[string]any{"valid": false, "error": err.Error(), "operation": "validate"}}, nil
		}
		return Result{Success: true, Data: map[string]any{"valid": true, "operation": "validate"}}, nil
	case "prettify":
		var parsed any
		if err := json.Unmarshal([]byte(input), &parsed); err != nil {
			return Result{Success: false, Data: fmt.Sprintf("invalid JSON: %v", err)}, nil
		}
		pretty, err := json.MarshalIndent(parsed, "", "  ")
		if err != nil {
			return Result{Success: false, Data: fmt.Sprintf("marshal error: %v", err)}, nil
		}
		return Result{Success: true, Data: map[string]any{"result": string(pretty), "operation": "prettify"}}, nil
	default:
		return Result{Success: false, Data: fmt.Sprintf("unknown operation: %s", operation)}, nil
	}
}

// ── Web Search ───────────────────────────────────────────

// webSearchTool searches the web using SearXNG meta search engine.
// Requires a running SearXNG instance (default: http://localhost:5605).
type webSearchTool struct {
	client  *http.Client
	baseURL string
}

func (t *webSearchTool) Name() string { return "web_search" }
func (t *webSearchTool) Description() string {
	return "Search the web using SearXNG meta search engine"
}
func (t *webSearchTool) Capabilities() []string { return nil }

func (t *webSearchTool) Execute(ctx context.Context, params map[string]any) (Result, error) {
	query, _ := params["query"].(string)
	if query == "" {
		return Result{Success: false, Data: "query is required"}, nil
	}

	baseURL := t.baseURL
	if baseURL == "" {
		baseURL = "http://localhost:5605"
	}
	if override, ok := params["searxng_base_url"].(string); ok && override != "" {
		baseURL = override
	}

	maxResults := 10
	if v, ok := params["max_results"].(float64); ok && v > 0 {
		maxResults = int(v)
	}

	searchURL := fmt.Sprintf("%s/search?q=%s&format=json&pageno=1",
		baseURL, url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return Result{Success: false, Data: err.Error()}, nil
	}
	req.Header.Set("Accept", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return Result{Success: false, Data: fmt.Sprintf("searxng request failed: %v", err)}, nil
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Warn("web_search: response body close failed", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return Result{Success: false, Data: fmt.Sprintf("searxng returned HTTP %d", resp.StatusCode)}, nil
	}

	var searxResp struct {
		Query   string `json:"query"`
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
			Engine  string `json:"engine"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&searxResp); err != nil {
		return Result{Success: false, Data: fmt.Sprintf("decode response: %v", err)}, nil
	}

	results := searxResp.Results
	if len(results) > maxResults {
		results = results[:maxResults]
	}

	items := make([]map[string]string, 0, len(results))
	for _, r := range results {
		items = append(items, map[string]string{
			"title":   r.Title,
			"url":     r.URL,
			"content": r.Content,
			"engine":  r.Engine,
		})
	}

	return Result{Success: true, Data: map[string]any{
		"query":       query,
		"count":       len(items),
		"results":     items,
		"searxng_url": baseURL,
	}}, nil
}

// ── File Tools ───────────────────────────────────────────

type fileTool struct{}

func (t *fileTool) Name() string           { return "file_tools" }
func (t *fileTool) Description() string    { return "File operations: read, write, list, exists, delete" }
func (t *fileTool) Capabilities() []string { return nil }

func (t *fileTool) Execute(_ context.Context, params map[string]any) (Result, error) {
	operation, _ := params["operation"].(string)
	path, _ := params["path"].(string)

	if path == "" {
		return Result{Success: false, Data: "path is required"}, nil
	}

	switch operation {
	case "read":
		data, err := os.ReadFile(path)
		if err != nil {
			return Result{Success: false, Data: err.Error()}, nil
		}
		return Result{Success: true, Data: map[string]any{"path": path, "content": string(data), "size": len(data)}}, nil
	case "write":
		content, _ := params["content"].(string)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return Result{Success: false, Data: err.Error()}, nil
		}
		return Result{Success: true, Data: map[string]any{"path": path, "bytes": len(content)}}, nil
	case "list":
		entries, err := os.ReadDir(path)
		if err != nil {
			return Result{Success: false, Data: err.Error()}, nil
		}
		files := make([]map[string]any, 0, len(entries))
		for _, e := range entries {
			info, _ := e.Info()
			files = append(files, map[string]any{"name": e.Name(), "is_dir": e.IsDir(), "size": info.Size()})
		}
		return Result{Success: true, Data: map[string]any{"path": path, "count": len(files), "files": files}}, nil
	case "exists":
		_, err := os.Stat(path)
		return Result{Success: true, Data: map[string]any{"path": path, "exists": err == nil}}, nil
	case "delete":
		if err := os.Remove(path); err != nil {
			return Result{Success: false, Data: err.Error()}, nil
		}
		return Result{Success: true, Data: map[string]any{"path": path}}, nil
	case "mkdir":
		if err := os.MkdirAll(path, 0o750); err != nil {
			return Result{Success: false, Data: err.Error()}, nil
		}
		return Result{Success: true, Data: map[string]any{"path": path}}, nil
	default:
		return Result{Success: false, Data: fmt.Sprintf("unknown operation: %s", operation)}, nil
	}
}

// FilePath returns the absolute path of a file.
func FilePath(path string) (string, error) {
	return filepath.Abs(path)
}
