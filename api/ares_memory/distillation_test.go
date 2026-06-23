package memory

import (
	"context"
	"testing"
	"time"
)

// TestMemoryType tests MemoryType constants.
func TestMemoryType(t *testing.T) {
	tests := []struct {
		name string
		typ  MemoryType
		want string
	}{
		{
			name: "knowledge type",
			typ:  MemoryKnowledge,
			want: "knowledge",
		},
		{
			name: "preference type",
			typ:  MemoryPreference,
			want: "preference",
		},
		{
			name: "interaction type",
			typ:  MemoryInteraction,
			want: "interaction",
		},
		{
			name: "profile type",
			typ:  MemoryProfile,
			want: "profile",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.typ) != tt.want {
				t.Errorf("MemoryType = %q, want %q", tt.typ, tt.want)
			}
		})
	}
}

// TestMemoryTypeUniqueness tests that all MemoryType values are unique.
func TestMemoryTypeUniqueness(t *testing.T) {
	types := map[string]bool{
		string(MemoryKnowledge):   true,
		string(MemoryPreference):  true,
		string(MemoryInteraction): true,
		string(MemoryProfile):     true,
	}

	if len(types) != 4 {
		t.Errorf("expected 4 unique memory types, got %d", len(types))
	}
}

// TestExtractionMethod tests ExtractionMethod constants.
func TestExtractionMethod(t *testing.T) {
	tests := []struct {
		name   string
		method ExtractionMethod
		want   string
	}{
		{
			name:   "direct extraction",
			method: ExtractionDirect,
			want:   "direct",
		},
		{
			name:   "cross-turn extraction",
			method: ExtractionCrossTurn,
			want:   "cross-turn",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.method) != tt.want {
				t.Errorf("ExtractionMethod = %q, want %q", tt.method, tt.want)
			}
		})
	}
}

// TestExtractionMethodUniqueness tests that all ExtractionMethod values are unique.
func TestExtractionMethodUniqueness(t *testing.T) {
	methods := map[string]bool{
		string(ExtractionDirect):    true,
		string(ExtractionCrossTurn): true,
	}

	if len(methods) != 2 {
		t.Errorf("expected 2 unique extraction methods, got %d", len(methods))
	}
}

// TestResolutionStrategy tests ResolutionStrategy constants.
func TestResolutionStrategy(t *testing.T) {
	tests := []struct {
		name     string
		strategy ResolutionStrategy
		want     string
	}{
		{
			name:     "replace old strategy",
			strategy: ReplaceOld,
			want:     "replace",
		},
		{
			name:     "keep both strategy",
			strategy: KeepBoth,
			want:     "version",
		},
		{
			name:     "merge strategy",
			strategy: Merge,
			want:     "merge",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.strategy) != tt.want {
				t.Errorf("ResolutionStrategy = %q, want %q", tt.strategy, tt.want)
			}
		})
	}
}

// TestResolutionStrategyUniqueness tests that all ResolutionStrategy values are unique.
func TestResolutionStrategyUniqueness(t *testing.T) {
	strategies := map[string]bool{
		string(ReplaceOld): true,
		string(KeepBoth):   true,
		string(Merge):      true,
	}

	if len(strategies) != 3 {
		t.Errorf("expected 3 unique resolution strategies, got %d", len(strategies))
	}
}

// TestExperience tests Experience struct.
func TestExperience(t *testing.T) {
	tests := []struct {
		name       string
		experience Experience
	}{
		{
			name: "full experience",
			experience: Experience{
				Problem:          "How to fix error X",
				Solution:         "Use command Y",
				Confidence:       0.95,
				ExtractionMethod: ExtractionDirect,
			},
		},
		{
			name: "minimal experience",
			experience: Experience{
				Problem:  "Problem",
				Solution: "Solution",
			},
		},
		{
			name: "experience with zero confidence",
			experience: Experience{
				Problem:    "Problem",
				Solution:   "Solution",
				Confidence: 0.0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = tt.experience.Problem
			_ = tt.experience.Solution
			_ = tt.experience.Confidence
			_ = tt.experience.ExtractionMethod
		})
	}
}

