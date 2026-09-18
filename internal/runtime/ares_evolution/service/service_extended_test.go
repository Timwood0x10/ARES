package evolution

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	evolutionPkg "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// ── Mock implementations ────────────────────────────────────────────────────

// mockScorer is a deterministic test scorer.
type mockScorer struct {
	score float64
	calls atomic.Int64
}

func (m *mockScorer) Score(_ *Strategy) float64 {
	m.calls.Add(1)
	return m.score
}

// mockBatchScorer implements BatchScorer for testing.
type mockBatchScorer struct {
	scores []float64
	calls  atomic.Int64
}

func (m *mockBatchScorer) BatchScore(strategies []*Strategy) []float64 {
	m.calls.Add(1)
	result := make([]float64, len(strategies))
	for i := range strategies {
		if i < len(m.scores) {
			result[i] = m.scores[i]
		} else {
			result[i] = 0.5
		}
	}
	return result
}

// mockEvidenceAggregator implements EvidenceAggregator.
type mockEvidenceAggregator struct {
	evidence Evidence
	err      error
	calls    atomic.Int64
}

func (m *mockEvidenceAggregator) Aggregate(_ context.Context, _ string) (Evidence, error) {
	m.calls.Add(1)
	return m.evidence, m.err
}

// mockPromotionLogic implements PromotionLogic.
type mockPromotionLogic struct {
	state  string
	reason string
	err    error
	calls  atomic.Int64
}

func (m *mockPromotionLogic) Evaluate(_ context.Context, _ string, _ Evidence) (string, string, error) {
	m.calls.Add(1)
	return m.state, m.reason, m.err
}

// mockMemoryProvider implements MemoryExperienceProvider.
type mockMemoryProvider struct {
	count      int
	confidence float64
	err        error
}

func (m *mockMemoryProvider) FindSimilar(_ context.Context, _ string, _ int) (int, float64, error) {
	return m.count, m.confidence, m.err
}

// mockGuidanceProvider implements GuidanceProvider.
type mockGuidanceProvider struct {
	hints []EvolutionHint
	err   error
}

func (m *mockGuidanceProvider) HintsForTask(_ context.Context, _ string, _ int) ([]EvolutionHint, error) {
	return m.hints, m.err
}

func (m *mockGuidanceProvider) RecordStrategyOutcome(_ context.Context, _ StrategyOutcome) error {
	return m.err
}

// mockLLMClient implements LLMClient.
type mockLLMClient struct {
	response string
	err      error
}

func (m *mockLLMClient) Generate(_ context.Context, _ string) (string, error) {
	return m.response, m.err
}

// baseStrategy returns a minimal valid Strategy for NewService.
func baseStrategy() *Strategy {
	return &Strategy{
		ID:     "base",
		Params: map[string]any{"temperature": 0.5},
	}
}

// validConfig returns a minimal valid SystemConfig for NewService.
func validConfig() *SystemConfig {
	return &SystemConfig{
		BaseStrategy:      baseStrategy(),
		PopulationSize:    5,
		EliteCount:        1,
		SurvivalRate:      0.6,
		MutationRate:      0.2,
		MinMutationRate:   0.05,
		MaxMutationRate:   0.5,
		BreedingPoolRatio: 0.6,
		Generations:       2,
		Seed:              42,
		EnableWiredMode:   false,
	}
}

// ── NewService validation ───────────────────────────────────────────────────

func TestNewServiceNilConfig(t *testing.T) {
	_, err := NewService(nil)
	if !errors.Is(err, ErrNilConfig) {
		t.Errorf("err = %v, want ErrNilConfig", err)
	}
}

func TestNewServiceNilBaseStrategy(t *testing.T) {
	cfg := validConfig()
	cfg.BaseStrategy = nil
	_, err := NewService(cfg)
	if !errors.Is(err, ErrNilBaseStrategy) {
		t.Errorf("err = %v, want ErrNilBaseStrategy", err)
	}
}

