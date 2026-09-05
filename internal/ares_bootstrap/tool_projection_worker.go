// tool_projection_worker.go is the production trigger for the ToolStep
// projection (Y1 §12-1). The projection layer and its evidence writer already
// existed and were covered end-to-end by tests, but nothing in serve ever
// called them, so the tool_step fitness dimension had no live producer: the
// scoped fitness read filtered on a `tool_step_id` that only ever appeared in
// tests. This worker is that missing call site.
//
// WHY A PERIODIC WORKER AND NOT AN EVENT SUBSCRIBER: a ToolStep's value is a
// success RATE over a window of calls sharing an argument shape. A per-event
// hook cannot compute it (one event is one call — rate 0 or 1), and the
// per-call channel already has a producer (ChannelFeedbackRecorder, the
// binder observer). The two are complementary: that one answers "did this call
// work", this one answers "does this WAY of calling this tool work".
package ares_bootstrap

import (
	"context"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/logger"
	"github.com/Timwood0x10/ares/internal/toolprojection"
)

var logToolProjection = logger.Module("ares_bootstrap.toolprojection")

// projectorRunner is the worker's dependency on the projection, declared at the
// consumer so the loop is testable without an event store or evidence store
// (*toolprojection.Projector satisfies it).
type projectorRunner interface {
	Run(ctx context.Context, opts toolprojection.Options) (int, error)
}

// startToolProjectionWorker builds the ToolStep projector and starts its
// periodic loop on comp.bgGroup. It returns without wiring anything — an
// explicit "not started", logged — in each case where the worker could not
// produce real evidence:
//
//   - disabled in config (the default): nothing to do.
//   - no event store: the projection reads events, so with none it would
//     write an empty graph on every tick forever. Y1 §9.3-2 requires this be
//     refused loudly rather than silently producing empty projections.
//   - no evidence store: nowhere to write the projected rates.
//
// Args:
//   - ctx: bootstrap context; cancelling it stops the loop.
//   - comp: the component graph (event store + background goroutine group).
//   - newEvol: the evolution components (evidence store).
//   - cfg: the evolution.tool_projection YAML block.
func startToolProjectionWorker(
	ctx context.Context,
	comp *Components,
	newEvol *NewEvolutionComponents,
	cfg ares_config.ToolProjectionConfig,
) {
	if !cfg.Enabled {
		return
	}
	if comp == nil || comp.EventStore == nil {
		logToolProjection.WarnContext(ctx, "bootstrap: tool projection disabled — no event store, "+
			"every projection would be empty")
		return
	}
	if newEvol == nil || newEvol.EvidenceStore == nil {
		logToolProjection.WarnContext(ctx, "bootstrap: tool projection disabled — no evidence store")
		return
	}
	projector, err := toolprojection.NewProjector(comp.EventStore, newEvol.EvidenceStore)
	if err != nil {
		logToolProjection.WarnContext(ctx, "bootstrap: tool projection disabled", "error", err)
		return
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = ares_config.DefaultToolProjectionInterval
	}
	comp.bgGroup.Go(func() error {
		runToolProjectionLoop(ctx, projector, interval, cfg.MinSamples)
		return nil
	})
	logToolProjection.InfoContext(ctx, "bootstrap: tool projection worker started",
		"interval", interval.String(), "min_samples", cfg.MinSamples)
}

// runToolProjectionLoop ticks the projector until ctx is cancelled.
//
// The read cursor starts at "now" rather than at the beginning of the log:
// projecting the whole accumulated history on the first tick would attribute
// behavior produced by PREVIOUS strategies to whichever strategy happens to be
// active at boot — the same misattribution the active-strategy resolver exists
// to prevent on the other channels.
//
// The cursor advances only after a SUCCESSFUL run. A failed run leaves it in
// place so the window it could not project is retried on the next tick instead
// of being skipped; that can re-emit records for calls the failed run had
// already written before erroring mid-way, which is the deliberate tradeoff —
// double-counting a window's evidence degrades a score slightly, silently
// dropping a window of tool failures hides the signal evolution exists to see.
//
// A tick that projects nothing (idle system, or every step below the sample
// threshold) is not an error and still advances the cursor.
func runToolProjectionLoop(
	ctx context.Context,
	projector projectorRunner,
	interval time.Duration,
	minSamples int,
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	since := timeNowUTC()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Capture the window end BEFORE the read so calls landing during
			// the projection are not skipped: they fall after this bound and
			// belong to the next window.
			windowEnd := timeNowUTC()
			written, err := projector.Run(ctx, toolprojection.Options{
				MinSamples: minSamples,
				Since:      since,
			})
			if err != nil {
				// ctx cancellation during shutdown is expected, not a fault.
				if ctx.Err() != nil {
					return
				}
				logToolProjection.WarnContext(ctx, "bootstrap: tool projection run failed",
					"error", err, "since", since.Format(time.RFC3339))
				continue
			}
			since = windowEnd
			if written > 0 {
				logToolProjection.DebugContext(ctx, "bootstrap: tool projection wrote fitness records",
					"records", written)
			}
		}
	}
}

// timeNowUTC is the worker's clock hook (UTC so cursor logs and the evidence
// timestamps share a frame). Overridden in tests.
var timeNowUTC = func() time.Time { return time.Now().UTC() }
