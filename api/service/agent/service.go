// Package agent provides the public API for agent lifecycle management.
package agent

// Service is a placeholder for agent operations.
// Full implementation requires bootstrap-level wiring of internal/agents.
type Service struct {
	inner CoreRuntime
}

// CoreRuntime provides access to the runtime for agent operations.
type CoreRuntime interface {
	ListAgents() []string
}

// New creates a new agent service with the given runtime.
func New(rt CoreRuntime) *Service {
	return &Service{inner: rt}
}
