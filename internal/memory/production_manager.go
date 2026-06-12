// Package memory provides unified memory management for the StyleAgent framework.
// This is the production-grade MemoryManager that integrates with PostgreSQL + pgvector storage.
package memory

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"goagentx/internal/core/models"
	"goagentx/internal/errors"
	"goagentx/internal/storage/postgres"
	"goagentx/internal/storage/postgres/embedding"
	storage_models "goagentx/internal/storage/postgres/models"
	"goagentx/internal/storage/postgres/repositories"
	"goagentx/internal/storage/postgres/services"
)

// ProductionMemoryManager implements MemoryManager interface with production-grade storage.
// It integrates with PostgreSQL + pgvector for persistent storage and intelligent retrieval.
type ProductionMemoryManager struct {
	// Storage components
	dbPool           *postgres.Pool
	tenantGuard      *postgres.TenantGuard
	retrievalService *services.RetrievalService
	embeddingClient  *embedding.EmbeddingClient
	writeBuffer      *postgres.WriteBuffer    // Write buffer for rate limiting
	embeddingQueue   *postgres.EmbeddingQueue // Async embedding queue

	// Repositories
	knowledgeRepository    *repositories.KnowledgeRepository
	experienceRepository   *repositories.ExperienceRepository
	conversationRepository *repositories.ConversationRepository
	taskResultRepository   *repositories.TaskResultRepository

	// Configuration
	config          *MemoryConfig
	currentTenantID string

	// Lifecycle
	mu      sync.RWMutex
	started bool
	stopped bool
	cancel  context.CancelFunc // Context cancellation function for graceful shutdown
	baseCtx context.Context    // Base context for all operations

	// Optional: keep in-memory cache for hot data
	sessionCache map[string]*SessionData
	maxCacheSize int
}

// SessionData holds session information with optional caching.
type SessionData struct {
	SessionID    string
	UserID       string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	MessageCount int
}

// generateSessionID creates a cryptographically random session ID.
func generateSessionID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("session_%d", time.Now().UnixNano())
	}
	return "sess_" + hex.EncodeToString(b)
}

// NewProductionMemoryManager creates a new production-grade MemoryManager.
// Args:
// dbPool - PostgreSQL connection pool
// embeddingClient - Embedding service client
// config - Memory manager configuration
// Returns new ProductionMemoryManager instance.
func NewProductionMemoryManager(
	dbPool *postgres.Pool,
	embeddingClient *embedding.EmbeddingClient,
	config *MemoryConfig,
) (*ProductionMemoryManager, error) {
	if config == nil {
		config = DefaultMemoryConfig()
	}

	if dbPool == nil {
		return nil, fmt.Errorf("database pool is required")
	}

	if embeddingClient == nil {
		return nil, fmt.Errorf("embedding client is required")
	}

	// Create tenant guard
	tenantGuard := postgres.NewTenantGuard(dbPool)

	// Create repositories
	dbConn := dbPool.GetDB()
	knowledgeRepo := repositories.NewKnowledgeRepository(dbPool.GetDB(), dbConn)
	experienceRepo := repositories.NewExperienceRepository(dbConn)
	conversationRepo := repositories.NewConversationRepository(dbConn)
	taskResultRepo := repositories.NewTaskResultRepository(dbConn)

	// Create retrieval service
	retrievalGuard := postgres.NewRetrievalGuard(
		100,            // maxRequestsPerSec
		5,              // failureThreshold
		30*time.Second, // openTimeout
		30*time.Second, // dbTimeout
	)

	retrievalService := services.NewRetrievalService(
		dbPool,
		embeddingClient,
		nil, // llmClient (will be created from env if needed)
		tenantGuard,
		retrievalGuard,
		knowledgeRepo,
		nil, // expRepo
		nil, // toolRepo
	)

	// Create embedding queue (asynchronous embedding chain per design standard)
	embeddingQueue := postgres.NewEmbeddingQueue(
		dbPool,
		postgres.DefaultEmbeddingConfig(),
	)

	// Create write buffer (write backpressure layer per design standard)
	writeBuffer := postgres.NewWriteBuffer(
		dbPool,
		embeddingQueue,
		32,            // batchSize
		5*time.Second, // flushInterval
		postgres.DefaultEmbeddingConfig(),
	)

	return &ProductionMemoryManager{

		dbPool: dbPool,

		tenantGuard: tenantGuard,

		retrievalService: retrievalService,

		embeddingClient: embeddingClient,

		writeBuffer: writeBuffer,

		embeddingQueue: embeddingQueue,

		knowledgeRepository: knowledgeRepo,

		experienceRepository: experienceRepo,

		conversationRepository: conversationRepo,

		taskResultRepository: taskResultRepo,

		config: config,

		sessionCache: make(map[string]*SessionData),

		maxCacheSize: config.MaxSessions,
	}, nil
}

