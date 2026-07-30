// Package store wraps a knowledge.KnowledgeStore as a GraphProvider so the
// AKF KnowledgeRuntime can read back facts distilled and persisted by the AKG
// pipeline. This closes the 0.2.9 write→read loop: distillation writes active
// objects into the store, and the StoreProvider streams them back out as a
// KnowledgeObject source for retrieval and prompt injection.
//
// Beta: this package is part of the AKG (Autonomous Knowledge Graph)
// subsystem and is currently BETA. The API is not yet stable and may
// change between minor releases. Do not depend on it in production
// without pinning a version. Feedback welcome.
package store
