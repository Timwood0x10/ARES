package ares_config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateKernelMaxReadyTasks: a negative cap is a configuration error, not "unlimited".
// Accepting it would silently disable the limit the operator asked for, and the deployment would
// behave as though the knob did nothing.
func TestValidateKernelMaxReadyTasks(t *testing.T) {
	negative := &Config{}
	negative.setDefaults()
	negative.Kernel.MaxReadyTasks = -1

	err := negative.validateKernel()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max_ready_tasks")

	zero := &Config{}
	zero.setDefaults()
	zero.Kernel.MaxReadyTasks = 0
	assert.NoError(t, zero.validateKernel(), "0 means unlimited and must stay valid")
}
