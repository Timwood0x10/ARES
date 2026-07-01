package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// GACfg configures a genetic-algorithm evolution demo run.
//
// Mode selection (mutually exclusive):
//   - Dream=true  → Scenario 5: single Dream Cycle (mutate→test→select→record)
//   - Wired=true  → Scenario 7: WiredEvolutionSystem high-level API
//   - both false  → Scenario 6: standard Population-based multi-gen GA
type GACfg struct {
	Title                     string
	BaseID                    string
	PopSize, EliteCount, NGen int
	SurvRate, MutRate         float64
	MinMutRate, MaxMutRate    float64
	SelectionStrategy         string
	HistoryMaxSize            int
	Wired                     bool
}

// Predefined configurations for each GA scenario.
var (
	cfgGA    = GACfg{Title: "Scenario 6: GA Evolution (Rank Selection)", BaseID: "ga-root", PopSize: 20, EliteCount: 2, SurvRate: 0.6, MutRate: 0.2, MinMutRate: 0.05, MaxMutRate: 0.5, SelectionStrategy: "rank", HistoryMaxSize: 100, NGen: 15}
	cfgWired = GACfg{Title: "Scenario 7: Wired System (SUS Selection)", BaseID: "wired-root", PopSize: 10, EliteCount: 1, SurvRate: 0.5, MutRate: 0.3, MinMutRate: 0.05, MaxMutRate: 0.5, SelectionStrategy: "sus", HistoryMaxSize: 100, NGen: 10, Wired: true}
)

// EvolutionConfigFromFile mirrors internal/config.EvolutionConfig for YAML parsing
// without importing the internal package from examples.
type EvolutionConfigFromFile struct {
	Enabled           bool    `yaml:"enabled"`
	PopulationSize    int     `yaml:"population_size"`
	EliteCount        int     `yaml:"elite_count"`
	SurvivalRate      float64 `yaml:"survival_rate"`
	MutationRate      float64 `yaml:"mutation_rate"`
	MinMutationRate   float64 `yaml:"min_mutation_rate"`
	MaxMutationRate   float64 `yaml:"max_mutation_rate"`
	Generations       int     `yaml:"generations"`
	BreedingPoolRatio float64 `yaml:"breeding_pool_ratio"`
	SelectionStrategy string  `yaml:"selection_strategy"`
	HistoryMaxSize    int     `yaml:"history_max_size"`
	MinInterval       string  `yaml:"min_interval"`
}

// loadProjectEvolutionConfig attempts to find and parse evolution config from
// standard project locations. It checks (in order):
//  1. Environment variable EVOLUTION_ENABLED=true
//  2. ./config/config.yaml  (project root relative)
//  3. ../config/config.yaml (examples dir relative)
//
// Returns the parsed EvolutionConfig or an error if not found/unparseable.
func loadProjectEvolutionConfig() (*EvolutionConfigFromFile, error) {
	if os.Getenv("EVOLUTION_ENABLED") == "true" {
		return &EvolutionConfigFromFile{Enabled: true}, nil
	}

	locations := []string{
		"config/config.yaml",
		"../config/config.yaml",
		"../../config.yaml",
		"examples/autonomous-evolution/config/config.yaml",
	}

	for _, loc := range locations {
		data, err := os.ReadFile(loc)
		if err != nil {
			continue
		}

		var raw struct {
			Evolution *EvolutionConfigFromFile `yaml:"evolution"`
		}
		if err := yaml.Unmarshal(data, &raw); err != nil {
			continue
		}

		if raw.Evolution != nil && raw.Evolution.Enabled {
			return raw.Evolution, nil
		}
		return &EvolutionConfigFromFile{Enabled: false}, nil
	}

	return nil, fmt.Errorf("no config file found with evolution settings")
}

// mergeGACfg merges project-level evolution config values into GACfg defaults.
func mergeGACfg(base GACfg, proj *EvolutionConfigFromFile) GACfg {
	out := base
	if proj.PopulationSize > 0 {
		out.PopSize = proj.PopulationSize
	}
	if proj.EliteCount > 0 {
		out.EliteCount = proj.EliteCount
	}
	if proj.SurvivalRate > 0 {
		out.SurvRate = proj.SurvivalRate
	}
	if proj.MutationRate > 0 {
		out.MutRate = proj.MutationRate
	}
	if proj.MinMutationRate > 0 {
		out.MinMutRate = proj.MinMutationRate
	}
	if proj.MaxMutationRate > 0 {
		out.MaxMutRate = proj.MaxMutationRate
	}
	if proj.Generations > 0 {
		out.NGen = proj.Generations
	}
	if proj.SelectionStrategy != "" {
		out.SelectionStrategy = proj.SelectionStrategy
	}
	if proj.HistoryMaxSize > 0 {
		out.HistoryMaxSize = proj.HistoryMaxSize
	}
	return out
}

func statusStr(enabled bool) string {
	if enabled {
		return "✓ ON "
	}
	return "✗ OFF"
}
