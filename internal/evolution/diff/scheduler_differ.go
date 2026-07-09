package diff

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/internal/evolution/genome"
	"github.com/Timwood0x10/ares/internal/evolution/patch"
	"github.com/Timwood0x10/ares/internal/workflow/graph"
)

// SchedulerDiffer computes scheduler type differences between two genome snapshots.
// Snapshots must be graph.Scheduler values.
type SchedulerDiffer struct{}

// NewSchedulerDiffer creates a new SchedulerDiffer.
func NewSchedulerDiffer() *SchedulerDiffer {
	return &SchedulerDiffer{}
}

// Name returns the differ identifier, matching the SchedulerGenome name.
func (d *SchedulerDiffer) Name() string { return genome.SchedulerGenomeName }

// Diff compares old and new scheduler snapshots.
func (d *SchedulerDiffer) Diff(_ context.Context, old, new any) ([]patch.RuntimePatch, error) {
	oldSched, ok := old.(graph.Scheduler)
	if !ok {
		return nil, fmt.Errorf("scheduler differ: old snapshot is %T, want graph.Scheduler", old)
	}
	newSched, ok := new.(graph.Scheduler)
	if !ok {
		return nil, fmt.Errorf("scheduler differ: new snapshot is %T, want graph.Scheduler", new)
	}

	// Same scheduler type — no patch needed.
	if fmt.Sprintf("%T", oldSched) == fmt.Sprintf("%T", newSched) {
		return nil, nil
	}

	return []patch.RuntimePatch{
		{
			Type:   patch.PatchChangeScheduler,
			Target: "graph.scheduler",
			Value:  newSched,
			Reason: fmt.Sprintf("scheduler: %T → %T", oldSched, newSched),
			Source: srcScheduler,
		},
	}, nil
}
