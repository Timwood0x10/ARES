// Package memory provides unified memory management for the StyleAgent framework.
package memory

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"sync"
	"time"

	"goagent/internal/core/models"
	"goagent/internal/errors"
	memctx "goagent/internal/memory/context"
	"goagent/internal/memory/distillation"
	"goagent/internal/storage/postgres/embedding"
)

// memoryManager implements MemoryManager interface.
// It coordinates session memory, task memory, and distilled task storage.
type memoryManager struct {
	sessionMemory  *memctx.SessionMemory
	taskMemory     *memctx.TaskMemory
	distilledTasks map[string]*DistilledTaskData
	mu             sync.RWMutex
	config         *MemoryConfig
	started        bool
	stopped        bool
	vectorDim      int

	// New distillation components
	distiller     *distillation.Distiller
	embedder      embedding.EmbeddingService
	expRepo       distillation.ExperienceRepository
	useNewDistill bool // Flag to use new distillation engine
	cleanupCancel context.CancelFunc
}

// DistilledTaskData holds distilled task information with local vector.
type DistilledTaskData struct {
	TaskID    string
	Input     string
	Output    string
	Context   map[string]interface{}
	Vector    []float64
	CreatedAt time.Time
}

// NewMemoryManager creates a new MemoryManager with the given configuration.
// For production use with new distillation engine, use NewMemoryManagerWithDistiller.
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
		sessionMemory:  sessionMemory,
		taskMemory:     taskMemory,
		distilledTasks: make(map[string]*DistilledTaskData),
		config:         config,
		vectorDim:      config.VectorDim,
		useNewDistill:  false, // Use old hash-based distillation by default
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
		sessionMemory:  sessionMemory,
		taskMemory:     taskMemory,
		distilledTasks: make(map[string]*DistilledTaskData),
		config:         config,
		vectorDim:      config.VectorDim,
		distiller:      distiller,
		embedder:       embedder,
		expRepo:        expRepo,
		useNewDistill:  true, // Use new distillation engine
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
	m.taskMemory.Start(ctx) // Start task memory cleanup
	m.started = true

	// Start distilled tasks cleanup goroutine with independent cancel.
	cleanupCtx, cleanupCancel := context.WithCancel(ctx)
	m.cleanupCancel = cleanupCancel
	go m.cleanupDistilledTasks(cleanupCtx)

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

	// Stop background cleanup goroutine.
	if m.cleanupCancel != nil {
		m.cleanupCancel()
		m.cleanupCancel = nil
	}

	m.taskMemory.Stop()

	if err := m.sessionMemory.Close(ctx); err != nil {
		slog.Warn("Failed to close session memory", "error", err)
	}

	m.stopped = true
	slog.Info("Memory manager stopped")
	return nil
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

	// Use new distillation engine if enabled
	if m.useNewDistill {
		return m.distillTaskNew(ctx, taskID)
	}

	// Use old hash-based distillation for backward compatibility
	return m.distillTaskOld(ctx, taskID)
}

// distillTaskOld uses the old hash-based distillation method (backward compatibility).
func (m *memoryManager) distillTaskOld(ctx context.Context, taskID string) (*models.Task, error) {
	return m.distillTaskCommon(ctx, taskID, "old method")
}

// distillTaskNew uses the new distillation engine with experience extraction.
func (m *memoryManager) distillTaskNew(ctx context.Context, taskID string) (*models.Task, error) {
	if m.distiller == nil {
		slog.Warn("[Memory Distillation] Distiller not initialized, falling back to old method", "task_id", taskID)
		return m.distillTaskOld(ctx, taskID)
	}
	return m.distillTaskCommon(ctx, taskID, "new method")
}

// distillTaskCommon is the common implementation for task distillation.
func (m *memoryManager) distillTaskCommon(ctx context.Context, taskID, method string) (*models.Task, error) {
	task, err := m.taskMemory.Distill(ctx, taskID)
	if err != nil {
		slog.Error("[Memory Distillation] Failed to distill task",
			"task_id", taskID, "error", err)
		return nil, errors.Wrap(err, "distill task")
	}

	inputStr, ok := task.Payload["input"].(string)
	if !ok {
		slog.Warn("[Memory Distillation] Task input is not a string", "task_id", taskID)
		inputStr = ""
	}
	slog.Info("[Memory Distillation] Task distilled successfully",
		"task_id", taskID,
		"method", method,
		"input_length", len(inputStr))
	return task, nil
}

