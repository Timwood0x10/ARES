package client

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
)

type workflowTestAgent struct {
	id string
}

func (a *workflowTestAgent) ID() string { return a.id }

func (a *workflowTestAgent) Type() models.AgentType { return models.AgentType("test") }

func (a *workflowTestAgent) Status() models.AgentStatus { return models.AgentStatusReady }

func (a *workflowTestAgent) Start(context.Context) error { return nil }

func (a *workflowTestAgent) Stop(context.Context) error { return nil }

func (a *workflowTestAgent) Process(_ context.Context, input any) (any, error) {
	return "processed:" + input.(string), nil
}

func (a *workflowTestAgent) ProcessStream(context.Context, any) (<-chan base.AgentEvent, error) {
	events := make(chan base.AgentEvent)
	close(events)
	return events, nil
}

func TestWorkflowClientExecute_UsesUnifiedRunner(t *testing.T) {
	t.Parallel()

	client, err := NewClient(&Config{})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	workflowClient, err := NewWorkflowClient(client)
	if err != nil {
		t.Fatalf("NewWorkflowClient() error = %v", err)
	}
	if err := workflowClient.registry.Register("test", func(context.Context, interface{}) (base.Agent, error) {
		return &workflowTestAgent{id: "test"}, nil
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	workflowDef := &engine.Workflow{
		ID: "client-runner",
		Steps: []*engine.Step{
			{ID: "one", AgentType: "test"},
			{
				ID:        "two",
				AgentType: "test",
				DependsOn: []string{"one"},
				Input:     "{{.one}}",
			},
		},
	}

	result, err := workflowClient.Execute(context.Background(), workflowDef, "request")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Status != engine.WorkflowStatusCompleted {
		t.Fatalf("result status = %q", result.Status)
	}
	if result.Output["one"] != "processed:request" {
		t.Fatalf("one output = %#v", result.Output["one"])
	}
	if result.Output["two"] != "processed:processed:request" {
		t.Fatalf("two output = %#v", result.Output["two"])
	}
}

func TestWorkflowClientExecute_RejectsNilWorkflow(t *testing.T) {
	t.Parallel()

	client, err := NewClient(&Config{})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	workflowClient, err := NewWorkflowClient(client)
	if err != nil {
		t.Fatalf("NewWorkflowClient() error = %v", err)
	}
	if _, err := workflowClient.Execute(context.Background(), nil, "request"); err == nil {
		t.Fatal("expected nil workflow error")
	}
}
