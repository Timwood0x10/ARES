// Runtime Evolution Demo — 展示 WorkflowGenome 如何自动优化 DAG 拓扑
//
// 运行：go run examples/runtime_evolution/basic/main.go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/Timwood0x10/ares/internal/evolution/genome"
	"github.com/Timwood0x10/ares/internal/evolution/patch"
)

func main() {
	ctx := context.Background()
	fmt.Println("═══ ARES Runtime Evolution Demo ═══")
	fmt.Println()

	// 1. Create the genome registry and register a mock workflow genome.
	reg := genome.NewRegistry()
	wfGenome := &mockWorkflowGenome{name: "workflow", fitness: 0.65}
	if err := reg.Register(wfGenome); err != nil {
		log.Fatalf("register genome: %v", err)
	}

	// 2. Create a patch registry and register a mock executor.
	patchReg := patch.NewRegistry()
	exec := &mockPatchExecutor{}
	if err := patchReg.Register("workflow.graph", exec); err != nil {
		log.Fatalf("register executor: %v", err)
	}

	fmt.Println("1. Registered genomes:", reg.List())

	// 3. Get the workflow genome and mutate it.
	wf, err := reg.Get("workflow")
	if err != nil {
		log.Fatalf("get genome: %v", err)
	}
	currentFit, _ := wf.Fitness(ctx)
	fmt.Printf("2. Current fitness: %.2f\n", currentFit)

	// 4. Generate candidate genomes.
	candidates, err := wf.Mutate(ctx, 3)
	if err != nil {
		log.Fatalf("mutate: %v", err)
	}
	fmt.Printf("3. Generated %d candidate genomes\n", len(candidates))

	// 5. Evaluate candidates and pick the best.
	var bestFit float64
	var bestPatch *patch.RuntimePatch
	for _, candidate := range candidates {
		fit, err := candidate.Fitness(ctx)
		if err != nil {
			continue
		}
		fmt.Printf("   Candidate %q fitness: %.2f\n", candidate.Name(), fit)
		if fit > bestFit {
			bestFit = fit
			bestPatch = &patch.RuntimePatch{
				Type:   patch.PatchReplaceNode,
				Target: "workflow.graph",
				Value:  candidate.Name(),
				Reason: fmt.Sprintf("fitness improved from %.2f to %.2f", wfGenome.fitness, fit),
				Source: "genome.workflow",
			}
		}
	}

	// 6. Apply the best patch.
	if bestPatch != nil {
		fmt.Printf("4. Applying best patch: %s → %s (fitness: %.2f)\n",
			bestPatch.Type, bestPatch.Target, bestFit)
		if err := patchReg.Apply(ctx, *bestPatch); err != nil {
			log.Fatalf("apply patch: %v", err)
		}
		fmt.Printf("5. Executor received %d patches\n", len(exec.applied))
	}

	fmt.Println()
	fmt.Println("═══ Done ═══")
}

// ── Mock implementations ─────────────────────

type mockWorkflowGenome struct {
	name    string
	fitness float64
}

func (g *mockWorkflowGenome) Name() string { return g.name }

func (g *mockWorkflowGenome) Mutate(_ context.Context, n int) ([]genome.Genome, error) {
	children := make([]genome.Genome, n)
	for i := 0; i < n; i++ {
		children[i] = &mockWorkflowGenome{
			name:    fmt.Sprintf("%s-mutated-%d", g.name, i),
			fitness: g.fitness + float64(i+1)*0.05,
		}
	}
	return children, nil
}

func (g *mockWorkflowGenome) Crossover(_ context.Context, other genome.Genome) (genome.Genome, error) {
	return &mockWorkflowGenome{
		name:    fmt.Sprintf("%s-x-%s", g.name, other.Name()),
		fitness: (g.fitness + 0.7) / 2,
	}, nil
}

func (g *mockWorkflowGenome) Fitness(_ context.Context) (float64, error) {
	return g.fitness, nil
}

func (g *mockWorkflowGenome) Snapshot(_ context.Context) (any, error) {
	return map[string]any{"name": g.name, "fitness": g.fitness}, nil
}

type mockPatchExecutor struct {
	applied []patch.RuntimePatch
}

func (e *mockPatchExecutor) Apply(_ context.Context, p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	e.applied = append(e.applied, p)
	return &patch.RuntimePatch{Type: patch.PatchRemoveNode, Target: "workflow.graph"}, nil
}

func (e *mockPatchExecutor) CanApply(_ context.Context, p patch.RuntimePatch) error {
	return nil
}