// SetTenantID sets the current tenant ID for multi-tenant operations.
// Args:
// tenantID - tenant identifier.
// Returns error if tenant ID is invalid.
func (m *ProductionMemoryManager) SetTenantID(tenantID string) error {
	if tenantID == "" {
		return fmt.Errorf("tenant ID cannot be empty")
	}

	m.mu.Lock()
	m.currentTenantID = tenantID
	m.mu.Unlock()

	slog.Debug("Tenant ID set", "tenant_id", tenantID)
	return nil
}

// Start starts the memory manager and background workers.
// This method creates a new context for the memory manager and starts
// the write buffer goroutine. The context is used for graceful shutdown.
//
// Thread-safety: Uses mutex to ensure only one goroutine can start the manager.
//
// Args:
// ctx - context for cancellation.
// Returns error if starting fails.
func (m *ProductionMemoryManager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.started {
		return nil
	}

	// Allow restart after stop by resetting the stopped flag
	if m.stopped {
		m.stopped = false
	}

	// Create a new context for the memory manager lifecycle
	// This allows us to cancel all operations during shutdown
	m.baseCtx, m.cancel = context.WithCancel(ctx)

	// Start write buffer in background goroutine
	if err := m.writeBuffer.Start(m.baseCtx); err != nil {
		return errors.Wrap(err, "start write buffer")
	}

	m.started = true
	slog.Info("Production memory manager started")
	return nil
}

// Stop stops the memory manager and cleans up resources.
// This method cancels the memory manager context and waits for all
// background goroutines to finish.
//
// Thread-safety: Uses mutex to ensure only one goroutine can stop the manager.
//
// Args:
// ctx - context for cancellation (used for timeout).
// Returns error if stopping fails.
func (m *ProductionMemoryManager) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.stopped {
		return nil
	}

	// Cancel the memory manager context to signal all goroutines to stop
	if m.cancel != nil {
		m.cancel()
	}

	// Stop write buffer with timeout
	stopCtx, stopCancel := context.WithTimeout(ctx, 30*time.Second)
	defer stopCancel()
	if err := m.writeBuffer.Stop(stopCtx); err != nil {
		slog.Warn("Failed to stop write buffer", "error", err)
	}

	// Clear cache
	m.sessionCache = make(map[string]*SessionData)

	m.stopped = true
	slog.Info("Production memory manager stopped")
	return nil
}

// CreateSession creates a new session and returns the session ID.
// Args:
// ctx - database operation context.
// userID - user identifier.
// Returns session ID or error if creation fails.
func (m *ProductionMemoryManager) CreateSession(ctx context.Context, userID string) (string, error) {
	sessionID := generateSessionID()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Add to cache
	m.sessionCache[sessionID] = &SessionData{
		SessionID:    sessionID,
		UserID:       userID,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
		MessageCount: 0,
	}

	// Manage cache size
	if len(m.sessionCache) > m.maxCacheSize {
		// Remove oldest entry (simple LRU)
		var oldestKey string
		var oldestTime time.Time
		for k, v := range m.sessionCache {
			if oldestKey == "" || v.CreatedAt.Before(oldestTime) {
				oldestKey = k
				oldestTime = v.CreatedAt
			}
		}
		if oldestKey != "" {
			delete(m.sessionCache, oldestKey)
		}
	}

	slog.Debug("Session created", "session_id", sessionID, "user_id", userID)
	return sessionID, nil
}