func TestNewServiceInvalidRates(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SystemConfig)
	}{
		{"survival_rate_negative", func(c *SystemConfig) { c.SurvivalRate = -0.1 }},
		{"survival_rate_above_one", func(c *SystemConfig) { c.SurvivalRate = 1.1 }},
		{"mutation_rate_negative", func(c *SystemConfig) { c.MutationRate = -0.1 }},
		{"mutation_rate_above_one", func(c *SystemConfig) { c.MutationRate = 1.1 }},
		{"min_mutation_rate_negative", func(c *SystemConfig) { c.MinMutationRate = -0.1 }},
		{"max_mutation_rate_above_one", func(c *SystemConfig) { c.MaxMutationRate = 1.1 }},
		{"min_gt_max_mutation", func(c *SystemConfig) { c.MinMutationRate = 0.8; c.MaxMutationRate = 0.2 }},
		{"breeding_pool_ratio_negative", func(c *SystemConfig) { c.BreedingPoolRatio = -0.1 }},
		{"breeding_pool_ratio_above_one", func(c *SystemConfig) { c.BreedingPoolRatio = 1.1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(cfg)
			_, err := NewService(cfg)
			if err == nil {
				t.Error("expected error for invalid config")
			}
			// min_gt_max_mutation uses a plain error, not ErrInvalidRate.
			if tt.name != "min_gt_max_mutation" && !errors.Is(err, ErrInvalidRate) {
				t.Errorf("err = %v, want ErrInvalidRate", err)
			}
		})
	}
}

func TestNewServiceNonWiredSuccess(t *testing.T) {
	cfg := validConfig()
	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if svc == nil {
		t.Fatal("NewService returned nil service")
	}
	if svc.population == nil {
		t.Error("population should not be nil in non-wired mode")
	}
	if svc.mutator == nil {
		t.Error("mutator should not be nil in non-wired mode")
	}
	if svc.crosser == nil {
		t.Error("crosser should not be nil in non-wired mode")
	}
}

func TestNewServiceWiredSuccess(t *testing.T) {
	cfg := validConfig()
	cfg.EnableWiredMode = true
	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if svc.wiredSystem == nil {
		t.Error("wiredSystem should not be nil in wired mode")
	}
}

// ── Service lifecycle ───────────────────────────────────────────────────────

func TestServiceReportPath(t *testing.T) {
	cfg := validConfig()
	cfg.ReportPath = "/tmp/test_report.json"
	svc, svcErr := NewService(cfg)
	if svcErr != nil {
		t.Fatalf("NewService: %v", svcErr)
	}
	if svc.ReportPath() != "/tmp/test_report.json" {
		t.Errorf("ReportPath() = %q, want /tmp/test_report.json", svc.ReportPath())
	}

	// Nil config.
	emptySvc := &Service{}
	if emptySvc.ReportPath() != "" {
		t.Errorf("ReportPath() for nil config = %q, want empty", emptySvc.ReportPath())
	}
}

func TestServiceBestStrategyNotInitialized(t *testing.T) {
	svc := &Service{}
	_, err := svc.BestStrategy()
	if !errors.Is(err, ErrNotInitialized) {
		t.Errorf("err = %v, want ErrNotInitialized", err)
	}
}

func TestServiceStatsNotInitialized(t *testing.T) {
	svc := &Service{}
	_, err := svc.Stats()
	if !errors.Is(err, ErrNotInitialized) {
		t.Errorf("err = %v, want ErrNotInitialized", err)
	}
}

func TestServiceLineagesNotInitialized(t *testing.T) {
	svc := &Service{}
	lineages, err := svc.Lineages()
	if err != nil {
		t.Fatalf("Lineages: %v", err)
	}
	if len(lineages) != 0 {
		t.Errorf("Lineages len = %d, want 0 for uninitialized service", len(lineages))
	}
}

func TestServiceEvolveNotInitialized(t *testing.T) {
	svc := &Service{config: validConfig()}
	_, err := svc.Evolve(context.Background(), 1)
	if !errors.Is(err, ErrNotInitialized) {
		t.Errorf("err = %v, want ErrNotInitialized", err)
	}
}

func TestServiceShutdownIdempotent(t *testing.T) {
	cfg := validConfig()
	svc, svcErr := NewService(cfg)
	if svcErr != nil {
		t.Fatalf("NewService: %v", svcErr)
	}
	svc.Shutdown()
	svc.Shutdown() // second call should be a no-op
}

func TestServiceSaveBestStrategyNotInitialized(t *testing.T) {
	svc := &Service{}
	err := svc.SaveBestStrategy("/tmp/test.json")
	if err == nil {
		t.Error("expected error for uninitialized service")
	}
}

// ── Evolve with non-wired mode ──────────────────────────────────────────────

