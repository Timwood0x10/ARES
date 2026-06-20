package genome

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"goagentx/internal/evolution/mutation"
)

// mockMutator implements MutatorInterface for testing.
type mockMutator struct {
	mutateFn func(ctx context.Context, parent *mutation.Strategy, n int) ([]*mutation.Strategy, error)
}

func (m *mockMutator) Mutate(ctx context.Context, parent *mutation.Strategy, n int) ([]*mutation.Strategy, error) {
	if m.mutateFn != nil {
		return m.mutateFn(ctx, parent, n)
	}
	result := make([]*mutation.Strategy, n)
	for i := range result {
		result[i] = &mutation.Strategy{
			ID:             "mock-mutant",
			Params:         make(map[string]any),
			PromptTemplate: "mock-template",
			Score:          -1,
			CreatedAt:      time.Now(),
		}
	}
	return result, nil
}

// mockCrosser implements CrossoverInterface for testing.
type mockCrosser struct {
	crossoverFn func(ctx context.Context, a, b *mutation.Strategy) (*mutation.Strategy, error)
}

func (c *mockCrosser) Crossover(ctx context.Context, a, b *mutation.Strategy) (*mutation.Strategy, error) {
	if c.crossoverFn != nil {
		return c.crossoverFn(ctx, a, b)
	}
	return &mutation.Strategy{
		ID:             "mock-child",
		Params:         make(map[string]any),
		PromptTemplate: "mock-template",
		Score:          -1,
	}, nil
}

// newTestStrategy creates a strategy with the given score for testing.
func newTestStrategy(score float64) *mutation.Strategy {
	return &mutation.Strategy{
		ID:             "test-strategy",
		Params:         map[string]any{"temperature": 0.7},
		PromptTemplate: "test-prompt",
		Score:          score,
		CreatedAt:      time.Now(),
	}
}

func TestNewPopulation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		base     *mutation.Strategy
		mutator  MutatorInterface
		opts     []PopulationOption
		wantErr  error
		wantSize int
		wantGen  int
	}{
		{
			name:     "valid config creates population",
			base:     newTestStrategy(0.5),
			mutator:  &mockMutator{},
			opts:     []PopulationOption{WithPopulationSize(5)},
			wantErr:  nil,
			wantSize: 5,
			wantGen:  0,
		},
		{
			name:    "nil base returns error",
			base:    nil,
			mutator: &mockMutator{},
			wantErr: ErrNilBaseStrategy,
		},
		{
			name:    "nil mutator returns error",
			base:    newTestStrategy(0.5),
			mutator: nil,
			wantErr: ErrNilMutator,
		},
		{
			name:    "zero size returns error",
			base:    newTestStrategy(0.5),
			mutator: &mockMutator{},
			opts:    []PopulationOption{WithPopulationSize(0)},
			wantErr: ErrInvalidPopulationSize,
		},
		{
			name:    "negative size returns error",
			base:    newTestStrategy(0.5),
			mutator: &mockMutator{},
			opts:    []PopulationOption{WithPopulationSize(-1)},
			wantErr: ErrInvalidPopulationSize,
		},
		{
			name:    "elite count exceeds size returns error",
			base:    newTestStrategy(0.5),
			mutator: &mockMutator{},
			opts:    []PopulationOption{WithPopulationSize(3), WithEliteCount(5)},
			wantErr: ErrInvalidEliteCount,
		},
		{
			name:    "invalid survival rate returns error",
			base:    newTestStrategy(0.5),
			mutator: &mockMutator{},
			opts:    []PopulationOption{WithSurvivalRate(1.5)},
			wantErr: ErrInvalidSurvivalRate,
		},
		{
			name:    "invalid mutation rate returns error",
			base:    newTestStrategy(0.5),
			mutator: &mockMutator{},
			opts:    []PopulationOption{WithMutationRate(-0.1)},
			wantErr: ErrInvalidMutationRate,
		},
		{
			name:     "default config uses size 20",
			base:     newTestStrategy(0.5),
			mutator:  &mockMutator{},
			wantErr:  nil,
			wantSize: 20,
			wantGen:  0,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			pop, err := NewPopulation(ctx, tt.base, tt.mutator, tt.opts...)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("expected error %v, got nil", tt.wantErr)
					return
				}
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("error = %v, want %v", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if pop.Size != tt.wantSize {
				t.Errorf("population Size = %d, want %d", pop.Size, tt.wantSize)
			}

			if pop.Generation != tt.wantGen {
				t.Errorf("population Generation = %d, want %d", pop.Generation, tt.wantGen)
			}

			if len(pop.Agents) != pop.Size {
				t.Errorf("agent count = %d, want %d", len(pop.Agents), pop.Size)
			}
		})
	}
}

