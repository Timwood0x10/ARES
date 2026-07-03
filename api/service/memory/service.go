// Package memory provides the public API for memory management.
package memory

import (
	internal "github.com/Timwood0x10/ares/internal/ares_memory"
)

// Config re-exports internal's memory config.
type Config = internal.MemoryConfig

// Service wraps internal/ares_memory.MemoryManager for public consumption.
type Service struct {
	inner internal.MemoryManager
}

// New creates a new memory service with the given config.
// When cfg is nil, DefaultMemoryConfig() is used.
func New(cfg *Config) (*Service, error) {
	if cfg == nil {
		cfg = internal.DefaultMemoryConfig()
	}
	mgr, err := internal.NewMemoryManager(cfg)
	if err != nil {
		return nil, err
	}
	return &Service{inner: mgr}, nil
}
