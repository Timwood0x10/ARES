package agents

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// MockLLMExecutor provides a mock implementation of LLMExecutor for unit testing.
// It allows setting predefined responses for specific prompt prefixes and injecting
// errors for testing error handling paths.
type MockLLMExecutor struct {
	mu         sync.Mutex
	responses  map[string]string // prompt prefix -> mock response
	errors     map[string]error  // prompt prefix -> error to inject
	callCount  int
	lastPrompt string
}

// NewMockLLMExecutor creates a new MockLLMExecutor with empty response map.
//
// Returns:
//   - pointer to the initialized mock executor.
func NewMockLLMExecutor() *MockLLMExecutor {
	return &MockLLMExecutor{
		responses: make(map[string]string),
		errors:    make(map[string]error),
	}
}

// SetResponse registers a mock response for prompts starting with the given prefix.
// Longer prefixes take precedence over shorter ones during matching.
//
// Args:
//   - promptPrefix: the prefix to match against incoming prompts.
//   - response: the mock LLM response to return when matched.
func (m *MockLLMExecutor) SetResponse(promptPrefix string, response string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses[promptPrefix] = response
}

// SetError registers an error to be returned for prompts matching the given prefix.
// Error responses take precedence over normal responses.
//
// Args:
//   - promptPrefix: the prefix to match against incoming prompts.
//   - err: the error to return when matched.
func (m *MockLLMExecutor) SetError(promptPrefix string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errors[promptPrefix] = err
}

// SetDefaultResponse sets a fallback response when no specific prefix matches.
//
// Args:
//   - response: the default mock response.
func (m *MockLLMExecutor) SetDefaultResponse(response string) {
	m.SetResponse("", response)
}

// Complete implements LLMExecutor interface.
// It matches the last message content against registered prefixes and returns
// the corresponding mock response or error.
//
// Args:
//   - ctx: context (unused in mock, but required by interface).
//   - messages: the conversation messages sent to the LLM.
//
// Returns:
//   - the mock response string.
//   - error if a matching error is registered or no response found.
func (m *MockLLMExecutor) Complete(ctx context.Context, messages []Message) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.callCount++
	if len(messages) > 0 {
		m.lastPrompt = messages[len(messages)-1].Content
	}

	prompt := m.lastPrompt

	// Check for injected errors first (longest prefix first).
	bestErr := findBestErrorMatch(prompt, m.errors)
	if bestErr != "" {
		return "", m.errors[bestErr]
	}

	// Find best matching response (longest prefix first).
	bestResp := findBestStringMatch(prompt, m.responses)
	if bestResp != "" {
		return m.responses[bestResp], nil
	}

	return "", fmt.Errorf("mock: no response configured for prompt (calls: %d)", m.callCount)
}

// CompleteStructured implements LLMExecutor interface for structured output.
// The mock executor does not support native structured output; it always returns
// an error so callers fall back to the text-based Complete path.
//
// Args:
//   - ctx: context (unused in mock).
//   - messages: the conversation messages.
//   - schema: the target schema (unused in mock).
//
// Returns:
//   - nil result (structured output not supported).
//   - error indicating fallback to text mode is needed.
func (m *MockLLMExecutor) CompleteStructured(ctx context.Context, messages []Message, schema any) (any, error) {
	m.mu.Lock()
	m.callCount++
	m.mu.Unlock()
	return nil, fmt.Errorf("mock: structured output not supported, use Complete instead")
}

// CallCount returns the total number of LLM calls made to this executor.
// Useful for assertions in test cases.
//
// Returns:
//   - the number of Complete/CompleteStructured calls received.
func (m *MockLLMExecutor) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount
}

// LastPrompt returns the content of the most recent prompt sent to the executor.
// Useful for asserting prompt content in tests.
//
// Returns:
//   - the last prompt string, or empty if no calls were made.
func (m *MockLLMExecutor) LastPrompt() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastPrompt
}

// Reset clears all registered responses, errors, and call count.
func (m *MockLLMExecutor) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses = make(map[string]string)
	m.errors = make(map[string]error)
	m.callCount = 0
	m.lastPrompt = ""
}

// findBestStringMatch finds the longest registered prefix that matches the prompt.
func findBestStringMatch(prompt string, candidates map[string]string) string {
	best := ""
	for prefix := range candidates {
		if len(prefix) > len(best) && strings.HasPrefix(prompt, prefix) {
			best = prefix
		}
	}
	return best
}

// findBestErrorMatch finds the longest registered prefix that matches the prompt in an error map.
func findBestErrorMatch(prompt string, candidates map[string]error) string {
	best := ""
	for prefix := range candidates {
		if len(prefix) > len(best) && strings.HasPrefix(prompt, prefix) {
			best = prefix
		}
	}
	return best
}
