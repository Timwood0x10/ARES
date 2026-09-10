// guardrails_handler_reentrancy_test.go locks REVIEW 3.4#4: the event
// handler was invoked while the caller held g.mu, so any handler that
// reads back guardrail state (a completely reasonable thing for an
// observer to do) self-deadlocked on the non-reentrant RWMutex. Handlers
// now run after the lock is released.
package evolution

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGuardrailEventHandlerReentrancy(t *testing.T) {
	defer discardLogs()()

	var handlerCalls sync.WaitGroup

	// The handler reads back guardrail state — Events() takes g.mu.RLock,
	// which deadlocked pre-fix because RecordEvent held the write lock.
	var g *EvolutionGuardrails
	g, err := NewEvolutionGuardrails(
		WithBaselineScore(80.0),
		WithGuardrailEventHandler(func(ev GuardrailEvent) {
			_ = g.Events()
			_ = g.StagnantCount()
			handlerCalls.Done()
		}),
	)
	require.NoError(t, err)

	handlerCalls.Add(1)
	done := make(chan struct{})
	go func() {
		g.RecordEvent(GuardrailEvent{Level: GuardrailWarning, Rule: "reentrancy"})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RecordEvent deadlocked: event handler ran under the guardrail lock")
	}
	handlerCalls.Wait()

	events := g.Events()
	require.Len(t, events, 1)
	assert.Equal(t, "reentrancy", events[0].Rule)
}

func TestGuardrailEventHandlerReentrancyViaCheck(t *testing.T) {
	defer discardLogs()()

	var handlerCalls sync.WaitGroup
	var g *EvolutionGuardrails
	g, err := NewEvolutionGuardrails(
		WithBaselineScore(80.0),
		WithGuardrailEventHandler(func(ev GuardrailEvent) {
			// Read-back through the public surface.
			_ = g.Events()
			handlerCalls.Done()
		}),
	)
	require.NoError(t, err)

	// PreEvolveCheck with a critical unevaluated population fires the
	// handler while the check holds g.mu (pre-fix).
	handlerCalls.Add(1)
	done := make(chan struct{})
	go func() {
		g.PreEvolveCheck(context.Background(), 75.0, 1, 100, 60)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("PreEvolveCheck deadlocked: event handler ran under the guardrail lock")
	}
	handlerCalls.Wait()
}
