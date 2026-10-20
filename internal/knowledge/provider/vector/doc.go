// Package vector implements a GraphProvider for any VectorStore backend.
// By depending on the storage.VectorStore interface, this single provider
// works with PostgreSQL+pgvector, Qdrant, Milvus, SQLite-vec, in-memory,
// and any future backend that implements the interface — covering ~60% of
// vector database types with zero code changes.
package vector
