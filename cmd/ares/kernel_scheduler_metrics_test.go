package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// TestNewKernelScheduler_WiresTheObservationSink is the wiring half of the M0 baseline.
//
// The three ARES_scheduler_* histograms are registered whether or not anything feeds them, and an
// empty histogram reads on a dashboard as "no slow dispatches" rather than "nobody is measuring".
// Nothing connected the kernel's observation interface to the instrument set until this wiring
// existed, so the assertion is that the production constructor installs a real sink rather than the
// no-op default.
func TestNewKernelScheduler_WiresTheObservationSink(t *testing.T) {
	sched := NewKernelScheduler(taskfabric.NewFabric(), nil, newLoadTracker())

	assert.True(t, sched.HasSchedulerMetrics(),
		"the scheduler built for serve must observe dispatch latency; otherwise the histograms stay empty")
}
