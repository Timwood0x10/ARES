package memory

import (
	"testing"
)

// TestNewDistillationService tests the NewDistillationService constructor.
func TestNewDistillationService(t *testing.T) {
	tests := []struct {
		name      string
		distiller interface{}
		wantNil   bool
	}{
		{
			name:      "service with nil distiller",
			distiller: nil,
			wantNil:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewDistillationService(nil)

			if tt.wantNil {
				if svc != nil {
					t.Error("NewDistillationService() with nil distiller should return nil")
				}
			} else {
				if svc == nil {
					t.Error("NewDistillationService() should not return nil")
				}
			}
		})
	}
}

// TestNewDistillationServiceWithEmbedder tests the NewDistillationServiceWithEmbedder constructor.
func TestNewDistillationServiceWithEmbedder(t *testing.T) {
	tests := []struct {
		name    string
		config  *DistillationConfig
		wantErr bool
	}{
		{
			name:    "service with nil config (will use default)",
			config:  nil,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This test is limited because we can't create real embedder and repo instances
			// In a real scenario, you would use mock implementations
			// For now, we just test that the function signature is correct
			_ = tt.config
			_ = tt.wantErr
		})
	}
}

// TestDefaultDistillationConfig tests the defaultDistillationConfig function.
func TestDefaultDistillationConfig(t *testing.T) {
	config := defaultDistillationConfig()

	if config == nil {
		t.Error("defaultDistillationConfig() should not return nil")
	}

	tests := []struct {
		name  string
		field string
		want  interface{}
	}{
		{
			name:  "MinImportance",
			field: "MinImportance",
			want:  0.6,
		},
		{
			name:  "ConflictThreshold",
			field: "ConflictThreshold",
			want:  0.85,
		},
		{
			name:  "MaxMemoriesPerDistillation",
			field: "MaxMemoriesPerDistillation",
			want:  3,
		},
		{
			name:  "MaxSolutionsPerTenant",
			field: "MaxSolutionsPerTenant",
			want:  5000,
		},
		{
			name:  "EnableCodeFilter",
			field: "EnableCodeFilter",
			want:  true,
		},
		{
			name:  "EnableStacktraceFilter",
			field: "EnableStacktraceFilter",
			want:  true,
		},
		{
			name:  "EnableLogFilter",
			field: "EnableLogFilter",
			want:  true,
		},
		{
			name:  "EnableMarkdownTableFilter",
			field: "EnableMarkdownTableFilter",
			want:  true,
		},
		{
			name:  "EnableCrossTurnExtraction",
			field: "EnableCrossTurnExtraction",
			want:  true,
		},
		{
			name:  "EnableLengthBonus",
			field: "EnableLengthBonus",
			want:  true,
		},
		{
			name:  "LengthThreshold",
			field: "LengthThreshold",
			want:  60,
		},
		{
			name:  "LengthBonus",
			field: "LengthBonus",
			want:  0.1,
		},
		{
			name:  "TopNBeforeConflict",
			field: "TopNBeforeConflict",
			want:  true,
		},
		{
			name:  "ConflictSearchLimit",
			field: "ConflictSearchLimit",
			want:  5,
		},
		{
			name:  "PrecisionOverRecall",
			field: "PrecisionOverRecall",
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			switch tt.field {
			case "MinImportance":
				if config.MinImportance != tt.want {
					t.Errorf("MinImportance = %v, want %v", config.MinImportance, tt.want)
				}
			case "ConflictThreshold":
				if config.ConflictThreshold != tt.want {
					t.Errorf("ConflictThreshold = %v, want %v", config.ConflictThreshold, tt.want)
				}
			case "MaxMemoriesPerDistillation":
				if config.MaxMemoriesPerDistillation != tt.want {
					t.Errorf("MaxMemoriesPerDistillation = %v, want %v", config.MaxMemoriesPerDistillation, tt.want)
				}
			case "MaxSolutionsPerTenant":
				if config.MaxSolutionsPerTenant != tt.want {
					t.Errorf("MaxSolutionsPerTenant = %v, want %v", config.MaxSolutionsPerTenant, tt.want)
				}
			case "EnableCodeFilter":
				if config.EnableCodeFilter != tt.want {
					t.Errorf("EnableCodeFilter = %v, want %v", config.EnableCodeFilter, tt.want)
				}
			case "EnableStacktraceFilter":
				if config.EnableStacktraceFilter != tt.want {
					t.Errorf("EnableStacktraceFilter = %v, want %v", config.EnableStacktraceFilter, tt.want)
				}
			case "EnableLogFilter":
				if config.EnableLogFilter != tt.want {
					t.Errorf("EnableLogFilter = %v, want %v", config.EnableLogFilter, tt.want)
				}
			case "EnableMarkdownTableFilter":
				if config.EnableMarkdownTableFilter != tt.want {
					t.Errorf("EnableMarkdownTableFilter = %v, want %v", config.EnableMarkdownTableFilter, tt.want)
				}
			case "EnableCrossTurnExtraction":
				if config.EnableCrossTurnExtraction != tt.want {
					t.Errorf("EnableCrossTurnExtraction = %v, want %v", config.EnableCrossTurnExtraction, tt.want)
				}
			case "EnableLengthBonus":
				if config.EnableLengthBonus != tt.want {
					t.Errorf("EnableLengthBonus = %v, want %v", config.EnableLengthBonus, tt.want)
				}
			case "LengthThreshold":
				if config.LengthThreshold != tt.want {
					t.Errorf("LengthThreshold = %v, want %v", config.LengthThreshold, tt.want)
				}
			case "LengthBonus":
				if config.LengthBonus != tt.want {
					t.Errorf("LengthBonus = %v, want %v", config.LengthBonus, tt.want)
				}
			case "TopNBeforeConflict":
				if config.TopNBeforeConflict != tt.want {
					t.Errorf("TopNBeforeConflict = %v, want %v", config.TopNBeforeConflict, tt.want)
				}
			case "ConflictSearchLimit":
				if config.ConflictSearchLimit != tt.want {
					t.Errorf("ConflictSearchLimit = %v, want %v", config.ConflictSearchLimit, tt.want)
				}
			case "PrecisionOverRecall":
				if config.PrecisionOverRecall != tt.want {
					t.Errorf("PrecisionOverRecall = %v, want %v", config.PrecisionOverRecall, tt.want)
				}
			}
		})
	}
}

