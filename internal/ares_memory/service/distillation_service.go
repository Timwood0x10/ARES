// Package memory provides implementation for memory distillation operations.
package memoryapi

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_memory/distillation"
	"github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/storage/postgres/embedding"
)

// DistillationServiceImpl implements the DistillationService interface.
type DistillationServiceImpl struct {
	distiller *distillation.Distiller
	config    *DistillationConfig
	metrics   *DistillationMetrics
	repo      ExperienceRepository
	embedder  embedding.EmbeddingService
}

// NewDistillationService creates a new DistillationService instance.
//
// Args:
// distiller - internal distiller instance.
//
// Returns new distillation service instance.
func NewDistillationService(distiller *distillation.Distiller) *DistillationServiceImpl {
	if distiller == nil {
		return nil
	}

	return &DistillationServiceImpl{
		distiller: distiller,
		config:    defaultDistillationConfig(),
		metrics:   convertToAPIDistillationMetrics(distiller.GetMetrics()),
	}
}

// NewDistillationServiceWithEmbedder creates a new DistillationService with embedder and repository.
//
// Args:
// config - distillation configuration.
// embedder - embedding service for generating vectors.
// repo - experience repository for storage and retrieval.
//
// Returns new distillation service instance or error.
func NewDistillationServiceWithEmbedder(config *DistillationConfig, embedder embedding.EmbeddingService, repo ExperienceRepository) (*DistillationServiceImpl, error) {
	if config == nil {
		config = defaultDistillationConfig()
	}

	// Convert API config to internal config
	internalConfig := convertFromAPIDistillationConfig(config)

	// Create adapter for ExperienceRepository
	internalRepo := &experienceRepositoryAdapter{apiRepo: repo}

	// Create internal distiller
	internalDistiller := distillation.NewDistiller(internalConfig, embedder, internalRepo)

	return &DistillationServiceImpl{
		distiller: internalDistiller,
		config:    config,
		metrics:   &DistillationMetrics{},
		repo:      repo,
		embedder:  embedder,
	}, nil
}

// experienceRepositoryAdapter adapts API ExperienceRepository to internal ExperienceRepository interface
type experienceRepositoryAdapter struct {
	apiRepo ExperienceRepository
}

// SearchByVector implements internal ExperienceRepository interface

func (a *experienceRepositoryAdapter) SearchByVector(ctx context.Context, vector []float64, tenantID string, limit int) ([]distillation.Experience, error) {

	apiExperiences, err := a.apiRepo.SearchByVector(ctx, vector, tenantID, limit)

	if err != nil {

		return nil, err

	}

	// Convert API experiences to internal experiences

	internalExperiences := make([]distillation.Experience, len(apiExperiences))

	for i, exp := range apiExperiences {

		internalExperiences[i] = distillation.Experience{

			Problem: exp.Problem,

			Solution: exp.Solution,

			Confidence: exp.Confidence,

			ExtractionMethod: distillation.ExtractionMethod(exp.ExtractionMethod),
		}

	}

	return internalExperiences, nil

}

// GetByMemoryType implements internal ExperienceRepository interface
func (a *experienceRepositoryAdapter) GetByMemoryType(ctx context.Context, tenantID string, memoryType distillation.MemoryType) ([]distillation.Experience, error) {
	apiExperiences, err := a.apiRepo.GetByMemoryType(ctx, tenantID, MemoryType(memoryType))
	if err != nil {
		return nil, err
	}

	// Convert API experiences to internal experiences
	internalExperiences := make([]distillation.Experience, len(apiExperiences))
	for i, exp := range apiExperiences {
		internalExperiences[i] = distillation.Experience{
			Problem:          exp.Problem,
			Solution:         exp.Solution,
			Confidence:       exp.Confidence,
			ExtractionMethod: distillation.ExtractionMethod(exp.ExtractionMethod),
		}
	}

	return internalExperiences, nil
}

// CountByMemoryType implements internal ExperienceRepository interface
func (a *experienceRepositoryAdapter) CountByMemoryType(ctx context.Context, tenantID string, memoryType distillation.MemoryType) (int, error) {
	return a.apiRepo.CountByMemoryType(ctx, tenantID, MemoryType(memoryType))
}