// AddMessage adds a message to the session.
// Args:
// ctx - database operation context.
// sessionID - session identifier.
// role - message role (user/assistant/system).
// content - message content.
// Returns error if operation fails.
// Note: This stores conversations WITHOUT vector embedding (per design standard).
// conversations table is for history tracking only, retrieval uses knowledge/experience tables.
func (m *ProductionMemoryManager) AddMessage(ctx context.Context, sessionID, role, content string) error {
	if sessionID == "" {
		return fmt.Errorf("session ID cannot be empty")
	}
	if role == "" {
		return fmt.Errorf("role cannot be empty")
	}
	if content == "" {
		return fmt.Errorf("content cannot be empty")
	}

	// Set tenant context (MUST be called for every tenant-specific operation)
	tenantID := m.getCurrentTenantID()
	if err := m.tenantGuard.SetTenantContext(ctx, tenantID); err != nil {
		return errors.Wrap(err, "set tenant context")
	}

	// Create conversation record (NO vector embedding per design standard)
	// conversations table: NO vector + expires_at + tenant_id

	// Get user ID from session cache
	userID := ""
	m.mu.RLock()
	if sessionData, exists := m.sessionCache[sessionID]; exists {
		userID = sessionData.UserID
	}
	m.mu.RUnlock()

	// If user ID not found in cache, use a default value
	// In production, you might want to extract this from context or other sources
	if userID == "" {
		slog.Warn("session not found in cache, message assigned to anonymous user",
			"session_id", sessionID)
		userID = "anonymous"
	}

	conv := &storage_models.Conversation{
		SessionID: sessionID,
		TenantID:  tenantID,
		UserID:    userID,
		AgentID:   "style-agent",
		Role:      role,
		Content:   content,
		ExpiresAt: time.Now().Add(24 * time.Hour), // 24 hour TTL as per design
	}

	if err := m.conversationRepository.Create(ctx, conv); err != nil {
		return errors.Wrap(err, "create conversation")
	}

	// Update session cache
	m.mu.Lock()
	if sessionData, exists := m.sessionCache[sessionID]; exists {
		sessionData.UpdatedAt = time.Now()
		sessionData.MessageCount++
	}
	m.mu.Unlock()

	slog.Debug("Message added", "session_id", sessionID, "role", role)
	return nil
}

// GetMessages retrieves all messages from the session.
// Args:
// ctx - database operation context.
// sessionID - session identifier.
// Returns list of messages or error if retrieval fails.
func (m *ProductionMemoryManager) GetMessages(ctx context.Context, sessionID string) ([]Message, error) {
	if sessionID == "" {
		return nil, errors.New("session ID cannot be empty")
	}

	// Set tenant context
	tenantID := m.getCurrentTenantID()
	if err := m.tenantGuard.SetTenantContext(ctx, tenantID); err != nil {
		return nil, errors.Wrap(err, "set tenant context")
	}

	// Retrieve conversations from database
	conversations, err := m.conversationRepository.GetBySession(ctx, tenantID, sessionID, m.config.MaxHistory)
	if err != nil {
		return nil, errors.Wrap(err, "get conversations")
	}

	// Convert to Message format
	messages := make([]Message, len(conversations))
	for i, conv := range conversations {
		messages[i] = Message{
			Role:    conv.Role,
			Content: conv.Content,
			Time:    conv.CreatedAt,
		}
	}

	return messages, nil
}

// DeleteSession deletes a session and all its messages immediately.
// Args:
// ctx - database operation context.
// sessionID - session identifier.
// Returns error if deletion fails.
func (m *ProductionMemoryManager) DeleteSession(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return errors.New("session ID cannot be empty")
	}

	// Set tenant context (MUST be called for every tenant-specific operation)
	tenantID := m.getCurrentTenantID()
	if err := m.tenantGuard.SetTenantContext(ctx, tenantID); err != nil {
		return errors.Wrap(err, "set tenant context")
	}

	// Delete all conversations for this session
	deletedCount, err := m.conversationRepository.DeleteBySession(ctx, sessionID, tenantID)
	if err != nil {
		return errors.Wrap(err, "delete conversations")
	}

	// Remove from cache
	m.mu.Lock()
	delete(m.sessionCache, sessionID)
	m.mu.Unlock()

	slog.Debug("Session deleted", "session_id", sessionID, "tenant_id", tenantID, "deleted_messages", deletedCount)
	return nil
}

