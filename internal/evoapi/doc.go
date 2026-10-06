// Package evoapi provides the legacy evolution API.
//
// Deprecation notice (A3, 0.3.2): this package exposes the DreamCycle
// orchestrator, GA Population, mutation, and promotion subsystems. In
// production, DreamCycle is hard-coded off (EnableDreamCycle=false in
// ares_bootstrap/bootstrap_evolution.go) and the real evolution path runs
// through internal/runtime/ares_evolution (genome_wiring_* series). None of
// the methods exposed here are reachable from the production execution path.
//
// The production-observable surface (decision records, fitness curves) lives
// in the genome_wiring_* package and is not yet exposed through this API.
// External callers should treat this package as read-only legacy and migrate
// to the SDK (github.com/Timwood0x10/ares/sdk) for evolution access.
//
// Migration note: scheduled to fold into github.com/Timwood0x10/ares/sdk
// (removal targeted for v0.5.0); internal callers remain during the window.
package evoapi
