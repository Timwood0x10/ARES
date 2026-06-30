package sub

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/Timwood0x10/ares/api/core"
	"github.com/Timwood0x10/ares/internal/ares_callbacks"
	"github.com/Timwood0x10/ares/internal/ares_events"
	apperrors "github.com/Timwood0x10/ares/internal/core/errors"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/llm/output"
	resources "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// FallbackHandler produces a recommendation fallback result for a given task type.
// Used when the LLM is unavailable or fails. Returns items, explanation, error.
type FallbackHandler func(ctx context.Context, task *models.Task) ([]*models.RecommendItem, string, error)

// ChatClient sends chat messages with tool support to the LLM.
// When set on the executor, the agent can use native tool calling
// instead of text-only prompt generation.
type ChatClient interface {
	Chat(ctx context.Context, messages []*core.LLMMessage, tools []core.Tool) (*core.GenerateResponse, error)
}

const defaultMaxToolRounds = 5

// taskExecutor executes recommendation tasks.
type taskExecutor struct {
	toolBinder       ToolBinder
	llmAdapter       output.LLMAdapter
	chatClient       ChatClient // Optional: enables native tool calling via Chat API
	maxToolRounds    int        // Max tool-calling iterations (default 5)
	template         *output.TemplateEngine
	promptTpl        string
	validator        *output.Validator
	maxRetries       int
	retryOnFail      bool // Retry LLM call when validation fails
	strictMode       bool // Return error on validation failure
	logger           *slog.Logger
	eventStore       ares_events.EventStore // Optional: emits ares_events for tool/LLM calls
	agentID          string                 // Agent ID for event emission
	ares_callbacks   ares_callbacks.Emitter // Optional: emits lifecycle callback ares_events.
	fallbackHandlers map[models.AgentType]FallbackHandler
}

// TaskExecutorOption configures a taskExecutor instance during construction.
type TaskExecutorOption func(*taskExecutor)

// WithTaskExecutorCallbacks returns a TaskExecutorOption that sets the callback emitter.
// The emitter will receive lifecycle ares_events (tool.start, tool.end, tool.error)
// during task execution.
func WithTaskExecutorCallbacks(emitter ares_callbacks.Emitter) TaskExecutorOption {
	return func(e *taskExecutor) {
		e.ares_callbacks = emitter
	}
}

// WithChatClient returns a TaskExecutorOption that enables native tool calling
// via the Chat API. When set, the executor will pass tool definitions to the LLM
// and handle tool_calls in a loop until the LLM returns a final text response.
func WithChatClient(client ChatClient) TaskExecutorOption {
	return func(e *taskExecutor) {
		e.chatClient = client
	}
}

// WithMaxToolRounds sets the maximum number of tool-calling iterations.
// Defaults to 5 if not set. A value of 0 means no tool calling.
func WithMaxToolRounds(n int) TaskExecutorOption {
	return func(e *taskExecutor) {
		e.maxToolRounds = n
	}
}

// NewTaskExecutor creates a new TaskExecutor with LLM support.
func NewTaskExecutor(
	toolBinder ToolBinder,
	llmAdapter output.LLMAdapter,
	template *output.TemplateEngine,
	promptTpl string,
	validator *output.Validator,
	maxRetries int,
	opts ...TaskExecutorOption,
) TaskExecutor {
	return NewTaskExecutorWithValidation(toolBinder, llmAdapter, template, promptTpl, validator, maxRetries, false, false, opts...)
}

