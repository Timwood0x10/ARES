// registry.go holds the compat layer's package-level sentinel errors.
// The package documentation (including the boundary rules and the audit of what
// is actually wired) lives in doc.go.
package compat

import "errors"

// Sentinel errors for the compat layer. Sub-packages define their own
// subsystem-named equivalents (compat/llm.ErrNotFound and friends); these are
// the generic forms.
var (
	// ErrNotFound is returned when a requested component is not registered.
	ErrNotFound = errors.New("compat: component not found")
	// ErrAlreadyRegistered is returned when registering a duplicate name.
	ErrAlreadyRegistered = errors.New("compat: component already registered")
)
