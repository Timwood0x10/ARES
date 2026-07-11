//nolint:gosec // GA mutation intentionally uses math/rand (performance, not crypto).
package genome

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"

	"github.com/Timwood0x10/ares/internal/evidence"
	"github.com/Timwood0x10/ares/internal/logger"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
)

var wfLog = logger.Module("genome.workflow")

// Genome name constants.
const (
	WorkflowGenomeName  = "workflow"
	KnowledgeGenomeName = "knowledge"
	SchedulerGenomeName = "scheduler"
	RecoveryGenomeName  = "recovery"
	defaultAgent        = "default"
)

type wfMutationOp int

const (
	wfOpInsertNode wfMutationOp = iota
	wfOpRemoveNode
	wfOpReplaceNode
	wfOpParallelize
	wfOpSerialize
	wfOpSwap
	wfOpSplit
	wfOpMerge
)

var wfOps = []wfMutationOp{wfOpInsertNode, wfOpRemoveNode, wfOpReplaceNode, wfOpParallelize, wfOpSerialize, wfOpSwap, wfOpSplit, wfOpMerge}

// WorkflowGenomeConfig controls the DAG topology evolution behaviour.
type WorkflowGenomeConfig struct {
	// AgentPool is the set of agent types available for inserting new nodes.
	AgentPool []string

	// MaxNodes caps the DAG size to prevent unbounded growth.
	MaxNodes int

	// InsertionRate controls how aggressively new nodes are inserted [0, 1].
	InsertionRate float64

	// PruneRate controls how aggressively low-value nodes are removed [0, 1].
	PruneRate float64

	// EvidenceStore provides execution evidence for fitness evaluation.
	// May be nil; fitness falls back to a constant when nil.
	EvidenceStore *evidence.MemoryStore
}

// DefaultWorkflowGenomeConfig returns a sensible default configuration.
func DefaultWorkflowGenomeConfig() WorkflowGenomeConfig {
	return WorkflowGenomeConfig{
		AgentPool:     []string{defaultAgent},
		MaxNodes:      20,
		InsertionRate: 0.3,
		PruneRate:     0.2,
	}
}

// WorkflowGenome evolves the DAG execution topology.
// Mutation operators directly correspond to MutableDAG operations:
//
//	InsertNode   → AddNode + AddEdge
//	RemoveNode   → RemoveNode + RemoveEdge
//	ReplaceNode  → ReplaceNode
//	Parallelize  → parallel fan-out conversion
//	Serialize    → linear chain conversion
type WorkflowGenome struct {
	dag    *engine.MutableDAG
	config WorkflowGenomeConfig
}

// NewWorkflowGenome creates a new WorkflowGenome wrapping the given DAG.
func NewWorkflowGenome(dag *engine.MutableDAG, config WorkflowGenomeConfig) *WorkflowGenome {
	return &WorkflowGenome{
		dag:    dag,
		config: config,
	}
}

// Name returns the genome identifier.
func (g *WorkflowGenome) Name() string { return WorkflowGenomeName }

// DAG returns the underlying MutableDAG. Used by the Diff Engine to compare snapshots.
func (g *WorkflowGenome) DAG() *engine.MutableDAG { return g.dag }

// Mutate generates n candidate genomes from this parent.
// Each mutation applies one random operator to the DAG topology.
func (g *WorkflowGenome) Mutate(_ context.Context, n int) ([]Genome, error) {
	if n <= 0 {
		return nil, nil
	}

	children := make([]Genome, 0, n)
	for i := 0; i < n; i++ {
		child := g.clone()
		op := wfOps[rand.Intn(len(wfOps))]
		switch op {
		case wfOpInsertNode:
			child.mutateInsertNode()
		case wfOpRemoveNode:
			child.mutateRemoveNode()
		case wfOpReplaceNode:
			child.mutateReplaceNode()
		case wfOpParallelize:
			child.mutateParallelize()
		case wfOpSerialize:
			child.mutateSerialize()
		case wfOpSwap:
			child.mutateSwapNodes()
		case wfOpSplit:
			child.mutateSplitNode()
		case wfOpMerge:
			child.mutateMergeNodes()
		}
		children = append(children, child)
	}
	return children, nil
}

