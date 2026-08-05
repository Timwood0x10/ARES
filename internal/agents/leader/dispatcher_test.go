package leader

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/ares_protocol/ahp"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capturingSender records every AHPMessage it is asked to send so tests can
// assert on the sender identity stamped by getAgentID.
type capturingSender struct {
	sent []*ahp.AHPMessage
	err  error
}

func (s *capturingSender) Send(_ context.Context, _ string, msg *ahp.AHPMessage) error {
	s.sent = append(s.sent, msg)
	return s.err
}

// TestDispatcher_DefaultAgentID verifies that a dispatcher constructed
// without WithDispatcherAgentID falls back to DefaultDispatcherAgentID.
// This locks in the legacy default for tests that never use the message
// sender path.
func TestDispatcher_DefaultAgentID(t *testing.T) {
	d, err := NewTaskDispatcher(map[models.AgentType]string{}, 1, 1, nil)
	require.NoError(t, err)

	td := d.(*taskDispatcher)
	assert.Equal(t, DefaultDispatcherAgentID, td.getAgentID(),
		"default agent ID should be the legacy constant")
}

// TestDispatcher_WithAgentID verifies that WithDispatcherAgentID injects
// the real agent ID, so distributed messages carry the correct sender
// instead of the hardcoded "leader".
func TestDispatcher_WithAgentID(t *testing.T) {
	const realLeaderID = "leader-prod-42"
	d, err := NewTaskDispatcher(
		map[models.AgentType]string{},
		1, 1, nil,
		WithDispatcherAgentID(realLeaderID),
	)
	require.NoError(t, err)

	td := d.(*taskDispatcher)
	assert.Equal(t, realLeaderID, td.getAgentID(),
		"getAgentID must return the configured real ID, not 'leader'")
	assert.NotEqual(t, "leader", td.getAgentID(),
		"getAgentID must not return the legacy 'leader' constant")
}

// TestDispatcher_WithAgentIDEmptyIgnored verifies that an empty agent ID
// option is ignored and the default is retained.
func TestDispatcher_WithAgentIDEmptyIgnored(t *testing.T) {
	d, err := NewTaskDispatcher(
		map[models.AgentType]string{},
		1, 1, nil,
		WithDispatcherAgentID(""),
	)
	require.NoError(t, err)

	td := d.(*taskDispatcher)
	assert.Equal(t, DefaultDispatcherAgentID, td.getAgentID(),
		"empty agent ID should fall back to the default")
}

// TestDispatcher_DispatchStampsConfiguredAgentID verifies end-to-end that
// when a task is dispatched via the message sender, the outgoing AHPMessage
// carries the configured agent ID, not the hardcoded "leader".
func TestDispatcher_DispatchStampsConfiguredAgentID(t *testing.T) {
	const realLeaderID = "leader-real-99"
	sender := &capturingSender{}

	// Register a target agent address but no local executor, forcing the
	// dispatcher down the message-sender path.
	registry := map[models.AgentType]string{
		models.AgentTypeTop: "agent-top-addr",
	}
	d, err := NewTaskDispatcher(
		registry,
		1, 30, sender,
		WithDispatcherAgentID(realLeaderID),
	)
	require.NoError(t, err)

	tasks := []*models.Task{
		models.NewTask("task-1", models.AgentTypeTop, &models.UserProfile{}),
	}

	results, err := d.Dispatch(context.Background(), tasks)
	require.NoError(t, err)
	require.Len(t, results, 1)

	require.Len(t, sender.sent, 1, "sender should have received one message")
	assert.Equal(t, realLeaderID, sender.sent[0].AgentID,
		"outgoing message AgentID must be the configured real ID")
	assert.NotEqual(t, "leader", sender.sent[0].AgentID,
		"outgoing message AgentID must not be the hardcoded 'leader'")
}