// StoreDistilledTask stores a distilled task with local vector embedding.
func (m *memoryManager) StoreDistilledTask(ctx context.Context, taskID string, distilled *models.Task) error {
	if distilled == nil {
		return errors.New("distilled task cannot be nil")
	}

	if m.useNewDistill {
		return m.storeDistilledTaskNew(ctx, taskID, distilled)
	}

	return m.storeDistilledTaskOld(ctx, taskID, distilled)
}

// storeDistilledTaskOld uses the old hash-based storage method (backward compatibility).
func (m *memoryManager) storeDistilledTaskOld(ctx context.Context, taskID string, distilled *models.Task) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	slog.Info("[Memory Distillation] Storing distilled task (old method)", "task_id", taskID)

	// Extract input string from payload
	inputStr, ok := distilled.Payload["input"].(string)
	if !ok {
		inputStr = ""
		slog.Warn("[Memory Distillation] No input found in payload", "task_id", taskID)
	}

	slog.Info("[Memory Distillation] Task details",
		"task_id", taskID,
		"input_length", len(inputStr),
		"vector_dimension", m.vectorDim)

	// Generate local vector using simple hash-based approach
	vector := m.generateHashVector(inputStr)
	slog.Info("[Memory Distillation] Vector generated (hash-based)",
		"task_id", taskID,
		"vector_dimension", len(vector))

	outputStr := fmt.Sprintf("%v", distilled.Payload["output"])
	contextMap, ok := distilled.Payload["context"].(map[string]interface{})
	if !ok {
		slog.Warn("[Memory Distillation] Task context is not a map", "task_id", taskID)
		contextMap = nil
	}
	data := &DistilledTaskData{
		TaskID:    taskID,
		Input:     inputStr,
		Output:    outputStr,
		Context:   contextMap,
		Vector:    vector,
		CreatedAt: time.Now(),
	}

	// Evict oldest tasks if limit exceeded (LRU eviction)
	m.evictOldestDistilledTask()

	m.distilledTasks[taskID] = data

	slog.Info("[Memory Distillation] Distilled task stored successfully (old method)",
		"task_id", taskID,
		"total_distilled_tasks", len(m.distilledTasks))

	return nil
}

// storeDistilledTaskNew uses the new distillation engine with experience storage.
func (m *memoryManager) storeDistilledTaskNew(ctx context.Context, taskID string, distilled *models.Task) error {
	if m.distiller == nil || m.expRepo == nil {
		slog.Warn("[Memory Distillation] Distiller or repo not initialized, falling back to old method", "task_id", taskID)
		return m.storeDistilledTaskOld(ctx, taskID, distilled)
	}

	slog.Info("[Memory Distillation] Storing distilled task (new method)", "task_id", taskID)

	// Extract input and output from payload
	inputStr, ok := distilled.Payload["input"].(string)
	if !ok {
		inputStr = ""
		slog.Warn("[Memory Distillation] No input found in payload", "task_id", taskID)
	}

	outputStr := fmt.Sprintf("%v", distilled.Payload["output"])

	// Convert task to messages for distillation
	messages := []distillation.Message{
		{Role: "user", Content: inputStr},
		{Role: "assistant", Content: outputStr},
	}

	// Extract metadata
	userID, ok := distilled.Payload["user_id"].(string)
	if !ok {
		slog.Warn("[Memory Distillation] Task user_id is not a string", "task_id", taskID)
		userID = ""
	}
	tenantID, ok := distilled.Payload["tenant_id"].(string)
	if !ok {
		slog.Warn("[Memory Distillation] Task tenant_id is not a string", "task_id", taskID)
		tenantID = "default"
	}

	// Distill conversation
	memories, err := m.distiller.DistillConversation(ctx, taskID, messages, tenantID, userID)
	if err != nil {
		slog.Error("[Memory Distillation] Failed to distill conversation",
			"task_id", taskID, "error", err)
		return errors.Wrap(err, "distill conversation")
	}

	// Store memories in experience repository
	for _, mem := range memories {
		// Convert distillation.Memory to distillation.Experience
		problem, ok := mem.Metadata["problem"].(string)
		if !ok {
			slog.Warn("[Memory Distillation] Memory problem is not a string", "task_id", taskID)
			problem = ""
		}
		solution, ok := mem.Metadata["solution"].(string)
		if !ok {
			slog.Warn("[Memory Distillation] Memory solution is not a string", "task_id", taskID)
			solution = ""
		}
		confidence, ok := mem.Metadata["confidence"].(float64)
		if !ok {
			slog.Warn("[Memory Distillation] Memory confidence is not a float64", "task_id", taskID)
			confidence = 0.0
		}
		extractionMethodStr, ok := mem.Metadata["extraction_method"].(string)
		if !ok {
			slog.Warn("[Memory Distillation] Memory extraction_method is not a string", "task_id", taskID)
			extractionMethodStr = "unknown"
		}

		// Default extraction method if not set
		if extractionMethodStr == "" {
			extractionMethodStr = string(distillation.ExtractionDirect)
		}

		exp := &distillation.Experience{
			Problem:          problem,
			Solution:         solution,
			Confidence:       confidence,
			ExtractionMethod: distillation.ExtractionMethod(extractionMethodStr),
		}

		// Store experience in repository
		err := m.expRepo.Create(ctx, exp)
		if err != nil {
			slog.Error("[Memory Distillation] Failed to store experience",
				"task_id", taskID, "error", err)
			continue
		}

		slog.Debug("[Memory Distillation] Memory stored successfully",
			"task_id", taskID,
			"memory_type", mem.Metadata["memory_type"],
			"importance", mem.Importance)
	}

	metrics := m.distiller.GetMetrics()
	slog.Info("[Memory Distillation] Distillation completed (new method)",
		"task_id", taskID,
		"memories_created", len(memories),
		"metrics_total", metrics.SuccessTotal)

	return nil
}

