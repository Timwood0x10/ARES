// Package vector implements a GraphProvider for any VectorStore backend.
// By depending on the storage.VectorStore interface, this single provider
// works with PostgreSQL+pgvector, Qdrant, Milvus, SQLite-vec, in-memory,
// and any future backend that implements the interface — covering ~60% of
// vector database types with zero code changes.
//
// Beta: this package is part of the AKG (Autonomous Knowledge Graph)
// subsystem and is currently BETA. The API is not yet stable and may
// change between minor releases. Do not depend on it in production
// without pinning a version. Feedback welcome.
package vector
