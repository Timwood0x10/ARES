package monitoring

// Test constants for repeated strings in tests
const (
	// Field names
	FieldAgentID       = "agent_id"
	FieldStatus        = "status"
	FieldEvents        = "events"
	FieldSpans         = "spans"
	FieldTraceID       = "trace_id"
	FieldLevel         = "level"
	FieldError         = "error"
	FieldName          = "name"
	FieldTaskID        = "task_id"
	FieldEstimatedCost = "estimated_cost"
	FieldInputTokens   = "input_tokens"
	FieldOutputTokens  = "output_tokens"
	FieldPath          = "path"

	// Test values
	TestTool1  = "tool1"
	TestAgent1 = "agent-1"
	TestAgent2 = "agent-2"
	TestTask1  = "task-1"
	TestLLM1   = "llm1"
	TestCost   = "cost"
)

// Additional constants for production code
const (
	StatusCompleted = "completed"
	APIAgentsPath   = "/api/agents/"
)