// Update implements internal ExperienceRepository interface
func (a *experienceRepositoryAdapter) Update(ctx context.Context, experience *distillation.Experience) error {
	apiExperience := &Experience{
		Problem:          experience.Problem,
		Solution:         experience.Solution,
		Confidence:       experience.Confidence,
		ExtractionMethod: ExtractionMethod(experience.ExtractionMethod),
	}

	return a.apiRepo.Update(ctx, apiExperience)
}

// Delete implements internal ExperienceRepository interface
func (a *experienceRepositoryAdapter) Delete(ctx context.Context, id string) error {
	return a.apiRepo.Delete(ctx, id)
}

// DeleteBatch implements internal ExperienceRepository interface
func (a *experienceRepositoryAdapter) DeleteBatch(ctx context.Context, ids []string) error {
	return a.apiRepo.DeleteBatch(ctx, ids)
}

// Create implements internal ExperienceRepository interface
func (a *experienceRepositoryAdapter) Create(ctx context.Context, experience *distillation.Experience) error {
	// Convert internal experience to API experience
	apiExperience := &Experience{
		Problem:          experience.Problem,
		Solution:         experience.Solution,
		Confidence:       experience.Confidence,
		ExtractionMethod: ExtractionMethod(experience.ExtractionMethod),
	}

	return a.apiRepo.Create(ctx, apiExperience)
}

// GetDistiller returns the internal distiller instance (for advanced usage).
func (s *DistillationServiceImpl) GetDistiller() interface{} {
	return s.distiller
}

// DistillConversation distills memories from a conversation.
//
// Args:
// ctx - operation context.
// conversationID - unique identifier for the conversation.
// messages - conversation messages.
// tenantID - tenant ID for multi-tenancy.
// userID - user ID for the conversation.
//
// Returns distilled memories or error.
func (s *DistillationServiceImpl) DistillConversation(ctx context.Context, conversationID string, messages []ConversationMessage, tenantID, userID string) ([]*DistilledMemory, error) {
	if conversationID == "" {
		return nil, ErrInvalidConversationID
	}
	if len(messages) == 0 {
		return nil, ErrNoMessages
	}
	if tenantID == "" {
		return nil, ErrInvalidTenantID
	}

	slog.Info("Starting memory distillation",
		"conversation_id", conversationID,
		"tenant_id", tenantID,
		"user_id", userID,
		"message_count", len(messages))

	// Convert API messages to internal messages
	internalMessages := make([]distillation.Message, len(messages))
	for i, msg := range messages {
		internalMessages[i] = distillation.Message{
			Role:    msg.Role,
			Content: msg.Content,
		}
	}

	// Execute distillation
	internalMemories, err := s.distiller.DistillConversation(ctx, conversationID, internalMessages, tenantID, userID)
	if err != nil {
		slog.Error("Memory distillation failed",
			"conversation_id", conversationID,
			"error", err)
		return nil, errors.Wrap(err, "distill conversation")
	}

	// Convert internal memories to API memories
	apiMemories := make([]*DistilledMemory, len(internalMemories))
	for i, mem := range internalMemories {
		apiMemories[i] = convertToAPIDistilledMemory(&mem)
	}

	slog.Info("Memory distillation completed",
		"conversation_id", conversationID,
		"memories_created", len(apiMemories))

	// Update metrics
	s.metrics = convertToAPIDistillationMetrics(s.distiller.GetMetrics())

	return apiMemories, nil
}

// GetMetrics returns the current distillation metrics.
//
// Returns the metrics.
func (s *DistillationServiceImpl) GetMetrics() *DistillationMetrics {
	if s.distiller == nil {
		return s.metrics
	}

	return convertToAPIDistillationMetrics(s.distiller.GetMetrics())
}

// ResetMetrics resets the distillation metrics.
func (s *DistillationServiceImpl) ResetMetrics() {
	if s.distiller == nil {
		s.metrics = &DistillationMetrics{}
		return
	}

	s.distiller.ResetMetrics()
	s.metrics = &DistillationMetrics{}
}

// GetConfig returns the current distillation configuration.
//
// Returns the configuration.
func (s *DistillationServiceImpl) GetConfig() *DistillationConfig {
	return s.config
}

