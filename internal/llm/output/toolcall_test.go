package output

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestToolDefinitionSerialization(t *testing.T) {
	t.Run("marshal tool definition", func(t *testing.T) {
		tool := ToolDefinition{
			Name:        "get_weather",
			Description: "Get current weather for a city",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"city": map[string]interface{}{
						"type":        "string",
						"description": "The city name",
					},
				},
				"required": []string{"city"},
			},
		}

		data, err := json.Marshal(tool)
		if err != nil {
			t.Fatalf("marshal error: %v", err)
		}

		var parsed map[string]interface{}
		if err := json.Unmarshal(data, &parsed); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}

		if parsed["name"] != "get_weather" {
			t.Errorf("expected name 'get_weather', got %v", parsed["name"])
		}
		if parsed["description"] != "Get current weather for a city" {
			t.Errorf("expected description, got %v", parsed["description"])
		}
		if parsed["parameters"] == nil {
			t.Error("expected parameters to be present")
		}
	})

	t.Run("marshal API tool definition wrapper", func(t *testing.T) {
		def := ToolDefinition{
			Name:        "calculate",
			Description: "Perform a calculation",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"expression": map[string]interface{}{
						"type": "string",
					},
				},
			},
		}

		apiDefs := BuildToolAPIDefinitions([]ToolDefinition{def})
		if len(apiDefs) != 1 {
			t.Fatalf("expected 1 API definition, got %d", len(apiDefs))
		}

		apiDef := apiDefs[0]
		if apiDef.Type != "function" {
			t.Errorf("expected type 'function', got %q", apiDef.Type)
		}
		if apiDef.Function == nil {
			t.Fatal("expected function to be non-nil")
		}
		if apiDef.Function.Name != "calculate" {
			t.Errorf("expected function name 'calculate', got %q", apiDef.Function.Name)
		}

		data, err := json.Marshal(apiDefs)
		if err != nil {
			t.Fatalf("marshal error: %v", err)
		}

		jsonStr := string(data)
		if !strings.Contains(jsonStr, `"type":"function"`) {
			t.Error("expected type field in JSON")
		}
		if !strings.Contains(jsonStr, `"name":"calculate"`) {
			t.Error("expected name field in JSON")
		}
	})

	t.Run("empty tools list", func(t *testing.T) {
		apiDefs := BuildToolAPIDefinitions(nil)
		if len(apiDefs) != 0 {
			t.Errorf("expected 0 API definitions, got %d", len(apiDefs))
		}
	})
}

