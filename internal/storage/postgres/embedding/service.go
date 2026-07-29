// Package embedding provides vector embedding functionality with caching.
//
// The EmbeddingService interface is defined in the public package
// api/embedding. This internal package provides the EmbeddingClient
// implementation, which satisfies api/embedding.EmbeddingService.
//
// Importing the public interface here lets internal callers depend on the
// storage-agnostic contract while still using the PostgreSQL-backed
// implementation provided below.
package embedding

import (
	"github.com/Timwood0x10/ares/api/embedding"
)

// Ensure EmbeddingClient implements the public EmbeddingService interface.
var _ embedding.EmbeddingService = (*EmbeddingClient)(nil)