// evictOldestDistilledTask removes the oldest distilled task when limit is reached.
// This implements LRU (Least Recently Used) eviction policy for distilled tasks.
// MUST be called while holding m.mu lock.
func (m *memoryManager) evictOldestDistilledTask() {
	if m.config.MaxDistilledTasks <= 0 {
		return
	}

	if len(m.distilledTasks) <= m.config.MaxDistilledTasks {
		return
	}

	// Find the oldest task
	var oldestID string
	var oldestTime time.Time
	for id, data := range m.distilledTasks {
		if oldestID == "" || data.CreatedAt.Before(oldestTime) {
			oldestID = id
			oldestTime = data.CreatedAt
		}
	}

	// Delete the oldest task
	if oldestID != "" {
		delete(m.distilledTasks, oldestID)
		slog.Debug("Evicted oldest distilled task", "task_id", oldestID, "total_tasks", len(m.distilledTasks))
	}
}

// cleanupDistilledTasks removes expired distilled tasks based on TTL.
func (m *memoryManager) cleanupDistilledTasks(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute) // Check every 10 minutes
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.cleanupExpiredDistilledTasks()
		}
	}
}

// cleanupExpiredDistilledTasks removes distilled tasks that are older than TTL.
func (m *memoryManager) cleanupExpiredDistilledTasks() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.config.DistilledTaskTTL <= 0 {
		return // No TTL cleanup configured
	}

	now := time.Now()
	expiredIDs := make([]string, 0)

	for id, data := range m.distilledTasks {
		if now.Sub(data.CreatedAt) > m.config.DistilledTaskTTL {
			expiredIDs = append(expiredIDs, id)
		}
	}

	for _, id := range expiredIDs {
		delete(m.distilledTasks, id)
		slog.Debug("Cleaned up expired distilled task", "task_id", id, "total_tasks", len(m.distilledTasks))
	}
}

// generateHashVector generates a simple hash-based vector from text.
func (m *memoryManager) generateHashVector(text string) []float64 {
	vector := make([]float64, m.vectorDim)

	if len(text) == 0 {
		return vector
	}

	// Generate multiple hash values using different seeds for better distribution
	hashes := make([]uint64, (m.vectorDim+12)/13)
	if len(hashes) == 0 {
		return vector
	}
	for i := range hashes {
		hashes[i] = m.hashWithSeed(text, uint64(i)*0x9e3779b97f4a7c15)
	}

	// Spread hash values across vector dimensions
	for i := range vector {
		hashIdx := i / 13
		hashIdx = hashIdx % len(hashes)
		shift := (i % 13) * 5
		// shift is always in range [0, 60], no need for >= 64 check
		vector[i] = float64((hashes[hashIdx]>>shift)%1000) / 1000.0
	}

	// Normalize vector
	norm := 0.0
	for _, v := range vector {
		norm += v * v
	}
	norm = math.Sqrt(norm)

	if norm > 0 {
		for i := range vector {
			vector[i] /= norm
		}
	}

	return vector
}