// Crossover recombines this genome with another to produce a child.
func (g *WorkflowGenome) Crossover(_ context.Context, other Genome) (Genome, error) {
	otherWF, ok := other.(*WorkflowGenome)
	if !ok {
		return nil, fmt.Errorf("workflow: crossover incompatible genome type %T", other)
	}

	child := g.clone()
	otherSteps := otherWF.dag.StepIndex()

	// Uniform crossover: randomly replace nodes with the other parent's version.
	for id, step := range otherSteps {
		if rand.Float64() < 0.5 {
			if child.dag.StepIndex()[id] != nil {
				if err := child.dag.ReplaceNode(context.Background(), id, step); err != nil {
					wfLog.Warn("crossover replace failed", "node", id, "error", err)
				}
			} else if child.dag.NodeCount() < child.config.MaxNodes {
				if err := child.dag.AddNode(context.Background(), step); err != nil {
					wfLog.Warn("crossover add failed", "node", id, "error", err)
				}
			}
		}
	}
	return child, nil
}

// Fitness evaluates this genome's quality.
// Uses evidence from the store when available; falls back to 0.5.
func (g *WorkflowGenome) Fitness(ctx context.Context) (float64, error) {
	if g.config.EvidenceStore == nil {
		return 0.5, nil
	}

	evs, err := g.config.EvidenceStore.Query(ctx, evidence.Filter{
		Source: WorkflowGenomeName,
		Limit:  100,
	})
	if err != nil {
		return 0.0, fmt.Errorf("workflow: query evidence: %w", err)
	}

	if len(evs) == 0 {
		return 0.5, nil
	}

	// Simple heuristic: average of numeric payload values.
	var sum float64
	var count int
	for _, ev := range evs {
		if len(ev.Payload) > 0 {
			var v float64
			if err := json.Unmarshal(ev.Payload, &v); err == nil {
				sum += v
				count++
			}
		}
	}
	if count == 0 {
		return 0.5, nil
	}
	fitness := sum / float64(count)

	// Emit fitness evidence so other subsystems can consume GA results.
	_ = g.config.EvidenceStore.Append(ctx, evidence.NewEvidence(
		WorkflowGenomeName,
		evidence.KindFitness,
		fitness,
		evidence.WithMetadata("type", "workflow"),
		evidence.WithMetadata("version", fmt.Sprintf("%d", g.dag.Version())),
	))

	return fitness, nil
}

// Snapshot returns a serializable snapshot of the current DAG state.
// Used by the Diff Engine to compute changes between generations.
func (g *WorkflowGenome) Snapshot(_ context.Context) (any, error) {
	return g.dag.Snapshot(), nil
}

// ── Mutation implementations ─────────────────

func (g *WorkflowGenome) mutateInsertNode() {
	if g.dag.NodeCount() >= g.config.MaxNodes {
		return
	}
	agentType := g.config.AgentPool[rand.Intn(len(g.config.AgentPool))]
	stepID := fmt.Sprintf("wf-mut-%d", g.dag.Version()+1)

	step := &engine.Step{
		ID:        stepID,
		Name:      stepID,
		AgentType: agentType,
		Input:     "auto-evolved",
	}

	// Pick a random existing node as dependency.
	steps := g.dag.Steps()
	if len(steps) > 0 {
		dep := steps[rand.Intn(len(steps))]
		step.DependsOn = []string{dep.ID}
	}

	if err := g.dag.AddNode(context.Background(), step); err != nil {
		wfLog.Warn("insert node mutation failed", "node", stepID, "error", err)
	}
}

