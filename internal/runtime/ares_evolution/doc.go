// Package evolution is the ARES evolution runtime wiring. The package keeps
// its historic name; the directory marks the
// layering.
//
// It holds everything that deploys and judges strategies at runtime:
// lifecycle gates, fitness aggregation over evidence windows, the shadow
// sampler (replay-only today), deployment adapters with
// monitor-and-rollback, guardrails, and the strategy/experience stores the
// planner reads. The genetic machinery itself (genome, patches, GA loop)
// lives next door in evolution/ — wiring vs. engine, not old vs. new.
//
// TRUST ROOT (M-C1 disambiguation): the promote trust root is THIS
// package's StrategyLifecycle gate chain (G1 guardrail → G2 shadow → G3
// eval → arena regression → staging). The v2 engine's CandidatePipeline
// (internal/runtime/evolution) has its own SetStable path but ZERO
// production assembly — it is examples/fixtures-only today; the
// trust-root-boundary test in evolutiontest (TestCandidatePipelineNotInProductionImportGraph)
// keeps it that way, and any future production promotion through v2 MUST
// route through this package's gates (see ARCHITECTURE.md high-risk #3).
//
// UNGATED PATCH PATH: the Coordinator's patch path
// (GenomePopulationAdapter.submitToCoordinator → Coordinator.Evaluate →
// PatchExecutor.Apply) is NOT gated by the StrategyLifecycle chain. The
// Coordinator applies diff patches with its own fitness threshold
// (ApplyFitnessThreshold=70, scale 0-100) and does NOT read G1/G2/G3
// results. This is an explicit design decision: the patch path carries
// structural topology mutations (DAG edges, knowledge config, recovery
// policy) that operate at a different abstraction level than strategy
// promotion. The boundary is enforced by test
// (TestCoordinatorPatchPathNotGatedByLifecycle) and documented here so
// the trust-root declaration above is not misread as covering the patch
// path. A future change that gates the patch path through StrategyLifecycle
// MUST update this declaration and remove the test.
package evolution
