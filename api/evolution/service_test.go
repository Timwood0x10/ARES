package evolution

import (
	"context"
	"errors"
	"testing"
	"time"

	"goagentx/internal/evolution"
)

// testBaseStrategy returns a valid base strategy for testing.
func testBaseStrategy() *Strategy {
	return &Strategy{
		ID:             "base-strategy-001",
		Version:        1,
		Params:         map[string]any{"temperature": 0.7, "top_k": 40},
		PromptTemplate: "You are a helpful assistant.",
		MutationType:   "",
		Score:          0.5,
		CreatedAt:      time.Now(),
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg == nil {
		t.Fatal("DefaultConfig() returned nil")
	}
	if cfg.PopulationSize != 20 {
		t.Errorf("PopulationSize = %d, want 20", cfg.PopulationSize)
	}
	if cfg.EliteCount != 2 {
		t.Errorf("EliteCount = %d, want 2", cfg.EliteCount)
	}
	if cfg.SurvivalRate != 0.6 {
		t.Errorf("SurvivalRate = %f, want 0.6", cfg.SurvivalRate)
	}
	if cfg.MutationRate != 0.2 {
		t.Errorf("MutationRate = %f, want 0.2", cfg.MutationRate)
	}
	if cfg.Generations != 15 {
		t.Errorf("Generations = %d, want 15", cfg.Generations)
	}
	if !cfg.EnableWiredMode {
		t.Error("EnableWiredMode should be true by default")
	}
}

func TestNewService_WithDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BaseStrategy = testBaseStrategy()

	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if svc == nil {
		t.Fatal("NewService() returned nil service")
	}
	defer svc.Shutdown()
}

func TestNewService_WithNilConfig_ReturnsError(t *testing.T) {
	svc, err := NewService(nil)

	if err == nil {
		t.Error("expected error for nil config, got nil")
		defer svc.Shutdown()
	}
	if !errors.Is(err, ErrNilConfig) {
		t.Errorf("error = %v, want ErrNilConfig", err)
	}
	if svc != nil {
		t.Error("expected nil service for nil config")
	}
}

func TestNewService_WithNilBaseStrategy_ReturnsError(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BaseStrategy = nil

	svc, err := NewService(cfg)

	if err == nil {
		t.Error("expected error for nil base strategy, got nil")
		defer svc.Shutdown()
	}
	if !errors.Is(err, ErrNilBaseStrategy) {
		t.Errorf("error = %v, want ErrNilBaseStrategy", err)
	}
}

