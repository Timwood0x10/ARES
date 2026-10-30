package agentfabric

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/fabric/planprojection"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
)

// TestS10_AsyncCompileSeesThePolicy is the ordering half of the acceptance list: the compile worker
// consumes graph EVENTS, asynchronously, so it must never observe a node whose policy fields are not
// filled in yet.
//
// The test drives the real path — session graph, event subscription, projection, CompilePlan — rather
// than reading the node back, because reading the node only proves the fields are set eventually.
func TestS10_AsyncCompileSeesThePolicy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fabric := taskfabric.NewFabric()
	coord := planprojection.NewCompileCoordinator(fabric, nil)
	reg := NewSessionRegistry()

	var stop func()
	g, err := reg.InitSession("sess-async", "prompt", nil, func(_ context.Context, dag *engine.MutableDAG) func() {
		stop = coord.SubscribeGraphEvents(ctx, dag)
		return stop
	})
	require.NoError(t, err)
	if stop != nil {
		defer stop()
	}

	require.NoError(t, g.AddToolNodeWithPolicy(ctx, "tool-node", "rg", map[string]any{}, "",
		StepPolicy{MaxAttempts: 2, Timeout: 30 * time.Second}))

	deadline := time.Now().Add(3 * time.Second)
	for {
		task, taskErr := fabric.Task("tool-node")
		if taskErr == nil {
			assert.Equal(t, 2, task.RetryPolicy.MaxRetries,
				"the compile worker must see the retry budget in the event that announced the node")
			assert.False(t, task.Deadline.IsZero(),
				"and the deadline, which is resolved from the step's relative timeout")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the node was never compiled: %v", taskErr)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