// BuildContext builds input with conversation history context.
// Args:
// ctx - database operation context.
// input - current user input.
// sessionID - session identifier.
// Returns context string or error if building fails.
func (m *ProductionMemoryManager) BuildContext(ctx context.Context, input string, sessionID string) (string, error) {
	messages, err := m.GetMessages(ctx, sessionID)
	if err != nil {
		slog.Warn("Failed to get messages, using raw input", "error", err)
		return input, nil
	}

	// Keep only last N messages to avoid long context
	maxHistory := m.config.MaxHistory
	if len(messages) > maxHistory {
		messages = messages[len(messages)-maxHistory:]
	}

	// Build context string
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
// Args:
// ctx - database operation context.
// sessionID - session identifier.
// userID - user identifier.
// input - task input.
// Returns task ID or error if creation fails.
// Note: This creates task_result WITHOUT embedding (embedding only for experiences).
// task_results table stores execution history, experiences store reusable knowledge.
func (m *ProductionMemoryManager) CreateTask(ctx context.Context, sessionID, userID, input string) (string, error) {
	taskID := "task_" + strconv.FormatInt(time.Now().UnixNano(), 10)

	// Set tenant context (MUST be called for every tenant-specific operation)
	tenantID := m.getCurrentTenantID()
	if err := m.tenantGuard.SetTenantContext(ctx, tenantID); err != nil {
		return "", errors.Wrap(err, "set tenant context")
	}

	// Create task result record (NO embedding, only for execution history)
	taskResult := &storage_models.TaskResult{
		ID:               taskID,
		TenantID:         tenantID,
		SessionID:        sessionID,
		TaskType:         "user_request",
		AgentID:          "style-agent",
		Input:            map[string]interface{}{"content": input},
		Output:           nil,
		Embedding:        nil, // No embedding for task results
		EmbeddingModel:   "intfloat/e5-large",
		EmbeddingVersion: 1,
		Status:           "pending",
		Metadata:         make(map[string]interface{}),
	}

	if err := m.taskResultRepository.Create(ctx, taskResult); err != nil {
		return "", errors.Wrap(err, "create task result")
	}

	slog.Debug("Task created", "task_id", taskID, "session_id", sessionID)
	return taskID, nil
}

// UpdateTaskOutput updates the task output.
// Args:
// ctx - database operation context.
// taskID - task identifier.
// output - task output.
// Returns error if update fails.
func (m *ProductionMemoryManager) UpdateTaskOutput(ctx context.Context, taskID, output string) error {
	if taskID == "" {
		return errors.New("task ID cannot be empty")
	}

	// Set tenant context
	tenantID := m.getCurrentTenantID()
	if err := m.tenantGuard.SetTenantContext(ctx, tenantID); err != nil {
		return errors.Wrap(err, "set tenant context")
	}

	// Get existing task
	task, err := m.taskResultRepository.GetByID(ctx, taskID)
	if err != nil {
		return errors.Wrap(err, "get task result")
	}

	// Update task
	task.Output = map[string]interface{}{"content": output}
	task.Status = "completed"
	task.LatencyMs = int(time.Since(task.CreatedAt).Milliseconds())

	if err := m.taskResultRepository.Update(ctx, task); err != nil {
		return errors.Wrap(err, "update task result")
	}

	slog.Debug("Task output updated", "task_id", taskID)
	return nil
}

// DistillTask extracts key information from task for future reference.
// Args:
// ctx - database operation context.
// taskID - task identifier.
// Returns distilled task or error if distillation fails.
// Note: This retrieves stored task result and converts to Task format.
func (m *ProductionMemoryManager) DistillTask(ctx context.Context, taskID string) (*models.Task, error) {
	// Set tenant context (MUST be called for every tenant-specific operation)
	tenantID := m.getCurrentTenantID()
	if err := m.tenantGuard.SetTenantContext(ctx, tenantID); err != nil {
		return nil, errors.Wrap(err, "set tenant context")
	}

	// Get task result
	taskResult, err := m.taskResultRepository.GetByID(ctx, taskID)
	if err != nil {
		return nil, errors.Wrap(err, "get task result")
	}

	// Convert to models.Task format
	task := &models.Task{
		TaskID:    taskResult.ID,
		TaskType:  models.AgentType(taskResult.TaskType),
		Payload:   taskResult.Input,
		Priority:  50, // Default priority
		CreatedAt: taskResult.CreatedAt,
	}

	slog.Debug("Task distilled", "task_id", taskID)
	return task, nil
}

// StoreDistilledTask stores a distilled task with async embedding chain.
// Args:
// ctx - database operation context.
// taskID - task identifier.
// distilled - distilled task data.
// Returns error if storage fails.
// Note: Per design standard, this uses asynchronous embedding chain:
// 1. Write to DB with embedding_status = 'pending'
// 2. Write to embedding_queue with dedupe_key (for deduplication)
// 3. Background Worker processes embedding tasks
// 4. Worker updates DB with embedding and status = 'completed'
func (m *ProductionMemoryManager) StoreDistilledTask(ctx context.Context, taskID string, distilled *models.Task) error {
	if distilled == nil {
		return errors.New("distilled task cannot be nil")
	}

	// Set tenant context (MUST be called for every tenant-specific operation)
	tenantID := m.getCurrentTenantID()
	if err := m.tenantGuard.SetTenantContext(ctx, tenantID); err != nil {
		return errors.Wrap(err, "set tenant context")
	}

	// Use write buffer for async embedding chain (write backpressure layer per design standard)
	writeItem := &postgres.WriteItem{
		TenantID: tenantID,
		Table:    "experiences_1024",
		Content:  fmt.Sprintf("%v", distilled.Payload), // Use payload as content
		Metadata: map[string]interface{}{
			"output":   "", // Extract from payload if available
			"type":     "solution",
			"agent_id": "style-agent",
		},
	}

	if err := m.writeBuffer.Write(ctx, writeItem); err != nil {
		return errors.Wrap(err, "write to buffer")
	}

	slog.Debug("Distilled task queued for async embedding", "task_id", taskID)
	return nil
}

// SearchSimilarTasks searches for similar tasks using intelligent retrieval.
// Args:
// ctx - database operation context.
// query - search query.
// limit - maximum number of results.
// Returns list of similar tasks or error if search fails.
// Note: This returns experiences (agent knowledge) rather than execution tasks.
func (m *ProductionMemoryManager) SearchSimilarTasks(ctx context.Context, query string, limit int) ([]*models.Task, error) {
	if query == "" {
		return nil, errors.New("query cannot be empty")
	}

	// Set tenant context (MUST be called for every tenant-specific operation)
	tenantID := m.getCurrentTenantID()
	if err := m.tenantGuard.SetTenantContext(ctx, tenantID); err != nil {
		return nil, errors.Wrap(err, "set tenant context")
	}

	// Create search request
	searchRequest := &services.SearchRequest{
		Query:    query,
		TenantID: tenantID,
		TopK:     limit,
		Plan:     services.DefaultRetrievalPlan(),
	}

	// Enable experience search only (hybrid search: vector + BM25)
	searchRequest.Plan.SearchExperience = true
	searchRequest.Plan.SearchKnowledge = false
	searchRequest.Plan.SearchTools = false
	searchRequest.Plan.ExperienceWeight = 1.0

	// Execute search with fallback (per design standard)
	results, err := m.retrievalService.Search(ctx, searchRequest)
	if err != nil {
		return nil, errors.Wrap(err, "search similar tasks")
	}

	// Convert experiences to models.Task format
	tasks := make([]*models.Task, 0, len(results))
	for _, result := range results {
		if result.Source == "experience" {
			// Convert experience to Task format for backward compatibility
			task := &models.Task{
				TaskID:   result.ID,
				TaskType: models.AgentType("experience"),
				Payload: map[string]any{
					"input":  result.Content,
					"output": result.Metadata["output"],
					"score":  result.Score,
				},
				Priority:  int(result.Score * 100), // Convert score to priority
				CreatedAt: result.CreatedAt,
			}
			tasks = append(tasks, task)
		}
	}

	slog.Debug("Similar experiences found", "query", query, "count", len(tasks))
	return tasks, nil
}

// getCurrentTenantID returns the current tenant ID with fallback.
func (m *ProductionMemoryManager) getCurrentTenantID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.currentTenantID != "" {
		return m.currentTenantID
	}

	return "default" // Fallback to default tenant
}

// GetLatestSessionForLeader retrieves the most recent session ID for a leader from checkpoint.
// Returns ("", nil) if no checkpoint exists.
func (m *ProductionMemoryManager) GetLatestSessionForLeader(ctx context.Context, leaderID string) (string, error) {
	if leaderID == "" {
		return "", nil
	}

	tenantID := m.getCurrentTenantID()
	if err := m.tenantGuard.SetTenantContext(ctx, tenantID); err != nil {
		return "", errors.Wrap(err, "get latest session for leader: set tenant context")
	}

	query := `SELECT session_id FROM leader_checkpoints WHERE leader_id = $1 ORDER BY updated_at DESC LIMIT 1`
	row := m.dbPool.QueryRow(ctx, query, leaderID)

	var sessionID string
	if err := row.Scan(&sessionID); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", errors.Wrap(err, "get latest session for leader")
	}

	return sessionID, nil
}