// TestDistilledMemory tests DistilledMemory struct.
func TestDistilledMemory(t *testing.T) {
	now := time.Now()
	expiresAt := now.Add(24 * time.Hour)

	tests := []struct {
		name   string
		memory DistilledMemory
	}{
		{
			name: "full memory",
			memory: DistilledMemory{
				ID:         "mem-123",
				Type:       MemoryKnowledge,
				Content:    "Test content",
				Importance: 0.9,
				Source:     "conv-456",
				TenantID:   "tenant-789",
				UserID:     "user-999",
				CreatedAt:  now,
				ExpiresAt:  &expiresAt,
				Metadata: map[string]interface{}{
					"key": "value",
				},
			},
		},
		{
			name: "minimal memory",
			memory: DistilledMemory{
				ID:        "mem-888",
				Type:      MemoryPreference,
				Content:   "Test",
				CreatedAt: now,
			},
		},
		{
			name: "memory with nil expires at",
			memory: DistilledMemory{
				ID:        "mem-666",
				Type:      MemoryInteraction,
				Content:   "Test",
				CreatedAt: now,
				ExpiresAt: nil,
				Metadata:  make(map[string]interface{}),
			},
		},
		{
			name: "memory with nil metadata",
			memory: DistilledMemory{
				ID:        "mem-555",
				Type:      MemoryProfile,
				Content:   "Test",
				CreatedAt: now,
				Metadata:  nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = tt.memory.ID
			_ = tt.memory.Type
			_ = tt.memory.Content
			_ = tt.memory.Importance
			_ = tt.memory.Source
			_ = tt.memory.TenantID
			_ = tt.memory.UserID
			_ = tt.memory.CreatedAt
			_ = tt.memory.ExpiresAt
			_ = tt.memory.Metadata
		})
	}
}

// TestDistillationConfig tests DistillationConfig struct.
func TestDistillationConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  DistillationConfig
	}{
		{
			name: "full config",
			cfg: DistillationConfig{
				MinImportance:              0.5,
				ConflictThreshold:          0.8,
				MaxMemoriesPerDistillation: 100,
				MaxSolutionsPerTenant:      1000,
				EnableCodeFilter:           true,
				EnableStacktraceFilter:     true,
				EnableLogFilter:            true,
				EnableMarkdownTableFilter:  true,
				EnableCrossTurnExtraction:  true,
				EnableLengthBonus:          true,
				LengthThreshold:            100,
				LengthBonus:                0.1,
				TopNBeforeConflict:         true,
				ConflictSearchLimit:        50,
				PrecisionOverRecall:        true,
			},
		},
		{
			name: "minimal config",
			cfg: DistillationConfig{
				MinImportance: 0.5,
			},
		},
		{
			name: "config with zero values",
			cfg: DistillationConfig{
				MinImportance:              0.0,
				ConflictThreshold:          0.0,
				MaxMemoriesPerDistillation: 0,
				MaxSolutionsPerTenant:      0,
				LengthThreshold:            0,
				LengthBonus:                0.0,
				ConflictSearchLimit:        0,
			},
		},
		{
			name: "config with all filters disabled",
			cfg: DistillationConfig{
				EnableCodeFilter:          false,
				EnableStacktraceFilter:    false,
				EnableLogFilter:           false,
				EnableMarkdownTableFilter: false,
				EnableCrossTurnExtraction: false,
				EnableLengthBonus:         false,
				TopNBeforeConflict:        false,
				PrecisionOverRecall:       false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = tt.cfg.MinImportance
			_ = tt.cfg.ConflictThreshold
			_ = tt.cfg.MaxMemoriesPerDistillation
			_ = tt.cfg.MaxSolutionsPerTenant
			_ = tt.cfg.EnableCodeFilter
			_ = tt.cfg.EnableStacktraceFilter
			_ = tt.cfg.EnableLogFilter
			_ = tt.cfg.EnableMarkdownTableFilter
			_ = tt.cfg.EnableCrossTurnExtraction
			_ = tt.cfg.EnableLengthBonus
			_ = tt.cfg.LengthThreshold
			_ = tt.cfg.LengthBonus
			_ = tt.cfg.TopNBeforeConflict
			_ = tt.cfg.ConflictSearchLimit
			_ = tt.cfg.PrecisionOverRecall
		})
	}
}

