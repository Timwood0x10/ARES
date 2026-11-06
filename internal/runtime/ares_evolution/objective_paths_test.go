package evolution

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	aresconfig "github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
	"github.com/Timwood0x10/ares/internal/runtime/evolution/coordinator"
	"github.com/Timwood0x10/ares/internal/runtime/evolution/diff"
	evogenome "github.com/Timwood0x10/ares/internal/runtime/evolution/genome"
	"github.com/Timwood0x10/ares/internal/runtime/evolution/patch"
)

// objRecordingCoordinator captures what the adapter submits. It exists because the real
// UngatedPatcher needs a PolicyGenome and a patch registry just to be constructed, which made
// "no proposals were submitted" unobservable — and therefore unfalsifiable.
type objRecordingCoordinator struct {
	mu        sync.Mutex
	proposals []coordinator.PatchProposal
}

func (c *objRecordingCoordinator) Submit(p coordinator.PatchProposal) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.proposals = append(c.proposals, p)
}
func (c *objRecordingCoordinator) Policy() coordinator.PolicyGenome {
	return coordinator.PolicyGenome{}
}
func (c *objRecordingCoordinator) DecisionHistory() []coordinator.PatchDecision { return nil }
func (c *objRecordingCoordinator) Evaluate(context.Context)                     {}
func (c *objRecordingCoordinator) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.proposals)
}

// objStubGenome produces a stable snapshot so the diff engine has something to compare.
type objStubGenome struct{ name string }

func (g objStubGenome) Name() string { return g.name }
func (g objStubGenome) Mutate(context.Context, int) ([]evogenome.Genome, error) {
	return []evogenome.Genome{objStubGenome{name: "test-genome"}}, nil
}
func (g objStubGenome) Snapshot(context.Context) (any, error) { return "snapshot", nil }

// objStubDiffer returns one patch per diff so the coordinator actually receives a proposal.
type objStubDiffer struct{ name string }

func (d objStubDiffer) Name() string { return d.name }
func (d objStubDiffer) Diff(context.Context, any, any) ([]patch.RuntimePatch, error) {
	return []patch.RuntimePatch{{Type: patch.PatchInsertNode, Target: "test-target", Value: "value"}}, nil
}

// gatedAdapter builds an adapter whose runtime-patch path is fully wired: coordinator, diff
// registry with a differ, and genome registry with a matching genome. The objective is then the
// only thing that can change the outcome.
func gatedAdapter(t *testing.T, objective string) (*GenomePopulationAdapter, *objRecordingCoordinator) {
	t.Helper()
	ctx := context.Background()
	base := &mutation.Strategy{
		ID: "objective-test", Version: 1,
		Params: map[string]any{"temperature": 0.7}, Score: 50.0, CreatedAt: time.Now(),
	}
	mut := &mockGenomeMutator{}
	crosser, err := genome.NewCrossover(genome.WithSeed(11))
	require.NoError(t, err)
	pop, err := genome.NewPopulation(ctx, base, mut,
		genome.WithPopulationSize(6), genome.WithEliteCount(1))
	require.NoError(t, err)
	adapter, err := NewGenomePopulationAdapter(pop, mut, crosser)
	require.NoError(t, err)

	coord := &objRecordingCoordinator{}
	genomeReg := evogenome.NewRegistry()
	require.NoError(t, genomeReg.Register(objStubGenome{name: "test-genome"}))
	diffReg := diff.NewRegistry()
	require.NoError(t, diffReg.Register(objStubDiffer{name: "test-genome"}))

	adapter.coordinator = coord
	adapter.diffReg = diffReg
	adapter.genomeReg = genomeReg
	adapter.objective = objective
	return adapter, coord
}

// TestGAObjective_GateDecidesWithEveryDependencyPresent asserts the decision Run() branches on,
// with coordinator and both registries wired so the objective is the only variable.
//
// A test that counted submitted proposals would be vacuous here: with an empty genome registry
// submitToCoordinator produces nothing under ANY objective, so it would pass with the gate
// deleted. That is why this asserts the gate itself and the wiring test below proves the gate is
// fed the configured value.
func TestGAObjective_GateDecidesWithEveryDependencyPresent(t *testing.T) {
	cases := []struct {
		objective string
		want      bool
	}{
		{EvolutionObjectiveStrategy, false},
		{EvolutionObjectiveRuntime, true},
		{EvolutionObjectiveBoth, true},
		{"", false},         // empty normalises to strategy
		{"nonsense", false}, // unknown must not widen the gate
	}
	for _, tc := range cases {
		t.Run(tc.objective, func(t *testing.T) {
			adapter, _ := gatedAdapter(t, tc.objective)
			assert.Equal(t, tc.want, adapter.shouldSubmitRuntimePatches())
		})
	}
}