// UpdateConfig updates the distillation configuration.
//
// Args:
// config - new configuration.
//
// Returns error if update fails.
func (s *DistillationServiceImpl) UpdateConfig(config *DistillationConfig) error {
	if config == nil {
		return ErrInvalidConfig
	}

	if s.distiller == nil {
		s.config = config
		return nil
	}

	// Note: The internal distiller doesn't support runtime config updates yet
	// For now, just update the API config
	s.config = config

	slog.Info("Distillation config updated",
		"min_importance", config.MinImportance,
		"max_memories", config.MaxMemoriesPerDistillation)

	return nil
}

// Helper functions for converting between API and internal types

func convertToAPIDistilledMemory(mem *distillation.Memory) *DistilledMemory {
	if mem == nil {
		return nil
	}

	var expiresAt *time.Time
	if !mem.ExpiresAt.IsZero() {
		expiresAt = &mem.ExpiresAt
	}

	return &DistilledMemory{
		ID:         mem.ID,
		Type:       MemoryType(mem.Type),
		Content:    mem.Content,
		Importance: mem.Importance,
		Source:     mem.Source,
		TenantID:   getMetadataString(mem.Metadata, "tenant_id"),
		UserID:     getMetadataString(mem.Metadata, "user_id"),
		CreatedAt:  mem.CreatedAt,
		ExpiresAt:  expiresAt,
		Metadata:   mem.Metadata,
	}
}

func convertFromAPIDistillationConfig(config *DistillationConfig) *distillation.DistillationConfig {
	if config == nil {
		return distillation.DefaultDistillationConfig()
	}

	return &distillation.DistillationConfig{
		MinImportance:              config.MinImportance,
		ConflictThreshold:          config.ConflictThreshold,
		MaxMemoriesPerDistillation: config.MaxMemoriesPerDistillation,
		MaxSolutionsPerTenant:      config.MaxSolutionsPerTenant,
		EnableCodeFilter:           config.EnableCodeFilter,
		EnableStacktraceFilter:     config.EnableStacktraceFilter,
		EnableLogFilter:            config.EnableLogFilter,
		EnableMarkdownTableFilter:  config.EnableMarkdownTableFilter,
		EnableCrossTurnExtraction:  config.EnableCrossTurnExtraction,
		EnableLengthBonus:          config.EnableLengthBonus,
		LengthThreshold:            config.LengthThreshold,
		LengthBonus:                config.LengthBonus,
		TopNBeforeConflict:         config.TopNBeforeConflict,
		ConflictSearchLimit:        config.ConflictSearchLimit,
		PrecisionOverRecall:        config.PrecisionOverRecall,
	}
}

func convertToAPIDistillationMetrics(metrics *distillation.DistillationMetrics) *DistillationMetrics {
	if metrics == nil {
		return &DistillationMetrics{}
	}

	return &DistillationMetrics{
		AttemptTotal:     metrics.AttemptTotal,
		SuccessTotal:     metrics.SuccessTotal,
		FilteredNoise:    metrics.FilteredNoise,
		FilteredSecurity: metrics.FilteredSecurity,
		ConflictResolved: metrics.ConflictResolved,
		MemoriesCreated:  metrics.MemoriesCreated,
	}
}