// TestDistillationMetrics tests DistillationMetrics struct.
func TestDistillationMetrics(t *testing.T) {
	tests := []struct {
		name    string
		metrics DistillationMetrics
	}{
		{
			name: "full metrics",
			metrics: DistillationMetrics{
				AttemptTotal:     100,
				SuccessTotal:     95,
				FilteredNoise:    3,
				FilteredSecurity: 1,
				ConflictResolved: 2,
				MemoriesCreated:  50,
			},
		},
		{
			name: "zero metrics",
			metrics: DistillationMetrics{
				AttemptTotal:     0,
				SuccessTotal:     0,
				FilteredNoise:    0,
				FilteredSecurity: 0,
				ConflictResolved: 0,
				MemoriesCreated:  0,
			},
		},
		{
			name: "partial metrics",
			metrics: DistillationMetrics{
				AttemptTotal: 10,
				SuccessTotal: 5,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = tt.metrics.AttemptTotal
			_ = tt.metrics.SuccessTotal
			_ = tt.metrics.FilteredNoise
			_ = tt.metrics.FilteredSecurity
			_ = tt.metrics.ConflictResolved
			_ = tt.metrics.MemoriesCreated
		})
	}
}

// TestConversationMessage tests ConversationMessage struct.
func TestConversationMessage(t *testing.T) {
	tests := []struct {
		name    string
		message ConversationMessage
	}{
		{
			name: "user message",
			message: ConversationMessage{
				Role:    "user",
				Content: "Hello",
			},
		},
		{
			name: "assistant message",
			message: ConversationMessage{
				Role:    "assistant",
				Content: "Hi there!",
			},
		},
		{
			name: "system message",
			message: ConversationMessage{
				Role:    "system",
				Content: "You are helpful",
			},
		},
		{
			name: "empty message",
			message: ConversationMessage{
				Role:    "",
				Content: "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = tt.message.Role
			_ = tt.message.Content
		})
	}
}

// TestDistillationService tests that DistillationService interface is properly defined.
func TestDistillationService(t *testing.T) {
	var _ DistillationService = (*mockDistillationService)(nil)
}

// mockDistillationService is a mock implementation of DistillationService for testing.
type mockDistillationService struct{}

func (m *mockDistillationService) DistillConversation(ctx context.Context, conversationID string, messages []ConversationMessage, tenantID, userID string) ([]*DistilledMemory, error) {
	return nil, nil
}

func (m *mockDistillationService) GetMetrics() *DistillationMetrics {
	return nil
}

func (m *mockDistillationService) ResetMetrics() {}

func (m *mockDistillationService) GetConfig() *DistillationConfig {
	return nil
}

func (m *mockDistillationService) UpdateConfig(config *DistillationConfig) error {
	return nil
}

func (m *mockDistillationService) UpdateMemory(ctx context.Context, memoryID string, updates map[string]interface{}) error {
	return nil
}

func (m *mockDistillationService) DeleteMemory(ctx context.Context, memoryID string) error {
	return nil
}

func (m *mockDistillationService) SearchMemories(ctx context.Context, query string, tenantID string, limit int) ([]*DistilledMemory, error) {
	return nil, nil
}

// TestExperienceRepository tests that ExperienceRepository interface is properly defined.
func TestExperienceRepository(t *testing.T) {
	var _ ExperienceRepository = (*mockExperienceRepository)(nil)
}

// mockExperienceRepository is a mock implementation of ExperienceRepository for testing.
type mockExperienceRepository struct{}

func (m *mockExperienceRepository) SearchByVector(ctx context.Context, vector []float64, tenantID string, limit int) ([]*Experience, error) {
	return nil, nil
}

func (m *mockExperienceRepository) GetByMemoryType(ctx context.Context, tenantID string, memoryType MemoryType) ([]*Experience, error) {
	return nil, nil
}

func (m *mockExperienceRepository) Update(ctx context.Context, experience *Experience) error {
	return nil
}

func (m *mockExperienceRepository) Delete(ctx context.Context, id string) error {
	return nil
}

func (m *mockExperienceRepository) Create(ctx context.Context, experience *Experience) error {
	return nil
}

func (m *mockExperienceRepository) GetInternalRepository() interface{} {
	return nil
}