func (g *WorkflowGenome) mutateRemoveNode() {
	steps := g.dag.Steps()
	if len(steps) <= 1 {
		return // keep at least one node
	}

	// Find nodes that no other step depends on (true leaf nodes).
	referenced := make(map[string]bool)
	for _, s := range steps {
		for _, dep := range s.DependsOn {
			referenced[dep] = true
		}
	}

	for _, step := range steps {
		if !referenced[step.ID] {
			if err := g.dag.RemoveNode(context.Background(), step.ID); err != nil {
				wfLog.Warn("remove leaf node failed", "node", step.ID, "error", err)
			}
			return
		}
	}

	// Fallback: remove random node.
	target := steps[rand.Intn(len(steps))]
	if err := g.dag.RemoveNode(context.Background(), target.ID); err != nil {
		wfLog.Warn("remove node fallback failed", "node", target.ID, "error", err)
	}
}

func (g *WorkflowGenome) mutateReplaceNode() {
	steps := g.dag.Steps()
	if len(steps) == 0 {
		return
	}
	oldStep := steps[rand.Intn(len(steps))]
	agentType := g.config.AgentPool[rand.Intn(len(g.config.AgentPool))]

	newStep := &engine.Step{
		ID:        oldStep.ID,
		Name:      oldStep.Name + "-v2",
		AgentType: agentType,
		Input:     oldStep.Input,
		DependsOn: oldStep.DependsOn,
	}
	if err := g.dag.ReplaceNode(context.Background(), oldStep.ID, newStep); err != nil {
		wfLog.Warn("replace node mutation failed", "node", oldStep.ID, "error", err)
	}
}

func (g *WorkflowGenome) mutateParallelize() {
	// Pick 3 consecutive nodes A → B → C and insert a parallel B2 node.
	steps := g.dag.Steps()
	if len(steps) < 3 {
		return
	}

	// Pick a random start index.
	start := rand.Intn(len(steps) - 2)
	a, b, c := steps[start], steps[start+1], steps[start+2]

	if g.dag.NodeCount()+1 > g.config.MaxNodes {
		return
	}

	b2 := &engine.Step{
		ID:        b.ID + "-parallel",
		Name:      b.Name + "-parallel",
		AgentType: b.AgentType,
		Input:     b.Input,
		DependsOn: []string{a.ID},
	}
	if err := g.dag.AddNode(context.Background(), b2); err != nil {
		wfLog.Warn("parallelize add node failed", "node", b2.ID, "error", err)
		return
	}
	if g.dag.StepIndex()[c.ID] != nil {
		c.DependsOn = append(c.DependsOn, b2.ID)
	}
}

func (g *WorkflowGenome) mutateSerialize() {
	// Convert a parallel fan-out into a linear chain.
	steps := g.dag.Steps()
	for _, step := range steps {
		deps := g.dag.ReadDeps(step.ID)
		if len(deps) >= 2 {
			// Remove all but the first dependency, creating a chain.
			step.DependsOn = deps[:1]
			return
		}
	}
}

func (g *WorkflowGenome) mutateSwapNodes() {
	// Swap the dependencies of two random nodes in the DAG.
	steps := g.dag.Steps()
	if len(steps) < 2 {
		return
	}
	i, j := rand.Intn(len(steps)), rand.Intn(len(steps))
	if i == j {
		j = (j + 1) % len(steps)
	}
	// Swap the dependency lists of the two nodes via the DAG.
	depsI := g.dag.ReadDeps(steps[i].ID)
	depsJ := g.dag.ReadDeps(steps[j].ID)
	steps[i].DependsOn = depsJ
	steps[j].DependsOn = depsI
}

