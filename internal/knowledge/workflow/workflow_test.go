package workflow

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/compiler"
	"github.com/Timwood0x10/ares/internal/knowledge/planner"
	"github.com/Timwood0x10/ares/internal/knowledge/provider"
	"github.com/Timwood0x10/ares/internal/knowledge/runtime"
)

type testProvider struct {
	name    string
	objects []*knowledge.KnowledgeObject
}

func (p *testProvider) Name() string                           { return p.name }
func (p *testProvider) IntentMatch(_ knowledge.Intent) float64 { return 0.9 }
func (p *testProvider) Stream(_ context.Context, _ knowledge.Intent) (<-chan *knowledge.KnowledgeObject, <-chan error) {
	ch := make(chan *knowledge.KnowledgeObject, len(p.objects))
	errCh := make(chan error, 1)
	go func() {
		defer close(ch)
		defer close(errCh)
		for _, obj := range p.objects {
			ch <- obj
		}
	}()
	return ch, errCh
}

type testPlanner struct{}

func (q *testPlanner) PlanQuery(_ context.Context, req planner.KnowledgeRequirement, _, _ string) (*planner.QueryPlan, error) {
	return &planner.QueryPlan{Query: req.Description, QueryType: planner.QuerySQL, MaxResults: req.MaxResults}, nil
}

func TestKnowledgeAgentAsGraphNode_BuildGraph(t *testing.T) {
	reg := provider.NewProviderRegistry()
	require.NoError(t, reg.Register(&testProvider{
		name: "test",
		objects: []*knowledge.KnowledgeObject{
			{ID: "obj:1", Type: knowledge.ObjectDecision, Summary: "test", Raw: []byte("data"), Confidence: 0.9},
		},
	}))

	sd := planner.NewSourceDiscovery(reg, &testPlanner{})
	rt := runtime.New(planner.NewKnowledgePlanner(), sd, reg, nil, nil, nil)
	comp := compiler.NewDefaultCompiler()

	agent := NewKnowledgeAgent("akf-test", rt, comp, StepConfig{
		Step: StepBuildGraph,
		Goal: "test",
	})
	fn := agent.AsGraphNode()

	state := map[string]any{"input": ""}
	err := fn(context.Background(), state)
	require.NoError(t, err)
	require.Contains(t, state, "output")
	graph, ok := state["output"].(*knowledge.WorkingGraph)
	require.True(t, ok, "output should be *knowledge.WorkingGraph")
	assert.NotNil(t, graph)
}

func TestKnowledgeAgentAsGraphNode_ReadsInput(t *testing.T) {
	reg := provider.NewProviderRegistry()
	require.NoError(t, reg.Register(&testProvider{
		name: "test",
		objects: []*knowledge.KnowledgeObject{
			{ID: "obj:1", Type: knowledge.ObjectDecision, Summary: "test", Raw: []byte("data"), Confidence: 0.9},
		},
	}))

	sd := planner.NewSourceDiscovery(reg, &testPlanner{})
	rt := runtime.New(planner.NewKnowledgePlanner(), sd, reg, nil, nil, nil)
	comp := compiler.NewDefaultCompiler()

	agent := NewKnowledgeAgent("akf-test", rt, comp, StepConfig{
		Step: StepBuildGraph,
		Goal: "custom goal",
	})
	fn := agent.AsGraphNode()

	state := map[string]any{"input": `{"goal": "overridden goal"}`}
	err := fn(context.Background(), state)
	require.NoError(t, err)
	require.Contains(t, state, "output")
}

func TestKnowledgeAgentAsGraphNode_MissingInput(t *testing.T) {
	reg := provider.NewProviderRegistry()
	require.NoError(t, reg.Register(&testProvider{
		name: "test",
		objects: []*knowledge.KnowledgeObject{
			{ID: "obj:1", Type: knowledge.ObjectDecision, Summary: "test", Raw: []byte("data"), Confidence: 0.9},
		},
	}))

	sd := planner.NewSourceDiscovery(reg, &testPlanner{})
	rt := runtime.New(planner.NewKnowledgePlanner(), sd, reg, nil, nil, nil)
	comp := compiler.NewDefaultCompiler()

	agent := NewKnowledgeAgent("akf-test", rt, comp, StepConfig{
		Step: StepBuildGraph,
		Goal: "fallback goal",
	})
	fn := agent.AsGraphNode()

	state := map[string]any{}
	err := fn(context.Background(), state)
	require.NoError(t, err)
	require.Contains(t, state, "output")
}

func TestKnowledgeAgentAsGraphNode_CompileStep(t *testing.T) {
	reg := provider.NewProviderRegistry()
	require.NoError(t, reg.Register(&testProvider{
		name: "test",
		objects: []*knowledge.KnowledgeObject{
			{ID: "obj:1", Type: knowledge.ObjectDecision, Summary: "test", Raw: []byte("data"), Confidence: 0.9},
		},
	}))

	sd := planner.NewSourceDiscovery(reg, &testPlanner{})
	rt := runtime.New(planner.NewKnowledgePlanner(), sd, reg, nil, nil, nil)
	comp := compiler.NewDefaultCompiler()

	agent := NewKnowledgeAgent("akf-compile", rt, comp, StepConfig{
		Step: StepCompile,
		Goal: "test",
	})
	fn := agent.AsGraphNode()

	state := map[string]any{"input": ""}
	err := fn(context.Background(), state)
	require.NoError(t, err)
	require.Contains(t, state, "output")
	compiled, ok := state["output"].(*compiler.CompiledContext)
	require.True(t, ok, "compile step should output *compiler.CompiledContext")
	assert.NotNil(t, compiled)
}
