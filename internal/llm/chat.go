package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Timwood0x10/ares/api/core"
	"github.com/Timwood0x10/ares/internal/ares_callbacks"
	coreerrors "github.com/Timwood0x10/ares/internal/core/errors"
	"github.com/Timwood0x10/ares/internal/errors"
)

// Chat sends a chat request with tool support to the LLM.
// Accepts structured messages and optional tools, returning a rich response
// that may include tool_calls. Supported by all providers.
// Args:
//
//	ctx - operation context.
//	messages - conversation messages.
//	tools - available tools for function calling (may be empty).
//
// Returns:
//
//	*core.GenerateResponse - the chat response including optional tool_calls.
//	error - request, decode, or unsupported provider error.
func (c *Client) Chat(ctx context.Context, messages []*core.LLMMessage, tools []core.Tool) (*core.GenerateResponse, error) {
	start := time.Now()
	model := ""
	if c.config != nil {
		model = c.config.Model
	}

	// Validate input messages.
	if len(messages) == 0 {
		err := coreerrors.ErrInvalidArgument
		c.emitCallback(&ares_callbacks.Context{
			Event: ares_callbacks.EventLLMError,
			Model: model,
			Error: err,
		})
		return nil, fmt.Errorf("chat: %w", err)
	}

	// Apply rate limiter before making the API call.
	if c.limiter != nil {
		if waitErr := c.limiter.Wait(ctx); waitErr != nil {
			c.recordLLMCall(ctx, "chat", "", 0, start, waitErr)
			c.emitCallback(&ares_callbacks.Context{
				Event: ares_callbacks.EventLLMError,
				Model: model,
				Error: waitErr,
			})
			return nil, waitErr
		}
	}

	c.emitCallback(&ares_callbacks.Context{
		Event: ares_callbacks.EventLLMStart,
		Model: model,
	})

	var result *core.GenerateResponse
	var err error

	switch ProviderType(c.config.Provider) {
	case ProviderOllama:
		result, err = c.chatOllama(ctx, messages, tools)
	case ProviderOpenAI, ProviderOpenRouter:
		result, err = c.chatOpenAI(ctx, messages, tools)
	case ProviderAnthropic:
		result, err = c.chatAnthropic(ctx, messages, tools)
	default:
		err = fmt.Errorf("chat: unsupported provider: %s", c.config.Provider)
	}

	duration := time.Since(start)
	promptSummary := summarizeMessages(messages)
	var responseContent string
	if result != nil {
		responseContent = result.Content
	}
	c.recordLLMCall(ctx, promptSummary, responseContent, 0, start, err)

	if err != nil {
		c.emitCallback(&ares_callbacks.Context{
			Event:    ares_callbacks.EventLLMError,
			Model:    model,
			Error:    err,
			Duration: duration,
		})
	} else {
		c.emitCallback(&ares_callbacks.Context{
			Event:    ares_callbacks.EventLLMEnd,
			Model:    model,
			Output:   result.Content,
			Duration: duration,
		})
	}

	return result, err
}

// summarizeMessages returns a brief summary of messages for logging/tracing.
func summarizeMessages(messages []*core.LLMMessage) string {
	return fmt.Sprintf("chat(%d messages)", len(messages))
}

// chatOllama sends a chat request to Ollama with tool support.
// Uses /api/chat endpoint which natively supports OpenAI-compatible tools field.
// Args:
//
//	ctx - operation context.
//	messages - conversation messages to send.
//	tools - available tools for function calling (may be empty).
//
// Returns:
//
//	*core.GenerateResponse - the chat response, including tool_calls if present.
//	error - request or decode error.
func (c *Client) chatOllama(ctx context.Context, messages []*core.LLMMessage, tools []core.Tool) (*core.GenerateResponse, error) {
	body := map[string]any{
		"model":    c.config.Model,
		"messages": buildOpenAIChatMessages(messages),
		"stream":   false,
		"options": map[string]any{
			"temperature": 0.7,
			"num_predict": defaultMaxTokens,
		},
	}
	if len(tools) > 0 {
		body["tools"] = buildOpenAIChatTools(tools)
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, errors.Wrap(err, "marshal ollama chat request")
	}

	baseURL := c.config.BaseURL
	if baseURL == "" {
		baseURL = DefaultOllamaBaseURL
	}

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/api/chat", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, errors.Wrap(err, "create ollama chat request")
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama chat: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			log.Error("failed to close ollama chat response body", "error", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		if readErr != nil {
			log.Warn("llm: failed to read ollama chat error response body", "error", readErr)
		}
		return nil, &HTTPError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("ollama chat error (status %d): %s", resp.StatusCode, string(respBody)),
		}
	}

	var ollamaResp struct {
		Message struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, errors.Wrap(err, "decode ollama chat response")
	}

	respCore := &core.GenerateResponse{
		Content: ollamaResp.Message.Content,
	}
	for _, tc := range ollamaResp.Message.ToolCalls {
		respCore.ToolCalls = append(respCore.ToolCalls, core.ToolCall{
			ID:   tc.ID,
			Type: tc.Type,
			Function: core.FunctionCall{
				Name:      tc.Function.Name,
				Arguments: string(tc.Function.Arguments),
			},
		})
	}
	return respCore, nil
}