func (g *WorkflowGenome) mutateSplitNode() {
	// Split a random node into two sequential nodes.
	steps := g.dag.Steps()
	if len(steps) == 0 || g.dag.NodeCount()+1 > g.config.MaxNodes {
		return
	}
	target := steps[rand.Intn(len(steps))]
	splitID := target.ID + "-split"
	splitStep := &engine.Step{
		ID:        splitID,
		Name:      splitID,
		AgentType: target.AgentType,
		Input:     target.Input,
		DependsOn: []string{target.ID},
	}
	if err := g.dag.AddNode(context.Background(), splitStep); err != nil {
		wfLog.Warn("split add node failed", "node", splitID, "error", err)
		return
	}
	// Update downstream nodes to depend on the split node.
	for _, s := range steps {
		for idx, dep := range s.DependsOn {
			if dep == target.ID {
				s.DependsOn[idx] = splitID
			}
		}
	}
}

func (g *WorkflowGenome) mutateMergeNodes() {
	// Merge two consecutive nodes into one.
	steps := g.dag.Steps()
	if len(steps) < 2 {
		return
	}
	// Find two nodes where one depends on the other.
	for i := 0; i < len(steps); i++ {
		for j := 0; j < len(steps); j++ {
			if i == j {
				continue
			}
			if contains(steps[j].DependsOn, steps[i].ID) {
				// Merge j into i: remove j, update i's deps.
				steps[i].DependsOn = mergeDeps(steps[i].DependsOn, steps[j].DependsOn)
				steps[i].Input = steps[i].Input + " | " + steps[j].Input
				// Remove j from the DAG.
				_ = g.dag.RemoveNode(context.Background(), steps[j].ID)
				return
			}
		}
	}
}

// contains checks if a string is in a slice.
func contains(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

// mergeDeps combines two dependency lists, removing duplicates.
func mergeDeps(a, b []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(a)+len(b))
	for _, d := range a {
		if !seen[d] {
			seen[d] = true
			result = append(result, d)
		}
	}
	for _, d := range b {
		if !seen[d] {
			seen[d] = true
			result = append(result, d)
		}
	}
	return result
}

// clone creates a deep copy of the WorkflowGenome.
// Steps are sorted topologically before passing to NewMutableDAG
// because Steps() returns in non-deterministic map order, which would
// cause NewMutableDAG to reject the steps if dependents appear before deps.
func (g *WorkflowGenome) clone() *WorkflowGenome {
	steps := g.dag.Steps()
	cloneDag, err := engine.NewMutableDAG(sortByDeps(steps))
	if err != nil {
		// Last resort: rebuild from step positions to get deterministic order.
		ordered := make([]*engine.Step, 0, len(steps))
		for _, step := range steps {
			if len(step.DependsOn) == 0 {
				ordered = append(ordered, step)
			}
		}
		for _, step := range steps {
			if len(step.DependsOn) > 0 {
				ordered = append(ordered, step)
			}
		}
		cloneDag, err = engine.NewMutableDAG(ordered)
		if err != nil {
			// Absolute fallback: share parent (mutation may be no-op).
			cloneDag = g.dag
		}
	}
	return &WorkflowGenome{
		dag:    cloneDag,
		config: g.config,
	}
}

// sortByDeps returns steps in topological order (dependencies before dependents).
func sortByDeps(steps []*engine.Step) []*engine.Step {
	// Build in-degree map.
	inDegree := make(map[string]int, len(steps))
	stepMap := make(map[string]*engine.Step, len(steps))
	for _, s := range steps {
		inDegree[s.ID] = len(s.DependsOn)
		stepMap[s.ID] = s
	}

	// Find roots (no dependencies).
	var queue []string
	for _, s := range steps {
		if len(s.DependsOn) == 0 {
			queue = append(queue, s.ID)
		}
	}

	// Kahn's algorithm.
	result := make([]*engine.Step, 0, len(steps))
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		result = append(result, stepMap[id])

		// Decrease in-degree of nodes depending on this one.
		for _, s := range steps {
			if contains(s.DependsOn, id) {
				inDegree[s.ID]--
				if inDegree[s.ID] == 0 {
					queue = append(queue, s.ID)
				}
			}
		}
	}

	return result
}
