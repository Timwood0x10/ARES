// Command llm-test sends tool schemas to a local Ollama LLM and tests
// whether it correctly selects tools for various natural-language requests.
//
// Usage:
//
//	go run ./examples/tool-intelligence/cmd/llm-test
//
// This tests the tool layer with a "dumb model" (llama3.2) to verify
// that tool descriptions and schemas are clear enough for the LLM to
// make correct tool selection decisions without a powerful model.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// OllamaChatRequest matches the Ollama chat API format.
type OllamaChatRequest struct {
	Model    string       `json:"model"`
	Messages []OllamaMsg  `json:"messages"`
	Tools    []OllamaTool `json:"tools,omitempty"`
	Stream   bool         `json:"stream"`
}

type OllamaMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type OllamaTool struct {
	Function OllamaToolDef `json:"function"`
}

type OllamaToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type OllamaChatResponse struct {
	Message OllamaRespMsg `json:"message"`
}

type OllamaRespMsg struct {
	Content   string           `json:"content"`
	ToolCalls []OllamaToolCall `json:"tool_calls"`
}

type OllamaToolCall struct {
	Function OllamaCalledFunc `json:"function"`
}

type OllamaCalledFunc struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"` // can be string or object
}

func formatArgs(raw json.RawMessage) string {
	if raw == nil {
		return ""
	}
	// Try as object first
	var obj map[string]interface{}
	if err := json.Unmarshal(raw, &obj); err == nil {
		b, _ := json.Marshal(obj)
		return string(b)
	}
	// Fallback to string
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

func main() {
	ctx := context.Background()
	_ = ctx
	baseURL := getEnv("OLLAMA_URL", "http://localhost:11434")
	model := getEnv("OLLAMA_MODEL", "llama3.2:latest")
	client := &http.Client{Timeout: 120 * time.Second}

	// ── 1. Load real tool schemas ──────────────────────────
	fmt.Printf("🔧 Loading tool schemas for model: %s\n\n", model)

	// Register built-in tools to get real schemas.
	reg := core.NewRegistry()
	registerBuiltinTools(reg)
	schemas := reg.GetSchemas()

	// Convert to Ollama tool format.
	ollamaTools := make([]OllamaTool, 0, len(schemas))
	for _, s := range schemas {
		paramsJSON, _ := json.Marshal(s.Parameters)
		ollamaTools = append(ollamaTools, OllamaTool{
			Function: OllamaToolDef{
				Name:        s.Name,
				Description: s.Description,
				Parameters:  paramsJSON,
			},
		})
	}
	fmt.Printf("📦 Registered %d tools for LLM\n\n", len(ollamaTools))

	// ── 2. Test cases ──────────────────────────────────────
	type testCase struct {
		name         string
		prompt       string
		expectedTool string // substring match
	}

	tests := []testCase{
		{name: "basic_math", prompt: "What is 1+1?", expectedTool: "calculator"},
		{name: "sum_1_to_100", prompt: "Calculate the sum from 1 to 100", expectedTool: "calculator"},
		{name: "sha256_hash", prompt: "Compute the SHA256 hash of 'hello world'", expectedTool: "hash_tool"},
		{name: "base64_encode", prompt: "Encode 'hello' in base64", expectedTool: "hash_tool"},
		{name: "uppercase", prompt: "Convert 'hello world' to uppercase", expectedTool: "string_utils"},
		{name: "json_parse", prompt: "Parse this JSON: {\"name\": \"test\"}", expectedTool: "json_tools"},
		{name: "regex_match", prompt: "Match all email addresses in this text", expectedTool: "regex_tool"},
		{name: "uuid_gen", prompt: "Generate a UUID", expectedTool: "id_generator"},
		{name: "pdf_extract", prompt: "Extract text from a PDF file", expectedTool: "pdf_tool"},
		{name: "web_search", prompt: "Search for the latest Go programming news", expectedTool: "web_search"},
		{name: "gcd", prompt: "What is the GCD of 12 and 18?", expectedTool: "calculator"},
		{name: "factorial", prompt: "Calculate 10 factorial", expectedTool: "calculator"},
	}

	// ── 3. Run tests ───────────────────────────────────────
	pass := 0
	fail := 0

	for _, tc := range tests {
		fmt.Printf("  🧪 %s: ", tc.name)

		msg := OllamaMsg{Role: "user", Content: tc.prompt}
		reqBody, _ := json.Marshal(OllamaChatRequest{
			Model:    model,
			Messages: []OllamaMsg{msg},
			Tools:    ollamaTools,
			Stream:   false,
		})

		resp, err := client.Post(baseURL+"/api/chat", "application/json", bytes.NewReader(reqBody))
		if err != nil {
			fmt.Printf("❌ API error: %v\n", err)
			fail++
			continue
		}
		defer func() { _ = resp.Body.Close() }()

		body, _ := io.ReadAll(resp.Body)
		var ollamaResp OllamaChatResponse
		if err := json.Unmarshal(body, &ollamaResp); err != nil {
			fmt.Printf("❌ parse error: %v\n", err)
			fail++
			continue
		}

		toolCalls := ollamaResp.Message.ToolCalls
		if len(toolCalls) == 0 {
			// LLM didn't call a tool — check if it answered directly
			answer := strings.TrimSpace(ollamaResp.Message.Content)
			if len(answer) > 100 {
				answer = answer[:100] + "..."
			}
			fmt.Printf("⚠️  no tool call (answered directly): %s\n", answer)
			fail++
			continue
		}

		// Check which tool was called.
		calledTool := toolCalls[0].Function.Name
		args := formatArgs(toolCalls[0].Function.Arguments)
		if strings.Contains(calledTool, tc.expectedTool) ||
			strings.Contains(tc.expectedTool, calledTool) {
			fmt.Printf("✅ → %s(%s)\n", calledTool, truncateStr(args, 60))
			pass++
		} else {
			fmt.Printf("❌ expected=%s, got=%s(%s)\n",
				tc.expectedTool, calledTool, truncateStr(args, 60))
			fail++
		}
	}

	// ── 4. Summary ─────────────────────────────────────────
	fmt.Printf("\n═══════════════════════════════════════\n")
	fmt.Printf("  Model:     %s\n", model)
	fmt.Printf("  Pass:      %d/%d\n", pass, pass+fail)
	fmt.Printf("  Fail:      %d\n", fail)
	fmt.Printf("═══════════════════════════════════════\n")

	if fail > 0 {
		os.Exit(1)
	}
}

func registerBuiltinTools(reg *core.Registry) {
	tools := []core.Tool{
		&simpleTool{name: "calculator", desc: "Evaluate mathematical expressions. Supports: +, -, *, /, %, **, sqrt(), abs(), sin(), cos(), tan(), log(), ln(), round(), floor(), ceil(), pow(), min(), max(), factorial(), nPr(), nCr(), gcd(), lcm(), isPrime(), mean(), median(), stddev(), variance(), binomial(), normalPdf(), poissonPdf(). Also supports Gaussian sum formula n*(n+1)/2. Constants: pi, e.",
			params: `{"type":"object","properties":{"expression":{"type":"string","description":"Mathematical expression to evaluate"}},"required":["expression"]}`},
		&simpleTool{name: "hash_tool", desc: "Compute cryptographic hashes and Base64 encoding. Operations: md5, sha1, sha256, sha512, base64_encode, base64_decode.",
			params: `{"type":"object","properties":{"operation":{"type":"string","enum":["md5","sha1","sha256","sha512","base64_encode","base64_decode"]},"input":{"type":"string"}},"required":["operation","input"]}`},
		&simpleTool{name: "string_utils", desc: "String manipulation operations: upper, lower, trim, split, join, length, reverse, substring, replace.",
			params: `{"type":"object","properties":{"operation":{"type":"string","enum":["upper","lower","trim","split","join","length","reverse","substring","replace"]},"input":{"type":"string"}},"required":["operation","input"]}`},
		&simpleTool{name: "regex_tool", desc: "Perform regex match, extract, and replace operations.",
			params: `{"type":"object","properties":{"operation":{"type":"string","enum":["match","extract","replace"]},"text":{"type":"string"},"pattern":{"type":"string"}},"required":["operation","text","pattern"]}`},
		&simpleTool{name: "json_tools", desc: "Parse, extract, merge, and pretty-print JSON data.",
			params: `{"type":"object","properties":{"operation":{"type":"string","enum":["parse","extract","merge","pretty"]},"data":{"type":"string"}},"required":["operation","data"]}`},
		&simpleTool{name: "pdf_tool", desc: "Extract text content from PDF files.",
			params: `{"type":"object","properties":{"operation":{"type":"string","enum":["extract_text"]},"file_path":{"type":"string"}},"required":["operation","file_path"]}`},
		&simpleTool{name: "web_search", desc: "Search the web using SearXNG meta search engine.",
			params: `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`},
		&simpleTool{name: "id_generator", desc: "Generate unique identifiers: UUID or short ID.",
			params: `{"type":"object","properties":{"operation":{"type":"string","enum":["generate_uuid","generate_short_id"]}},"required":["operation"]}`},
		&simpleTool{name: "code_runner", desc: "Execute code snippets in sandboxed environments.",
			params: `{"type":"object","properties":{"language":{"type":"string"},"code":{"type":"string"}},"required":["language","code"]}`},
		&simpleTool{name: "embedding", desc: "Generate vector embeddings for text using the embedding service.",
			params: `{"type":"object","properties":{"action":{"type":"string","enum":["embed","embed_batch","health"]},"text":{"type":"string"}},"required":["action"]}`},
	}
	for _, t := range tools {
		_ = reg.Register(t)
	}
}

// simpleTool implements core.Tool with JSON-specified parameters.
type simpleTool struct {
	name   string
	desc   string
	params string
}

func (t *simpleTool) Name() string                      { return t.name }
func (t *simpleTool) Description() string               { return t.desc }
func (t *simpleTool) Category() core.ToolCategory       { return core.CategoryCore }
func (t *simpleTool) Capabilities() []core.Capability   { return nil }
func (t *simpleTool) Parameters() *core.ParameterSchema { return parseParams(t.params) }
func (t *simpleTool) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	return core.Result{Success: true, Data: map[string]interface{}{"result": "mock"}}, nil
}

func parseParams(s string) *core.ParameterSchema {
	var p core.ParameterSchema
	_ = json.Unmarshal([]byte(s), &p)
	return &p
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
