package evolution

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
)

// ToolClassSpec is one L1 ToolClass node as DECLARED by a tool schema.
//
// It is a declaration, not a measurement: the values come from the tool's own tags (enabled /
// budget / prior), which is where a tool author states how the tool should be admitted. Nothing in
// this path reads fitness, and nothing should: an optimisation loop that could disable a
// ToolClass would raise the measured score by removing the work it cannot do, and the loss (tasks
// the system can no longer attempt) is invisible to it.
type ToolClassSpec struct {
	NodeID      string
	Name        string
	AgentType   string
	Description string
	Metadata    map[string]string
}

// ToolClassDiff reports what one reconcile changed. Sorted, so a caller can log or compare it.
type ToolClassDiff struct {
	Added   []string
	Removed []string
	Updated []string
	// Deferred lists nodes that are missing from the declaration but still inside the removal
	// grace window. They stay in the graph, and the caller can surface them instead of letting a
	// partial registry read quietly shrink the governance surface.
	Deferred []string
	// Note explains a decision that produced no mutation (currently: an empty declaration). It
	// exists so the caller can warn about it — silence is how a governance surface degrades
	// without anyone noticing.
	Note string
}

// Empty reports whether the graph already matched the declaration and nothing was noted.
func (d ToolClassDiff) Empty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Updated) == 0 &&
		len(d.Deferred) == 0 && d.Note == ""
}

// ReconcileState carries the miss counters that make removal tolerant of a partial read.
//
// A registry that briefly returns fewer tools (an MCP server reconnecting, a bridge that has not
// synced yet) is indistinguishable from "the tool was removed" on a single pass. Removing a node
// whose tags said enabled="false" makes that tool permissive again until the next tick, so removal
// waits for Grace consecutive misses. The zero value is usable.
type ReconcileState struct {
	misses map[string]int
}

// noteMissing records one miss and returns the running count.
func (s *ReconcileState) noteMissing(id string) int {
	if s.misses == nil {
		s.misses = make(map[string]int)
	}
	s.misses[id]++
	return s.misses[id]
}

// clear resets a node's miss count once it is declared again.
func (s *ReconcileState) clear(id string) {
	if s.misses == nil {
		return
	}
	delete(s.misses, id)
}

// ReconcileToolClassDAG aligns dag with specs: declared nodes are added or updated, and nodes that
// have been missing for `grace` consecutive reconciles are removed.
//
// Scope: this synchronises the CONSTRAINT metadata (enabled / budget / prior) of a node that already
// exists. A changed Name or Description is NOT pushed into the graph — Step.Input therefore keeps
// the description the node was created with. The planner reads the constraint metadata and the tool
// registry separately, so a stale description changes no decision; updating it would mean removing
// and re-adding the node (dropping its edges) for text nothing reads.
//
// It is TRANSACTIONAL. A failure part-way through is rolled back to the snapshot taken on entry —
// a half-applied graph is worse than an unchanged one, and a caller that reports "keeping the
// previous graph" must be telling the truth.
//
// Idempotent: reconciling the same declaration twice changes nothing the second time.
func ReconcileToolClassDAG(ctx context.Context, dag *engine.MutableDAG, specs []ToolClassSpec, state *ReconcileState, grace int) (ToolClassDiff, error) {
	var diff ToolClassDiff
	if dag == nil {
		return diff, errors.New("toolclass reconcile: dag is nil")
	}
	if state == nil {
		state = &ReconcileState{}
	}

	want := make(map[string]ToolClassSpec, len(specs))
	for _, spec := range specs {
		if spec.NodeID == "" {
			// A schema without a usable node id cannot be reconciled against; skipping is the
			// only honest option because inventing an id would create a node nothing references.
			continue
		}
		if prev, dup := want[spec.NodeID]; dup && !metadataEqual(prev.Metadata, spec.Metadata) {
			// Two declarations of one ToolClass disagree. Picking one silently would make the
			// resulting graph depend on map iteration order.
			return diff, fmt.Errorf("toolclass reconcile: conflicting declarations for %q", spec.NodeID)
		}
		want[spec.NodeID] = spec
	}

	if len(want) == 0 {
		// An EMPTY declaration is ambiguous: it can mean "this deployment has no tools" (the boot
		// path already treats that as "skip, keep the placeholder") or "the registry could not be
		// read this tick". Wiping the graph would re-enable every tool an operator disabled via
		// tags, because an unknown node is permissive — so an empty catalogue is reported, not
		// applied.
		diff.Note = "empty declaration: kept the previous graph"
		return diff, nil
	}

	snapshot := dag.Steps()

	rollback := func(cause error) (ToolClassDiff, error) {
		if rbErr := restoreToolClassDAG(ctx, dag, snapshot, diff); rbErr != nil {
			return ToolClassDiff{}, fmt.Errorf("%w (rollback failed: %v)", cause, rbErr)
		}
		return ToolClassDiff{}, cause
	}

	// Additive work FIRST, destructive work last. A removal blocked by a dependent is the reachable
	// failure here, and doing it last means the graph is in a known, restorable state when it
	// happens instead of half-removed.
	for id, spec := range want {
		if !dag.HasNode(id) {
			step := &engine.Step{
				ID:        id,
				Name:      spec.Name,
				AgentType: spec.AgentType,
				Input:     spec.Description,
				Metadata:  spec.Metadata,
			}
			if err := dag.AddNode(ctx, step); err != nil {
				return rollback(fmt.Errorf("add toolclass %s: %w", id, err))
			}
			diff.Added = append(diff.Added, id)
			continue
		}
		current, ok := dag.NodeMetadata(id)
		if ok && metadataEqual(current, spec.Metadata) {
			continue
		}
		if err := dag.SetNodeMetadata(id, spec.Metadata); err != nil {
			return rollback(fmt.Errorf("update toolclass %s: %w", id, err))
		}
		diff.Updated = append(diff.Updated, id)
	}

	for _, step := range snapshot {
		if step == nil {
			continue
		}
		if _, declared := want[step.ID]; declared {
			state.clear(step.ID)
			continue
		}
		misses := state.noteMissing(step.ID)
		if grace > 0 && misses < grace {
			diff.Deferred = append(diff.Deferred, step.ID)
			continue
		}
		if err := dag.RemoveNode(ctx, step.ID); err != nil {
			return rollback(fmt.Errorf("remove toolclass %s: %w", step.ID, err))
		}
		diff.Removed = append(diff.Removed, step.ID)
		state.clear(step.ID)
	}

	sort.Strings(diff.Added)
	sort.Strings(diff.Removed)
	sort.Strings(diff.Updated)
	sort.Strings(diff.Deferred)
	return diff, nil
}

