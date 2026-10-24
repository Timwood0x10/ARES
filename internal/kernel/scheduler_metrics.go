package kernel

// SchedulerMetrics is the optional observability sink for scheduling-loop
// metrics (M0 baseline). The kernel defines the interface so it does not
// import the observability package; the wiring side (cmd/ares) adapts its
// concrete PrometheusMetrics to this interface.
//
// All methods are nil-safe by contract: the scheduler guards the nil
// receiver, so a metrics sink that does not implement every method can
// embed a no-op base.
type SchedulerMetrics interface {
	// RecordReadyToDispatch observes the time from a task becoming READY
	// to the scheduler dispatching it (seconds).
	RecordReadyToDispatch(seconds float64)
	// RecordWorkConservingGap observes the delay in dispatching new READY
	// work while a slow quantum is in flight (seconds). Zero when there
	// was no in-flight work.
	RecordWorkConservingGap(seconds float64)
	// RecordReadyAge observes a task's age (since READY) at dispatch time
	// (seconds).
	RecordReadyAge(seconds float64)
}

// noOpSchedulerMetrics is the default sink: every call is a no-op.
type noOpSchedulerMetrics struct{}

func (noOpSchedulerMetrics) RecordReadyToDispatch(float64)   {}
func (noOpSchedulerMetrics) RecordWorkConservingGap(float64) {}
func (noOpSchedulerMetrics) RecordReadyAge(float64)          {}