func TestServiceEvolveNonWired(t *testing.T) {
	cfg := validConfig()
	cfg.Generations = 2
	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	result, err := svc.Evolve(context.Background(), 2)
	if err != nil {
		t.Fatalf("Evolve: %v", err)
	}
	if result == nil {
		t.Fatal("Evolve returned nil result")
	}
	if result.TotalGens != 2 {
		t.Errorf("TotalGens = %d, want 2", result.TotalGens)
	}
	if len(result.Stats) != 2 {
		t.Errorf("Stats len = %d, want 2", len(result.Stats))
	}
}

func TestServiceEvolveContextCancelled(t *testing.T) {
	cfg := validConfig()
	cfg.Generations = 100
	svc, svcErr := NewService(cfg)
	if svcErr != nil {
		t.Fatalf("NewService: %v", svcErr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	_, err := svc.Evolve(ctx, 10)
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

func TestServiceEvolveZeroGenerationsUsesDefault(t *testing.T) {
	cfg := validConfig()
	cfg.Generations = 2
	svc, svcErr := NewService(cfg)
	if svcErr != nil {
		t.Fatalf("NewService: %v", svcErr)
	}

	result, err := svc.Evolve(context.Background(), 0)
	if err != nil {
		t.Fatalf("Evolve(0): %v", err)
	}
	if result.TotalGens != 2 {
		t.Errorf("TotalGens = %d, want 2 (from config.Generations)", result.TotalGens)
	}
}

func TestServiceEvolveWithCustomScorer(t *testing.T) {
	cfg := validConfig()
	cfg.Generations = 2
	cfg.Scorer = func(s *Strategy) float64 {
		return 50.0 // flat score
	}
	svc, svcErr := NewService(cfg)
	if svcErr != nil {
		t.Fatalf("NewService: %v", svcErr)
	}

	result, err := svc.Evolve(context.Background(), 2)
	if err != nil {
		t.Fatalf("Evolve: %v", err)
	}
	if result.TotalGens != 2 {
		t.Errorf("TotalGens = %d, want 2", result.TotalGens)
	}
}

func TestServiceEvolveWithBatchScorer(t *testing.T) {
	cfg := validConfig()
	cfg.Generations = 2
	cfg.BatchScorer = &mockBatchScorer{scores: []float64{80, 70, 60, 50, 40}}
	svc, svcErr := NewService(cfg)
	if svcErr != nil {
		t.Fatalf("NewService: %v", svcErr)
	}

	result, err := svc.Evolve(context.Background(), 2)
	if err != nil {
		t.Fatalf("Evolve: %v", err)
	}
	if result.TotalGens != 2 {
		t.Errorf("TotalGens = %d, want 2", result.TotalGens)
	}
}

// ── BestStrategy / Stats / Lineages after evolve ────────────────────────────

func TestServiceBestStrategyAfterEvolve(t *testing.T) {
	cfg := validConfig()
	cfg.Generations = 1
	svc, svcErr := NewService(cfg)
	if svcErr != nil {
		t.Fatalf("NewService: %v", svcErr)
	}

	_, err := svc.Evolve(context.Background(), 1)
	if err != nil {
		t.Fatalf("Evolve: %v", err)
	}

	best, err := svc.BestStrategy()
	if err != nil {
		t.Fatalf("BestStrategy: %v", err)
	}
	if best == nil {
		t.Error("BestStrategy returned nil after evolve")
	}
}

func TestServiceStatsAfterEvolve(t *testing.T) {
	cfg := validConfig()
	cfg.Generations = 1
	svc, svcErr := NewService(cfg)
	if svcErr != nil {
		t.Fatalf("NewService: %v", svcErr)
	}

	_, err := svc.Evolve(context.Background(), 1)
	if err != nil {
		t.Fatalf("Evolve: %v", err)
	}

	stats, err := svc.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats == nil {
		t.Error("Stats returned nil after evolve")
	}
}

// ── SaveBestStrategy / LoadBestStrategy ─────────────────────────────────────

func TestServiceSaveAndLoadBestStrategy(t *testing.T) {
	cfg := validConfig()
	cfg.Generations = 1
	svc, svcErr := NewService(cfg)
	if svcErr != nil {
		t.Fatalf("NewService: %v", svcErr)
	}

	_, err := svc.Evolve(context.Background(), 1)
	if err != nil {
		t.Fatalf("Evolve: %v", err)
	}

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "best_strategy.json")

	if err := svc.SaveBestStrategy(path); err != nil {
		t.Fatalf("SaveBestStrategy: %v", err)
	}

	// Verify file exists.
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("saved strategy file does not exist")
	}

	// Load and verify.
	loaded, err := LoadBestStrategy(path)
	if err != nil {
		t.Fatalf("LoadBestStrategy: %v", err)
	}
	if loaded == nil {
		t.Error("LoadBestStrategy returned nil")
	}
}

func TestLoadBestStrategyInvalidPath(t *testing.T) {
	_, err := LoadBestStrategy("/nonexistent/path/strategy.json")
	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}

func TestLoadBestStrategyInvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "invalid.json")
	_ = os.WriteFile(path, []byte("{invalid json"), 0600)

	_, err := LoadBestStrategy(path)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// ── scoreAgents paths ───────────────────────────────────────────────────────

