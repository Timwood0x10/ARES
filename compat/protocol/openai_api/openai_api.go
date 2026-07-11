// Package openaiapi is the official OpenAI-API-compatible protocol adapter for ARES.
//
// It exposes ARES agents/tools over the OpenAI Chat Completions wire format
// so any OpenAI-API-compatible client (LangChain, LLM frameworks, custom
// apps) plugs into the ARES runtime. The adapter binds api/core.LLMService
// to the compat/protocol.ProtocolAdapter interface.
//
// Inbound requests follow the OpenAI Chat Completions schema:
//
//	POST /v1/chat/completions
//	{
//	  "model": "gpt-4",
//	  "messages": [{"role": "user", "content": "Hello"}],
//	  "temperature": 0.7,
//	  "max_tokens": 1024
//	}
//
// The adapter maps these to core.GenerateRequest, calls the LLMService,
// and returns the standard OpenAI Chat Completions response shape.
package openaiapi

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Timwood0x10/ares/compat/protocol"

	"github.com/Timwood0x10/ares/api/core"
)

// Adapter satisfies compat/protocol.ProtocolAdapter for the OpenAI API format.
type Adapter struct {
	svc core.LLMService
}

// New constructs an Adapter from a raw config map.
//
// Recognized keys:
//
//	service core.LLMService — REQUIRED. The LLM service to proxy requests to.
func New(config map[string]any) (*Adapter, error) {
	svc, _ := config["service"].(core.LLMService)
	if svc == nil {
		return nil, fmt.Errorf("compat/protocol/openai_api: service is required")
	}
	return &Adapter{svc: svc}, nil
}

// chatRequest represents the OpenAI Chat Completions request body subset
// that the adapter currently supports. Unsupported fields are silently ignored.
type chatRequest struct {
	Model       string            `json:"model"`
	Messages    []chatMessage     `json:"messages"`
	Temperature *float64          `json:"temperature,omitempty"`
	MaxTokens   *int              `json:"max_tokens,omitempty"`
	Stream      bool              `json:"stream,omitempty"`
	Tools       []json.RawMessage `json:"tools,omitempty"`
}

// chatMessage represents a single message in the OpenAI Chat Completions format.
type chatMessage struct {
	Role       string          `json:"role"`
	Content    string          `json:"content"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
}

// chatResponse represents the OpenAI Chat Completions response shape.
type chatResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []choice `json:"choices"`
	Usage   *usage   `json:"usage,omitempty"`
}

type choice struct {
	Index        int         `json:"index"`
	Message      chatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Serve handles a single inbound OpenAI-format request and returns the encoded response.
//
// raw is expected to be a JSON-serialized OpenAI Chat Completions request body
// (not wrapped in an HTTP envelope — the adapter operates on the body only).
// The adapter deserializes it, calls core.LLMService.Generate, and returns
// the standard OpenAI Chat Completions response as JSON bytes.
func (a *Adapter) Serve(ctx context.Context, raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("compat/protocol/openai_api: empty request body")
	}

	var req chatRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("compat/protocol/openai_api: decode request: %w", err)
	}
	if len(req.Messages) == 0 {
		return nil, fmt.Errorf("compat/protocol/openai_api: messages must not be empty")
	}

	// Map OpenAI messages → core.LLMMessage.
	messages := make([]*core.LLMMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		llmMsg := &core.LLMMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
		}
		// Parse tool_calls if present.
		if len(m.ToolCalls) > 0 {
			var toolCalls []core.ToolCall
			if err := json.Unmarshal(m.ToolCalls, &toolCalls); err != nil {
				// Silently drop malformed tool_calls; the LLM service will ignore them.
				llmMsg.ToolCalls = nil
			} else {
				llmMsg.ToolCalls = toolCalls
			}
		}
		messages = append(messages, llmMsg)
	}

	genReq := &core.GenerateRequest{
		Messages:    messages,
		Model:       req.Model,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      req.Stream,
	}

	genResp, err := a.svc.Generate(ctx, genReq)
	if err != nil {
		return nil, fmt.Errorf("compat/protocol/openai_api: generate: %w", err)
	}

	// Build the OpenAI response.
	responseMsg := chatMessage{
		Role:    "assistant",
		Content: genResp.Content,
	}
	if len(genResp.ToolCalls) > 0 {
		tcRaw, err := json.Marshal(genResp.ToolCalls)
		if err == nil {
			responseMsg.ToolCalls = tcRaw
		}
	}

	resp := chatResponse{
		ID:      "chatcmpl-" + genResp.Model,
		Object:  "chat.completion",
		Created: 0, // Caller can set from context if needed.
		Model:   genResp.Model,
		Choices: []choice{
			{
				Index:        0,
				Message:      responseMsg,
				FinishReason: genResp.FinishReason,
			},
		},
	}

	if genResp.Usage.TotalTokens > 0 {
		resp.Usage = &usage{
			PromptTokens:     genResp.Usage.PromptTokens,
			CompletionTokens: genResp.Usage.CompletionTokens,
			TotalTokens:      genResp.Usage.TotalTokens,
		}
	}

	out, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("compat/protocol/openai_api: encode response: %w", err)
	}
	return out, nil
}

// Name returns the canonical protocol name.
func (*Adapter) Name() string { return "openai_api" }

// ContentType returns the MIME type this adapter produces.
func (*Adapter) ContentType() string { return "application/json" }

// Compile-time interface assertion.
var _ protocol.ProtocolAdapter = (*Adapter)(nil)
