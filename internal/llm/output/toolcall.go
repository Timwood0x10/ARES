package output

import "context"

// ToolDefinition describes a tool available to the LLM.
type ToolDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"` // JSON Schema object.
}

// toolDefinitionAPI is the OpenAI API wrapper format for a tool definition.
type toolDefinitionAPI struct {
	Type     string             `json:"type"`
	Function *ToolDefinitionAPI `json:"function"`
}

// ToolDefinitionAPI is the OpenAI API function definition format.
type ToolDefinitionAPI struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

// ToolCall represents an LLM's request to call a tool.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string.
}

// ToolResult represents the result of executing a tool call.
type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
}

// ToolChoice controls how the LLM uses tools.
type ToolChoice string

const (
	ToolChoiceAuto     ToolChoice = "auto"
	ToolChoiceNone     ToolChoice = "none"
	ToolChoiceRequired ToolChoice = "required"
)

// ToolCallResponse is the response from a tool-capable generation.
type ToolCallResponse struct {
	Content   string        `json:"content"`    // LLM text response (may be empty if tool call).
	ToolCalls []ToolCall    `json:"tool_calls"` // Requested tool calls (may be empty if text response).
	Done      bool          `json:"done"`       // True if the LLM is done (no more tool calls).
	Message   *AssistantMsg `json:"message"`    // Raw assistant message for conversation continuation.
}

// buildAPIMessages converts a ToolCallResponse.Message into the OpenAI API format.
func (r *ToolCallResponse) buildAPIMessages() []map[string]interface{} {
	if r.Message == nil {
		return nil
	}
	return []map[string]interface{}{r.Message.toMap()}
}

// ToolCapable is implemented by adapters that support function calling.
type ToolCapable interface {
	// GenerateWithTools sends a prompt with available tools.
	GenerateWithTools(ctx context.Context, prompt string, tools []ToolDefinition, choice ToolChoice) (*ToolCallResponse, error)

	// SendToolResult sends tool execution results back to continue the conversation.
	SendToolResult(ctx context.Context, messages []map[string]interface{}, toolResults []ToolResult) (*ToolCallResponse, error)
}

// AssistantMsg represents an assistant message with optional tool calls.
// This is used to carry the raw message for conversation continuation.
type AssistantMsg struct {
	Role      string              `json:"role"`
	Content   string              `json:"content,omitempty"`
	ToolCalls []AssistantToolCall `json:"tool_calls,omitempty"`
}

// toMap converts AssistantMsg to the OpenAI API message format.
func (m *AssistantMsg) toMap() map[string]interface{} {
	msg := map[string]interface{}{
		"role":    m.Role,
		"content": m.Content,
	}
	if len(m.ToolCalls) > 0 {
		calls := make([]map[string]interface{}, 0, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			call := map[string]interface{}{
				"id":   tc.ID,
				"type": "function",
				"function": map[string]interface{}{
					"name":      tc.Function.Name,
					"arguments": tc.Function.Arguments,
				},
			}
			calls = append(calls, call)
		}
		msg["tool_calls"] = calls
	}
	return msg
}

// AssistantToolCall represents a tool call in the assistant message.
type AssistantToolCall struct {
	ID       string               `json:"id"`
	Type     string               `json:"type"`
	Function AssistantToolFuncRef `json:"function"`
}

// AssistantToolFuncRef represents the function reference in a tool call.
type AssistantToolFuncRef struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// BuildToolResultMessages creates OpenAI API messages for tool results.
func BuildToolResultMessages(results []ToolResult) []map[string]interface{} {
	msgs := make([]map[string]interface{}, 0, len(results))
	for _, r := range results {
		msgs = append(msgs, map[string]interface{}{
			"role":         "tool",
			"tool_call_id": r.ToolCallID,
			"content":      r.Content,
		})
	}
	return msgs
}

// BuildToolAPIDefinitions converts ToolDefinition slice to OpenAI API format.
func BuildToolAPIDefinitions(tools []ToolDefinition) []toolDefinitionAPI {
	apiTools := make([]toolDefinitionAPI, 0, len(tools))
	for _, t := range tools {
		apiTools = append(apiTools, toolDefinitionAPI{
			Type: "function",
			Function: &ToolDefinitionAPI{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}
	return apiTools
}
