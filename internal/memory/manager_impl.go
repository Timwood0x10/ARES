// Package memory provides unified memory management for the StyleAgent framework.
package memory

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"sync"
	"time"

	"goagentx/internal/core/models"
	"goagentx/internal/errors"
	"goagentx/internal/events"
	memctx "goagentx/internal/memory/context"
	"goagentx/internal/memory/distillation"
	"goagentx/internal/storage/postgres/embedding"
)

// memoryManager implements MemoryManager interface.
// It coordinates session memory, task memory, and distilled task storage.
type memoryManager struct {
	sessionMemory *memctx.SessionMemory
	taskMemory    *memctx.TaskMemory
	mu            sync.RWMutex
	config        *MemoryConfig
	started       bool
	stopped       bool

	// Distillation components (nil when using NewMemoryManager without distiller).
	distiller *distillation.Distiller
	embedder  embedding.EmbeddingService
	expRepo   distillation.ExperienceRepository

	// Event sourcing: optional EventStore for emitting lifecycle events.
	eventStore events.EventStore
	streamID   string // Stream ID used when appending events.
}

// NewMemoryManager creates a new MemoryManager with the given configuration.
// For distillation support, use NewMemoryManagerWithDistiller.
func NewMemoryManager(config *MemoryConfig) (MemoryManager, error) {
	if config == nil {
		config = DefaultMemoryConfig()
	}

	sessionMemory := memctx.NewSessionMemory(
		config.MaxSessions,
		config.SessionTTL,
	)

	taskMemory := memctx.NewTaskMemory(
		config.MaxTasks,
		config.TaskTTL,
	)

	return &memoryManager{
		sessionMemory: sessionMemory,
		taskMemory:    taskMemory,
		config:        config,
	}, nil
}

// NewMemoryManagerWithDistiller creates a new MemoryManager with the new distillation engine.
// This is the recommended method for production use.
//
// Args:
//
//	config - memory configuration.
//	embedder - embedding service for generating vectors.
//	expRepo - experience repository for storage and retrieval.
//
// Returns:
//
//	MemoryManager - configured memory manager instance.
//	error - any error encountered.
func NewMemoryManagerWithDistiller(config *MemoryConfig, embedder embedding.EmbeddingService, expRepo distillation.ExperienceRepository) (MemoryManager, error) {
	if config == nil {
		config = DefaultMemoryConfig()
	}

	sessionMemory := memctx.NewSessionMemory(
		config.MaxSessions,
		config.SessionTTL,
	)

	taskMemory := memctx.NewTaskMemory(
		config.MaxTasks,
		config.TaskTTL,
	)

	// Create new distillation engine
	distillConfig := distillation.DefaultDistillationConfig()
	distiller := distillation.NewDistiller(distillConfig, embedder, expRepo)

	return &memoryManager{
		sessionMemory: sessionMemory,
		taskMemory:    taskMemory,
		config:        config,
		distiller:     distiller,
		embedder:      embedder,
		expRepo:       expRepo,
	}, nil
}

// Start starts the memory manager and background workers.
func (m *memoryManager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.started {
		return nil
	}

	m.sessionMemory.StartCleanup()
	m.taskMemory.Start(ctx)
	m.started = true

	slog.Info("Memory manager started")
	return nil
}

// Stop stops the memory manager and cleans up resources.
func (m *memoryManager) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.stopped {
		return nil
	}

	m.taskMemory.Stop()

	if err := m.sessionMemory.Close(ctx); err != nil {
		slog.Warn("Failed to close session memory", "error", err)
	}

	m.stopped = true
	slog.Info("Memory manager stopped")
	return nil
}

// SetEventStore configures an optional EventStore for emitting lifecycle events.
// If store is nil, event emission is a no-op.
func (m *memoryManager) SetEventStore(store events.EventStore, streamID string) {
	m.eventStore = store
	m.streamID = streamID
}

// emitEvent appends a single event to the event store. No-op if eventStore is nil.
func (m *memoryManager) emitEvent(ctx context.Context, eventType events.EventType, payload map[string]any) {
	if m.eventStore == nil {
		return
	}
	event := &events.Event{
		StreamID: m.streamID,
		Type:     eventType,
		Payload:  payload,
	}
	if err := m.eventStore.Append(ctx, m.streamID, []*events.Event{event}, 0); err != nil {
		slog.Warn("Failed to emit event", "type", eventType, "error", err)
	}
}

// CreateSession creates a new session and returns the session ID.
func (m *memoryManager) CreateSession(ctx context.Context, userID string) (string, error) {
	// Use both time and userID to ensure uniqueness
	sessionID := fmt.Sprintf("session_%s_%d", userID, time.Now().UnixNano())

	messages := []memctx.Message{
		{
			Role:    "system",
			Content: "New session started",
			Time:    time.Now(),
		},
	}

	if err := m.sessionMemory.Set(ctx, sessionID, userID, messages); err != nil {
		return "", errors.Wrap(err, "create session")
	}

	// Emit session created event.
	m.emitEvent(ctx, events.EventSessionCreated, map[string]any{
		"session_id": sessionID,
		"user_id":    userID,
	})

	slog.Debug("Session created", "session_id", sessionID, "user_id", userID)
	return sessionID, nil
}