func getMetadataString(metadata map[string]interface{}, key string) string {
	if metadata == nil {
		return ""
	}
	if val, ok := metadata[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

func defaultDistillationConfig() *DistillationConfig {
	return &DistillationConfig{
		MinImportance:              0.6,
		ConflictThreshold:          0.85,
		MaxMemoriesPerDistillation: 3,
		MaxSolutionsPerTenant:      5000,
		EnableCodeFilter:           true,
		EnableStacktraceFilter:     true,
		EnableLogFilter:            true,
		EnableMarkdownTableFilter:  true,
		EnableCrossTurnExtraction:  true,
		EnableLengthBonus:          true,
		LengthThreshold:            60,
		LengthBonus:                0.1,
		TopNBeforeConflict:         true,
		ConflictSearchLimit:        5,
		PrecisionOverRecall:        true,
	}
}

// UpdateMemory updates an existing distilled memory.
//
// Args:
// ctx - operation context.
// memoryID - the memory ID to update.
// updates - map of fields to update (content, importance, metadata, etc.).
// Returns error if update fails.
func (s *DistillationServiceImpl) UpdateMemory(ctx context.Context, memoryID string, updates map[string]interface{}) error {
	if memoryID == "" {
		return ErrInvalidMemoryID
	}
	if len(updates) == 0 {
		return nil
	}

	if s.repo == nil {
		return ErrMemoryUpdateFailed
	}

	slog.InfoContext(ctx, "Updating distilled memory", "memory_id", memoryID, "updates", updates)

	// Get the internal repository for advanced operations
	internalRepo := s.repo.GetInternalRepository()
	if internalRepo == nil {
		return ErrMemoryUpdateFailed
	}

	// Try to update the memory through the internal repository
	// The internal repository should have methods to update DistilledMemory records
	// For now, we'll return an error indicating this feature needs the internal repository
	// to implement the appropriate update methods

	// Check if this is a memory repository (internal implementation)
	type internalMemoryRepo interface {
		UpdateMemory(ctx context.Context, memoryID string, updates map[string]interface{}) error
	}

	if repo, ok := internalRepo.(internalMemoryRepo); ok {
		err := repo.UpdateMemory(ctx, memoryID, updates)
		if err != nil {
			slog.ErrorContext(ctx, "Failed to update distilled memory", "memory_id", memoryID, "error", err)
			return errors.Wrap(err, "update memory")
		}

		slog.InfoContext(ctx, "Distilled memory updated successfully", "memory_id", memoryID)
		return nil
	}

	return fmt.Errorf("internal repository does not support memory updates")
}

// DeleteMemory deletes a distilled memory.
//
// Args:
// ctx - operation context.
// memoryID - the memory ID to delete.
// Returns error if deletion fails.
func (s *DistillationServiceImpl) DeleteMemory(ctx context.Context, memoryID string) error {
	if memoryID == "" {
		return ErrInvalidMemoryID
	}

	if s.repo == nil {
		return ErrMemoryDeleteFailed
	}

	slog.InfoContext(ctx, "Deleting distilled memory", "memory_id", memoryID)

	err := s.repo.Delete(ctx, memoryID)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to delete distilled memory", "memory_id", memoryID, "error", err)
		return errors.Wrap(err, "delete memory")
	}

	slog.InfoContext(ctx, "Distilled memory deleted successfully", "memory_id", memoryID)
	return nil
}

// SearchMemories searches for memories by query text (using vector search).
//
// Args:
// ctx - operation context.
// query - the search query.
// tenantID - tenant ID for multi-tenancy.
// limit - maximum number of results.
// Returns matching memories or error.
func (s *DistillationServiceImpl) SearchMemories(ctx context.Context, query string, tenantID string, limit int) ([]*DistilledMemory, error) {
	if query == "" {
		return nil, ErrInvalidQuery
	}
	if limit <= 0 {
		return nil, ErrInvalidLimit
	}

	if s.repo == nil || s.embedder == nil {
		return nil, ErrVectorSearchFailed
	}

	slog.InfoContext(ctx, "Searching memories", "query", query, "tenant_id", tenantID, "limit", limit)

	// Generate embedding for the query
	embedding, err := s.embedder.Embed(ctx, query)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to generate embedding for query", "query", query, "error", err)
		return nil, errors.Wrap(err, "generate embedding")
	}

	// Search for similar memories
	experiences, err := s.repo.SearchByVector(ctx, embedding, tenantID, limit)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to search memories", "query", query, "error", err)
		return nil, errors.Wrap(err, "search memories")
	}

	// Convert experiences to distilled memories
	memories := make([]*DistilledMemory, 0, len(experiences))
	for _, exp := range experiences {
		memory := &DistilledMemory{
			ID:         "",              // Experience doesn't have ID field, will be populated by repository
			Type:       MemoryKnowledge, // Default type
			Content:    exp.Problem + "\n" + exp.Solution,
			Importance: exp.Confidence,
			Source:     "",
			TenantID:   tenantID,
			CreatedAt:  time.Now(),
			Metadata: map[string]interface{}{
				"extraction_method": string(exp.ExtractionMethod),
			},
		}
		memories = append(memories, memory)
	}

	slog.InfoContext(ctx, "Memory search completed", "query", query, "results_count", len(memories))
	return memories, nil
}
