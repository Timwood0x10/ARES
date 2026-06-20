// Package genome provides selection operators for the genetic algorithm evolution engine.
// It implements multiple natural selection strategies including truncation,
// tournament, and roulette wheel (fitness proportionate) selection.
package genome

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"sort"

	"goagentx/internal/evolution/mutation"
)

// Selection defines the strategy for selecting individuals from a population.
//
// Implementations determine which strategies are chosen as parents for the next
// generation based on their fitness scores and the selection algorithm's bias.
type Selection interface {
	// Select chooses n individuals from the population for reproduction.
	// Higher-scoring individuals should have higher probability of being selected,
	// depending on the specific algorithm implementation.
	//
	// Args:
	//
	//	ctx - operation context (used for cancellation).
	//	population - candidate strategies to select from (must not be nil).
	//	n - number of individuals to select (must be > 0).
	//
	// Returns:
	//
	//	[]*mutation.Strategy - selected individuals for reproduction.
	//	error - non-nil if population is empty, n is invalid, or context is cancelled.
	Select(ctx context.Context, population []*mutation.Strategy, n int) ([]*mutation.Strategy, error)
}

// Error definitions for selection operations.

// ErrSelectionEmptyPopulation is returned when an empty or nil population is provided to a selector.
var ErrSelectionEmptyPopulation = fmt.Errorf("selection: population must not be empty")

// ErrInvalidSelectionSize is returned when the requested selection count n is invalid.
var ErrInvalidSelectionSize = fmt.Errorf("selection size must be positive")

// ErrInvalidTournamentSize is returned when tournament size k is less than 2.
var ErrInvalidTournamentSize = fmt.Errorf("tournament size must be at least 2")

// TruncationSelection selects the top-n individuals by score.
// Simple but effective: just take the best scoring individuals.
// This is a deterministic selection method that always picks the highest scorers.
type TruncationSelection struct{}

// NewTruncationSelection creates a new truncation selector.
//
// Returns:
//
//	*TruncationSelection - a configured truncation selector instance.
func NewTruncationSelection() *TruncationSelection {
	return &TruncationSelection{}
}

// Select returns the top-n highest-scoring individuals from the population.
// If n exceeds the population size, all individuals are returned.
// Individuals are sorted by score in descending order before selection.
//
// Args:
//
//	ctx - operation context (used for cancellation).
//	population - candidate strategies (must not be nil or empty).
//	n - number of individuals to select (must be > 0).
//
// Returns:
//
//	[]*mutation.Strategy - the n highest-scoring strategies.
//	error - ErrEmptyPopulation if population is empty, ErrInvalidSelectionSize if n <= 0.
func (t *TruncationSelection) Select(ctx context.Context, population []*mutation.Strategy, n int) ([]*mutation.Strategy, error) {
	if err := validateSelectInputs(ctx, population, n); err != nil {
		return nil, err
	}

	sorted := make([]*mutation.Strategy, len(population))
	copy(sorted, population)
	SortByScore(sorted)

	if n > len(sorted) {
		n = len(sorted)
	}

	return sorted[:n], nil
}

// TournamentSelection selects individuals via tournament.
// Randomly pick k individuals, return the best among them. Repeat n times.
// Larger tournament sizes increase selection pressure toward higher-scoring individuals.
type TournamentSelection struct {
	tournamentSize int        // Number of competitors per tournament (default 3).
	rng            *rand.Rand // Deterministic randomness source.
}

// NewTournamentSelection creates a new tournament selector with options.
//
// Default configuration:
//   - tournamentSize: 3
//   - rng: seeded with current time (non-deterministic)
//
// Args:
//
//	opts - optional configuration functions (WithTournamentSize, WithTournamentSeed).
//
// Returns:
//
//	*TournamentSelection - the configured tournament selector instance.
//	error - non-nil if any option fails validation.
func NewTournamentSelection(opts ...TournamentOption) (*TournamentSelection, error) {
	ts := &TournamentSelection{
		tournamentSize: 3,
		rng:            rand.New(rand.NewSource(rand.Int63())), // #nosec G404 — selection doesn't need crypto rand
	}

	for _, opt := range opts {
		if err := opt(ts); err != nil {
			return nil, fmt.Errorf("apply tournament option: %w", err)
		}
	}

	return ts, nil
}

// TournamentOption configures TournamentSelection.
type TournamentOption func(*TournamentSelection) error

// WithTournamentSize sets the number of competitors per tournament.
// Larger values increase selection pressure (bias toward high-fitness individuals).
// Minimum valid value is 2.
//
// Args:
//
//	k - number of competitors per tournament (must be >= 2).
//
// Returns:
//
//	TournamentOption - configuration function.
//	error - ErrInvalidTournamentSize if k < 2.
func WithTournamentSize(k int) TournamentOption {
	return func(ts *TournamentSelection) error {
		if k < 2 {
			return fmt.Errorf("%w: got %d", ErrInvalidTournamentSize, k)
		}
		ts.tournamentSize = k
		return nil
	}
}