// AddMessage adds a message to the session.
func (m *memoryManager) AddMessage(ctx context.Context, sessionID, role, content string) error {
	msg := memctx.Message{
		Role:    role,
		Content: content,
		Time:    time.Now(),
	}

	if err := m.sessionMemory.AddMessage(ctx, sessionID, msg); err != nil {
		return errors.Wrap(err, "add message")
	}

	// Emit message added event.
	m.emitEvent(ctx, events.EventMessageAdded, map[string]any{
		"session_id": sessionID,
		"role":       role,
	})

	slog.Debug("Message added", "session_id", sessionID, "role", role)
	return nil
}

// GetMessages retrieves all messages from the session.
func (m *memoryManager) GetMessages(ctx context.Context, sessionID string) ([]Message, error) {
	sessionMemMessages, err := m.sessionMemory.GetMessages(ctx, sessionID)
	if err != nil {
		return nil, errors.Wrap(err, "get messages")
	}

	messages := make([]Message, len(sessionMemMessages))
	for i, msg := range sessionMemMessages {
		messages[i] = Message{
			Role:    msg.Role,
			Content: msg.Content,
			Time:    msg.Time,
		}
	}

	return messages, nil
}

// DeleteSession deletes a session and all its messages immediately.
func (m *memoryManager) DeleteSession(ctx context.Context, sessionID string) error {
	if err := m.sessionMemory.Delete(ctx, sessionID); err != nil {
		return errors.Wrap(err, "delete session")
	}

	slog.Debug("Session deleted", "session_id", sessionID)
	return nil
}

// BuildContext builds input with conversation history context.
func (m *memoryManager) BuildContext(ctx context.Context, input string, sessionID string) (string, error) {
	messages, err := m.GetMessages(ctx, sessionID)
	if err != nil {
		slog.Warn("Failed to get messages, using raw input", "error", err)
		return input, nil
	}

	// Keep only last N messages to avoid long context.
	maxHistory := m.config.MaxHistory
	if len(messages) > maxHistory {
		messages = messages[len(messages)-maxHistory:]
	}

	// Build context string.
	var contextBuilder string
	if len(messages) > 0 {
		contextBuilder = "Previous conversation history:\n\n"
		for _, msg := range messages {
			switch msg.Role {
			case "user":
				contextBuilder += fmt.Sprintf("User: %s\n", truncate(msg.Content, 100))
			case "assistant":
				contextBuilder += fmt.Sprintf("Assistant: %s\n", truncate(msg.Content, 100))
			}
		}
		contextBuilder += "\nCurrent request:\n"
	}
	contextBuilder += input

	slog.Debug("Context built", "session_id", sessionID, "history_length", len(messages))
	return contextBuilder, nil
}

// CreateTask creates a new task and returns the task ID.
func (m *memoryManager) CreateTask(ctx context.Context, sessionID, userID, input string) (string, error) {
	taskID := "task_" + strconv.FormatInt(time.Now().UnixNano(), 10)

	if err := m.taskMemory.Set(ctx, taskID, sessionID, userID, input); err != nil {
		return "", errors.Wrap(err, "create task")
	}

	slog.Debug("Task created", "task_id", taskID, "session_id", sessionID)
	return taskID, nil
}

// UpdateTaskOutput updates the task output.
func (m *memoryManager) UpdateTaskOutput(ctx context.Context, taskID, output string) error {
	if err := m.taskMemory.UpdateOutput(ctx, taskID, output); err != nil {
		return errors.Wrap(err, "update task output")
	}

	slog.Debug("Task output updated", "task_id", taskID)
	return nil
}

// DistillTask extracts key information from task for future reference.
func (m *memoryManager) DistillTask(ctx context.Context, taskID string) (*models.Task, error) {
	slog.Info("[Memory Distillation] Starting task distillation", "task_id", taskID)

	task, err := m.taskMemory.Distill(ctx, taskID)
	if err != nil {
		slog.Error("[Memory Distillation] Failed to distill task",
			"task_id", taskID, "error", err)
		return nil, errors.Wrap(err, "distill task")
	}

	inputStr, _ := task.Payload["input"].(string)

	m.emitEvent(ctx, events.EventMemoryDistilled, map[string]any{
		"task_id":     taskID,
		"input_count": len(inputStr),
	})

	slog.Info("[Memory Distillation] Task distilled successfully",
		"task_id", taskID,
		"input_length", len(inputStr))

	return task, nil
}

