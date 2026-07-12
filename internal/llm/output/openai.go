package output

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/errors"
)

const (
	keyRole        = "role"
	keyContent     = "content"
	keyModel       = "model"
	keyMessages    = "messages"
	keyMaxTokens   = "max_tokens"
	keyTemperature = "temperature"
	keyStream      = "stream"
	keyFunction    = "function"
	keyArguments   = "arguments"
	streamDataDone = "data: [DONE]"
	keyName        = "name"
)

// OpenAIAdapter implements LLMAdapter for OpenAI.
type OpenAIAdapter struct {
	config       *Config
	client       *http.Client
	streamClient *http.Client
}

// NewOpenAIAdapter creates a new OpenAIAdapter.
func NewOpenAIAdapter(config *Config) *OpenAIAdapter {
	if config == nil {
		config = &Config{}
	}
	if config.BaseURL == "" {
		config.BaseURL = "https://api.openai.com/v1"
	}
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 60
	}

	return &OpenAIAdapter{
		config: config,
		client: &http.Client{
			Timeout: time.Duration(timeout) * time.Second,
		},
		streamClient: &http.Client{
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// Generate generates text from prompt.
func (a *OpenAIAdapter) Generate(ctx context.Context, prompt string) (string, error) {
	if a.config.MaxPromptLength > 0 && len(prompt) > a.config.MaxPromptLength {
		return "", fmt.Errorf("prompt exceeds maximum length of %d characters", a.config.MaxPromptLength)
	}

	messages := []map[string]string{
		{keyRole: "user", keyContent: prompt},
	}

	reqBody := map[string]interface{}{
		keyModel:       a.config.Model,
		keyMessages:    messages,
		keyMaxTokens:   a.config.MaxTokens,
		keyTemperature: a.config.Temperature,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", errors.Wrap(err, "marshal request")
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		a.config.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", errors.Wrap(err, "create request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.config.APIKey)

	resp, err := a.client.Do(req)
	if err != nil {
		return "", errors.Wrap(err, "send request")
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Error("close response body failed", "err", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		if readErr != nil {
			return "", fmt.Errorf("API request failed with status %d: %w", resp.StatusCode, readErr)
		}
		return "", fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var result OpenAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", errors.Wrap(err, "decode response")
	}

	if len(result.Choices) == 0 {
		return "", ErrInvalidResponse
	}

	return result.Choices[0].Message.Content, nil
}

// GenerateWithParams generates text; per-call parameter overrides are
// not supported by this adapter, so it delegates to Generate.
func (a *OpenAIAdapter) GenerateWithParams(ctx context.Context, prompt string, _ map[string]any) (string, error) {
	return a.Generate(ctx, prompt)
}

// GenerateStructured generates structured output.
func (a *OpenAIAdapter) GenerateStructured(ctx context.Context, prompt string, schema string) (*models.RecommendResult, error) {
	messages := []map[string]interface{}{
		{
			keyRole:    "user",
			keyContent: prompt + "\n\nRespond with valid JSON only, matching this schema:\n" + schema,
		},
	}

	reqBody := map[string]interface{}{
		keyModel:       a.config.Model,
		keyMessages:    messages,
		keyMaxTokens:   a.config.MaxTokens,
		keyTemperature: a.config.Temperature,
		"response_format": map[string]string{
			"type": "json_object",
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, errors.Wrap(err, "marshal request")
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		a.config.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, errors.Wrap(err, "create request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.config.APIKey)

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "send request")
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Error("close response body failed", "err", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		respBody, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		if err != nil {
			return nil, errors.Wrap(err, "read response body")
		}
		return nil, fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var result OpenAIChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, errors.Wrap(err, "decode response")
	}

	if len(result.Choices) == 0 {
		return nil, ErrInvalidResponse
	}

	parser := NewParser()
	return parser.ParseRecommendResult(result.Choices[0].Message.Content)
}

// GetModel returns the model name.
func (a *OpenAIAdapter) GetModel() string {
	return a.config.Model
}

// GenerateStream generates text as a stream of chunks using OpenAI-compatible API.
func (a *OpenAIAdapter) GenerateStream(ctx context.Context, prompt string) (<-chan StreamChunk, error) {
	if prompt == "" {
		return nil, stderrors.New("empty prompt")
	}

	reqBody := map[string]interface{}{
		keyModel:       a.config.Model,
		keyMessages:    []map[string]string{{keyRole: "user", keyContent: prompt}},
		keyMaxTokens:   a.config.MaxTokens,
		keyTemperature: a.config.Temperature,
		keyStream:      true,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, errors.Wrap(err, "marshal stream request")
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		a.config.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, errors.Wrap(err, "create stream request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.config.APIKey)

	// Timeout is controlled via the request context, not the client.
	resp, err := a.streamClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "send stream request")
	}

	if resp.StatusCode != http.StatusOK {
		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		if closeErr := resp.Body.Close(); closeErr != nil {
			log.Warn("http: close response body failed", "error", closeErr)
		}
		if readErr != nil {
			return nil, fmt.Errorf("openai stream error (status %d): %w", resp.StatusCode, readErr)
		}
		return nil, fmt.Errorf("openai stream error (status %d): %s", resp.StatusCode, string(respBody))
	}

	ch := make(chan StreamChunk, 64)

	go func() {
		defer close(ch)
		defer func() {
			if err := resp.Body.Close(); err != nil {
				log.Error("Failed to close stream response body", "error", err)
			}
		}()

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 1MB max line for large SSE chunks
		for scanner.Scan() {
			line := scanner.Text()

			// Skip empty lines.
			if line == "" {
				continue
			}

			// Check for stream termination.
			if line == streamDataDone {
				return
			}

			// Strip "data: " prefix.
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")

			var chunk OpenAIChatResponse
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				// Log and skip malformed chunks instead of aborting.
				log.Warn("Failed to unmarshal stream chunk", "error", err)
				continue
			}

			if len(chunk.Choices) == 0 {
				continue
			}

			content := chunk.Choices[0].Delta.Content

			select {
			case ch <- StreamChunk{Content: content}:
			case <-ctx.Done():
				return
			}
		}

		if err := scanner.Err(); err != nil {
			select {
			case ch <- StreamChunk{Done: true, Err: errors.Wrap(err, "read stream")}:
			case <-ctx.Done():
			}
		}
	}()

	return ch, nil
}

// OpenAIChatResponse represents OpenAI chat completion response.
type OpenAIChatResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// Choice represents a chat completion choice.
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	Delta        Message `json:"delta"` // Used in streaming responses.
	FinishReason string  `json:"finish_reason"`
}

// Message represents a chat message.
type Message struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	ToolCalls []openAIToolCall `json:"tool_calls,omitempty"`
}

// Usage represents token usage.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// GenerateWithTools sends a prompt with available tools to the OpenAI API.
func (a *OpenAIAdapter) GenerateWithTools(ctx context.Context, prompt string, tools []ToolDefinition, choice ToolChoice) (*ToolCallResponse, error) {
	if prompt == "" {
		return nil, stderrors.New("empty prompt")
	}
	if len(tools) == 0 {
		return nil, stderrors.New("no tools provided")
	}

	messages := []map[string]interface{}{
		{"role": "user", "content": prompt},
	}

	return a.sendToolRequest(ctx, messages, tools, choice)
}

// SendToolResult sends tool execution results back to continue the conversation.
func (a *OpenAIAdapter) SendToolResult(ctx context.Context, messages []map[string]interface{}, toolResults []ToolResult) (*ToolCallResponse, error) {
	if len(messages) == 0 {
		return nil, stderrors.New("empty conversation messages")
	}
	if len(toolResults) == 0 {
		return nil, stderrors.New("no tool results provided")
	}

	toolMsgs := BuildToolResultMessages(toolResults)
	allMessages := make([]map[string]interface{}, 0, len(messages)+len(toolMsgs))
	allMessages = append(allMessages, messages...)
	allMessages = append(allMessages, toolMsgs...)

	return a.sendToolRequest(ctx, allMessages, nil, ToolChoiceAuto)
}

// sendToolRequest sends a chat completion request with optional tools.
func (a *OpenAIAdapter) sendToolRequest(ctx context.Context, messages []map[string]interface{}, tools []ToolDefinition, choice ToolChoice) (*ToolCallResponse, error) {
	reqBody := map[string]interface{}{
		"model":       a.config.Model,
		"messages":    messages,
		"max_tokens":  a.config.MaxTokens,
		"temperature": a.config.Temperature,
	}

	if len(tools) > 0 {
		reqBody["tools"] = BuildToolAPIDefinitions(tools)
		reqBody["tool_choice"] = string(choice)
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, errors.Wrap(err, "marshal tool request")
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		a.config.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, errors.Wrap(err, "create tool request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.config.APIKey)

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "send tool request")
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			log.Error("close response body failed", "err", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		if readErr != nil {
			return nil, fmt.Errorf("tool API request failed with status %d: %w", resp.StatusCode, readErr)
		}
		return nil, fmt.Errorf("tool API request failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var result OpenAIChatResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10*1024*1024)).Decode(&result); err != nil {
		return nil, errors.Wrap(err, "decode tool response")
	}

	if len(result.Choices) == 0 {
		return nil, ErrInvalidResponse
	}

	return parseToolCallsFromResponse(&result.Choices[0])
}

// parseToolCallsFromResponse converts an OpenAI Choice into a ToolCallResponse.
func parseToolCallsFromResponse(choice *Choice) (*ToolCallResponse, error) {
	msg := choice.Message

	assistantMsg := &AssistantMsg{
		Role:    msg.Role,
		Content: msg.Content,
	}

	resp := &ToolCallResponse{
		Content: msg.Content,
		Done:    true,
		Message: assistantMsg,
	}

	if len(msg.ToolCalls) > 0 {
		resp.Done = false
		assistantCalls := make([]AssistantToolCall, 0, len(msg.ToolCalls))
		respCalls := make([]ToolCall, 0, len(msg.ToolCalls))

		for _, tc := range msg.ToolCalls {
			assistantCalls = append(assistantCalls, AssistantToolCall{
				ID:   tc.ID,
				Type: tc.Type,
				Function: AssistantToolFuncRef{
					Name:      tc.Function.Name,
					Arguments: tc.Function.Arguments,
				},
			})
			respCalls = append(respCalls, ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})
		}

		assistantMsg.ToolCalls = assistantCalls
		resp.ToolCalls = respCalls
	}

	return resp, nil
}

// openAIToolCall is the OpenAI API format for a tool call in the response.
type openAIToolCall struct {
	ID       string                `json:"id"`
	Type     string                `json:"type"`
	Function openAIToolCallFuncRef `json:"function"`
}

// openAIToolCallFuncRef is the function reference in an OpenAI tool call.
type openAIToolCallFuncRef struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
