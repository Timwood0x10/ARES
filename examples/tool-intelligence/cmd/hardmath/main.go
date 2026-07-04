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

	"github.com/Timwood0x10/ares/internal/tools/resources/core"
	builtin_math "github.com/Timwood0x10/ares/internal/tools/resources/builtin/math"
)

func main() {
	client := &http.Client{Timeout: 120 * time.Second}
	baseURL := getEnv("OLLAMA_URL", "http://localhost:11434")
	model := getEnv("OLLAMA_MODEL", "gemma4:e4b")

	reg := core.NewRegistry()
	_ = reg.Register(builtin_math.NewCalculator())
	schemas := reg.GetSchemas()

	ollamaTools := toTools(schemas)

	prompt := `Given equation: z^3 = x^5 + y^6 + 5, so z = (x^5 + y^6 + 5)^(1/3)

To check if z is monotonically increasing on (0,20], compute these values using the calculator tool:

1. x=1: (1^5 + 1^6 + 5)^(1/3)    → calculator("(1**5 + 1**6 + 5)**(1/3)")
2. x=2: (2^5 + 2^6 + 5)^(1/3)    → calculator("(2**5 + 2**6 + 5)**(1/3)")
3. x=5: (5^5 + 5^6 + 5)^(1/3)    → calculator("(5**5 + 5**6 + 5)**(1/3)")
4. x=10: (10^5 + 10^6 + 5)^(1/3)  → calculator("(10**5 + 10**6 + 5)**(1/3)")
5. x=20: (20^5 + 20^6 + 5)^(1/3)  → calculator("(20**5 + 20**6 + 5)**(1/3)")

Call the calculator for EACH of the 5 points and tell me if z is increasing. Use the exact expressions shown.`

	body, _ := json.Marshal(map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"tools":  ollamaTools,
		"stream": false,
	})

	resp, err := client.Post(baseURL+"/api/chat", "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Printf("ERROR: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	var result map[string]interface{}
	json.Unmarshal(data, &result)
	msg, _ := result["message"].(map[string]interface{})

	if toolCalls, ok := msg["tool_calls"].([]interface{}); ok && len(toolCalls) > 0 {
		fmt.Printf("✅ %s used calculator %d times!\n\n", model, len(toolCalls))
		var prevResult float64
		for i, tc := range toolCalls {
			tcMap := tc.(map[string]interface{})
			fn, _ := tcMap["function"].(map[string]interface{})
			toolName, _ := fn["name"].(string)
			argsStr, _ := fn["arguments"].(string)

			var args map[string]interface{}
			json.Unmarshal([]byte(argsStr), &args)
			expr, _ := args["expression"].(string)

			tool, exists := reg.Get(toolName)
			if !exists {
				continue
			}
			result, err := tool.Execute(context.Background(), args)
			if err != nil {
				fmt.Printf("  %d. %s = ERROR: %v\n", i+1, expr, err)
				continue
			}
			data, ok := result.Data.(map[string]interface{})
			if !ok || data == nil {
				fmt.Printf("  %d. %s = failed (no result data)\n", i+1, expr)
				continue
			}
			val, _ := data["result"].(float64)
			arrow := "↑"
			if i > 0 && val < prevResult {
				arrow = "✗ DECREASING!"
			}
			fmt.Printf("  %d. x=%s → z = %.4f %s\n", i+1, extractX(expr), val, arrow)
			prevResult = val
		}
	} else {
		content, _ := msg["content"].(string)
		if len(content) > 500 {
			content = content[:500]
		}
		fmt.Printf("❌ %s did NOT use calculator.\n%s\n", model, content)
	}
}

func extractX(expr string) string {
	for _, s := range []string{"20", "10", "5", "2", "1"} {
		for _, p := range []string{s + "**", s + "^"} {
			if len(expr) > 2 && expr[:2] == p[:2] {
				return s
			}
		}
	}
	return expr
}

func toTools(schemas []core.ToolSchema) []map[string]interface{} {
	out := make([]map[string]interface{}, len(schemas))
	for i, s := range schemas {
		params, _ := json.Marshal(s.Parameters)
		out[i] = map[string]interface{}{
			"function": map[string]interface{}{
				"name":        s.Name,
				"description": s.Description,
				"parameters":  json.RawMessage(params),
			},
		}
	}
	return out
}

func getEnv(k, f string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return f
}