// hashWithSeed generates a hash with a specific seed for better distribution.
func (m *memoryManager) hashWithSeed(text string, seed uint64) uint64 {
	hash := seed
	for _, c := range text {
		hash ^= uint64(c) // #nosec G115
		hash *= 0x100000001b3
		hash ^= hash >> 33
		hash *= 0xff51afd7ed558ccd
		hash ^= hash >> 33
	}
	return hash
}

// SearchSimilarTasks searches for similar tasks using local cosine similarity.
func (m *memoryManager) SearchSimilarTasks(ctx context.Context, query string, limit int) ([]*models.Task, error) {
	// Use new distillation engine if enabled
	if m.useNewDistill {
		return m.searchSimilarTasksNew(ctx, query, limit)
	}

	// Use old hash-based search for backward compatibility
	return m.searchSimilarTasksOld(ctx, query, limit)
}

// searchSimilarTasksOld uses the old hash-based search method (backward compatibility).
func (m *memoryManager) searchSimilarTasksOld(ctx context.Context, query string, limit int) ([]*models.Task, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	slog.Info("[Memory Search] Searching for similar tasks (old method)",
		"query", truncate(query, 50),
		"limit", limit,
		"available_tasks", len(m.distilledTasks))

	// Generate vector for query
	queryVector := m.generateHashVector(query)
	slog.Info("[Memory Search] Query vector generated (hash-based)", "dimension", len(queryVector))

	// Calculate cosine similarity for all tasks
	type similarityResult struct {
		task  *models.Task
		score float64
	}

	results := []similarityResult{}
	for _, data := range m.distilledTasks {
		score := m.cosineSimilarity(queryVector, data.Vector)
		if score > 0.5 { // Only return tasks with similarity > 0.5
			results = append(results, similarityResult{
				task: &models.Task{
					TaskID: data.TaskID,
					Payload: map[string]any{
						"input":   data.Input,
						"output":  data.Output,
						"context": data.Context,
					},
				},
				score: score,
			})
		}
	}

	slog.Info("[Memory Search] Similarity calculated (old method)",
		"total_tasks", len(m.distilledTasks),
		"above_threshold", len(results),
		"threshold", 0.5)

	// Sort by score (descending)
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	// Apply limit
	if len(results) > limit {
		results = results[:limit]
	}

	tasks := make([]*models.Task, 0, len(results))
	for _, result := range results {
		tasks = append(tasks, result.task)
	}

	slog.Info("[Memory Search] Search completed (old method)",
		"results_count", len(tasks),
		"limit", limit)

	return tasks, nil
}

// searchSimilarTasksNew uses the new vector-based search with experience repository.
func (m *memoryManager) searchSimilarTasksNew(ctx context.Context, query string, limit int) ([]*models.Task, error) {
	if m.embedder == nil || m.expRepo == nil {
		slog.Warn("[Memory Search] Embedder or repo not initialized, falling back to old method")
		return m.searchSimilarTasksOld(ctx, query, limit)
	}

	slog.Info("[Memory Search] Searching for similar tasks (new method)",
		"query", truncate(query, 50),
		"limit", limit)

	// Generate embedding for query
	queryVector, err := m.embedder.EmbedWithPrefix(ctx, query, "query:")
	if err != nil {
		slog.Error("[Memory Search] Failed to generate query embedding", "error", err)
		return nil, errors.Wrap(err, "generate query embedding")
	}

	slog.Info("[Memory Search] Query vector generated", "dimension", len(queryVector))

	// Search for similar experiences in experience repository
	experiences, err := m.expRepo.SearchByVector(ctx, queryVector, "default", limit)
	if err != nil {
		slog.Error("[Memory Search] Failed to search experiences", "error", err)
		return nil, errors.Wrap(err, "search experiences")
	}

	// Convert experiences to tasks
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

	slog.Info("[Memory Search] Search completed (new method)",
		"results_count", len(tasks),
		"limit", limit)

	return tasks, nil
}

// cosineSimilarity calculates cosine similarity between two vectors.
func (m *memoryManager) cosineSimilarity(v1, v2 []float64) float64 {
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

	// Optimization: Use single sqrt instead of two
	// math.Sqrt(norm1) * math.Sqrt(norm2) == math.Sqrt(norm1 * norm2)
	result := dotProduct / math.Sqrt(norm1*norm2)

	// Check for NaN or Inf in the result
	if math.IsNaN(result) || math.IsInf(result, 0) {
		return 0.0
	}

	return result
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
