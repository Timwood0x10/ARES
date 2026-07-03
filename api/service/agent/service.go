// Package agent provides the public API for agent lifecycle management.
//
// Deprecated: Agent service is not yet fully wired to the internal agents
// system. Use ares_runtime.Manager or internal/agents directly until
// api/service/agent is completed.
package agent

// Service is a placeholder for agent operations.
// Full implementation requires bootstrap-level wiring.
type Service struct{}

// New creates a new agent service.
func New() *Service {
	return &Service{}
}

// ListAgents returns the list of agent IDs managed by the runtime.
func (s *Service) ListAgents() []string {
	return nil
}
