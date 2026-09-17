// rollback_deploy_io_test.go locks REVIEW 3.4#2:
//
//   - Deploy/Rollback executed their StrategyStore I/O while holding the
//     state mutex, so any reader (Current/Previous) blocked for the whole
//     database round-trip. The I/O now runs under a dedicated deployMu and
//     mu is held only for the state transitions.
//   - When the guardrail-triggered rollback failed to persist, Deploy
//     returned "store set active (rollback)" — reading as "deployment
//     reverted" — while the NEW strategy was in fact still active in both
//     the store and memory. The error now states that reality.
package evolution

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// blockingStrategyStore blocks every SetActive until its gate is released.
type blockingStrategyStore struct {
	gate        chan struct{}
	setActiveFn func(s *Strategy) error
}

func newBlockingStrategyStore() *blockingStrategyStore {
	return &blockingStrategyStore{gate: make(chan struct{})}
}

func (s *blockingStrategyStore) GetActive(ctx context.Context) (*Strategy, error) {
	return nil, ErrNoActiveStrategy
}

func (s *blockingStrategyStore) SetActive(ctx context.Context, strategy *Strategy) error {
	if s.setActiveFn != nil {
		return s.setActiveFn(strategy)
	}
	<-s.gate
	return nil
}

func (s *blockingStrategyStore) GetHistory(ctx context.Context, id string, n int) ([]*Strategy, error) {
	return nil, nil
}

func TestDeployStoreIODoesNotBlockReaders(t *testing.T) {
	store := newBlockingStrategyStore()
	m, err := NewActiveStrategyManager(store, nil)
	require.NoError(t, err)

	// Seed an in-memory current strategy so Current() has a value to
	// return while the deploy is parked in the store.
	m.mu.Lock()
	m.current = &mutation.Strategy{ID: "seed", Version: 1}
	m.mu.Unlock()

	deployDone := make(chan error, 1)
	go func() {
		deployDone <- m.Deploy(context.Background(), &mutation.Strategy{ID: "new", Version: 2})
	}()

	// Give the deploy time to enter SetActive (it must be parked there —
	// the gate stays closed until we open it).
	time.Sleep(50 * time.Millisecond)

	// Current() must return promptly even though Deploy is mid-I/O.
	read := make(chan *mutation.Strategy, 1)
	go func() { read <- m.Current() }()

	select {
	case got := <-read:
		require.NotNil(t, got)
		assert.Equal(t, "seed", got.ID, "reader must observe the pre-deploy state")
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Current() blocked behind Deploy's store I/O — the state mutex is held across I/O")
	}

	// Release the parked deploy and let it finish.
	close(store.gate)
	select {
	case <-deployDone:
	case <-time.After(2 * time.Second):
		t.Fatal("deploy did not finish after the store unblocked")
	}
}

// failingRollbackStore persists deploys but fails the SECOND SetActive (the
// guardrail-triggered rollback write).
type failingRollbackStore struct {
	calls    int
	failFrom int
	last     *Strategy
}

func (s *failingRollbackStore) GetActive(ctx context.Context) (*Strategy, error) {
	if s.last == nil {
		return nil, ErrNoActiveStrategy
	}
	return s.last, nil
}

func (s *failingRollbackStore) SetActive(ctx context.Context, strategy *Strategy) error {
	s.calls++
	if s.calls >= s.failFrom {
		return errors.New("db unavailable")
	}
	cp := *strategy
	s.last = &cp
	return nil
}

func (s *failingRollbackStore) GetHistory(ctx context.Context, id string, n int) ([]*Strategy, error) {
	return nil, nil
}

func TestDeployRollbackFailureErrorStatesReality(t *testing.T) {
	defer discardLogs()()
	store := &failingRollbackStore{failFrom: 2} // first SetActive OK, rollback fails
	guardrails, err := NewEvolutionGuardrails(WithBaselineScore(100))
	require.NoError(t, err)

	m, err := NewActiveStrategyManager(store, nil, WithASMGuardrails(guardrails))
	require.NoError(t, err)

	// Seed a current strategy so the deploy moves it into `previous` and
	// the guardrail rollback path has something to restore.
	m.mu.Lock()
	m.current = &mutation.Strategy{ID: "old", Version: 1}
	m.mu.Unlock()

	// Score 50 < baseline 100 → guardrail fires critical right after the
	// deploy persists; the rollback write then fails.
	err = m.Deploy(context.Background(), &mutation.Strategy{ID: "flagged", Version: 2, Score: 50})
	require.Error(t, err)

	assert.Contains(t, err.Error(), "remains active",
		"the error must state that the flagged strategy is still active, not read as 'deployment reverted'")
	assert.Contains(t, err.Error(), "flagged")

	// State reality must match the error: the new strategy is active in
	// both memory and the store.
	cur := m.Current()
	require.NotNil(t, cur)
	assert.Equal(t, "flagged", cur.ID, "in-memory state must match the store (new strategy active)")

	stored, err := store.GetActive(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "flagged", stored.ID, "the store must still hold the flagged strategy")
}
