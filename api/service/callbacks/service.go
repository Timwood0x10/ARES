// Package callbacks provides the public API for event callback registration.
package callbacks

import (
	internal "github.com/Timwood0x10/ares/internal/ares_callbacks"
)

// Registry wraps internal ares_callbacks.Registry for public consumption.
type Registry struct {
	inner *internal.Registry
}

// New creates a new callback registry.
func New() *Registry {
	return &Registry{inner: internal.NewRegistry()}
}