// StoreDistilledTask stores a distilled task using the distillation engine.
func (m *memoryManager) StoreDistilledTask(ctx context.Context, taskID string, distilled *models.Task) error {
	if distilled == nil {
		return errors.New("distilled task cannot be nil")
	}
	if m.distiller == nil || m.expRepo == nil {
		return errors.New("distillation engine not initialized, use NewMemoryManagerWithDistiller")
	}

	slog.Info("[Memory Distillation] Storing distilled task", "task_id", taskID)

	inputStr, _ := distilled.Payload["input"].(string)
	outputStr := fmt.Sprintf("%v", distilled.Payload["output"])

	messages := []distillation.Message{
		{Role: "user", Content: inputStr},
		{Role: "assistant", Content: outputStr},
	}

	userID, _ := distilled.Payload["user_id"].(string)
	tenantID, _ := distilled.Payload["tenant_id"].(string)
	if tenantID == "" {
		tenantID = "default"
	}

	memories, err := m.distiller.DistillConversation(ctx, taskID, messages, tenantID, userID)
	if err != nil {
		slog.Error("[Memory Distillation] Failed to distill conversation",
			"task_id", taskID, "error", err)
		return errors.Wrap(err, "distill conversation")
	}

	for _, mem := range memories {
		problem, _ := mem.Metadata["problem"].(string)
		solution, _ := mem.Metadata["solution"].(string)
		confidence, _ := mem.Metadata["confidence"].(float64)
		extractionMethodStr, _ := mem.Metadata["extraction_method"].(string)
		if extractionMethodStr == "" {
			extractionMethodStr = string(distillation.ExtractionDirect)
		}

		exp := &distillation.Experience{
			Problem:          problem,
			Solution:         solution,
			Confidence:       confidence,
			ExtractionMethod: distillation.ExtractionMethod(extractionMethodStr),
		}

		if err := m.expRepo.Create(ctx, exp); err != nil {
			slog.Error("[Memory Distillation] Failed to store experience",
				"task_id", taskID, "error", err)
			continue
		}
	}

	m.emitEvent(ctx, events.EventMemoryDistilled, map[string]any{
		"task_id":      taskID,
		"output_count": len(memories),
	})

	slog.Info("[Memory Distillation] Distillation completed",
		"task_id", taskID,
		"memories_created", len(memories))

	return nil
}

// SearchSimilarTasks searches for similar tasks using vector-based search.
func (m *memoryManager) SearchSimilarTasks(ctx context.Context, query string, limit int) ([]*models.Task, error) {
	if m.embedder == nil || m.expRepo == nil {
		return nil, errors.New("distillation engine not initialized, use NewMemoryManagerWithDistiller")
	}

	slog.Info("[Memory Search] Searching for similar tasks",
		"query", truncate(query, 50),
		"limit", limit)

	queryVector, err := m.embedder.EmbedWithPrefix(ctx, query, "query:")
	if err != nil {
		slog.Error("[Memory Search] Failed to generate query embedding", "error", err)
		return nil, errors.Wrap(err, "generate query embedding")
	}

	experiences, err := m.expRepo.SearchByVector(ctx, queryVector, "default", limit)
	if err != nil {
		slog.Error("[Memory Search] Failed to search experiences", "error", err)
		return nil, errors.Wrap(err, "search experiences")
	}

	tasks := make([]*models.Task, 0, limit)
	for i, exp := range experiences {
		task := &models.Task{
			TaskID: fmt.Sprintf("exp_%d_search", i),
			Payload: map[string]any{
				"input":  exp.Problem,
				"output": exp.Solution,
				"context": map[string]interface{}{
					"confidence":        exp.Confidence,
					"extraction_method": string(exp.ExtractionMethod),
					"source":            "experience_repository",
					"similarity_rank":   i + 1,
				},
			},
		}
		tasks = append(tasks, task)
	}

	slog.Info("[Memory Search] Search completed",
		"results_count", len(tasks),
		"limit", limit)

	return tasks, nil
}

// GetLatestSessionForLeader returns an empty session ID for in-memory implementation.
// Session recovery requires persistent storage; use ProductionMemoryManager for that.
func (m *memoryManager) GetLatestSessionForLeader(_ context.Context, _ string) (string, error) {
	return "", nil
}

// truncate truncates a string to the maximum length (in runes) and adds "..." if truncated.
// This correctly handles multi-byte UTF-8 characters by using runes instead of bytes.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}

	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}

	return string(runes[:maxLen]) + "..."
}

// cosineSimilarity calculates cosine similarity between two vectors.
func cosineSimilarity(v1, v2 []float64) float64 {
	if len(v1) != len(v2) {
		return 0.0
	}

	dotProduct := 0.0
	norm1 := 0.0
	norm2 := 0.0

	for i := range v1 {
		dotProduct += v1[i] * v2[i]
		norm1 += v1[i] * v1[i]
		norm2 += v2[i] * v2[i]
	}

	if norm1 == 0 || norm2 == 0 {
		return 0.0
	}

	result := dotProduct / math.Sqrt(norm1*norm2)

	if math.IsNaN(result) || math.IsInf(result, 0) {
		return 0.0
	}

	return result
}