func TestToolCallResponseParsing(t *testing.T) {
	t.Run("text response without tool calls", func(t *testing.T) {
		choice := &Choice{
			Message: Message{
				Role:    "assistant",
				Content: "The weather is sunny.",
			},
			FinishReason: "stop",
		}

		resp, err := parseToolCallsFromResponse(choice)
		if err != nil {
			t.Fatalf("parse error: %v", err)
		}

		if resp.Content != "The weather is sunny." {
			t.Errorf("expected content 'The weather is sunny.', got %q", resp.Content)
		}
		if len(resp.ToolCalls) != 0 {
			t.Errorf("expected 0 tool calls, got %d", len(resp.ToolCalls))
		}
		if !resp.Done {
			t.Error("expected Done=true for text response")
		}
		if resp.Message == nil {
			t.Fatal("expected Message to be non-nil")
		}
		if resp.Message.Role != "assistant" {
			t.Errorf("expected role 'assistant', got %q", resp.Message.Role)
		}
	})

	t.Run("tool call response", func(t *testing.T) {
		choice := &Choice{
			Message: Message{
				Role:    "assistant",
				Content: "",
				ToolCalls: []openAIToolCall{
					{
						ID:   "call_abc123",
						Type: "function",
						Function: openAIToolCallFuncRef{
							Name:      "get_weather",
							Arguments: `{"city":"Seattle"}`,
						},
					},
				},
			},
			FinishReason: "tool_calls",
		}

		resp, err := parseToolCallsFromResponse(choice)
		if err != nil {
			t.Fatalf("parse error: %v", err)
		}

		if resp.Content != "" {
			t.Errorf("expected empty content, got %q", resp.Content)
		}
		if resp.Done {
			t.Error("expected Done=false for tool call response")
		}
		if len(resp.ToolCalls) != 1 {
			t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
		}

		tc := resp.ToolCalls[0]
		if tc.ID != "call_abc123" {
			t.Errorf("expected ID 'call_abc123', got %q", tc.ID)
		}
		if tc.Name != "get_weather" {
			t.Errorf("expected name 'get_weather', got %q", tc.Name)
		}
		if tc.Arguments != `{"city":"Seattle"}` {
			t.Errorf("expected arguments, got %q", tc.Arguments)
		}

		if resp.Message == nil {
			t.Fatal("expected Message to be non-nil")
		}
		if len(resp.Message.ToolCalls) != 1 {
			t.Fatalf("expected 1 assistant tool call, got %d", len(resp.Message.ToolCalls))
		}
		if resp.Message.ToolCalls[0].ID != "call_abc123" {
			t.Errorf("expected assistant tool call ID 'call_abc123', got %q", resp.Message.ToolCalls[0].ID)
		}
	})

	t.Run("multiple tool calls", func(t *testing.T) {
		choice := &Choice{
			Message: Message{
				Role:    "assistant",
				Content: "",
				ToolCalls: []openAIToolCall{
					{
						ID:   "call_1",
						Type: "function",
						Function: openAIToolCallFuncRef{
							Name:      "get_weather",
							Arguments: `{"city":"Seattle"}`,
						},
					},
					{
						ID:   "call_2",
						Type: "function",
						Function: openAIToolCallFuncRef{
							Name:      "get_time",
							Arguments: `{"timezone":"PST"}`,
						},
					},
				},
			},
			FinishReason: "tool_calls",
		}

		resp, err := parseToolCallsFromResponse(choice)
		if err != nil {
			t.Fatalf("parse error: %v", err)
		}

		if len(resp.ToolCalls) != 2 {
			t.Fatalf("expected 2 tool calls, got %d", len(resp.ToolCalls))
		}
		if resp.ToolCalls[0].Name != "get_weather" {
			t.Errorf("expected first tool name 'get_weather', got %q", resp.ToolCalls[0].Name)
		}
		if resp.ToolCalls[1].Name != "get_time" {
			t.Errorf("expected second tool name 'get_time', got %q", resp.ToolCalls[1].Name)
		}
		if resp.Done {
			t.Error("expected Done=false for multiple tool calls")
		}
	})
}

func TestBuildToolResultMessages(t *testing.T) {
	t.Run("single result", func(t *testing.T) {
		results := []ToolResult{
			{ToolCallID: "call_123", Content: "Temperature: 72F"},
		}

		msgs := BuildToolResultMessages(results)
		if len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %d", len(msgs))
		}

		msg := msgs[0]
		if msg["role"] != "tool" {
			t.Errorf("expected role 'tool', got %v", msg["role"])
		}
		if msg["tool_call_id"] != "call_123" {
			t.Errorf("expected tool_call_id 'call_123', got %v", msg["tool_call_id"])
		}
		if msg["content"] != "Temperature: 72F" {
			t.Errorf("expected content, got %v", msg["content"])
		}
	})

	t.Run("multiple results", func(t *testing.T) {
		results := []ToolResult{
			{ToolCallID: "call_1", Content: "result1"},
			{ToolCallID: "call_2", Content: "result2"},
		}

		msgs := BuildToolResultMessages(results)
		if len(msgs) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(msgs))
		}
		if msgs[0]["tool_call_id"] != "call_1" {
			t.Errorf("expected first tool_call_id 'call_1', got %v", msgs[0]["tool_call_id"])
		}
		if msgs[1]["tool_call_id"] != "call_2" {
			t.Errorf("expected second tool_call_id 'call_2', got %v", msgs[1]["tool_call_id"])
		}
	})

	t.Run("empty results", func(t *testing.T) {
		msgs := BuildToolResultMessages(nil)
		if len(msgs) != 0 {
			t.Errorf("expected 0 messages, got %d", len(msgs))
		}
	})
}

