// Package evoapi provides the legacy evolution API.
//
// Deprecation notice: this package exposes the DreamCycle orchestrator, GA
// Population, mutation, and promotion subsystems. In production, DreamCycle is
// hard-coded off (EnableDreamCycle=false in
// ares_bootstrap/bootstrap_evolution.go) and the real evolution path runs
// through internal/runtime/ares_evolution (genome_wiring_* series). None of
// the methods exposed here are reachable from the production execution path.
//
// CURRENT CALLERS: none. Verified 2026-10-18 — no package inside the module
// imports this one, and `go list -deps ./cmd/ares` does not contain it, so the
// earlier "internal callers remain during the window" is no longer true. The
// window has effectively closed; what is left is the removal itself.
//
// GenomeWiringView is AVAILABLE but also UNUSED: it has zero references outside
// this package. It was documented as the "production-observable surface", and
// that was an intention, not a fact — the production surface currently exposes
// nothing through it. Wiring it is a decision to make, not a behaviour that
// exists; anyone reading this package expecting live observability should know
// that before relying on it.
//
// Migration note: scheduled to fold into github.com/Timwood0x10/ares/sdk
// (removal targeted for v0.5.0). Nothing has been folded yet — `sdk` does not
// import this package. Until that happens, code analysis will keep reporting
// this package as dead code; that report is CORRECT and is not a defect to fix,
// it is the removal still being pending.
package evoapi