// TestGAObjective_Predicates pins the three-value mapping, including the fallback: an empty or
// unknown objective normalises to strategy, because falling back to "both" would let a typo
// re-enable live patching.
func TestGAObjective_Predicates(t *testing.T) {
	cases := []struct {
		objective string
		runtime   bool
		strategy  bool
	}{
		{EvolutionObjectiveStrategy, false, true},
		{EvolutionObjectiveRuntime, true, false},
		{EvolutionObjectiveBoth, true, true},
		{"", false, true},
		{"nonsense", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.objective, func(t *testing.T) {
			s := &WiredEvolutionSystem{Objective: tc.objective}
			assert.Equal(t, tc.runtime, s.RuntimeObjective())
			assert.Equal(t, tc.strategy, s.StrategyObjective())

			a := &GenomePopulationAdapter{objective: tc.objective}
			assert.Equal(t, tc.runtime, a.runtimeObjective())
			assert.Equal(t, tc.strategy, a.strategyObjective())
		})
	}
}

// TestGAObjective_AdapterInheritsTheSystemValue is the wiring half: NewWiredEvolutionSystem must
// hand its normalised objective to the adapter that actually runs Run(). If it does not, the
// system reports one objective while the path that submits acts on another.
func TestGAObjective_AdapterInheritsTheSystemValue(t *testing.T) {
	base := &mutation.Strategy{ID: "wire", Params: map[string]any{"temperature": 0.7}, Score: 50.0, CreatedAt: time.Now()}
	sys, err := NewWiredEvolutionSystem(base, SystemConfig{
		Objective:       EvolutionObjectiveBoth,
		GenomeConfig:    GenomeConfig{PopulationSize: 4, EliteCount: 1},
		MutationConfig:  MutationConfig{},
		SchedulerConfig: SchedulerConfig{},
	})
	if err != nil {
		t.Skipf("full system construction needs more wiring: %v", err)
	}
	require.NotNil(t, sys.PopAdapter)
	assert.Equal(t, EvolutionObjectiveBoth, sys.PopAdapter.objective)
}

// TestGAObjective_RunSubmitsNothingUnderStrategy is the behavioural claim, not a predicate: with a
// working diff pipeline and a coordinator that counts, a generation under objective=strategy must
// deliver zero proposals.
func TestGAObjective_RunSubmitsNothingUnderStrategy(t *testing.T) {
	adapter, coord := gatedAdapter(t, EvolutionObjectiveStrategy)

	require.NoError(t, adapter.Run(context.Background()))

	assert.Equal(t, 0, coord.count(),
		"a strategy-only generation must not submit runtime patches")
}

// TestGAObjective_RunSubmitsUnderBoth is the CONTROL. Without it the assertion above could pass
// because the fixture never produces a patch at all — which is exactly what happened before the
// diff registry and genome were wired here.
func TestGAObjective_RunSubmitsUnderBoth(t *testing.T) {
	adapter, coord := gatedAdapter(t, EvolutionObjectiveBoth)

	require.NoError(t, adapter.Run(context.Background()))

	assert.Greater(t, coord.count(), 0,
		"the control must submit at least one patch, or the strategy-only test proves nothing")
}

// TestObjectiveConstantsMatchTheConfigLayer guards a deliberate duplication: this package mirrors
// the three objective names rather than importing the config layer. If they drift, a config that
// validates as "runtime" would normalise to "strategy" here and silently disable the path the
// deployment asked for.
func TestObjectiveConstantsMatchTheConfigLayer(t *testing.T) {
	assert.Equal(t, aresconfig.EvolutionObjectiveStrategy, EvolutionObjectiveStrategy)
	assert.Equal(t, aresconfig.EvolutionObjectiveRuntime, EvolutionObjectiveRuntime)
	assert.Equal(t, aresconfig.EvolutionObjectiveBoth, EvolutionObjectiveBoth)
}

