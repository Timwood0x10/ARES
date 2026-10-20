// Package planner provides knowledge planning, source discovery, and query planning.
//
// Architecture (v3):
//
//	KnowledgePlanner → KnowledgeRequirement → SourceDiscovery → QueryPlanner → Provider
//
// Planner outputs only "what knowledge is needed" (not "where to get it"),
// SourceDiscovery maps needs to providers, and QueryPlanner translates
// needs into concrete queries (SQL, Cypher, Vector, etc.).
package planner
