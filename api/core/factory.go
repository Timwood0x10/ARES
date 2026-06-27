package core

import "time"

// DefaultEvolutionConfig returns a sensible default evolution configuration.
func DefaultEvolutionConfig() *EvolutionConfig {
	return &EvolutionConfig{
		PopulationSize: 20,
		MaxGenerations: 50,
		MutationRate:   0.3,
		CrossoverRate:  0.7,
		EliteCount:     3,
		ScoringMethod:  "hybrid",
	}
}

// DefaultArenaConfig returns a sensible default arena configuration.
func DefaultArenaConfig() *ArenaConfig {
	return &ArenaConfig{
		Duration:   5 * time.Minute,
		FaultTypes: []string{"kill_agent", "network_partition", "latency_spike"},
	}
}

// DefaultDreamCycleConfig returns a sensible default dream cycle configuration.
func DefaultDreamCycleConfig() *DreamCycleConfig {
	return &DreamCycleConfig{
		TriggerThreshold: 0.8,
		MaxCycles:        10,
		CycleTimeout:     30 * time.Minute,
	}
}

// DefaultRuntimeConfig returns a sensible default runtime configuration.
func DefaultRuntimeConfig() *RuntimeConfig {
	return &RuntimeConfig{
		HealthCheckInterval: 5 * time.Second,
		MaxRestartsPerAgent: 0,
		MaxReplayEvents:     1000,
		AgentStopTimeout:    10 * time.Second,
		OverallStopTimeout:  30 * time.Second,
		RestoreTimeout:      15 * time.Second,
	}
}
