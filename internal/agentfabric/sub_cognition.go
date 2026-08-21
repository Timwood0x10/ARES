package agentfabric

import (
	"context"

	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/core/models"
)

// SubAgentCognition adapts a sub.Agent (the legacy quantum executor) to the
// agentfabric.Cognition contract. It is the LEGACY-ONLY cognition: it is used
// solely on the leader flip path (wireKernelLifecycle,
// kernel.leader_enabled=true / kernel.policy=legacy), where the configured
// sub-agents' own executor must remain the execution body.
//
// The PEER production path (createPeerAgents, the default) does NOT use this
// adapter: it spawns fabric agents with ChatCognition
// (internal/agentfabric/chat_cognition.go), the tool-loop execution logic
// MOVED DOWN from the sub package (aresos-agentos-plan A1.4: tool-loop 下沉到
// agentfabric 作为默认实现). StepOutcome semantics match by construction —
// both paths produce Done/Checkpoint/Result.
//
// When the legacy leader path is fully retired (createLeaderAgent removed),
// this adapter and the sub executor retire together.
type SubAgentCognition struct {
	agent sub.Agent
}

// NewSubAgentCognition wraps a sub.Agent as an agentfabric.Cognition.
func NewSubAgentCognition(agent sub.Agent) *SubAgentCognition {
	return &SubAgentCognition{agent: agent}
}

// ExecuteStep delegates to the wrapped sub.Agent's ExecuteStep and converts
// the outcome. Semantics are identical by construction — the same executor
// runs the same quantum.
func (c *SubAgentCognition) ExecuteStep(ctx context.Context, task *models.Task) (*StepOutcome, error) {
	out, err := c.agent.ExecuteStep(ctx, task)
	if err != nil {
		return nil, err
	}
	return &StepOutcome{
		Done:       out.Done,
		Checkpoint: out.Checkpoint,
		Result:     out.Result,
	}, nil
}
