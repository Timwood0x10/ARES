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
package evolution