// restoreToolClassDAG puts the graph back the way the snapshot found it.
//
// ResetFromSteps refuses an empty graph, so an empty snapshot is undone by removing what this pass
// added — the only mutation an empty snapshot can have seen.
func restoreToolClassDAG(ctx context.Context, dag *engine.MutableDAG, snapshot []*engine.Step, applied ToolClassDiff) error {
	if len(snapshot) > 0 {
		return dag.ResetFromSteps(snapshot)
	}
	var errs []error
	for _, id := range applied.Added {
		if err := dag.RemoveNode(ctx, id); err != nil && !errors.Is(err, engine.ErrNodeNotFound) {
			errs = append(errs, fmt.Errorf("rollback remove %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// metadataEqual compares two metadata maps by value.
func metadataEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// ToolClassReconciler re-derives the L1 capability graph from the tool schemas on a timer and on
// demand.
//
// It exists because the graph used to be built exactly once, at serve startup: a tool registered
// afterwards (an MCP server attached at runtime) was absent from L1. That is fail-open — an unknown
// node is permissive — so nothing broke, but the governance surface silently stopped describing the
// system. Trigger() closes the window further: a registry change can reconcile immediately instead
// of waiting for the next tick.
type ToolClassReconciler struct {
	dag      *engine.MutableDAG
	specs    func(context.Context) ([]ToolClassSpec, error)
	interval time.Duration
	grace    int
	logger   *slog.Logger

	state   ReconcileState
	trigger chan struct{}
}

// NewToolClassReconciler wires a reconciler. A nil logger disables logging (useful in tests); a
// non-positive interval leaves Run driven only by Trigger.
func NewToolClassReconciler(dag *engine.MutableDAG, specs func(context.Context) ([]ToolClassSpec, error), interval time.Duration, logger *slog.Logger) *ToolClassReconciler {
	return &ToolClassReconciler{
		dag:      dag,
		specs:    specs,
		interval: interval,
		grace:    3,
		logger:   logger,
		trigger:  make(chan struct{}, 1),
	}
}

// WithGrace sets how many consecutive reconciles a node may be missing before it is removed. A
// value <= 0 removes on the first miss.
func (r *ToolClassReconciler) WithGrace(n int) *ToolClassReconciler {
	if r != nil {
		r.grace = n
	}
	return r
}

// WithInterval sets the periodic interval; a non-positive value disables the timer.
func (r *ToolClassReconciler) WithInterval(d time.Duration) *ToolClassReconciler {
	if r != nil {
		r.interval = d
	}
	return r
}

// Trigger asks for a reconcile as soon as possible. Non-blocking: a trigger that arrives while one
// is already queued is redundant, and blocking a registry callback would stall the callback.
func (r *ToolClassReconciler) Trigger() {
	if r == nil || r.trigger == nil {
		return
	}
	select {
	case r.trigger <- struct{}{}:
	default:
	}
}

// ReconcileOnce derives the declared specs and applies them.
//
// It logs additions and updates at Info, and removals or notes at Warn: a shrinking or skipped
// governance surface is not routine, and burying it at the same level as an ordinary add is how it
// goes unnoticed.
func (r *ToolClassReconciler) ReconcileOnce(ctx context.Context) (ToolClassDiff, error) {
	if r == nil || r.dag == nil {
		return ToolClassDiff{}, errors.New("toolclass reconcile: reconciler has no dag")
	}
	if r.specs == nil {
		return ToolClassDiff{}, errors.New("toolclass reconcile: no spec source")
	}
	specs, err := r.specs(ctx)
	if err != nil {
		return ToolClassDiff{}, fmt.Errorf("toolclass reconcile: read specs: %w", err)
	}
	diff, err := ReconcileToolClassDAG(ctx, r.dag, specs, &r.state, r.grace)
	if err != nil {
		return diff, err
	}
	if diff.Empty() || r.logger == nil {
		return diff, nil
	}

	attrs := []any{
		"method", "reconcileToolClassDAG",
		"added", len(diff.Added),
		"removed", len(diff.Removed),
		"updated", len(diff.Updated),
		"deferred", len(diff.Deferred),
		"added_nodes", diff.Added,
		"removed_nodes", diff.Removed,
		"updated_nodes", diff.Updated,
	}
	if len(diff.Removed) > 0 || diff.Note != "" {
		if diff.Note != "" {
			attrs = append(attrs, "note", diff.Note)
		}
		r.logger.WarnContext(ctx, "toolclass graph changed outside the routine path", attrs...)
		return diff, nil
	}
	r.logger.InfoContext(ctx, "toolclass graph reconciled", attrs...)
	return diff, nil
}

// Run reconciles on every tick and on every Trigger until ctx is cancelled.
//
// It is meant to be driven by Components.GoBackground, which owns the panic boundary and restarts a
// failed worker. Run therefore does NOT swallow panics: recovering here and returning would leave
// the graph frozen for the life of the process while the supervisor believed the worker exited
// cleanly.
func (r *ToolClassReconciler) Run(ctx context.Context) {
	if r == nil {
		return
	}
	var tick <-chan time.Time
	if r.interval > 0 {
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()
		tick = ticker.C
	}

	// Align once immediately: waiting a full interval before the first pass would keep a graph
	// built at boot stale for up to that interval if anything changed in between, and it makes
	// "started" and "reconciled" observably different for no benefit.
	if _, err := r.ReconcileOnce(ctx); err != nil && r.logger != nil {
		r.logger.WarnContext(ctx, "toolclass reconcile failed on the first pass; graph left unchanged",
			"method", "reconcileToolClassDAG", "error", err)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
		case <-r.trigger:
		}
		if _, err := r.ReconcileOnce(ctx); err != nil && r.logger != nil {
			r.logger.WarnContext(ctx, "toolclass reconcile failed; graph left unchanged",
				"method", "reconcileToolClassDAG", "error", err)
		}
	}
}

// RunUnsupervised drives the reconciler without a supervisor.
//
// Components.GoBackground owns the panic boundary in production; this path exists for unit
// assembly and for tests that want a live reconciler without a component tree. Without a recover
// here a panic inside ReconcileOnce would take the whole process down, not just this worker — the
// unsupervised equivalent of the supervisor's guarantee.
//
// It does not restart: there is nothing to restart it with. The contract is therefore "a panic
// stops this worker and is logged", which is why production must use GoBackground, where a restart
// actually happens.
func (r *ToolClassReconciler) RunUnsupervised(ctx context.Context) {
	defer func() {
		if rec := recover(); rec != nil && r.logger != nil {
			r.logger.Error("toolclass reconcile panicked without a supervisor; worker stopped",
				"method", "reconcileToolClassDAG", "panic", rec)
		}
	}()
	r.Run(ctx)
}
