// Package memory provides unified memory management for the StyleAgent framework.
package memory

import "sync"

// ProductionMemoryManager is the config-only memory store used by the evolution
// system's MemoryPatchExecutor as a MemoryConfigStore fallback (see
// NewMinimalMemoryManager) when the live memory manager is disabled or does not
// expose a mutable config.
//
// It formerly carried a PostgreSQL-backed persistence implementation
// (sessions, tasks, RAG retrieval, write buffer). That path had zero production
// constructors — only optional DSN-gated integration tests exercised it — and
// was removed together with postgres.WriteBuffer; only the config-store surface
// consumed by runtime evolution remains.
type ProductionMemoryManager struct {
	// config is the mutable memory configuration evolved via MemoryPatchExecutor.
	config *MemoryConfig

	// mu protects config during patch apply/snapshot.
	mu sync.RWMutex
}

// GetConfig returns the current MemoryConfig pointer.
// The caller must hold the lock (Lock()) before reading the returned config.
// This method implements the MemoryConfigStore interface.
func (m *ProductionMemoryManager) GetConfig() *MemoryConfig {
	return m.config
}

// Lock acquires the exclusive lock protecting the config.
// This method implements the MemoryConfigStore interface.
func (m *ProductionMemoryManager) Lock() {
	m.mu.Lock()
}

// Unlock releases the exclusive lock protecting the config.
// This method implements the MemoryConfigStore interface.
func (m *ProductionMemoryManager) Unlock() {
	m.mu.Unlock()
}

// Ensure ProductionMemoryManager implements MemoryConfigStore.
var _ MemoryConfigStore = (*ProductionMemoryManager)(nil)
