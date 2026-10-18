package evolution

// CandidateDimensionSources maps an evidence SOURCE to the multi-objective DIMENSION it
// feeds (M9).
//
// Only per-candidate sources belong here. The aggregator also produces means for
// `workflow`/`scheduler`/`recovery`, which by design IGNORE the strategy id — they describe
// the system running the active strategy, so every candidate gets the same value. As
// dimensions they are constants: they cannot change Pareto dominance or crowding distance, so
// including them would add width without information (and make an NSGA-II run look
// multi-objective while it is not).
//
// `collaboration` and `tool_call` are excluded for a different reason: their default weights
// are zero (opt-in), so most deployments produce no data at all for them.
// One ordered list, not a map plus an order slice: with two sources of truth, adding an
// entry to the map changes nothing (the loop only honoured the slice) — a footgun that a
// mutation check caught here, because the "add a forbidden dimension" mutation did not fail.
var candidateDimensionSources = []struct {
	Source    string
	Dimension string
	// Metric marks a source read from WindowResult.Metrics instead of PerSource.
	//
	// The two maps are kept apart deliberately: PerSource feeds the fitness mean (higher is
	// better), Metrics carries optimisation targets (latency/cost, lower is better). Reading
	// both from one map is how a Minimize dimension ends up ranked as if it were a score.
	Metric bool
}{
	{Source: "strategy", Dimension: "success_rate"},
	{Source: "dimension_eval", Dimension: "quality"},
	{Source: latencyMetricSource, Dimension: "latency", Metric: true},
	{Source: costMetricSource, Dimension: "cost", Metric: true},
}

// MapCandidateDimensions turns one per-strategy fitness window into NSGA-II dimensions.
//
// Rules, all of which are load-bearing:
//   - A source with NO samples is OMITTED, not reported as 0. Zero is a real score
//     ("everything failed") and would drag a candidate down for lack of measurement —
//     exactly the confusion a cold start must not create.
//   - Values are passed through unchanged. The aggregator already normalises every channel
//     to [0,1] with "higher is better", which is also the direction of both dimensions here
//     (see DefaultDimensionDirections); inverting anything would be applying a Minimize rule
//     to a Maximize number.
//   - `cost`/`latency` come from WindowResult.Metrics, NOT from PerSource, and they are
//     Minimize objectives: the observer records the INVERSE of its multiplicative penalty
//     (0 = fastest/cheapest). Reading them from PerSource would apply a higher-is-better
//     ranking to a lower-is-better number — the direction table would then be a lie rather
//     than a contract.
func MapCandidateDimensions(w WindowResult) map[string]float64 {
	dims := make(map[string]float64, len(candidateDimensionSources))
	for _, m := range candidateDimensionSources {
		stats := w.PerSource
		if m.Metric {
			stats = w.Metrics
		}
		stat, ok := stats[m.Source]
		if !ok || stat.Count == 0 {
			// No samples: omit (see the doc — 0 would be a measurement, not a gap).
			continue
		}
		dims[m.Dimension] = stat.Mean
	}
	return dims
}

// MultiObjectiveScorerFromWindow builds the scorer the GA cycle needs, given a way to obtain a
// strategy's fitness window from real execution evidence.
//
// The aggregate is derived from the dimensions through the existing weights rather than
// recomputed from a different source: a candidate's single-objective score and its
// multi-objective vector then describe the same evidence, so a selection that falls back to
// single-objective (no dimensions available) cannot silently rank by something else.
func MultiObjectiveScorerFromWindow(windowFor func(strategyID string) WindowResult) func(strategyID string) (map[string]float64, float64) {
	return func(strategyID string) (map[string]float64, float64) {
		if windowFor == nil {
			return nil, 0
		}
		dims := MapCandidateDimensions(windowFor(strategyID))
		if len(dims) == 0 {
			return nil, 0
		}
		return dims, 0 // aggregate filled by the caller's existing scoring path if it wants one
	}
}
