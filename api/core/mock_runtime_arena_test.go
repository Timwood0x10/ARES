package core

import (
	"context"
	"errors"
	"time"
)

// Ensure compile-time checks.
var _ Runtime = (*mockRuntime)(nil)
var _ Arena = (*mockArena)(nil)
var _ Evolution = (*mockEvolution)(nil)
var _ DreamCycle = (*mockDreamCycle)(nil)
var _ WorkflowService = (*mockWorkflowService)(nil)
var _ EventStore = (*mockEventStore)(nil)

// ── mockRuntime ────────────────────────────────────

type mockRuntime struct{}

func (m *mockRuntime) RegisterAgent(_ Agent, _ AgentFactory)                          {}
func (m *mockRuntime) StartAgent(_ context.Context, _ Agent) error                    { return nil }
func (m *mockRuntime) StopAgent(_ context.Context, _ string) error                    { return nil }
func (m *mockRuntime) GetAgent(_ string) Agent                                        { return Agent{} }
func (m *mockRuntime) RestartAgent(_ context.Context, _ string) error                 { return nil }
func (m *mockRuntime) RestoreAgent(_ context.Context, _ string, _ AgentFactory) error { return nil }
func (m *mockRuntime) NotifyAgentDead(_ string, _ string)                             {}
func (m *mockRuntime) Start(_ context.Context) error                                  { return nil }
func (m *mockRuntime) Stop() error                                                    { return nil }
func (m *mockRuntime) Stats() RuntimeStats                                            { return RuntimeStats{} }

// ── mockArena ──────────────────────────────────────

type mockArena struct{}

func (m *mockArena) InjectFault(_ context.Context, _ FaultType, _ string) error { return nil }
func (m *mockArena) RunScenario(_ context.Context, _ string) (*ArenaReport, error) {
	return nil, errors.New("not implemented")
}
func (m *mockArena) RunRandom(_ context.Context, _ time.Duration) (*ArenaReport, error) {
	return nil, errors.New("not implemented")
}
func (m *mockArena) Score() *ResilienceScore { return nil }
func (m *mockArena) ListAgents() []string    { return nil }
func (m *mockArena) Stop() error             { return nil }

// ── mockEvolution ──────────────────────────────────

type mockEvolution struct{}

func (m *mockEvolution) Evolve(_ context.Context, _ int) (*EvolutionResult, error) {
	return nil, errors.New("not implemented")
}
func (m *mockEvolution) RunIdleEvolution(_ context.Context, _ int) error { return nil }
func (m *mockEvolution) LatestReport() (string, error)                   { return "", nil }
func (m *mockEvolution) BestStrategy() (*EvolutionStrategy, error) {
	return nil, errors.New("not implemented")
}
func (m *mockEvolution) Stats() (*EvolutionStats, error) { return nil, errors.New("not implemented") }
func (m *mockEvolution) Lineages() ([]LineageRecord, error) {
	return nil, errors.New("not implemented")
}
func (m *mockEvolution) SaveBestStrategy(_ string) error { return nil }
func (m *mockEvolution) Shutdown()                       {}

// ── mockDreamCycle ─────────────────────────────────

type mockDreamCycle struct{}

func (m *mockDreamCycle) Start(_ context.Context) error { return nil }
func (m *mockDreamCycle) Stop() error                   { return nil }
func (m *mockDreamCycle) Trigger(_ context.Context) (*EvolutionResult, error) {
	return nil, errors.New("not implemented")
}
func (m *mockDreamCycle) Status() DreamCycleStatus { return DreamCycleStatus{} }

// ── mockWorkflowService ────────────────────────────

type mockWorkflowService struct{}

func (m *mockWorkflowService) Execute(_ context.Context, _ *WorkflowRequest) (*WorkflowResponse, error) {
	return nil, errors.New("not implemented")
}
func (m *mockWorkflowService) ExecuteStream(_ context.Context, _ *WorkflowRequest) (<-chan WorkflowEvent, error) {
	return nil, errors.New("not implemented")
}
func (m *mockWorkflowService) ListWorkflows(_ context.Context) ([]*WorkflowSummary, error) {
	return nil, errors.New("not implemented")
}
func (m *mockWorkflowService) GetWorkflow(_ context.Context, _ string) (*WorkflowDefinition, error) {
	return nil, errors.New("not implemented")
}

// ── mockEventStore ─────────────────────────────────

type mockEventStore struct{}

func (m *mockEventStore) Append(_ context.Context, _ string, _ []interface{}, _ int64) error {
	return nil
}
func (m *mockEventStore) Read(_ context.Context, _ string, _ int64, _ int) ([]interface{}, error) {
	return nil, errors.New("not found")
}
func (m *mockEventStore) Close() error { return nil }
