// Package store wraps a knowledge.KnowledgeStore as a GraphProvider so the
// AKF KnowledgeRuntime can read back facts distilled and persisted by the AKG
// pipeline. This closes the 0.2.9 write→read loop: distillation writes active
// objects into the store, and the StoreProvider streams them back out as a
// KnowledgeObject source for retrieval and prompt injection.
package store
