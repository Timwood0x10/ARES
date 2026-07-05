// Package genome provides population management for genetic algorithm evolution.
package genome

import "fmt"

// Population validation errors.
var (
	ErrNilBaseStrategy               = fmt.Errorf("base strategy must not be nil")
	ErrNilMutator                    = fmt.Errorf("mutator must not be nil")
	ErrNilCrosser                    = fmt.Errorf("crosser must not be nil")
	ErrInvalidPopulationSize         = fmt.Errorf("population size must be positive")
	ErrInvalidSurvivalRate           = fmt.Errorf("survival rate must be between 0 and 1")
	ErrInvalidMutationRate           = fmt.Errorf("mutation rate must be between 0 and 1")
	ErrInvalidEliteCount             = fmt.Errorf("elite count must be non-negative and <= population size")
	ErrInvalidBreedingPoolRatio      = fmt.Errorf("breeding pool ratio must be between 0 and 1")
	ErrInvalidMinMutationRate        = fmt.Errorf("min mutation rate must be between 0 and 1")
	ErrInvalidMaxMutationRate        = fmt.Errorf("max mutation rate must be between 0 and 1")
	ErrInvalidMaxStagnantGenerations = fmt.Errorf("max stagnant generations must be non-negative")
	ErrInvalidDiversityThreshold     = fmt.Errorf("diversity threshold must be between 0 and 1")
)

// Selection errors.
var (
	ErrSelectionEmptyPopulation = fmt.Errorf("selection: population must not be empty")
	ErrInvalidSelectionSize     = fmt.Errorf("selection size must be positive")
	ErrInvalidTournamentSize    = fmt.Errorf("tournament size must be at least 2")
	ErrNoSelectorNeeded         = fmt.Errorf("no selector needed for random selection")
)
