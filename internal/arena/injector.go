package arena

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"goagentx/internal/runtime"
)

var (
	// ErrRuntimeNil indicates the runtime dependency was not provided.
	ErrRuntimeNil = errors.New("arena: runtime is nil")
	// ErrDAGNil indicates the DAG dependency was not provided.
	ErrDAGNil = errors.New("arena: dag is nil")
	// ErrLeaderNotFound indicates no agent with type "leader" was found.
	ErrLeaderNotFound = errors.New("arena: leader agent not found")
)

// RuntimeProvider is the subset of runtime capabilities needed by the arena.
type RuntimeProvider interface {
	StopAgent(ctx context.Context, agentID string) error
	ListAgents() []runtime.AgentInfo
}

// DAGProvider is the subset of DAG mutation capabilities needed by the arena.
type DAGProvider interface {
	RemoveNode(ctx context.Context, id string) error
	RemoveEdge(ctx context.Context, from, to string) error
}

// Injector wraps existing runtime/DAG APIs to inject chaos.
// It does NOT implement recovery; the existing resurrection plugin and
// failover handle that automatically.
type Injector struct {
	runtime RuntimeProvider
	dag     DAGProvider
}

// NewInjector creates an Injector with the given dependencies.
// Either dependency may be nil; calling the corresponding methods will return
// ErrRuntimeNil or ErrDAGNil in that case.
func NewInjector(rt RuntimeProvider, dag DAGProvider) *Injector {
	return &Injector{
		runtime: rt,
		dag:     dag,
	}
}

// KillAgent stops an agent by ID via the runtime.
func (in *Injector) KillAgent(ctx context.Context, id string) error {
	if in.runtime == nil {
		return ErrRuntimeNil
	}
	slog.Warn("arena: killing agent", "agent_id", id)
	if err := in.runtime.StopAgent(ctx, id); err != nil {
		return fmt.Errorf("arena: kill agent %s: %w", id, err)
	}
	return nil
}

// KillLeader finds the leader agent and stops it.
func (in *Injector) KillLeader(ctx context.Context) (string, error) {
	if in.runtime == nil {
		return "", ErrRuntimeNil
	}
	leaderID := ""
	for _, info := range in.runtime.ListAgents() {
		if info.Type == "leader" {
			leaderID = info.ID
			break
		}
	}
	if leaderID == "" {
		return "", ErrLeaderNotFound
	}
	slog.Warn("arena: assassinating leader", "agent_id", leaderID)
	if err := in.runtime.StopAgent(ctx, leaderID); err != nil {
		return "", fmt.Errorf("arena: kill leader %s: %w", leaderID, err)
	}
	return leaderID, nil
}

// RemoveNode removes a node from the DAG.
func (in *Injector) RemoveNode(ctx context.Context, id string) error {
	if in.dag == nil {
		return ErrDAGNil
	}
	slog.Warn("arena: removing node from DAG", "node_id", id)
	if err := in.dag.RemoveNode(ctx, id); err != nil {
		return fmt.Errorf("arena: remove node %s: %w", id, err)
	}
	return nil
}

// RemoveEdge removes a directed edge from the DAG.
func (in *Injector) RemoveEdge(ctx context.Context, from, to string) error {
	if in.dag == nil {
		return ErrDAGNil
	}
	slog.Warn("arena: removing edge from DAG", "from", from, "to", to)
	if err := in.dag.RemoveEdge(ctx, from, to); err != nil {
		return fmt.Errorf("arena: remove edge %s->%s: %w", from, to, err)
	}
	return nil
}
