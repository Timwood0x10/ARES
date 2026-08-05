// Package linker provides pluggable Relation generators for AKF.
// Each LinkerPlugin implements runtime.Linker and generates domain-specific
// edges (architecture, decision, similarity, timeline, etc.) for the
// KnowledgeGraph.
//
// Beta: this package is part of the AKG (Autonomous Knowledge Graph)
// subsystem and is currently BETA. The API is not yet stable and may
// change between minor releases. Do not depend on it in production
// without pinning a version. Feedback welcome.
package linker
