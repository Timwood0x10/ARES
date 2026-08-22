// Deprecated: use github.com/Timwood0x10/ares/sdk instead. Knowledge
// management is superseded by the sdk KnowledgeStore entry points; this
// package is kept only for migration.
// Package knowledge provides the public HTTP API for AKF.
//
// Endpoints:
//
//	POST /kg/build     — build a WorkingGraph from a goal
//	POST /kg/context   — build + compile into LLM-ready formats
//	POST /kg/query     — query knowledge via Intent → Graph → Compile
//	POST /kg/distill   — distill content into KnowledgeObjects
//
// Beta: this package is part of the AKG (Autonomous Knowledge Graph)
// subsystem and is currently BETA. The API is not yet stable and may
// change between minor releases. Do not depend on it in production
// without pinning a version. Feedback welcome.
package knowledge
