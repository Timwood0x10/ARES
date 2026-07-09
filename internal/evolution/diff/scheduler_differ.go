package diff

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/internal/evolution/genome"
	"github.com/Timwood0x10/ares/internal/evolution/patch"
)

// SchedulerDiffer computes scheduler type differences between two genome snapshots.
// Snapshots must be strings (scheduler type names like "*graph.DefaultScheduler").
type SchedulerDiffer struct{}

// NewSchedulerDiffer creates a new SchedulerDiffer.
func NewSchedulerDiffer() *SchedulerDiffer {
	return &SchedulerDiffer{}
}

// Name returns the differ identifier, matching the SchedulerGenome name.
func (d *SchedulerDiffer) Name() string { return genome.SchedulerGenomeName }

// Diff compares old and new scheduler type snapshots.
func (d *SchedulerDiffer) Diff(_ context.Context, old, new any) ([]patch.RuntimePatch, error) {
	oldType, ok := old.(string)
	if !ok {
		return nil, fmt.Errorf("scheduler differ: old snapshot is %T, want string", old)
	}
	newType, ok := new.(string)
	if !ok {
		return nil, fmt.Errorf("scheduler differ: new snapshot is %T, want string", new)
	}

	// Same scheduler type — no patch needed.
	if oldType == newType {
		return nil, nil
	}

	return []patch.RuntimePatch{
		{
			Type:   patch.PatchChangeScheduler,
			Target: "graph.scheduler",
			Value:  newType,
			Reason: fmt.Sprintf("scheduler: %s → %s", oldType, newType),
			Source: "diff.scheduler",
		},
	}, nil
}
