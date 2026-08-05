// Package planner provides knowledge planning, source discovery, and query planning.
//
// Architecture (v3):
//
//	KnowledgePlanner → KnowledgeRequirement → SourceDiscovery → QueryPlanner → Provider
//
// Planner outputs only "what knowledge is needed" (not "where to get it"),
// SourceDiscovery maps needs to providers, and QueryPlanner translates
// needs into concrete queries (SQL, Cypher, Vector, etc.).
//
// Beta: this package is part of the AKG (Autonomous Knowledge Graph)
// subsystem and is currently BETA. The API is not yet stable and may
// change between minor releases. Do not depend on it in production
// without pinning a version. Feedback welcome.
package planner
