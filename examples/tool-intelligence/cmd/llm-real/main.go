// Command llm-real registers real built-in tools, lets Ollama call them,
// and returns real execution results. This tests the full tool-calling loop.
//
// Usage:
//
//	go run ./examples/tool-intelligence/cmd/llm-real
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	builtin_hash "github.com/Timwood0x10/ares/internal/tools/resources/builtin/hash"
	builtin_math "github.com/Timwood0x10/ares/internal/tools/resources/builtin/math"
	builtin_stringutils "github.com/Timwood0x10/ares/internal/tools/resources/builtin/stringutils"
	builtin_system "github.com/Timwood0x10/ares/internal/tools/resources/builtin/system"
	builtin_text "github.com/Timwood0x10/ares/internal/tools/resources/builtin/text"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

func main() {
	baseURL := getEnv("OLLAMA_URL", "http://localhost:11434")
	model := getEnv("OLLAMA_MODEL", "llama3.2:latest")
	client := &http.Client{Timeout: 120 * time.Second}

	// ── 1. Register REAL tools ─────────────────────────────
	fmt.Printf("🔧 Loading real tools, model: %s\n\n", model)

	reg := core.NewRegistry()
	_ = reg.Register(builtin_math.NewCalculator())
	_ = reg.Register(builtin_hash.NewHashTool())
	_ = reg.Register(builtin_stringutils.NewStringUtils())
	_ = reg.Register(builtin_text.NewJSONTools())
	_ = reg.Register(builtin_text.NewRegexTool())
	_ = reg.Register(builtin_system.NewIDGenerator())

	schemas := reg.GetSchemas()
	ollamaTools := toOllamaTools(schemas)
	fmt.Printf("📦 %d real tools ready\n\n", len(ollamaTools))

	// ── 2. Test cases ──────────────────────────────────────
	type testCase struct {
		name   string
		prompt string
	}

	tests := []testCase{
		{name: "basic_math", prompt: "What is 1+1? Use the calculator tool."},
		{name: "sum_100", prompt: "Calculate the sum from 1 to 100 using the Gaussian formula n*(n+1)/2 with the calculator."},
		{name: "sha256", prompt: "Compute the SHA256 hash of the text 'hello' using the hash_tool."},
		{name: "base64", prompt: "Encode 'hello world' in base64 using the hash_tool."},
		{name: "uppercase", prompt: "Convert 'hello world' to uppercase using string_utils."},
		{name: "uuid", prompt: "Generate a UUID using the id_generator tool."},
		{name: "gcd", prompt: "Calculate the GCD of 12 and 18 using the calculator tool."},
		{name: "factorial", prompt: "What is 10 factorial? Use the calculator tool with factorial(10)."},
		{name: "nCr", prompt: "Calculate combination C(10,3) using the calculator tool with nCr(10,3)."},
		{name: "json_pretty", prompt: "Pretty-print the JSON {\"name\":\"test\",\"value\":42} using json_tools."},

		// ── Hard problems ──
		{name: "nPr", prompt: "Calculate permutation P(10,3) using the calculator with nPr(10,3)."},
		{name: "lcm", prompt: "Calculate the LCM of 12 and 18 using the calculator with lcm(12,18)."},
		{name: "prime_check", prompt: "Check if 17 is a prime number using the calculator with isPrime(17)."},
		{name: "sin_cos", prompt: "Calculate sin(pi/2) + cos(0) using the calculator."},
		{name: "sqrt_pow", prompt: "Calculate sqrt(16) + 3**2 using the calculator."},
		{name: "mean_stddev", prompt: "Calculate the mean and standard deviation of 1,2,3,4,5,6,7,8,9,10 using mean() and stddev() in the calculator."},
		{name: "variance", prompt: "Calculate the variance of 1,2,3,4,5 using variance() in the calculator."},
		{name: "modulo", prompt: "Calculate 17 modulo 5 using the calculator."},
		{name: "power", prompt: "Calculate 2 to the power of 10 (2**10) using the calculator."},
		{name: "complex_arithmetic", prompt: "Calculate ((10+20)*5-50)/2 using the calculator."},
		{name: "multi_gcd_lcm", prompt: "Calculate both gcd(48, 72) and lcm(48, 72). Call the calculator twice."},
		{name: "regression_math", prompt: "What is 100*(100+1)/2? Use the calculator tool."},
	}

	pass, fail := 0, 0

	for _, tc := range tests {
		fmt.Printf("  🧪 %s:\n", tc.name)

		// Send to LLM
		resp := callLLM(client, baseURL, model, tc.prompt, ollamaTools)

		if len(resp.Message.ToolCalls) == 0 {
			answer := resp.Message.Content
			if len(answer) > 120 {
				answer = answer[:120] + "..."
			}
			fmt.Printf("      ⚠️  no tool call: %s\n", answer)
			fail++
			continue
		}

		// Execute each tool call with REAL tools
		for _, tc2 := range resp.Message.ToolCalls {
			toolName := tc2.Function.Name
			var args map[string]interface{}
			if err := json.Unmarshal(tc2.Function.Arguments, &args); err != nil {
				fmt.Printf("      ❌ bad args from LLM: %v\n", err)
				fail++
				continue
			}

			// Get the real tool from registry
			tool, exists := reg.Get(toolName)
			if !exists {
				fmt.Printf("      ❌ tool %q not registered\n", toolName)
				fail++
				continue
			}

			// Execute!
			start := time.Now()
			result, err := tool.Execute(context.Background(), args)
			latency := time.Since(start)

			var resultStr string
			if err != nil {
				resultStr = fmt.Sprintf("Error: %v", err)
			} else {
				data, _ := json.Marshal(result.Data)
				resultStr = string(data)
			}

			fmt.Printf("      → %s(%s) = %s [%dms]\n",
				toolName, truncate(formatArgs(tc2.Function.Arguments), 50),
				truncate(resultStr, 80), latency.Milliseconds())
		}
		pass++
	}

	fmt.Printf("\n═══════════════════════════════════════\n")
	fmt.Printf("  Model: %s\n", model)
	fmt.Printf("  Pass:  %d/%d\n", pass, pass+fail)
	fmt.Printf("═══════════════════════════════════════\n")

	if fail > 0 {
		os.Exit(1)
	}
}

