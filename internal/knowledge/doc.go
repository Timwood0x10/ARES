// Package knowledge provides core types for the ARES Knowledge Fabric (AKF),
// the Agent Knowledge Graph (AKG).
//
// KnowledgeObject is the universal knowledge representation. Every external
// data source (PostgreSQL, MySQL, Git, Memory, Code, etc.) is converted into
// KnowledgeObject via GraphProvider. The three-layer structure (Raw → Normalized
// → Summary) preserves original data while optimizing for LLM consumption.
package knowledge