// NewTaskExecutorWithValidation creates a new TaskExecutor with validation config.
func NewTaskExecutorWithValidation(
	toolBinder ToolBinder,
	llmAdapter output.LLMAdapter,
	template *output.TemplateEngine,
	promptTpl string,
	validator *output.Validator,
	maxRetries int,
	retryOnFail bool,
	strictMode bool,
	opts ...TaskExecutorOption,
) TaskExecutor {
	if maxRetries <= 0 {
		maxRetries = 3
	}
	e := &taskExecutor{
		toolBinder:    toolBinder,
		llmAdapter:    llmAdapter,
		template:      template,
		promptTpl:     promptTpl,
		validator:     validator,
		maxRetries:    maxRetries,
		retryOnFail:   retryOnFail,
		strictMode:    strictMode,
		maxToolRounds: defaultMaxToolRounds,
		logger:        slog.Default(),
	}
	e.fallbackHandlers = make(map[models.AgentType]FallbackHandler)
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// RegisterFallback registers a type-specific fallback handler used when
// the LLM is unavailable or execution fails. If no handler is registered
// for an agent type, executeByType returns an empty result with a warning
// instead of erroring out.
func (e *taskExecutor) RegisterFallback(agentType models.AgentType, handler FallbackHandler) {
	if handler == nil {
		return
	}
	e.fallbackHandlers[agentType] = handler
}

// SetEventStore configures the executor to emit ares_events for tool/LLM calls.
func (e *taskExecutor) SetEventStore(store ares_events.EventStore, agentID string) {
	e.eventStore = store
	e.agentID = agentID
}

// SetCallbacks configures the callback emitter for lifecycle event emission.
func (e *taskExecutor) SetCallbacks(emitter ares_callbacks.Emitter) {
	e.ares_callbacks = emitter
}

// emitCallback emits a lifecycle callback event if the emitter is set.
func (e *taskExecutor) emitCallback(ctx *ares_callbacks.Context) {
	if e.ares_callbacks == nil {
		return
	}
	e.ares_callbacks.Emit(ctx)
}

// emitEvent appends a single event using the canonical ares_events.Emit helper.
// No-op if eventStore is nil.
func (e *taskExecutor) emitEvent(ctx context.Context, eventType ares_events.EventType, payload map[string]any) {
	if !ares_events.Emit(ctx, e.eventStore, e.agentID, eventType, "sub", payload) {
		log.Warn("failed to emit event", "event_type", eventType, "stream_id", e.agentID)
	}
}

// Execute executes a task and returns result.
func (e *taskExecutor) Execute(ctx context.Context, task *models.Task) (*models.TaskResult, error) {
	result := models.NewTaskResult("", models.AgentTypeTop)
	if task == nil {
		result.SetError(apperrors.ErrInvalidInput.Error())
		return result, nil
	}

	result = models.NewTaskResult(task.TaskID, task.AgentType)
	startTime := time.Now()

	// Emit tool start event.
	e.emitCallback(&ares_callbacks.Context{
		Event:   ares_callbacks.EventToolStart,
		AgentID: e.agentID,
		Input:   task.TaskID,
	})

	// If no LLM adapter, use fallback execution
	if e.llmAdapter == nil {
		items, reason, err := e.executeByType(ctx, task)
		if err != nil {
			result.SetError(err.Error())
			e.emitCallback(&ares_callbacks.Context{
				Event:    ares_callbacks.EventToolError,
				AgentID:  e.agentID,
				Error:    err,
				Duration: time.Since(startTime),
			})
			return result, nil
		}
		result.SetSuccess(items, reason)
		result.Duration = time.Since(startTime)
		e.emitCallback(&ares_callbacks.Context{
			Event:    ares_callbacks.EventToolEnd,
			AgentID:  e.agentID,
			Duration: time.Since(startTime),
		})
		return result, nil
	}

	// Get profile from task (either from UserProfile field or Payload)
	var profile *models.UserProfile
	if task.UserProfile != nil {
		profile = task.UserProfile
	} else if task.Payload != nil {
		if p, ok := task.Payload["profile"].(*models.UserProfile); ok {
			profile = p
		}
	}

	if profile == nil {
		// Fallback to type-specific execution
		items, reason, err := e.executeByType(ctx, task)
		if err != nil {
			result.SetError(err.Error())
			e.emitCallback(&ares_callbacks.Context{
				Event:    ares_callbacks.EventToolError,
				AgentID:  e.agentID,
				Error:    err,
				Duration: time.Since(startTime),
			})
			return result, nil
		}
		result.SetSuccess(items, reason)
		result.Duration = time.Since(startTime)
		e.emitCallback(&ares_callbacks.Context{
			Event:    ares_callbacks.EventToolEnd,
			AgentID:  e.agentID,
			Duration: time.Since(startTime),
		})
		return result, nil
	}

	// Execute LLM-based recommendation
	items, err := e.executeWithLLM(ctx, task, profile)
	if err != nil {
		log.Debug("LLM execution failed, using fallback", "error", err)
		// Fallback to type-specific execution
		fallbackItems, reason, fallbackErr := e.executeByType(ctx, task)
		if fallbackErr != nil {
			log.Debug("Fallback also failed", "error", fallbackErr)
			result.SetError(err.Error())
			e.emitCallback(&ares_callbacks.Context{
				Event:    ares_callbacks.EventToolError,
				AgentID:  e.agentID,
				Error:    err,
				Duration: time.Since(startTime),
			})
			return result, nil
		}
		log.Debug("Using fallback", "item_count", len(fallbackItems))
		result.SetSuccess(fallbackItems, reason)
		result.Duration = time.Since(startTime)
		e.emitCallback(&ares_callbacks.Context{
			Event:    ares_callbacks.EventToolEnd,
			AgentID:  e.agentID,
			Duration: time.Since(startTime),
		})
		return result, nil
	}

	result.SetSuccess(items, "LLM recommendation completed")
	result.Duration = time.Since(startTime)
	e.emitCallback(&ares_callbacks.Context{
		Event:    ares_callbacks.EventToolEnd,
		AgentID:  e.agentID,
		Duration: time.Since(startTime),
	})
	return result, nil
}

func (e *taskExecutor) executeWithLLM(ctx context.Context, task *models.Task, profile *models.UserProfile) ([]*models.RecommendItem, error) {
	var lastErr error
	for attempt := 0; attempt < e.maxRetries; attempt++ {
		if attempt > 0 {
			if nonIdempotent := e.listNonIdempotentTools(); len(nonIdempotent) > 0 {
				log.Error("LLM retry blocked: non-idempotent tools may have been called",
					"attempt", attempt+1,
					"max_retries", e.maxRetries,
					"tools", nonIdempotent,
				)
				return nil, errors.Wrap(lastErr, "retry aborted: non-idempotent tools may have been called")
			}
		}

		items, err := e.executeWithLLMSingle(ctx, task, profile)
		if err != nil {
			lastErr = err
			log.Error("LLM call failed", "attempt", attempt+1, "error", err)
			continue
		}

		// Validate results using validator
		if e.validator != nil {
			if err := e.validator.ValidateRecommendResult(&models.RecommendResult{Items: items}); err != nil {
				log.Debug("Validation failed", "error", err)
				// Retry if enabled and not already at max retries
				if e.retryOnFail && attempt < e.maxRetries-1 {
					log.Debug("Will retry LLM call", "next_attempt", attempt+2, "max_retries", e.maxRetries)
					continue
				}
				// Strict mode: return error
				if e.strictMode {
					return nil, errors.Wrap(err, "validation failed")
				}
				// Non-strict mode: log and continue with whatever we got
				log.Debug("Continuing with unvalidated result", "strict_mode", false)
			} else {
				log.Debug("Validation passed")
			}
		}

		log.Info("Got items from LLM", "count", len(items))
		return items, nil
	}

	return nil, errors.Wrap(lastErr, "all retries failed")
}

func (e *taskExecutor) executeWithLLMSingle(ctx context.Context, task *models.Task, profile *models.UserProfile) ([]*models.RecommendItem, error) {
	// Render prompt - support generic profile fields.
	// Use lowercase keys to match template's {{index . "key"}} syntax.
	promptData := map[string]any{
		"Category": string(task.AgentType), // Uppercase to match template
	}

	// Check if this is a travel request - use Preferences map
	if len(profile.Preferences) > 0 {
		// Copy all preferences to promptData (lowercase keys)
		for k, v := range profile.Preferences {
			promptData[k] = v
		}
	}

	// Include budget from profile.Budget for backward compatibility.
	promptData["budget"] = formatBudget(profile.Budget)

	// Also set style from profile
	if len(profile.Style) > 0 {
		promptData["style"] = profile.Style
	}

	prompt, err := e.template.Render(e.promptTpl, promptData)
	if err != nil {
		return nil, errors.Wrap(err, "render prompt")
	}
	log.Debug("Generated prompt", "preview", prompt[:min(200, len(prompt))])

	// Try Chat API with tool support when chatClient is available and tools exist.
	if e.chatClient != nil && e.toolBinder != nil {
		schemas := e.toolBinder.GetToolSchemas()
		if len(schemas) > 0 {
			return e.executeWithChatAndTools(ctx, prompt, schemas)
		}
	}

	// Fall back to text-only generation.
	return e.executeWithLLMTextOnly(ctx, prompt)
}

// executeWithChatAndTools uses the Chat API with native tool calling.
// Implements the agentic loop: LLM → tool_calls → execute → result → LLM → final answer.
func (e *taskExecutor) executeWithChatAndTools(ctx context.Context, prompt string, schemas []resources.ToolSchema) ([]*models.RecommendItem, error) {
	// Convert tool schemas to LLM format.
	llmTools := make([]core.Tool, 0, len(schemas))
	for _, s := range schemas {
		llmTools = append(llmTools, resources.ToolSchemaToLLMTool(s))
	}

	// Build initial messages.
	messages := []*core.LLMMessage{
		{Role: "user", Content: prompt},
	}

	maxRounds := e.maxToolRounds
	if maxRounds <= 0 {
		maxRounds = defaultMaxToolRounds
	}

	for round := 0; round < maxRounds; round++ {
		e.emitEvent(ctx, ares_events.EventLLMCall, map[string]any{
			"agent_id":   e.agentID,
			"round":      round + 1,
			"max_rounds": maxRounds,
			"tool_count": len(llmTools),
			"msg_count":  len(messages),
		})

		resp, err := e.chatClient.Chat(ctx, messages, llmTools)
		if err != nil {
			return nil, errors.Wrap(err, "chat API call failed")
		}

		// No tool calls: LLM gave a final text answer.
		if len(resp.ToolCalls) == 0 {
			log.Debug("Chat API returned final text", "round", round+1, "content_len", len(resp.Content))
			return e.parseRecommendResult(resp.Content)
		}

		// Execute each tool call and collect results.
		log.Debug("Chat API returned tool calls", "round", round+1, "count", len(resp.ToolCalls))

		// Append assistant message with tool_calls to conversation.
		messages = append(messages, &core.LLMMessage{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		// Keep tools available so the LLM can make additional calls if needed.
		// The loop naturally terminates when the LLM produces a final text
		// answer (no tool_calls in response).

		// Execute each tool call and append tool result messages.
		for _, tc := range resp.ToolCalls {
			e.emitEvent(ctx, ares_events.EventToolCallStarted, map[string]any{
				"agent_id":     e.agentID,
				"tool_name":    tc.Function.Name,
				"tool_call_id": tc.ID,
			})

			result, err := e.executeToolCall(ctx, tc)
			if err != nil {
				log.Warn("tool execution failed", "tool", tc.Function.Name, "error", err)
				result = fmt.Sprintf("error: %s", err.Error())
			}

			e.emitEvent(ctx, ares_events.EventToolCallCompleted, map[string]any{
				"agent_id":     e.agentID,
				"tool_name":    tc.Function.Name,
				"tool_call_id": tc.ID,
			})

			messages = append(messages, &core.LLMMessage{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
	}

	return nil, fmt.Errorf("exceeded max tool rounds (%d) without final answer", maxRounds)
}

// executeToolCall parses arguments and calls the tool via toolBinder.
func (e *taskExecutor) executeToolCall(ctx context.Context, tc core.ToolCall) (string, error) {
	var args map[string]any
	if tc.Function.Arguments != "" {
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return "", errors.Wrap(err, "parse tool arguments")
		}
	}

	result, err := e.toolBinder.CallTool(ctx, tc.Function.Name, args)
	if err != nil {
		return "", err
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Sprintf("%v", result), nil
	}
	return string(resultJSON), nil
}

// executeWithLLMTextOnly performs a text-only LLM generation (original behavior).
func (e *taskExecutor) executeWithLLMTextOnly(ctx context.Context, prompt string) ([]*models.RecommendItem, error) {
	e.emitEvent(ctx, ares_events.EventLLMCall, map[string]any{
		"agent_id": e.agentID,
		"prompt":   prompt[:min(200, len(prompt))],
	})
	response, err := e.llmAdapter.Generate(ctx, prompt)
	if err != nil {
		e.emitEvent(ctx, ares_events.EventLLMCall, map[string]any{
			"agent_id": e.agentID,
			"error":    err.Error(),
			"status":   "failed",
		})
		return nil, errors.Wrap(err, "LLM call failed")
	}
	log.Debug("LLM response", "preview", response[:min(500, len(response))])

	return e.parseRecommendResult(response)
}

// parseRecommendResult parses the LLM text response into RecommendItems.
func (e *taskExecutor) parseRecommendResult(response string) ([]*models.RecommendItem, error) {
	parser := output.NewParser()
	result, err := parser.ParseRecommendResult(response)
	if err != nil {
		return nil, errors.Wrap(err, "parse result")
	}

	if result == nil || result.Items == nil {
		return nil, errors.New("empty result from LLM")
	}

	log.Info("Parsed result items", "count", len(result.Items))
	return result.Items, nil
}

func formatBudget(budget *models.PriceRange) string {
	if budget == nil {
		return "0 - 10000"
	}
	return fmt.Sprintf("%.0f - %.0f", budget.Min, budget.Max)
}

// listNonIdempotentTools returns names of non-idempotent tools bound to this executor.
func (e *taskExecutor) listNonIdempotentTools() []string {
	var names []string
	if e.toolBinder == nil {
		return nil
	}
	all := e.toolBinder.ListTools()
	for _, n := range all {
		if !e.toolBinder.IsToolIdempotent(n) {
			names = append(names, n)
		}
	}
	return names
}

// executeByType dispatches to type-specific handlers.
// If no handler is registered for the agent type, returns an empty result
// with a warning (graceful degradation instead of hard error).
func (e *taskExecutor) executeByType(ctx context.Context, task *models.Task) ([]*models.RecommendItem, string, error) {
	if handler, ok := e.fallbackHandlers[task.AgentType]; ok {
		log.Debug("executeByType: using registered fallback", "agent_type", task.AgentType)
		return handler(ctx, task)
	}
	log.Warn("executeByType: no fallback handler registered",
		"agent_type", task.AgentType,
		"task_id", task.TaskID,
	)
	return []*models.RecommendItem{}, "fallback: empty result (no handler)", nil
}