// chatOpenAI sends a chat request to OpenAI/OpenRouter with tool support.
// Uses /chat/completions endpoint which natively supports the tools field.
// Args:
//
//	ctx - operation context.
//	messages - conversation messages to send.
//	tools - available tools for function calling (may be empty).
//
// Returns:
//
//	*core.GenerateResponse - the chat response, including tool_calls if present.
//	error - request or decode error.
func (c *Client) chatOpenAI(ctx context.Context, messages []*core.LLMMessage, tools []core.Tool) (*core.GenerateResponse, error) {
	if c.config.APIKey == "" {
		return nil, fmt.Errorf("API key is required for OpenAI/OpenRouter chat")
	}

	maxTokens := c.config.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	reqBody := map[string]any{
		"model":       c.config.Model,
		"messages":    buildOpenAIChatMessages(messages),
		"temperature": 0.7,
		"max_tokens":  maxTokens,
	}
	if len(tools) > 0 {
		reqBody["tools"] = buildOpenAIChatTools(tools)
		reqBody["tool_choice"] = "auto"
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, errors.Wrap(err, "marshal openai chat request")
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.config.BaseURL+"/chat/completions", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, errors.Wrap(err, "create openai chat request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	req.Header.Set("X-Title", "GoAgent")

	return c.decodeOpenAIChatResponse(ctx, req)
}

// decodeOpenAIChatResponse sends the request and decodes the OpenAI chat response.
func (c *Client) decodeOpenAIChatResponse(ctx context.Context, req *http.Request) (*core.GenerateResponse, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai chat: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			log.Error("failed to close openai chat response body", "error", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		if readErr != nil {
			log.Warn("llm: failed to read openai chat error response body", "error", readErr)
		}
		return nil, &HTTPError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("openai chat error (status %d): %s", resp.StatusCode, string(respBody)),
		}
	}

	var chatResp struct {
		Choices []struct {
			Message struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, errors.Wrap(err, "decode openai chat response")
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in openai chat response")
	}

	choice := chatResp.Choices[0]
	respCore := &core.GenerateResponse{
		Content:      choice.Message.Content,
		FinishReason: choice.FinishReason,
	}
	for _, tc := range choice.Message.ToolCalls {
		respCore.ToolCalls = append(respCore.ToolCalls, core.ToolCall{
			ID:   tc.ID,
			Type: tc.Type,
			Function: core.FunctionCall{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		})
	}
	return respCore, nil
}

// buildOpenAIChatMessages converts core.LLMMessage slice to OpenAI chat format.
// Handles tool-role and assistant messages with tool_calls.
func buildOpenAIChatMessages(messages []*core.LLMMessage) []map[string]any {
	result := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		switch {
		case msg.Role == "tool":
			result = append(result, map[string]any{
				"role":         "tool",
				"tool_call_id": msg.ToolCallID,
				"content":      msg.Content,
			})
		case msg.Role == "assistant" && len(msg.ToolCalls) > 0:
			toolCalls := make([]map[string]any, 0, len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				toolCalls = append(toolCalls, map[string]any{
					"id":   tc.ID,
					"type": tc.Type,
					"function": map[string]any{
						"name":      tc.Function.Name,
						"arguments": tc.Function.Arguments,
					},
				})
			}
			m := map[string]any{
				"role":       "assistant",
				"tool_calls": toolCalls,
			}
			if msg.Content != "" {
				m["content"] = msg.Content
			}
			result = append(result, m)
		default:
			result = append(result, map[string]any{
				"role":    msg.Role,
				"content": msg.Content,
			})
		}
	}
	return result
}

// buildOpenAIChatTools converts core.Tool slice to OpenAI tools format.
func buildOpenAIChatTools(tools []core.Tool) []map[string]any {
	result := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		result = append(result, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Function.Name,
				"description": t.Function.Description,
				"parameters":  t.Function.Parameters,
			},
		})
	}
	return result
}