func TestAssistantMsgToMap(t *testing.T) {
	t.Run("message without tool calls", func(t *testing.T) {
		msg := &AssistantMsg{
			Role:    "assistant",
			Content: "Hello!",
		}

		m := msg.toMap()
		if m["role"] != "assistant" {
			t.Errorf("expected role 'assistant', got %v", m["role"])
		}
		if m["content"] != "Hello!" {
			t.Errorf("expected content 'Hello!', got %v", m["content"])
		}
		if _, ok := m["tool_calls"]; ok {
			t.Error("expected no tool_calls key for message without tool calls")
		}
	})

	t.Run("message with tool calls", func(t *testing.T) {
		msg := &AssistantMsg{
			Role:    "assistant",
			Content: "",
			ToolCalls: []AssistantToolCall{
				{
					ID:   "call_abc",
					Type: "function",
					Function: AssistantToolFuncRef{
						Name:      "search",
						Arguments: `{"query":"test"}`,
					},
				},
			},
		}

		m := msg.toMap()
		calls, ok := m["tool_calls"].([]map[string]interface{})
		if !ok {
			t.Fatal("expected tool_calls to be a slice")
		}
		if len(calls) != 1 {
			t.Fatalf("expected 1 tool call, got %d", len(calls))
		}

		call := calls[0]
		if call["id"] != "call_abc" {
			t.Errorf("expected id 'call_abc', got %v", call["id"])
		}
		if call["type"] != "function" {
			t.Errorf("expected type 'function', got %v", call["type"])
		}

		fn, ok := call["function"].(map[string]interface{})
		if !ok {
			t.Fatal("expected function to be a map")
		}
		if fn["name"] != "search" {
			t.Errorf("expected function name 'search', got %v", fn["name"])
		}
		if fn["arguments"] != `{"query":"test"}` {
			t.Errorf("expected arguments, got %v", fn["arguments"])
		}
	})
}

func TestToolCallResponseBuildAPIMessages(t *testing.T) {
	t.Run("with message", func(t *testing.T) {
		resp := &ToolCallResponse{
			Message: &AssistantMsg{
				Role:    "assistant",
				Content: "thinking...",
			},
		}

		msgs := resp.buildAPIMessages()
		if len(msgs) != 1 {
			t.Fatalf("expected 1 message, got %d", len(msgs))
		}
		if msgs[0]["role"] != "assistant" {
			t.Errorf("expected role 'assistant', got %v", msgs[0]["role"])
		}
	})

	t.Run("nil message", func(t *testing.T) {
		resp := &ToolCallResponse{}
		msgs := resp.buildAPIMessages()
		if msgs != nil {
			t.Errorf("expected nil messages, got %v", msgs)
		}
	})
}

