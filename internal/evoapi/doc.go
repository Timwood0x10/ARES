// Package evoapi provides the legacy evolution API.
//
// Deprecation notice (A3, 0.3.2): this package exposes the DreamCycle
// orchestrator, GA Population, mutation, and promotion subsystems. In
// production, DreamCycle is hard-coded off (EnableDreamCycle=false in
// ares_bootstrap/bootstrap_evolution.go) and the real evolution path runs
// through internal/runtime/ares_evolution (genome_wiring_* series). None of
// the methods exposed here are reachable from the production execution path.
//
// The production-observable surface (fitness curves, population stats,
// generation active state) is exposed through NewGenomeWiringView (A3,
// 0.3.2) — a read-only view over the live GenomePopulationAdapter. External
// callers should prefer GenomeWiringView for observing the production
// evolution path and treat this package's DreamCycle/Population types as
// read-only legacy.
//
// Migration note: scheduled to fold into github.com/Timwood0x10/ares/sdk
// (removal targeted for v0.5.0); internal callers remain during the window.
package evoapi
