package agentfabric

import (
	"context"

	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/core/models"
)

// SubAgentCognition adapts a sub.Agent (the production quantum executor) to
// the agentfabric.Cognition contract. This is the default Cognition
// implementation in A1 — it delegates to the existing production executor so
// a spawned agent is immediately executable with the same behavior as the
// existing sub agents (StepOutcome semantics match by construction).
//
// There is deliberately no second copy of the tool-loop logic here:
// code_rules_v2 §5.1 ("同一语义只允许一条生产执行路径，禁止并存两套执行循环").
// The tool-loop code (chatStep/decodeChatStepState/renderPrompt) lives in the
// sub package and is THE production execution path. When sub's standalone-agent
// positioning is retired (aresos-agentos-plan phase C), the tool-loop code
// relocates into agentfabric and this adapter is removed.
//
// TODO(tech-debt): before phase C, inline the sub executor's tool loop here
// and delete the sub package's taskExecutor. Until then, keep a single
// execution loop via this adapter.
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