// ── Ollama API types ──────────────────────────────────────
type ollamaReq struct {
	Model    string       `json:"model"`
	Messages []ollamaMsg  `json:"messages"`
	Tools    []ollamaTool `json:"tools,omitempty"`
	Stream   bool         `json:"stream"`
}

type ollamaMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaTool struct {
	Function ollamaToolDef `json:"function"`
}

type ollamaToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type ollamaResp struct {
	Message ollamaRespMsg `json:"message"`
}

type ollamaRespMsg struct {
	Content   string           `json:"content"`
	ToolCalls []ollamaToolCall `json:"tool_calls"`
}

type ollamaToolCall struct {
	Function ollamaCalledFunc `json:"function"`
}

type ollamaCalledFunc struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func callLLM(client *http.Client, baseURL, model, prompt string, tools []ollamaTool) ollamaResp {
	msg := ollamaMsg{Role: "user", Content: prompt}
	body, _ := json.Marshal(ollamaReq{Model: model, Messages: []ollamaMsg{msg}, Tools: tools, Stream: false})
	resp, err := client.Post(baseURL+"/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		panic(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	var r ollamaResp
	if err := json.Unmarshal(data, &r); err != nil {
		panic(err)
	}
	return r
}

func toOllamaTools(schemas []core.ToolSchema) []ollamaTool {
	out := make([]ollamaTool, 0, len(schemas))
	for _, s := range schemas {
		params, _ := json.Marshal(s.Parameters)
		out = append(out, ollamaTool{
			Function: ollamaToolDef{Name: s.Name, Description: s.Description, Parameters: params},
		})
	}
	return out
}

func formatArgs(raw json.RawMessage) string {
	if raw == nil {
		return ""
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(raw, &obj); err == nil {
		b, _ := json.Marshal(obj)
		return string(b)
	}
	return string(raw)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
