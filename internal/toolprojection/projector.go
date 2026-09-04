package toolprojection

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/evidence"
)

// toolCallEvidenceSource is the dedicated evidence source for tool-call fitness
// (must match ares_evolution.toolCallEvidenceSource and the aggregator's
// WindowToolStep read key).
const toolCallEvidenceSource = "tool_call"

// timeNow is a package-level clock hook so record timestamps/IDs are
// deterministic under test (production returns time.Now()).
var timeNow = time.Now

// Projector closes the C2→C3 link: it reads the real event store, projects the
// C1-contract tool-call events into ToolStep nodes, and writes each aggregated
// step's success rate as `tool_call` fitness evidence carrying its tool_step_id.
//
// WHY IT EXISTS (the production wiring that was previously missing): the
// projection layer (C2) produced ToolStep nodes but nothing consumed them, so
// the process-level attribution (C3) had no data source that fed the
// aggregator's WindowToolStep. This Projector is that consumer — it makes the
// projection reach the judgment/audit path instead of being a dead pure
// function. A step with fewer than one call is skipped (nothing to measure).
type Projector struct {
	// events is the source of tool-call events (the real event store).
	events EventSource
	// evidence receives the projected fitness records (the shared evidence store).
	evidence evidence.Store
}

// NewProjector creates a Projector bound to a real event source and evidence
// store. Both are required: a projector that reads nothing or writes nothing is
// not a projection, it is a placeholder.
func NewProjector(events EventSource, store evidence.Store) (*Projector, error) {
	if events == nil {
		return nil, fmt.Errorf("toolprojection: event source is nil")
	}
	if store == nil {
		return nil, fmt.Errorf("toolprojection: evidence store is nil")
	}
	return &Projector{events: events, evidence: store}, nil
}

// Run reads events from the source, projects them into ToolSteps, and writes
// one fitness record per step (source=tool_call, value=success_rate,
// payload.tool_step_id=<id>).
//
// It only appends: each call re-reads the whole event window and emits a fresh
// record per step, so callers that want a bounded window must narrow it via
// opts (MinSamples) or run against a trimmed event source.
//
// Returns the number of ToolStep fitness records written.
func (p *Projector) Run(ctx context.Context, opts Options) (int, error) {
	proj, err := ProjectFromSource(ctx, p.events, opts)
	if err != nil {
		return 0, err
	}
	written := 0
	for _, step := range proj.Steps {
		if step.Count == 0 {
			continue
		}
		payload, err := json.Marshal(map[string]any{
			"value":         step.SuccessRate,
			"success":       step.SuccessRate > 0,
			"tool_step_id":  step.ToolStepID,
			"tool":          step.ToolName,
			"count":         step.Count,
			"success_count": step.SuccessCount,
		})
		if err != nil {
			return written, fmt.Errorf("toolprojection: marshal step %s: %w", step.ToolStepID, err)
		}
		if err := p.evidence.Append(ctx, evidence.Evidence{
			// Full-date-with-fractional-seconds id so the PG store's
			// ON CONFLICT DO NOTHING dedup does not drop records that share a
			// coarse timestamp (same reasoning as the feedback recorder ids).
			ID:        "toolstep_" + step.ToolStepID + "_" + timeNow().UTC().Format("20060102150405.000000"),
			Source:    toolCallEvidenceSource,
			Kind:      evidence.KindFitness,
			Payload:   payload,
			Timestamp: timeNow(),
		}); err != nil {
			return written, fmt.Errorf("toolprojection: append evidence %s: %w", step.ToolStepID, err)
		}
		written++
	}
	return written, nil
}