// chatAnthropic sends a chat request to Anthropic with tool support.
// Uses /messages endpoint with Anthropic-specific tool format.
// Args:
//
//	ctx - operation context.
//	messages - conversation messages to send.
//	tools - available tools for function calling (may be empty).
//
// Returns:
//
//	*core.GenerateResponse - the chat response, including tool_calls if present.
//	error - request or decode error.
func (c *Client) chatAnthropic(ctx context.Context, messages []*core.LLMMessage, tools []core.Tool) (*core.GenerateResponse, error) {
	if c.config.APIKey == "" {
		return nil, fmt.Errorf("API key is required for Anthropic chat")
	}

	maxTokens := c.config.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}

	chatMsgs, systemPrompt := buildAnthropicChatMessages(messages)

	reqBody := map[string]any{
		"model":      c.config.Model,
		"messages":   chatMsgs,
		"max_tokens": maxTokens,
	}
	if systemPrompt != "" {
		reqBody["system"] = systemPrompt
	}
	if len(tools) > 0 {
		reqBody["tools"] = buildAnthropicChatTools(tools)
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, errors.Wrap(err, "marshal anthropic chat request")
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.config.BaseURL+"/messages", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, errors.Wrap(err, "create anthropic chat request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.config.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	return c.decodeAnthropicChatResponse(ctx, req)
}

// decodeAnthropicChatResponse sends the request and decodes the Anthropic chat response.
func (c *Client) decodeAnthropicChatResponse(ctx context.Context, req *http.Request) (*core.GenerateResponse, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic chat: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			log.Error("failed to close anthropic chat response body", "error", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		if readErr != nil {
			log.Warn("llm: failed to read anthropic chat error response body", "error", readErr)
		}
		return nil, &HTTPError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("anthropic chat error (status %d): %s", resp.StatusCode, string(respBody)),
		}
	}

	var chatResp struct {
		Content []struct {
			Type  string `json:"type"`
			Text  string `json:"text,omitempty"`
			ID    string `json:"id,omitempty"`
			Name  string `json:"name,omitempty"`
			Input any    `json:"input,omitempty"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, errors.Wrap(err, "decode anthropic chat response")
	}

	respCore := &core.GenerateResponse{}
	for _, block := range chatResp.Content {
		switch block.Type {
		case "text":
			respCore.Content += block.Text
		case "tool_use":
			argsJSON, err := json.Marshal(block.Input)
			if err != nil {
				log.Error("llm: marshal tool call input", "error", err)
				continue
			}
			respCore.ToolCalls = append(respCore.ToolCalls, core.ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: core.FunctionCall{
					Name:      block.Name,
					Arguments: string(argsJSON),
				},
			})
		}
	}
	if chatResp.StopReason != "" {
		respCore.FinishReason = chatResp.StopReason
	}
	return respCore, nil
}

// buildAnthropicChatMessages converts core.LLMMessage slice to Anthropic format.
// System-role messages are extracted into a separate system prompt string.
// Tool-result messages are converted to Anthropic's user+tool_result format.
// Returns (messages, systemPrompt).
func buildAnthropicChatMessages(messages []*core.LLMMessage) ([]map[string]any, string) {
	var systemParts []string
	result := make([]map[string]any, 0, len(messages))

	for _, msg := range messages {
		switch {
		case msg.Role == "system":
			systemParts = append(systemParts, msg.Content)
		case msg.Role == "tool":
			// Anthropic requires tool results as user messages with tool_result content blocks.
			result = append(result, map[string]any{
				"role": "user",
				"content": []map[string]any{
					{
						"type":        "tool_result",
						"tool_use_id": msg.ToolCallID,
						"content":     msg.Content,
					},
				},
			})
		case msg.Role == "assistant" && len(msg.ToolCalls) > 0:
			content := make([]map[string]any, 0, len(msg.ToolCalls)+1)
			if msg.Content != "" {
				content = append(content, map[string]any{
					"type": "text",
					"text": msg.Content,
				})
			}
			for _, tc := range msg.ToolCalls {
				var inputObj any
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &inputObj); err != nil {
					inputObj = map[string]any{}
				}
				content = append(content, map[string]any{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Function.Name,
					"input": inputObj,
				})
			}
			result = append(result, map[string]any{
				"role":    "assistant",
				"content": content,
			})
		default:
			result = append(result, map[string]any{
				"role":    msg.Role,
				"content": msg.Content,
			})
		}
	}

	var systemPrompt string
	if len(systemParts) > 0 {
		systemPrompt = systemParts[0]
		for _, s := range systemParts[1:] {
			systemPrompt += "\n" + s
		}
	}
	return result, systemPrompt
}

// buildAnthropicChatTools converts core.Tool slice to Anthropic flat tool format.
// Anthropic uses {name, description, input_schema} instead of OpenAI's
// {type:"function", function:{name,description,parameters}} wrapper.
func buildAnthropicChatTools(tools []core.Tool) []map[string]any {
	result := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		result = append(result, map[string]any{
			"name":         t.Function.Name,
			"description":  t.Function.Description,
			"input_schema": t.Function.Parameters,
		})
	}
	return result
}