// WithTournamentSeed sets the random seed for deterministic tournament selection.
// Useful for reproducible experiments and testing.
//
// Args:
//
//	seed - random seed value.
//
// Returns:
//
//	TournamentOption - configuration function.
func WithTournamentSeed(seed int64) TournamentOption {
	return func(ts *TournamentSelection) error {
		ts.rng = rand.New(rand.NewSource(seed)) // #nosec G404 — deterministic selection for reproducibility
		return nil
	}
}

// Select runs n tournaments and returns the winners.
// Each tournament randomly selects k individuals and returns the one with
// the highest score. The same individual may win multiple tournaments.
//
// Args:
//
//	ctx - operation context (used for cancellation).
//	population - candidate strategies (must not be nil or empty).
//	n - number of tournaments to run (must be > 0).
//
// Returns:
//
//	[]*mutation.Strategy - n tournament winners.
//	error - non-nil if inputs are invalid or context is cancelled.
func (ts *TournamentSelection) Select(ctx context.Context, population []*mutation.Strategy, n int) ([]*mutation.Strategy, error) {
	if err := validateSelectInputs(ctx, population, n); err != nil {
		return nil, err
	}

	winners := make([]*mutation.Strategy, 0, n)
	for i := 0; i < n; i++ {
		select {
		case <-ctx.Done():
			return winners, fmt.Errorf("tournament %d: %w", i, ctx.Err())
		default:
		}

		winner, err := ts.runTournament(population)
		if err != nil {
			return nil, fmt.Errorf("run tournament %d: %w", i, err)
		}
		winners = append(winners, winner)
	}

	return winners, nil
}

// runTournament executes a single tournament round.
// Randomly selects k competitors and returns the highest-scoring one.
func (ts *TournamentSelection) runTournament(population []*mutation.Strategy) (*mutation.Strategy, error) {
	k := ts.tournamentSize
	if k > len(population) {
		k = len(population)
	}

	indices := ts.pickUniqueIndices(len(population), k)
	bestIdx := indices[0]
	for _, idx := range indices[1:] {
		if population[idx].Score > population[bestIdx].Score {
			bestIdx = idx
		}
	}

	slog.Debug("runTournament completed",
		"competitors", k,
		"winner_score", population[bestIdx].Score)

	return population[bestIdx], nil
}

// pickUniqueIndices selects n unique random indices from [0, poolSize).
// Uses Fisher-Yates partial shuffle for efficiency.
func (ts *TournamentSelection) pickUniqueIndices(poolSize, n int) []int {
	indices := make([]int, poolSize)
	for i := range indices {
		indices[i] = i
	}

	for i := 0; i < n && i < poolSize; i++ {
		j := i + ts.rng.Intn(poolSize-i)
		indices[i], indices[j] = indices[j], indices[i]
	}

	if n > poolSize {
		n = poolSize
	}
	return indices[:n]
}

// RouletteWheelSelection (aka Fitness Proportionate Selection) selects individuals
// with probability proportional to their fitness score. Higher-scoring individuals
// have a larger slice of the "roulette wheel" and are more likely to be selected.
type RouletteWheelSelection struct {
	rng *rand.Rand // Deterministic randomness source.
}

// NewRouletteWheelSelection creates a new roulette wheel selector.
//
// Default configuration:
//   - rng: seeded with current time (non-deterministic)
//
// Args:
//
//	opts - optional configuration functions (WithRouletteSeed).
//
// Returns:
//
//	*RouletteWheelSelection - the configured roulette wheel selector instance.
//	error - non-nil if any option fails validation.
func NewRouletteWheelSelection(opts ...RouletteOption) (*RouletteWheelSelection, error) {
	rw := &RouletteWheelSelection{
		rng: rand.New(rand.NewSource(rand.Int63())), // #nosec G404 — selection doesn't need crypto rand
	}

	for _, opt := range opts {
		if err := opt(rw); err != nil {
			return nil, fmt.Errorf("apply roulette option: %w", err)
		}
	}

	return rw, nil
}

// RouletteOption configures RouletteWheelSelection.
type RouletteOption func(*RouletteWheelSelection) error

// WithRouletteSeed sets the random seed for deterministic selection.
// Useful for reproducible experiments and testing.
//
// Args:
//
//	seed - random seed value.
//
// Returns:
//
//	RouletteOption - configuration function.
func WithRouletteSeed(seed int64) RouletteOption {
	return func(rw *RouletteWheelSelection) error {
		rw.rng = rand.New(rand.NewSource(seed)) // #nosec G404 — deterministic selection for reproducibility
		return nil
	}
}