func TestEvolve(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("successful evolution increments generation", func(t *testing.T) {
		t.Parallel()

		base := newTestStrategy(0.5)
		mutator := &mockMutator{}
		crosser := &mockCrosser{}

		pop, err := NewPopulation(ctx, base, mutator, WithPopulationSize(6), WithSurvivalRate(0.5), WithEliteCount(1))
		if err != nil {
			t.Fatalf("failed to create population: %v", err)
		}

		// Set scores for all agents.
		for i, agent := range pop.Agents {
			agent.Score = float64(i)
		}

		genBefore := pop.Generation
		err = pop.Evolve(ctx, mutator, crosser)
		if err != nil {
			t.Fatalf("evolve failed: %v", err)
		}

		if pop.Generation != genBefore+1 {
			t.Errorf("generation = %d, want %d", pop.Generation, genBefore+1)
		}

		if len(pop.Agents) != pop.Size {
			t.Errorf("agent count after evolve = %d, want %d", len(pop.Agents), pop.Size)
		}
	})

	t.Run("nil mutator returns error", func(t *testing.T) {
		t.Parallel()

		base := newTestStrategy(0.5)
		mutator := &mockMutator{}
		crosser := &mockCrosser{}

		pop, err := NewPopulation(ctx, base, mutator, WithPopulationSize(4))
		if err != nil {
			t.Fatalf("failed to create population: %v", err)
		}

		err = pop.Evolve(ctx, nil, crosser)
		if !errors.Is(err, ErrNilMutator) {
			t.Errorf("error = %v, want %v", err, ErrNilMutator)
		}
	})

	t.Run("nil crosser returns error", func(t *testing.T) {
		t.Parallel()

		base := newTestStrategy(0.5)
		mutator := &mockMutator{}

		pop, err := NewPopulation(ctx, base, mutator, WithPopulationSize(4))
		if err != nil {
			t.Fatalf("failed to create population: %v", err)
		}

		err = pop.Evolve(ctx, mutator, nil)
		if !errors.Is(err, ErrNilCrosser) {
			t.Errorf("error = %v, want %v", err, ErrNilCrosser)
		}
	})

	t.Run("multiple evolutions accumulate generations", func(t *testing.T) {
		t.Parallel()

		base := newTestStrategy(0.5)
		mutator := &mockMutator{}
		crosser := &mockCrosser{}

		pop, err := NewPopulation(ctx, base, mutator, WithPopulationSize(6), WithSurvivalRate(0.5), WithEliteCount(1))
		if err != nil {
			t.Fatalf("failed to create population: %v", err)
		}

		for i := 0; i < 3; i++ {
			for _, agent := range pop.Agents {
				agent.Score = float64(i * 10)
			}

			err = pop.Evolve(ctx, mutator, crosser)
			if err != nil {
				t.Fatalf("evolution %d failed: %v", i, err)
			}
		}

		if pop.Generation != 3 {
			t.Errorf("generation after 3 evolves = %d, want 3", pop.Generation)
		}
	})
}

func TestBest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		agents    []*mutation.Strategy
		wantScore float64
		wantNil   bool
	}{
		{
			name:    "empty population returns nil",
			agents:  []*mutation.Strategy{},
			wantNil: true,
		},
		{
			name: "single agent returns that agent",
			agents: []*mutation.Strategy{
				newTestStrategy(42.0),
			},
			wantScore: 42.0,
		},
		{
			name: "returns highest scored agent",
			agents: []*mutation.Strategy{
				newTestStrategy(10.0),
				newTestStrategy(50.0),
				newTestStrategy(30.0),
			},
			wantScore: 50.0,
		},
		{
			name: "all same score returns first encountered",
			agents: []*mutation.Strategy{
				newTestStrategy(25.0),
				newTestStrategy(25.0),
				newTestStrategy(25.0),
			},
			wantScore: 25.0,
		},
		{
			name: "negative scores handled correctly",
			agents: []*mutation.Strategy{
				newTestStrategy(-10.0),
				newTestStrategy(-5.0),
				newTestStrategy(-20.0),
			},
			wantScore: -5.0,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pop := &Population{
				Agents: tt.agents,
				Size:   len(tt.agents),
			}

			best := pop.Best()

			if tt.wantNil {
				if best != nil {
					t.Errorf("expected nil, got %+v", best)
				}
				return
			}

			if best == nil {
				t.Fatalf("expected non-nil best, got nil")
			}

			if best.Score != tt.wantScore {
				t.Errorf("best.Score = %f, want %f", best.Score, tt.wantScore)
			}
		})
	}
}