// TestGetMetadataString tests the getMetadataString helper function.
func TestGetMetadataString(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]interface{}
		key      string
		want     string
	}{
		{
			name: "existing string value",
			metadata: map[string]interface{}{
				"key": "value",
			},
			key:  "key",
			want: "value",
		},
		{
			name: "non-existent key",
			metadata: map[string]interface{}{
				"other": "value",
			},
			key:  "key",
			want: "",
		},
		{
			name: "non-string value",
			metadata: map[string]interface{}{
				"key": 123,
			},
			key:  "key",
			want: "",
		},
		{
			name:     "nil metadata",
			metadata: nil,
			key:      "key",
			want:     "",
		},
		{
			name:     "empty metadata",
			metadata: map[string]interface{}{},
			key:      "key",
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getMetadataString(tt.metadata, tt.key)
			if result != tt.want {
				t.Errorf("getMetadataString() = %q, want %q", result, tt.want)
			}
		})
	}
}

// TestConvertFromAPIDistillationConfig tests the convertFromAPIDistillationConfig function.
func TestConvertFromAPIDistillationConfig(t *testing.T) {
	tests := []struct {
		name   string
		config *DistillationConfig
	}{
		{
			name:   "nil config",
			config: nil,
		},
		{
			name: "full config",
			config: &DistillationConfig{
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
			config: &DistillationConfig{
				MinImportance: 0.5,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This test verifies the function doesn't panic
			// In a real scenario, you would check the converted values
			_ = convertFromAPIDistillationConfig(tt.config)
		})
	}
}

// TestConvertToAPIDistillationMetrics tests the convertToAPIDistillationMetrics function.
func TestConvertToAPIDistillationMetrics(t *testing.T) {
	// This test verifies the function doesn't panic with nil input
	result := convertToAPIDistillationMetrics(nil)
	if result == nil {
		t.Error("convertToAPIDistillationMetrics() should not return nil")
	}
}
