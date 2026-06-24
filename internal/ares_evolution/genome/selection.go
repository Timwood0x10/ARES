// Package genome provides selection operators for the genetic algorithm evolution engine.
// It implements tournament selection for choosing parent strategies during evolution.
package genome

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"sort"

	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
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
		rng:            rand.New(rand.NewSource(rand.Int63())), // #nosec G404 - selection doesn't need crypto rand
	}

	for _, opt := range opts {
		if err := opt(ts); err != nil {
			return nil, fmt.Errorf("apply tournament option: %w", err)
		}
	}

	if err := ts.Validate(); err != nil {
		return nil, fmt.Errorf("validate tournament selection: %w", err)
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
		ts.rng = rand.New(rand.NewSource(seed)) // #nosec G404 - deterministic selection for reproducibility
		return nil
	}
}

// Validate checks the internal configuration state of TournamentSelection
// and returns an error if any invariant is violated. This is a defensive
// check that complements option-level validation in WithTournamentSize().
//
// Validated invariants:
//   - tournamentSize must be >= 2 (tournament requires at least 2 competitors).
//   - rng must not be nil (required for random competitor selection).
//
// Returns:
//
//	error - non-nil if configuration is invalid, nil if all invariants hold.
func (ts *TournamentSelection) Validate() error {
	if ts == nil {
		return fmt.Errorf("tournament selection instance is nil")
	}
	if ts.tournamentSize < 2 {
		return fmt.Errorf("%w: got %d", ErrInvalidTournamentSize, ts.tournamentSize)
	}
	if ts.rng == nil {
		return fmt.Errorf("tournament selection: rng must not be nil, ensure WithTournamentSeed was called or construction succeeded")
	}
	return nil
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

// SortByScore sorts a slice of strategies in descending order by score.
// Unevaluated strategies (IsScoreEvaluated() == false) go to the end of the slice.
// Uses stable sort to preserve original ordering among equal-scored strategies.
//
// Args:
//
//	strategies - strategies to sort (modified in-place, may be nil or empty).
func SortByScore(strategies []*mutation.Strategy) {
	sort.SliceStable(strategies, func(i, j int) bool {
		si, sj := strategies[i].Score, strategies[j].Score

		// Unevaluated strategies (score == -1) always sort last.
		if !IsScoreEvaluated(si) && !IsScoreEvaluated(sj) {
			return false
		}
		if !IsScoreEvaluated(si) {
			return false
		}
		if !IsScoreEvaluated(sj) {
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

// --- Deprecated APIs kept for backward compatibility with tests and benchmarks ---

// Deprecated: Kept for backward compatibility with tests and benchmarks.
// TruncationSelection selects the top n individuals by score (elite selection).
type TruncationSelection struct{}

// Deprecated: Kept for backward compatibility with tests and benchmarks.
func NewTruncationSelection() *TruncationSelection {
	return &TruncationSelection{}
}

// Deprecated: Kept for backward compatibility with tests and benchmarks.
func (ts *TruncationSelection) Select(ctx context.Context, population []*mutation.Strategy, n int) ([]*mutation.Strategy, error) {
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

// Deprecated: Kept for backward compatibility with tests and benchmarks.
// RouletteWheelSelection selects individuals with probability proportional to fitness.
type RouletteWheelSelection struct {
	rng *rand.Rand
}

// Deprecated: Kept for backward compatibility with tests and benchmarks.
type RouletteOption func(*RouletteWheelSelection) error

// Deprecated: Kept for backward compatibility with tests and benchmarks.
func WithRouletteSeed(seed int64) RouletteOption {
	return func(rw *RouletteWheelSelection) error {
		rw.rng = rand.New(rand.NewSource(seed)) // #nosec G404 - deterministic selection for reproducibility
		return nil
	}
}

// Deprecated: Kept for backward compatibility with tests and benchmarks.
func NewRouletteWheelSelection(opts ...RouletteOption) (*RouletteWheelSelection, error) {
	rw := &RouletteWheelSelection{
		rng: rand.New(rand.NewSource(rand.Int63())), // #nosec G404 - selection doesn't need crypto rand
	}
	for _, opt := range opts {
		if err := opt(rw); err != nil {
			return nil, fmt.Errorf("apply roulette option: %w", err)
		}
	}
	return rw, nil
}

// Deprecated: Kept for backward compatibility with tests and benchmarks.
func (rw *RouletteWheelSelection) Select(ctx context.Context, population []*mutation.Strategy, n int) ([]*mutation.Strategy, error) {
	if err := validateSelectInputs(ctx, population, n); err != nil {
		return nil, err
	}

	// Filter out unevaluated strategies (score == -1).
	var scored []*mutation.Strategy
	for _, s := range population {
		if IsScoreEvaluated(s.Score) {
			scored = append(scored, s)
		}
	}
	if len(scored) == 0 {
		return nil, ErrSelectionEmptyPopulation
	}

	// Shift scores to non-negative range.
	minScore := findMinScore(scored)
	weights := make([]float64, len(scored))
	totalWeight := 0.0
	for i, s := range scored {
		w := s.Score - minScore + 1e-9 // small epsilon to avoid zero weight
		weights[i] = w
		totalWeight += w
	}

	result := make([]*mutation.Strategy, 0, n)
	for i := 0; i < n; i++ {
		select {
		case <-ctx.Done():
			return result, fmt.Errorf("roulette select %d: %w", i, ctx.Err())
		default:
		}

		spin := rw.rng.Float64() * totalWeight
		cumulative := 0.0
		for j, w := range weights {
			cumulative += w
			if spin <= cumulative {
				result = append(result, scored[j])
				break
			}
		}
		// Fallback: if spin exceeds cumulative due to floating-point, pick last.
		if len(result) <= i {
			result = append(result, scored[len(scored)-1])
		}
	}

	return result, nil
}

// Deprecated: Kept for backward compatibility with tests and benchmarks.
// PickParent selects a single parent from the population using the given selector and RNG.
func PickParent(ctx context.Context, population []*mutation.Strategy, sel Selection, rng *rand.Rand) (*mutation.Strategy, error) {
	if sel == nil {
		var err error
		sel, err = NewTournamentSelection()
		if err != nil {
			return nil, fmt.Errorf("create default tournament selector: %w", err)
		}
	}
	selected, err := sel.Select(ctx, population, 1)
	if err != nil {
		return nil, err
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("no parent selected")
	}
	// Apply RNG-based index selection for reproducibility in tests.
	idx := rng.Intn(len(selected))
	return selected[idx], nil
}

// Deprecated: Kept for backward compatibility with tests and benchmarks.
// sumFloat64 returns the sum of all float64 values in the slice.
func sumFloat64(values []float64) float64 {
	var total float64
	for _, v := range values {
		total += v
	}
	return total
}