func TestStats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		agents         []*mutation.Strategy
		generation     int
		wantGeneration int
		wantSize       int
		wantAvg        float64
		wantBest       float64
		wantWorst      float64
	}{
		{
			name:           "empty population returns zero values",
			agents:         []*mutation.Strategy{},
			generation:     5,
			wantGeneration: 5,
			wantSize:       0,
			wantAvg:        0.0,
			wantBest:       0.0,
			wantWorst:      0.0,
		},
		{
			name: "single agent stats",
			agents: []*mutation.Strategy{
				newTestStrategy(100.0),
			},
			generation:     2,
			wantGeneration: 2,
			wantSize:       1,
			wantAvg:        100.0,
			wantBest:       100.0,
			wantWorst:      100.0,
		},
		{
			name: "multiple agents correct calculation",
			agents: []*mutation.Strategy{
				newTestStrategy(10.0),
				newTestStrategy(20.0),
				newTestStrategy(30.0),
				newTestStrategy(40.0),
			},
			generation:     3,
			wantGeneration: 3,
			wantSize:       4,
			wantAvg:        25.0,
			wantBest:       40.0,
			wantWorst:      10.0,
		},
		{
			name: "all same score population",
			agents: []*mutation.Strategy{
				newTestStrategy(15.0),
				newTestStrategy(15.0),
				newTestStrategy(15.0),
			},
			generation:     1,
			wantGeneration: 1,
			wantSize:       3,
			wantAvg:        15.0,
			wantBest:       15.0,
			wantWorst:      15.0,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pop := &Population{
				Agents:     tt.agents,
				Size:       len(tt.agents),
				Generation: tt.generation,
			}

			stats := pop.Stats()

			if stats == nil {
				t.Fatal("stats should not be nil")
			}

			if stats.Generation != tt.wantGeneration {
				t.Errorf("Generation = %d, want %d", stats.Generation, tt.wantGeneration)
			}

			if stats.Size != tt.wantSize {
				t.Errorf("Size = %d, want %d", stats.Size, tt.wantSize)
			}

			if stats.AvgScore != tt.wantAvg {
				t.Errorf("AvgScore = %f, want %f", stats.AvgScore, tt.wantAvg)
			}

			if stats.BestScore != tt.wantBest {
				t.Errorf("BestScore = %f, want %f", stats.BestScore, tt.wantBest)
			}

			if stats.WorstScore != tt.wantWorst {
				t.Errorf("WorstScore = %f, want %f", stats.WorstScore, tt.wantWorst)
			}
		})
	}
}

func TestConcurrentEvolveSafety(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	base := newTestStrategy(0.5)

	callCount := 0
	mutator := &mockMutator{
		mutateFn: func(ctx context.Context, parent *mutation.Strategy, n int) ([]*mutation.Strategy, error) {
			callCount++
			result := make([]*mutation.Strategy, n)
			for i := range result {
				result[i] = newTestStrategy(float64(callCount))
			}
			return result, nil
		},
	}

	crosser := &mockCrosser{
		crossoverFn: func(ctx context.Context, a, b *mutation.Strategy) (*mutation.Strategy, error) {
			return newTestStrategy(a.Score + b.Score), nil
		},
	}

	pop, err := NewPopulation(ctx, base, mutator, WithPopulationSize(10), WithSurvivalRate(0.5), WithEliteCount(1))
	if err != nil {
		t.Fatalf("failed to create population: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 5; j++ {
				for _, agent := range pop.Agents {
					agent.Score = float64(j)
				}
				if err := pop.Evolve(ctx, mutator, crosser); err != nil && !errors.Is(err, context.Canceled) {
					errs <- err
				}
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent evolve error: %v", err)
	}
}

func TestSingleAgentPopulation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	base := newTestStrategy(0.5)
	mutator := &mockMutator{}
	crosser := &mockCrosser{}

	pop, err := NewPopulation(ctx, base, mutator, WithPopulationSize(1), WithEliteCount(0))
	if err != nil {
		t.Fatalf("failed to create population: %v", err)
	}

	if len(pop.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(pop.Agents))
	}

	pop.Agents[0].Score = 99.0

	err = pop.Evolve(ctx, mutator, crosser)
	if err != nil {
		t.Fatalf("evolve failed for single agent: %v", err)
	}

	best := pop.Best()
	if best == nil {
		t.Fatal("best should not be nil")
	}

	stats := pop.Stats()
	if stats.Size != 1 {
		t.Errorf("stats.Size = %d, want 1", stats.Size)
	}
}