// TestShouldSubmitRuntimePatches_DependencyHalf pins the other half of the gate: a missing
// coordinator or registry must close the path even when the objective permits it.
//
// Run and RunIdleEvolution used to spell this condition out separately, so one could be fixed
// while the other kept submitting. They now call this function; the table is what proves it says
// what both paths need.
func TestShouldSubmitRuntimePatches_DependencyHalf(t *testing.T) {
	withDeps := func() *WiredEvolutionSystem {
		return &WiredEvolutionSystem{
			Objective:   EvolutionObjectiveBoth,
			Coordinator: &coordinator.UngatedPatcher{},
			DiffReg:     diff.NewRegistry(),
			GenomeReg:   evogenome.NewRegistry(),
		}
	}

	cases := []struct {
		name      string
		mutate    func(*WiredEvolutionSystem)
		objective string
		want      bool
	}{
		{"both with every dependency", func(*WiredEvolutionSystem) {}, EvolutionObjectiveBoth, true},
		{"runtime with every dependency", func(*WiredEvolutionSystem) {}, EvolutionObjectiveRuntime, true},
		{"strategy ignores the dependencies", func(*WiredEvolutionSystem) {}, EvolutionObjectiveStrategy, false},
		{"empty normalises to strategy", func(*WiredEvolutionSystem) {}, "", false},
		{"no coordinator", func(s *WiredEvolutionSystem) { s.Coordinator = nil }, EvolutionObjectiveBoth, false},
		{"no diff registry", func(s *WiredEvolutionSystem) { s.DiffReg = nil }, EvolutionObjectiveBoth, false},
		{"no genome registry", func(s *WiredEvolutionSystem) { s.GenomeReg = nil }, EvolutionObjectiveBoth, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := withDeps()
			s.Objective = tc.objective
			tc.mutate(s)
			assert.Equal(t, tc.want, s.ShouldSubmitRuntimePatches())
		})
	}

	assert.False(t, (*WiredEvolutionSystem)(nil).ShouldSubmitRuntimePatches(),
		"a nil system must not look permissive")
}

// objSystemCoordinator is a recording coordinator that also satisfies the two setters bootstrap
// uses, so a WiredEvolutionSystem can be built around it in a test.
type objSystemCoordinator struct {
	objRecordingCoordinator
}

func (c *objSystemCoordinator) SetDeployer(coordinator.PatchDeployer) {}
func (c *objSystemCoordinator) SetApplyGate(coordinator.ApplyGate)    {}

// idleSystem wires a system whose only submission path is RunIdleEvolution: the recording
// coordinator is the SYSTEM's, while the adapter carries its own recorder from gatedAdapter, so a
// count on the returned coordinator is idle-path submissions only.
func idleSystem(t *testing.T, objective string) (*WiredEvolutionSystem, *objSystemCoordinator) {
	t.Helper()
	adapter, _ := gatedAdapter(t, objective)
	coord := &objSystemCoordinator{}
	system := &WiredEvolutionSystem{
		Objective:   objective,
		Population:  adapter.pop,
		PopAdapter:  adapter,
		Coordinator: coord,
		DiffReg:     adapter.diffReg,
		GenomeReg:   adapter.genomeReg,
	}
	return system, coord
}

// TestIdleObjective_StrategySubmitsNothing is the second half of the single-path guarantee, and
// the one that previously had no test at all: the gate existed but nothing could observe whether
// the idle loop honoured it.
func TestIdleObjective_StrategySubmitsNothing(t *testing.T) {
	system, coord := idleSystem(t, EvolutionObjectiveStrategy)

	require.NoError(t, RunIdleEvolution(context.Background(), system, 1))

	assert.Equal(t, 0, coord.count(),
		"the idle loop must not submit runtime patches under objective=strategy")
}

// TestIdleObjective_BothSubmits is the control: without it the assertion above would also hold if
// the idle path simply never produced a patch, which is how the earlier adapter test was vacuous.
func TestIdleObjective_BothSubmits(t *testing.T) {
	system, coord := idleSystem(t, EvolutionObjectiveBoth)

	require.NoError(t, RunIdleEvolution(context.Background(), system, 1))

	assert.Greater(t, coord.count(), 0,
		"the control must submit at least one patch, or the strategy assertion proves nothing")
}