func TestOpenAIAdapter_GenerateWithTools(t *testing.T) {
	t.Run("successful tool call", func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("failed to decode request: %v", err)
				return
			}

			tools, ok := body["tools"].([]interface{})
			if !ok || len(tools) == 0 {
				t.Error("expected tools in request")
			}

			if body["tool_choice"] != "auto" {
				t.Errorf("expected tool_choice 'auto', got %v", body["tool_choice"])
			}

			resp := map[string]interface{}{
				"choices": []map[string]interface{}{
					{
						"message": map[string]interface{}{
							"role":    "assistant",
							"content": "",
							"tool_calls": []map[string]interface{}{
								{
									"id":   "call_test123",
									"type": "function",
									"function": map[string]interface{}{
										"name":      "get_weather",
										"arguments": `{"city":"Seattle"}`,
									},
								},
							},
						},
						"finish_reason": "tool_calls",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(resp); err != nil {
				t.Errorf("failed to encode response: %v", err)
			}
		})

		server := httptest.NewServer(handler)
		defer server.Close()

		adapter := NewOpenAIAdapter(&Config{
			BaseURL: server.URL,
			APIKey:  "test-key",
			Model:   "gpt-4",
			Timeout: 5,
		})

		tools := []ToolDefinition{
			{
				Name:        "get_weather",
				Description: "Get weather for a city",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"city": map[string]interface{}{
							"type": "string",
						},
					},
				},
			},
		}

		ctx := context.Background()
		resp, err := adapter.GenerateWithTools(ctx, "What is the weather?", tools, ToolChoiceAuto)
		if err != nil {
			t.Fatalf("GenerateWithTools error: %v", err)
		}

		if resp.Done {
			t.Error("expected Done=false for tool call response")
		}
		if len(resp.ToolCalls) != 1 {
			t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
		}
		if resp.ToolCalls[0].Name != "get_weather" {
			t.Errorf("expected tool name 'get_weather', got %q", resp.ToolCalls[0].Name)
		}
		if resp.ToolCalls[0].ID != "call_test123" {
			t.Errorf("expected tool ID 'call_test123', got %q", resp.ToolCalls[0].ID)
		}
		if resp.Message == nil {
			t.Fatal("expected Message to be non-nil")
		}
		if resp.Message.Role != "assistant" {
			t.Errorf("expected role 'assistant', got %q", resp.Message.Role)
		}
	})

	t.Run("text response without tool calls", func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := map[string]interface{}{
				"choices": []map[string]interface{}{
					{
						"message": map[string]interface{}{
							"role":    "assistant",
							"content": "The weather is sunny and 72F.",
						},
						"finish_reason": "stop",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(resp); err != nil {
				t.Errorf("failed to encode response: %v", err)
			}
		})

		server := httptest.NewServer(handler)
		defer server.Close()

		adapter := NewOpenAIAdapter(&Config{
			BaseURL: server.URL,
			APIKey:  "test-key",
			Model:   "gpt-4",
			Timeout: 5,
		})

		tools := []ToolDefinition{
			{Name: "get_weather", Description: "Get weather"},
		}

		ctx := context.Background()
		resp, err := adapter.GenerateWithTools(ctx, "Hello", tools, ToolChoiceAuto)
		if err != nil {
			t.Fatalf("GenerateWithTools error: %v", err)
		}

		if !resp.Done {
			t.Error("expected Done=true for text response")
		}
		if len(resp.ToolCalls) != 0 {
			t.Errorf("expected 0 tool calls, got %d", len(resp.ToolCalls))
		}
		if resp.Content != "The weather is sunny and 72F." {
			t.Errorf("expected content, got %q", resp.Content)
		}
	})

	t.Run("empty prompt rejected", func(t *testing.T) {
		adapter := NewOpenAIAdapter(&Config{Model: "gpt-4"})
		tools := []ToolDefinition{{Name: "test", Description: "test"}}

		_, err := adapter.GenerateWithTools(context.Background(), "", tools, ToolChoiceAuto)
		if err == nil {
			t.Error("expected error for empty prompt")
		}
	})

	t.Run("no tools rejected", func(t *testing.T) {
		adapter := NewOpenAIAdapter(&Config{Model: "gpt-4"})

		_, err := adapter.GenerateWithTools(context.Background(), "test", nil, ToolChoiceAuto)
		if err == nil {
			t.Error("expected error for no tools")
		}
	})

	t.Run("HTTP error", func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			if _, err := w.Write([]byte("internal error")); err != nil {
				t.Errorf("failed to write error: %v", err)
			}
		})

		server := httptest.NewServer(handler)
		defer server.Close()

		adapter := NewOpenAIAdapter(&Config{
			BaseURL: server.URL,
			APIKey:  "test-key",
			Model:   "gpt-4",
			Timeout: 5,
		})

		tools := []ToolDefinition{{Name: "test", Description: "test"}}
		_, err := adapter.GenerateWithTools(context.Background(), "test", tools, ToolChoiceAuto)
		if err == nil {
			t.Error("expected error for HTTP 500")
		}
		if !strings.Contains(err.Error(), "500") {
			t.Errorf("error should contain status code, got: %v", err)
		}
	})

	t.Run("empty choices", func(t *testing.T) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := map[string]interface{}{
				"choices": []map[string]interface{}{},
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(resp); err != nil {
				t.Errorf("failed to encode response: %v", err)
			}
		})

		server := httptest.NewServer(handler)
		defer server.Close()

		adapter := NewOpenAIAdapter(&Config{
			BaseURL: server.URL,
			APIKey:  "test-key",
			Model:   "gpt-4",
			Timeout: 5,
		})

		tools := []ToolDefinition{{Name: "test", Description: "test"}}
		_, err := adapter.GenerateWithTools(context.Background(), "test", tools, ToolChoiceAuto)
		if err == nil {
			t.Error("expected error for empty choices")
		}
	})
}