func TestServiceScoreAgentsDeterministic(t *testing.T) {
	cfg := validConfig()
	cfg.Scorer = nil // use deterministic scorer
	svc, svcErr := NewService(cfg)
	if svcErr != nil {
		t.Fatalf("NewService: %v", svcErr)
	}

	svc.initScores(context.Background())
	// Should not panic; deterministic scorer assigns scores.
}

func TestServiceScoreAgentsCustomScorer(t *testing.T) {
	cfg := validConfig()
	mock := &mockScorer{score: 42.0}
	cfg.Scorer = mock.Score
	svc, svcErr := NewService(cfg)
	if svcErr != nil {
		t.Fatalf("NewService: %v", svcErr)
	}

	svc.initScores(context.Background())
	if mock.calls.Load() == 0 {
		t.Error("custom scorer was never called")
	}
}

func TestServiceScoreAgentsBatchScorer(t *testing.T) {
	cfg := validConfig()
	mock := &mockBatchScorer{scores: []float64{90, 80, 70, 60, 50}}
	cfg.BatchScorer = mock
	cfg.Scorer = func(_ *Strategy) float64 { return 1 } // fallback, should not be used
	svc, svcErr := NewService(cfg)
	if svcErr != nil {
		t.Fatalf("NewService: %v", svcErr)
	}

	svc.initScores(context.Background())
	if mock.calls.Load() == 0 {
		t.Error("batch scorer was never called")
	}
}

// ── resolveEvidenceAggregator / resolvePromotionLogic ───────────────────────

func TestResolveEvidenceAggregatorNil(t *testing.T) {
	if resolveEvidenceAggregator(nil) != nil {
		t.Error("resolveEvidenceAggregator(nil) should return nil")
	}
}

