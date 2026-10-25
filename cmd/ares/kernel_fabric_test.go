package main

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	aresconfig "github.com/Timwood0x10/ares/internal/ares_config"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// TestNewKernelFabric_AppliesTheAdmissionCap is the wiring half of backpressure.
//
// WithMaxReadyTasks had no production caller: the cap could be set from a test and nowhere else, so
// the feature was implemented, unconfigured, and therefore never on. The assertion is behavioural
// rather than structural — build the fabric serve would build and try to exceed the cap.
func TestNewKernelFabric_AppliesTheAdmissionCap(t *testing.T) {
	cfg := &aresconfig.Config{}
	cfg.Kernel.MaxReadyTasks = 2

	fabric := newKernelFabric(cfg)

	require.NoError(t, fabric.Create(&taskfabric.Task{ID: "t-1"}))
	require.NoError(t, fabric.Create(&taskfabric.Task{ID: "t-2"}))
	err := fabric.Create(&taskfabric.Task{ID: "t-3"})

	require.Error(t, err, "the configured cap must reach the fabric that serve drains")
	assert.ErrorIs(t, err, taskfabric.ErrReadyQueueFull)
}

// TestNewKernelFabric_DefaultIsUnlimited: an existing deployment must not start rejecting work
// because a knob it never set defaults to something other than "off".
func TestNewKernelFabric_DefaultIsUnlimited(t *testing.T) {
	for name, fabric := range map[string]*taskfabric.Fabric{
		"nil config":   newKernelFabric(nil),
		"zero default": newKernelFabric(&aresconfig.Config{}),
	} {
		t.Run(name, func(t *testing.T) {
			for i := 0; i < 50; i++ {
				require.NoError(t, fabric.Create(&taskfabric.Task{ID: fmt.Sprintf("%s-%d", name, i)}))
			}
		})
	}
}
