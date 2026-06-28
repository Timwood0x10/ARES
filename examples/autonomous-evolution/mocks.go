package main

import (
	"context"
	"fmt"
	"sync"

	arena "github.com/Timwood0x10/ares/internal/ares_arena"
	evolution "github.com/Timwood0x10/ares/internal/ares_evolution"
	"github.com/Timwood0x10/ares/internal/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
	evolutionservice "github.com/Timwood0x10/ares/internal/ares_evolution/service"
	storageModels "github.com/Timwood0x10/ares/internal/storage/postgres/models"
)

// DemoKit provides all mock components required by demo scenarios.
type DemoKit struct {
	Repo      *mockExperienceRepo
	Scorer    *unifiedScorer
	Genealogy *mockGenealogyRecorder
	Tester    *mockTester
	Mutator   *mutation.Mutator
	Crosser   *genome.Crossover
}

// NewDemoKit creates a fully initialized DemoKit with all mock dependencies.
func NewDemoKit() (*DemoKit, error) {
	repo := newMockExperienceRepo()
	sc := newUnifiedScorer()

	tt, err := newMockTester(sc)
	if err != nil {
		return nil, fmt.Errorf("create tester: %w", err)
	}

	mu, err := mutation.NewMutator(mutation.WithSeed(42))
	if err != nil {
		return nil, fmt.Errorf("create mutator: %w", err)
	}

	cx, err := genome.NewCrossover(genome.WithSeed(42))
	if err != nil {
		return nil, fmt.Errorf("create crossover: %w", err)
	}

	return &DemoKit{
		Repo:      repo,
		Scorer:    sc,
		Genealogy: newMockGenealogyRecorder(),
		Tester:    tt,
		Mutator:   mu,
		Crosser:   cx,
	}, nil
}

type mockExperienceRecord struct {
	id, input, output string
	score             float64
}

type mockExperienceRepo struct {
	mu    sync.RWMutex
	data  map[string]*mockExperienceRecord
	usage map[string]int
	ranks map[string]float64
}

func newMockExperienceRepo() *mockExperienceRepo {
	return &mockExperienceRepo{
		data:  make(map[string]*mockExperienceRecord),
		usage: make(map[string]int),
		ranks: make(map[string]float64),
	}
}

func (r *mockExperienceRepo) Create(_ context.Context, e *storageModels.Experience) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[e.ID] = &mockExperienceRecord{id: e.ID, input: e.Input, output: e.Output, score: e.Score}
	r.ranks[e.ID] = e.Score
	return nil
}

func (r *mockExperienceRepo) GetByID(_ context.Context, id string) (*storageModels.Experience, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.data[id]
	if !ok {
		return nil, fmt.Errorf("experience not found: %s", id)
	}
	return &storageModels.Experience{ID: rec.id, Input: rec.input, Output: rec.output, Score: rec.score}, nil
}

func (r *mockExperienceRepo) Update(_ context.Context, e *storageModels.Experience) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[e.ID]; !ok {
		return fmt.Errorf("experience not found: %s", e.ID)
	}
	r.data[e.ID] = &mockExperienceRecord{id: e.ID, input: e.Input, output: e.Output, score: e.Score}
	return nil
}

func (r *mockExperienceRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, id)
	delete(r.usage, id)
	delete(r.ranks, id)
	return nil
}

func (r *mockExperienceRepo) SearchByVector(_ context.Context, _ []float64, _ string, limit int) ([]*storageModels.Experience, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*storageModels.Experience, 0, limit)
	for _, rec := range r.data {
		if len(out) >= limit {
			break
		}
		out = append(out, &storageModels.Experience{ID: rec.id, Input: rec.input, Output: rec.output, Score: rec.score})
	}
	return out, nil
}

func (r *mockExperienceRepo) SearchByKeyword(c context.Context, q, s string, n int) ([]*storageModels.Experience, error) {
	return r.SearchByVector(c, nil, s, n)
}

func (r *mockExperienceRepo) IncrementUsageCount(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.usage[id]++
	return nil
}

func (r *mockExperienceRepo) DecrementRank(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ranks[id] *= 0.9
	return nil
}

func (r *mockExperienceRepo) ListByType(c context.Context, t, s string, n int) ([]*storageModels.Experience, error) {
	return r.SearchByVector(c, nil, s, n)
}

func (r *mockExperienceRepo) ListByAgent(c context.Context, a, s string, n int) ([]*storageModels.Experience, error) {
	return r.SearchByVector(c, nil, s, n)
}

func (r *mockExperienceRepo) getRank(id string) float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.ranks[id]
}

func (r *mockExperienceRepo) getUsageCount(id string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.usage[id]
}

// unifiedScorer evaluates strategy fitness based on actual parameters.
// Score range: [0, 100]. Higher = better.
type unifiedScorer struct {
	mu sync.Mutex
}

func newUnifiedScorer() *unifiedScorer {
	return &unifiedScorer{}
}

func (s *unifiedScorer) Score(input any) (float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	score := evolutionservice.DeterministicScore(&evolutionservice.Strategy{
		Params:         extractParams(input),
		PromptTemplate: extractPromptTemplate(input),
	})

	return score, nil
}

// extractParams attempts to extract a Params map from an input value.
// Returns an empty map if extraction fails.
func extractParams(input any) map[string]any {
	if m, ok := input.(map[string]any); ok {
		return m
	}
	return make(map[string]any)
}

// extractPromptTemplate attempts to extract PromptTemplate from an input value.
// Returns empty string if extraction fails.
func extractPromptTemplate(input any) string {
	if m, ok := input.(map[string]any); ok {
		if pt, ok := m["prompt_template"].(string); ok {
			return pt
		}
		if pt, ok := m["PromptTemplate"].(string); ok {
			return pt
		}
	}
	// Also handle "temp" as alias for "temperature" compatibility.
	if m, ok := input.(map[string]any); ok {
		if _, hasTemp := m["temp"]; hasTemp {
			if _, ok := m["temperature"].(float64); !ok {
				m["temperature"] = m["temp"]
			}
		}
	}
	return ""
}

type mockGenealogyRecorder struct {
	mu       sync.Mutex
	lineages []evolution.StrategyLineage
}

func newMockGenealogyRecorder() *mockGenealogyRecorder {
	return &mockGenealogyRecorder{}
}

func (g *mockGenealogyRecorder) Record(_ context.Context, l evolution.StrategyLineage) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.lineages = append(g.lineages, l)
	return nil
}

type mockTester struct{ rt *arena.RegressionTester }

func newMockTester(scorer arena.Scorer) (*mockTester, error) {
	arenaSvc := arena.NewService(nil, nil)
	rt, err := arena.NewRegressionTester(arenaSvc, scorer)
	if err != nil {
		return nil, fmt.Errorf("create regression tester: %w", err)
	}
	return &mockTester{rt: rt}, nil
}

func (t *mockTester) Run(ctx context.Context, cfg evolution.RegressionConfig) (*evolution.RegressionResult, error) {
	r, err := t.rt.Run(ctx, arena.RegressionConfig{
		OldStrategy:  cfg.Baseline,
		NewStrategy:  cfg.Candidate,
		BaselineRuns: 5,
		CompareRuns:  5,
		TestSuite:    "test",
	})
	if err != nil {
		return nil, err
	}
	return &evolution.RegressionResult{
		CandidateScore: r.NewAvg,
		BaselineScore:  r.OldAvg,
		WinRate:        r.WinRate,
		TotalTasks:     r.Samples,
	}, nil
}