func TestResolveEvidenceAggregatorLocalInterface(t *testing.T) {
	mock := &mockEvidenceAggregator{
		evidence: Evidence{StrategyID: "s1", SuccessRate: 0.9, SampleCount: 100},
	}
	fn := resolveEvidenceAggregator(mock)
	if fn == nil {
		t.Fatal("resolveEvidenceAggregator returned nil for local interface")
	}
	ev, err := fn(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if ev.StrategyID != "s1" {
		t.Errorf("StrategyID = %q, want s1", ev.StrategyID)
	}
	if ev.SuccessRate != 0.9 {
		t.Errorf("SuccessRate = %v, want 0.9", ev.SuccessRate)
	}
}

func TestResolveEvidenceAggregatorUnrecognizedType(t *testing.T) {
	if resolveEvidenceAggregator("not_an_aggregator") != nil {
		t.Error("resolveEvidenceAggregator should return nil for unrecognized type")
	}
}

func TestResolvePromotionLogicNil(t *testing.T) {
	if resolvePromotionLogic(nil) != nil {
		t.Error("resolvePromotionLogic(nil) should return nil")
	}
}

func TestResolvePromotionLogicLocalInterface(t *testing.T) {
	mock := &mockPromotionLogic{state: "promoted", reason: "high score"}
	fn := resolvePromotionLogic(mock)
	if fn == nil {
		t.Fatal("resolvePromotionLogic returned nil for local interface")
	}
	state, reason, err := fn(context.Background(), "s1", Evidence{SampleCount: 10})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if state != "promoted" {
		t.Errorf("state = %q, want promoted", state)
	}
	if reason != "high score" {
		t.Errorf("reason = %q, want high score", reason)
	}
}

func TestResolvePromotionLogicUnrecognizedType(t *testing.T) {
	if resolvePromotionLogic(42) != nil {
		t.Error("resolvePromotionLogic should return nil for unrecognized type")
	}
}

// ── Bridge adapters ─────────────────────────────────────────────────────────

func TestAPIMemoryBridgeNilProvider(t *testing.T) {
	b := &apiMemoryBridge{provider: nil}
	count, conf, err := b.FindSimilar(context.Background(), "task", 10)
	if err != nil {
		t.Fatalf("FindSimilar: %v", err)
	}
	if count != 0 || conf != 0 {
		t.Errorf("FindSimilar = (%d, %v), want (0, 0)", count, conf)
	}
}

func TestAPIMemoryBridgeWithProvider(t *testing.T) {
	b := &apiMemoryBridge{provider: &mockMemoryProvider{count: 5, confidence: 0.8}}
	count, conf, err := b.FindSimilar(context.Background(), "task", 10)
	if err != nil {
		t.Fatalf("FindSimilar: %v", err)
	}
	if count != 5 {
		t.Errorf("count = %d, want 5", count)
	}
	if conf != 0.8 {
		t.Errorf("confidence = %v, want 0.8", conf)
	}
}

func TestAPIGuidanceBridgeNilProvider(t *testing.T) {
	var b *apiGuidanceBridge
	evolutionOutcome := evolutionPkg.StrategyOutcome{}
	err := b.RecordStrategyOutcome(context.Background(), evolutionOutcome)
	if err != nil {
		t.Errorf("RecordStrategyOutcome with nil bridge: %v", err)
	}

	b2 := &apiGuidanceBridge{provider: nil}
	err = b2.RecordStrategyOutcome(context.Background(), evolutionOutcome)
	if err != nil {
		t.Errorf("RecordStrategyOutcome with nil provider: %v", err)
	}
}

func TestAPIGuidanceBridgeHintsForTask(t *testing.T) {
	mock := &mockGuidanceProvider{
		hints: []EvolutionHint{
			{ID: "h1", TaskType: "code", Confidence: 0.8},
		},
	}
	b := &apiGuidanceBridge{provider: mock}
	hints, err := b.HintsForTask(context.Background(), "code", 10)
	if err != nil {
		t.Fatalf("HintsForTask: %v", err)
	}
	if len(hints) != 1 {
		t.Errorf("hints len = %d, want 1", len(hints))
	}
	if hints[0].ID != "h1" {
		t.Errorf("hints[0].ID = %q, want h1", hints[0].ID)
	}
}

func TestLLMClientAdapter(t *testing.T) {
	mock := &mockLLMClient{response: "test response"}
	adapter := &llmClientAdapter{inner: mock}
	resp, err := adapter.Generate(context.Background(), "prompt")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp != "test response" {
		t.Errorf("resp = %q, want test response", resp)
	}
}

// ── toAPILineage ────────────────────────────────────────────────────────────

func TestToAPILineageNil(t *testing.T) {
	result := toAPILineage(nil)
	if result.ParentID != "" || result.ChildID != "" {
		t.Errorf("toAPILineage(nil) = %+v, want zero value", result)
	}
}

func TestToAPILineageWrongType(t *testing.T) {
	result := toAPILineage("not a lineage")
	if result.ParentID != "" {
		t.Errorf("toAPILineage(wrong type) = %+v, want zero value", result)
	}
}

// ── cloneParams / cloneDimensionScores ──────────────────────────────────────

func TestCloneParamsNil(t *testing.T) {
	if cloneParams(nil) != nil {
		t.Error("cloneParams(nil) should return nil")
	}
}

func TestCloneParamsCopy(t *testing.T) {
	src := map[string]any{"key": "val", "num": 42}
	dst := cloneParams(src)
	if len(dst) != 2 {
		t.Errorf("dst len = %d, want 2", len(dst))
	}
	if dst["key"] != "val" {
		t.Errorf(`dst["key"] = %v, want val`, dst["key"])
	}
	// Verify it's a copy.
	dst["key"] = "mutated"
	if src["key"] != "val" {
		t.Error("cloneParams did not create a copy")
	}
}

func TestCloneDimensionScoresNil(t *testing.T) {
	if cloneDimensionScores(nil) != nil {
		t.Error("cloneDimensionScores(nil) should return nil")
	}
}

func TestCloneDimensionScoresCopy(t *testing.T) {
	src := map[string]float64{"quality": 0.9, "cost": 0.3}
	dst := cloneDimensionScores(src)
	if len(dst) != 2 {
		t.Errorf("dst len = %d, want 2", len(dst))
	}
	dst["quality"] = 0.0
	if src["quality"] != 0.9 {
		t.Error("cloneDimensionScores did not create a copy")
	}
}

// ── toInternalStrategy / toAPIStrategy roundtrip ────────────────────────────

func TestStrategyRoundtrip(t *testing.T) {
	original := &Strategy{
		ID:             "s1",
		Name:           "test",
		Version:        2,
		Score:          75.0,
		ParentID:       "s0",
		PromptTemplate: "precise",
		MutationType:   "param_tweak",
		Params:         map[string]any{"temperature": 0.3},
	}
	internal := toInternalStrategy(original)
	if internal == nil {
		t.Fatal("toInternalStrategy returned nil")
	}
	back := toAPIStrategy(internal)
	if back.ID != original.ID {
		t.Errorf("roundtrip ID = %q, want %q", back.ID, original.ID)
	}
	if back.Score != original.Score {
		t.Errorf("roundtrip Score = %v, want %v", back.Score, original.Score)
	}
	if back.Params["temperature"] != 0.3 {
		t.Errorf("roundtrip Params[temperature] = %v, want 0.3", back.Params["temperature"])
	}
}

// ── DeterministicScore ──────────────────────────────────────────────────────

func TestDeterministicScoreNilStrategy(t *testing.T) {
	score := DeterministicScore(nil)
	if score <= 0 {
		t.Errorf("DeterministicScore(nil) = %v, want > 0", score)
	}
}

func TestDeterministicScoreTemperature(t *testing.T) {
	lowTemp := &Strategy{Params: map[string]any{"temperature": 0.0}}
	highTemp := &Strategy{Params: map[string]any{"temperature": 1.0}}
	lowScore := DeterministicScore(lowTemp)
	highScore := DeterministicScore(highTemp)
	if lowScore <= highScore {
		t.Errorf("low temp score %v should be > high temp score %v", lowScore, highScore)
	}
}

func TestDeterministicScoreTopKOptimal(t *testing.T) {
	optimal := &Strategy{Params: map[string]any{"top_k": 30}}
	suboptimal := &Strategy{Params: map[string]any{"top_k": 5}}
	optimalScore := DeterministicScore(optimal)
	suboptimalScore := DeterministicScore(suboptimal)
	if optimalScore <= suboptimalScore {
		t.Errorf("optimal top_k score %v should be > suboptimal %v", optimalScore, suboptimalScore)
	}
}

func TestDeterministicScorePromptBonus(t *testing.T) {
	base := &Strategy{Params: map[string]any{}}
	precise := &Strategy{Params: map[string]any{"prompt_template": "precise"}}
	preciseScore := DeterministicScore(precise)
	baseScore := DeterministicScore(base)
	if preciseScore <= baseScore {
		t.Errorf("precise prompt score %v should be > base %v", preciseScore, baseScore)
	}
}

func TestDeterministicScoreClamped(t *testing.T) {
	// Extreme values should be clamped.
	extreme := &Strategy{Params: map[string]any{
		"temperature": -100,
		"top_k":       30,
	}}
	score := DeterministicScore(extreme)
	if score > 100 {
		t.Errorf("score = %v, want <= 100 (clamped)", score)
	}
}

// ── DefaultConfig ───────────────────────────────────────────────────────────

func TestDefaultConfigValues(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.PopulationSize != 20 {
		t.Errorf("PopulationSize = %d, want 20", cfg.PopulationSize)
	}
	if cfg.Generations != 15 {
		t.Errorf("Generations = %d, want 15", cfg.Generations)
	}
	if !cfg.EnableWiredMode {
		t.Error("EnableWiredMode should be true by default")
	}
	if cfg.MinLineageImprovement != 0.01 {
		t.Errorf("MinLineageImprovement = %v, want 0.01", cfg.MinLineageImprovement)
	}
}

// ── bestFromStrategies ──────────────────────────────────────────────────────

func TestBestFromStrategiesNil(t *testing.T) {
	if bestFromStrategies(nil) != nil {
		t.Error("bestFromStrategies(nil) should return nil")
	}
}

func TestBestFromStrategiesEmpty(t *testing.T) {
	if bestFromStrategies([]*mutation.Strategy{}) != nil {
		t.Error("bestFromStrategies(empty) should return nil")
	}
}

func TestBestFromStrategiesPicksHighest(t *testing.T) {
	strategies := []*mutation.Strategy{
		{ID: "a", Score: 30},
		{ID: "b", Score: 90},
		{ID: "c", Score: 60},
	}
	best := bestFromStrategies(strategies)
	if best == nil || best.ID != "b" {
		t.Errorf("best = %v, want ID=b", best)
	}
}