// Select picks n individuals with probability proportional to their score.
// Scores are normalized to be non-negative (min score subtracted) before selection.
// All-zero or identical scores result in uniform probability distribution.
//
// Args:
//
//	ctx - operation context (used for cancellation).
//	population - candidate strategies (must not be nil or empty).
//	n - number of individuals to select (must be > 0).
//
// Returns:
//
//	[]*mutation.Strategy - n selected strategies.
//	error - non-nil if inputs are invalid or context is cancelled.
func (rw *RouletteWheelSelection) Select(ctx context.Context, population []*mutation.Strategy, n int) ([]*mutation.Strategy, error) {
	if err := validateSelectInputs(ctx, population, n); err != nil {
		return nil, err
	}

	normalized := rw.normalizeScores(population)
	totalScore := sumFloat64(normalized)

	selected := make([]*mutation.Strategy, 0, n)
	for i := 0; i < n; i++ {
		select {
		case <-ctx.Done():
			return selected, fmt.Errorf("roulette select %d: %w", i, ctx.Err())
		default:
		}

		idx, err := rw.spinWheel(normalized, totalScore)
		if err != nil {
			return nil, fmt.Errorf("spin wheel %d: %w", i, err)
		}
		selected = append(selected, population[idx])
	}

	return selected, nil
}

// normalizeScores shifts all scores to be non-negative by subtracting the minimum.
// This handles negative scores gracefully while preserving relative differences.
func (rw *RouletteWheelSelection) normalizeScores(population []*mutation.Strategy) []float64 {
	minScore := findMinScore(population)
	normalized := make([]float64, len(population))
	for i, s := range population {
		normalized[i] = s.Score - minScore
	}
	return normalized
}

// spinWheel performs one roulette wheel spin and returns the selected index.
// Uses cumulative probability distribution for O(n) selection per spin.
func (rw *RouletteWheelSelection) spinWheel(normalized []float64, total float64) (int, error) {
	if total <= 0 {
		// Uniform distribution when all scores are equal (including all zero).
		return rw.rng.Intn(len(normalized)), nil
	}

	target := rw.rng.Float64() * total
	cumulative := 0.0
	for i, score := range normalized {
		cumulative += score
		if cumulative >= target {
			return i, nil
		}
	}

	// Fallback: return last index (handles floating-point edge cases).
	return len(normalized) - 1, nil
}

// PickParent is a convenience function that uses tournament selection
// to pick a single parent from survivors. Used by Population.Evolve().
// Falls back to truncation if the provided selector is nil.
//
// Args:
//
//	ctx - operation context (used for cancellation).
//	population - candidate strategies (must not be nil or empty).
//	sel - selection strategy to use (may be nil, defaults to truncation).
//	rng - random number generator for fallback tournament creation (may be nil).
//
// Returns:
//
//	*mutation.Strategy - the selected parent strategy.
//	error - non-nil if population is empty or selection fails.
func PickParent(ctx context.Context, population []*mutation.Strategy, sel Selection, rng *rand.Rand) (*mutation.Strategy, error) {
	if sel == nil {
		var err error
		sel, err = NewTournamentSelection(WithTournamentSeed(rng.Int63()))
		if err != nil {
			return nil, fmt.Errorf("create default selector: %w", err)
		}
	}

	parents, err := sel.Select(ctx, population, 1)
	if err != nil {
		return nil, fmt.Errorf("pick parent: %w", err)
	}

	return parents[0], nil
}

// SortByScore sorts a slice of strategies in descending order by score.
// Unevaluated strategies (Score == -1) go to the end of the slice.
// Uses stable sort to preserve original ordering among equal-scored strategies.
//
// Args:
//
//	strategies - strategies to sort (modified in-place, may be nil or empty).
func SortByScore(strategies []*mutation.Strategy) {
	sort.SliceStable(strategies, func(i, j int) bool {
		si, sj := strategies[i].Score, strategies[j].Score

		// Unevaluated strategies (score == -1) always sort last.
		if si == -1 && sj == -1 {
			return false
		}
		if si == -1 {
			return false
		}
		if sj == -1 {
			return true
		}

		return si > sj
	})
}

// --- Helper functions ---

// validateSelectInputs performs common input validation for all Select methods.
func validateSelectInputs(ctx context.Context, population []*mutation.Strategy, n int) error {
	if ctx == nil {
		return fmt.Errorf("context must not be nil")
	}
	if len(population) == 0 {
		return ErrSelectionEmptyPopulation
	}
	if n <= 0 {
		return fmt.Errorf("%w: got %d", ErrInvalidSelectionSize, n)
	}
	return nil
}

// findMinScore returns the minimum Score value in the population.
func findMinScore(population []*mutation.Strategy) float64 {
	min := population[0].Score
	for i := 1; i < len(population); i++ {
		if population[i].Score < min {
			min = population[i].Score
		}
	}
	return min
}

// sumFloat64 returns the sum of all float64 values in the slice.
func sumFloat64(values []float64) float64 {
	total := 0.0
	for _, v := range values {
		total += v
	}
	return total
}