func TestNewService_WithInvalidSurvivalRate_ReturnsError(t *testing.T) {
	tests := []struct {
		name    string
		rate    float64
		wantErr error
	}{
		{"negative rate", -0.5, ErrInvalidRate},
		{"rate > 1", 1.5, ErrInvalidRate},
		{"rate = -0.01", -0.01, ErrInvalidRate},
		{"rate = 1.01", 1.01, ErrInvalidRate},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.BaseStrategy = testBaseStrategy()
			cfg.SurvivalRate = tt.rate

			svc, err := NewService(cfg)
			if err == nil {
				t.Error("expected error, got nil")
				defer svc.Shutdown()
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewService_WithInvalidMutationRate_ReturnsError(t *testing.T) {
	tests := []struct {
		name    string
		rate    float64
		wantErr error
	}{
		{"negative mutation rate", -0.3, ErrInvalidRate},
		{"mutation rate > 1", 2.0, ErrInvalidRate},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.BaseStrategy = testBaseStrategy()
			cfg.MutationRate = tt.rate

			svc, err := NewService(cfg)
			if err == nil {
				t.Error("expected error, got nil")
				defer svc.Shutdown()
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewService_WithInvalidMinWinRate_ReturnsError(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BaseStrategy = testBaseStrategy()
	cfg.MinWinRate = 1.5

	svc, err := NewService(cfg)
	if err == nil {
		t.Error("expected error, got nil")
		defer svc.Shutdown()
	}
	if !errors.Is(err, ErrInvalidRate) {
		t.Errorf("error = %v, want ErrInvalidRate", err)
	}
}

func TestBestStrategy_BeforeEvolve_ReturnsStrategy(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BaseStrategy = testBaseStrategy()

	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer svc.Shutdown()

	best, err := svc.BestStrategy()
	if err != nil {
		t.Fatalf("BestStrategy() error = %v", err)
	}
	if best == nil {
		t.Fatal("BestStrategy() returned nil")
	}
	if best.ID == "" {
		t.Error("best strategy ID should not be empty")
	}
}

func TestStats_AfterCreation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BaseStrategy = testBaseStrategy()

	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer svc.Shutdown()

	stats, err := svc.Stats()
	if err != nil {
		t.Fatalf("Stats() error = %v", err)
	}
	if stats == nil {
		t.Fatal("Stats() returned nil")
	}
	if stats.Size != cfg.PopulationSize {
		t.Errorf("Stats().Size = %d, want %d", stats.Size, cfg.PopulationSize)
	}
	if stats.Generation != 0 {
		t.Errorf("Stats().Generation = %d, want 0 (initial)", stats.Generation)
	}
}

func TestLineages_EmptyInitially(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BaseStrategy = testBaseStrategy()

	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer svc.Shutdown()

	lineages, err := svc.Lineages()
	if err != nil {
		t.Fatalf("Lineages() error = %v", err)
	}
	if lineages == nil {
		t.Fatal("Lineages() returned nil (want empty slice)")
	}
	if len(lineages) != 0 {
		t.Errorf("len(Lineages()) = %d, want 0", len(lineages))
	}
}

func TestEvolve_SingleGeneration(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BaseStrategy = testBaseStrategy()
	cfg.Seed = 42 // Deterministic seed for reproducibility

	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer svc.Shutdown()

	result, err := svc.Evolve(context.Background(), 1)
	if err != nil {
		t.Fatalf("Evolve() error = %v", err)
	}
	if result == nil {
		t.Fatal("Evolve() returned nil result")
	}
	if result.TotalGens != 1 {
		t.Errorf("TotalGens = %d, want 1", result.TotalGens)
	}
	if len(result.Stats) != 1 {
		t.Errorf("len(Stats) = %d, want 1", len(result.Stats))
	}
	if result.BestStrategy == nil {
		t.Error("BestStrategy in result should not be nil after evolution")
	}
}

func TestEvolve_MultipleGenerations(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BaseStrategy = testBaseStrategy()
	cfg.Seed = 42
	cfg.PopulationSize = 10
	cfg.Generations = 5

	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer svc.Shutdown()

	result, err := svc.Evolve(context.Background(), 3)
	if err != nil {
		t.Fatalf("Evolve() error = %v", err)
	}
	if result.TotalGens != 3 {
		t.Errorf("TotalGens = %d, want 3", result.TotalGens)
	}
	if len(result.Stats) != 3 {
		t.Errorf("len(Stats) = %d, want 3", len(result.Stats))
	}
}

func TestEvolve_ContextCancellation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BaseStrategy = testBaseStrategy()
	cfg.Seed = 42

	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	defer svc.Shutdown()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	result, err := svc.Evolve(ctx, 100)
	if err == nil {
		t.Error("expected context cancellation error or partial result")
	}
	if result == nil {
		t.Error("result should not be nil even on cancellation (partial results)")
	}
}

func TestShutdown_Idempotent(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BaseStrategy = testBaseStrategy()

	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	// Calling Shutdown multiple times should not panic
	svc.Shutdown()
	svc.Shutdown()
	svc.Shutdown()
}

func TestNonWiredMode(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BaseStrategy = testBaseStrategy()
	cfg.EnableWiredMode = false
	cfg.Seed = 42

	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService() non-wired error = %v", err)
	}
	defer svc.Shutdown()

	best, err := svc.BestStrategy()
	if err != nil {
		t.Fatalf("BestStrategy() non-wired error = %v", err)
	}
	if best == nil {
		t.Fatal("BestStrategy() non-wired returned nil")
	}
}

func TestToAPIStrategy_NilInput(t *testing.T) {
	result := toAPIStrategy(nil)
	if result != nil {
		t.Error("toAPIStrategy(nil) should return nil")
	}
}

func TestToAPILineage_Conversion(t *testing.T) {
	internal := evolution.StrategyLineage{
		ParentID:         "parent-001",
		ChildID:          "child-002",
		MutationType:     "parameter",
		WinRate:          0.8,
		ScoreImprovement: 0.15,
		Timestamp:        1234567890,
	}

	api := toAPILineage(internal)

	if api.ParentID != "parent-001" {
		t.Errorf("ParentID = %s, want parent-001", api.ParentID)
	}
	if api.ChildID != "child-002" {
		t.Errorf("ChildID = %s, want child-002", api.ChildID)
	}
	if api.MutationType != "parameter" {
		t.Errorf("MutationType = %s, want parameter", api.MutationType)
	}
	if api.WinRate != 0.8 {
		t.Errorf("WinRate = %f, want 0.8", api.WinRate)
	}
	if api.ScoreDelta != 0.15 {
		t.Errorf("ScoreDelta = %f, want 0.15", api.ScoreDelta)
	}
	if api.Timestamp != 1234567890 {
		t.Errorf("Timestamp = %d, want 1234567890", api.Timestamp)
	}
}