func TestOpenAIAdapter_SendToolResult(t *testing.T) {
	t.Run("successful continuation", func(t *testing.T) {
		callCount := 0
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("failed to decode request: %v", err)
				return
			}

			messages, ok := body["messages"].([]interface{})
			if !ok {
				t.Error("expected messages in request")
				return
			}

			callCount++

			// Second call should include tool result messages.
			if callCount == 2 {
				// Should have: user, assistant (with tool_calls), tool result.
				if len(messages) < 3 {
					t.Errorf("expected at least 3 messages, got %d", len(messages))
				}

				// Verify the tool message is present.
				lastMsg, ok := messages[len(messages)-1].(map[string]interface{})
				if !ok {
					t.Error("expected last message to be a map")
					return
				}
				if lastMsg["role"] != "tool" {
					t.Errorf("expected last message role 'tool', got %v", lastMsg["role"])
				}
				if lastMsg["tool_call_id"] != "call_abc123" {
					t.Errorf("expected tool_call_id 'call_abc123', got %v", lastMsg["tool_call_id"])
				}
			}

			resp := map[string]interface{}{
				"choices": []map[string]interface{}{
					{
						"message": map[string]interface{}{
							"role":    "assistant",
							"content": "The weather in Seattle is 55F and rainy.",
						},
						"finish_reason": "stop",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(resp); err != nil {
				t.Errorf("failed to encode response: %v", err)
			}
		})

		server := httptest.NewServer(handler)
		defer server.Close()

		adapter := NewOpenAIAdapter(&Config{
			BaseURL: server.URL,
			APIKey:  "test-key",
			Model:   "gpt-4",
			Timeout: 5,
		})

		// Simulate conversation: first call gets tool call.
		tools := []ToolDefinition{
			{Name: "get_weather", Description: "Get weather"},
		}
		ctx := context.Background()
		firstResp, err := adapter.GenerateWithTools(ctx, "What is the weather in Seattle?", tools, ToolChoiceAuto)
		if err != nil {
			t.Fatalf("first call error: %v", err)
		}

		// Build conversation messages for continuation.
		messages := []map[string]interface{}{
			{"role": "user", "content": "What is the weather in Seattle?"},
		}
		messages = append(messages, firstResp.buildAPIMessages()...)

		// Send tool result.
		toolResults := []ToolResult{
			{ToolCallID: "call_abc123", Content: "55F, rainy"},
		}

		secondResp, err := adapter.SendToolResult(ctx, messages, toolResults)
		if err != nil {
			t.Fatalf("SendToolResult error: %v", err)
		}

		if !secondResp.Done {
			t.Error("expected Done=true after tool result")
		}
		if secondResp.Content != "The weather in Seattle is 55F and rainy." {
			t.Errorf("expected final content, got %q", secondResp.Content)
		}
		if len(secondResp.ToolCalls) != 0 {
			t.Errorf("expected 0 tool calls in final response, got %d", len(secondResp.ToolCalls))
		}
	})

	t.Run("empty messages rejected", func(t *testing.T) {
		adapter := NewOpenAIAdapter(&Config{Model: "gpt-4"})
		_, err := adapter.SendToolResult(context.Background(), nil, []ToolResult{{ToolCallID: "x"}})
		if err == nil {
			t.Error("expected error for empty messages")
		}
	})

	t.Run("empty tool results rejected", func(t *testing.T) {
		adapter := NewOpenAIAdapter(&Config{Model: "gpt-4"})
		msgs := []map[string]interface{}{{"role": "user", "content": "hi"}}
		_, err := adapter.SendToolResult(context.Background(), msgs, nil)
		if err == nil {
			t.Error("expected error for empty tool results")
		}
	})

	t.Run("multi-turn tool loop", func(t *testing.T) {
		callCount := 0
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount++

			var resp map[string]interface{}
			switch callCount {
			case 1:
				// First call: request tool call.
				resp = map[string]interface{}{
					"choices": []map[string]interface{}{
						{
							"message": map[string]interface{}{
								"role":    "assistant",
								"content": "",
								"tool_calls": []map[string]interface{}{
									{
										"id":   "call_1",
										"type": "function",
										"function": map[string]interface{}{
											"name":      "search",
											"arguments": `{"query":"weather"}`,
										},
									},
								},
							},
							"finish_reason": "tool_calls",
						},
					},
				}
			case 2:
				// Second call: request another tool call.
				resp = map[string]interface{}{
					"choices": []map[string]interface{}{
						{
							"message": map[string]interface{}{
								"role":    "assistant",
								"content": "",
								"tool_calls": []map[string]interface{}{
									{
										"id":   "call_2",
										"type": "function",
										"function": map[string]interface{}{
											"name":      "lookup",
											"arguments": `{"id":"42"}`,
										},
									},
								},
							},
							"finish_reason": "tool_calls",
						},
					},
				}
			default:
				// Third call: final text response.
				resp = map[string]interface{}{
					"choices": []map[string]interface{}{
						{
							"message": map[string]interface{}{
								"role":    "assistant",
								"content": "Done processing.",
							},
							"finish_reason": "stop",
						},
					},
				}
			}

			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(resp); err != nil {
				t.Errorf("failed to encode response: %v", err)
			}
		})

		server := httptest.NewServer(handler)
		defer server.Close()

		adapter := NewOpenAIAdapter(&Config{
			BaseURL: server.URL,
			APIKey:  "test-key",
			Model:   "gpt-4",
			Timeout: 5,
		})

		ctx := context.Background()
		tools := []ToolDefinition{
			{Name: "search", Description: "Search"},
			{Name: "lookup", Description: "Lookup"},
		}

		// First call.
		resp, err := adapter.GenerateWithTools(ctx, "test", tools, ToolChoiceAuto)
		if err != nil {
			t.Fatalf("call 1 error: %v", err)
		}
		if resp.Done {
			t.Fatal("expected Done=false after first call")
		}

		messages := []map[string]interface{}{
			{"role": "user", "content": "test"},
		}
		messages = append(messages, resp.buildAPIMessages()...)

		// Second call (first tool result).
		resp, err = adapter.SendToolResult(ctx, messages, []ToolResult{
			{ToolCallID: "call_1", Content: "search results"},
		})
		if err != nil {
			t.Fatalf("call 2 error: %v", err)
		}
		if resp.Done {
			t.Fatal("expected Done=false after second call")
		}

		messages = append(messages, resp.buildAPIMessages()...)

		// Third call (second tool result).
		resp, err = adapter.SendToolResult(ctx, messages, []ToolResult{
			{ToolCallID: "call_2", Content: "lookup result"},
		})
		if err != nil {
			t.Fatalf("call 3 error: %v", err)
		}
		if !resp.Done {
			t.Error("expected Done=true after final response")
		}
		if resp.Content != "Done processing." {
			t.Errorf("expected final content, got %q", resp.Content)
		}

		if callCount != 3 {
			t.Errorf("expected 3 API calls, got %d", callCount)
		}
	})
}

func TestOpenAIAdapter_ToolCapableInterface(t *testing.T) {
	adapter := NewOpenAIAdapter(&Config{Model: "gpt-4"})
	if _, ok := interface{}(adapter).(ToolCapable); !ok {
		t.Error("OpenAIAdapter should implement ToolCapable interface")
	}
}
