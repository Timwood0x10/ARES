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
)

// RegisterBuiltinTools registers all built-in tools into the given registry.
// These tools are self-contained with no dependency on internal/.
func RegisterBuiltinTools(r *Registry) error {
	tools := []Tool{
		&calculatorTool{},
		&regexTool{},
		&jsonTool{},
		&webSearchTool{client: &http.Client{Timeout: 10 * time.Second}},
		&fileTool{},
	}
	for _, t := range tools {
		if err := r.Register(t); err != nil {
			return err
		}
	}
	return nil
}

// ── Calculator ───────────────────────────────────────────

type calculatorTool struct{}

func (t *calculatorTool) Name() string        { return "calculator" }
func (t *calculatorTool) Description() string { return "Mathematical calculator with expression evaluation" }

func (t *calculatorTool) Execute(_ context.Context, params map[string]any) (Result, error) {
	expr, _ := params["expression"].(string)
	if expr == "" {
		return Result{Success: false, Data: "expression is required"}, nil
	}
	result, err := evalExpr(expr)
	if err != nil {
		return Result{Success: false, Data: err.Error()}, nil
	}
	return Result{Success: true, Data: map[string]any{
		"expression": expr,
		"result":     result,
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

func (t *regexTool) Name() string        { return "regex" }
func (t *regexTool) Description() string { return "Regex matching, extraction, and replacement" }

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

func (t *jsonTool) Name() string        { return "json_tools" }
func (t *jsonTool) Description() string { return "JSON parse, transform, and validation" }

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

type webSearchTool struct {
	client *http.Client
}

func (t *webSearchTool) Name() string        { return "web_search" }
func (t *webSearchTool) Description() string { return "Search the web using DuckDuckGo" }

func (t *webSearchTool) Execute(ctx context.Context, params map[string]any) (Result, error) {
	query, _ := params["query"].(string)
	if query == "" {
		return Result{Success: false, Data: "query is required"}, nil
	}

	searchURL := fmt.Sprintf("https://api.duckduckgo.com/?q=%s&format=json&no_html=1", url.QueryEscape(query))
	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return Result{Success: false, Data: err.Error()}, nil
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return Result{Success: false, Data: err.Error()}, nil
	}
	defer resp.Body.Close()

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return Result{Success: false, Data: err.Error()}, nil
	}

	return Result{Success: true, Data: map[string]any{"query": query, "results": result}}, nil
}

// ── File Tools ───────────────────────────────────────────

type fileTool struct{}

func (t *fileTool) Name() string        { return "file_tools" }
func (t *fileTool) Description() string { return "File operations: read, write, list, exists, delete" }

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
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
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
		if err := os.MkdirAll(path, 0o755); err != nil {
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