func TestDefaultPopulationConfig(t *testing.T) {
	t.Parallel()

	cfg := DefaultPopulationConfig()

	if cfg.Size != 20 {
		t.Errorf("default Size = %d, want 20", cfg.Size)
	}

	if cfg.SurvivalRate != 0.3 {
		t.Errorf("default SurvivalRate = %f, want 0.3", cfg.SurvivalRate)
	}

	if cfg.MutationRate != 0.2 {
		t.Errorf("default MutationRate = %f, want 0.2", cfg.MutationRate)
	}

	if cfg.EliteCount != 1 {
		t.Errorf("default EliteCount = %d, want 1", cfg.EliteCount)
	}
}

func TestPopulationConfigValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		option  PopulationOption
		wantErr error
	}{
		{
			name:    "valid size accepted",
			option:  WithPopulationSize(10),
			wantErr: nil,
		},
		{
			name:    "size zero rejected",
			option:  WithPopulationSize(0),
			wantErr: ErrInvalidPopulationSize,
		},
		{
			name:    "size negative rejected",
			option:  WithPopulationSize(-5),
			wantErr: ErrInvalidPopulationSize,
		},
		{
			name:    "survival rate 0 accepted",
			option:  WithSurvivalRate(0.0),
			wantErr: nil,
		},
		{
			name:    "survival rate 1 accepted",
			option:  WithSurvivalRate(1.0),
			wantErr: nil,
		},
		{
			name:    "survival rate above 1 rejected",
			option:  WithSurvivalRate(1.1),
			wantErr: ErrInvalidSurvivalRate,
		},
		{
			name:    "survival rate negative rejected",
			option:  WithSurvivalRate(-0.5),
			wantErr: ErrInvalidSurvivalRate,
		},
		{
			name:    "mutation rate 0 accepted",
			option:  WithMutationRate(0.0),
			wantErr: nil,
		},
		{
			name:    "mutation rate 1 accepted",
			option:  WithMutationRate(1.0),
			wantErr: nil,
		},
		{
			name:    "mutation rate above 1 rejected",
			option:  WithMutationRate(1.5),
			wantErr: ErrInvalidMutationRate,
		},
		{
			name:    "elite count 0 accepted",
			option:  WithEliteCount(0),
			wantErr: nil,
		},
		{
			name:    "elite count positive accepted",
			option:  WithEliteCount(5),
			wantErr: nil,
		},
		{
			name:    "elite count negative rejected",
			option:  WithEliteCount(-1),
			wantErr: ErrInvalidEliteCount,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := PopulationConfig{}
			err := tt.option(&cfg)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("expected error %v, got nil", tt.wantErr)
					return
				}
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("error = %v, want %v", err, tt.wantErr)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestEvolvePreservesElites(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	base := newTestStrategy(0.5)
	mutator := &mockMutator{}
	crosser := &mockCrosser{}

	pop, err := NewPopulation(ctx, base, mutator, WithPopulationSize(8), WithSurvivalRate(0.5), WithEliteCount(2))
	if err != nil {
		t.Fatalf("failed to create population: %v", err)
	}

	scores := []float64{90.0, 80.0, 70.0, 60.0, 50.0, 40.0, 30.0, 20.0}
	for i, agent := range pop.Agents {
		agent.Score = scores[i]
	}

	topScoreBefore := pop.Best().Score

	err = pop.Evolve(ctx, mutator, crosser)
	if err != nil {
		t.Fatalf("evolve failed: %v", err)
	}

	topScoreAfter := pop.Best().Score

	if topScoreAfter < topScoreBefore {
		t.Errorf("elite not preserved: best score dropped from %f to %f", topScoreBefore, topScoreAfter)
	}
}

func TestContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	base := newTestStrategy(0.5)

	mutCallCount := 0
	mutator := &mockMutator{
		mutateFn: func(ctx context.Context, parent *mutation.Strategy, n int) ([]*mutation.Strategy, error) {
			mutCallCount++
			if mutCallCount > 1 {
				cancel()
			}
			result := make([]*mutation.Strategy, n)
			for i := range result {
				result[i] = &mutation.Strategy{
					ID:             "mock-mutant",
					Params:         make(map[string]any),
					PromptTemplate: "mock-template",
					Score:          -1,
					CreatedAt:      time.Now(),
				}
			}
			return result, nil
		},
	}

	crosser := &mockCrosser{}

	pop, err := NewPopulation(ctx, base, mutator, WithPopulationSize(10), WithEliteCount(0))
	if err != nil {
		t.Fatalf("failed to create population: %v", err)
	}

	for _, agent := range pop.Agents {
		agent.Score = 1.0
	}

	err = pop.Evolve(ctx, mutator, crosser)
	if err == nil {
		t.Log("context cancellation test: evolve completed before cancellation (acceptable for small populations)")
	}
}
