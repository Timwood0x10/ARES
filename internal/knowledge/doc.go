// Package knowledge provides core types for the ARES Knowledge Fabric (AKF),
// the Agent Knowledge Graph (AKG).
//
// KnowledgeObject is the universal knowledge representation. Every external
// data source (PostgreSQL, MySQL, Git, Memory, Code, etc.) is converted into
// KnowledgeObject via GraphProvider. The three-layer structure (Raw → Normalized
// → Summary) preserves original data while optimizing for LLM consumption.
//
// Beta: this package is part of the AKG (Autonomous Knowledge Graph)
// subsystem and is currently BETA. The API is not yet stable and may
// change between minor releases. Do not depend on it in production
// without pinning a version. Feedback welcome.
package knowledge
