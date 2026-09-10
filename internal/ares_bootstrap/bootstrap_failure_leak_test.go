package ares_bootstrap

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
)

// TestBootstrap_FailureStopsBackgroundWorkers locks REVIEW 2.7#41: when a
// LATE wiring step fails, every background worker Bootstrap already started
// (the evolution scheduler's subscription, its shutdown watcher, the
// distillation subscriber) must stop — runCleanups cancels the
// bootstrap-scoped context even though the CALLER's context stays alive
// (e.g. a serve loop that keeps running after a failed bootstrap).
//
// The late failure is driven by evolution.gates.eval_strict=true with no
// eval suite wired: wireGAEvolution fails the bootstrap AFTER the legacy
// evolution scheduler has registered its event subscription.
func TestBootstrap_FailureStopsBackgroundWorkers(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	cfg := ares_config.NewMinimalConfig("https://api.example.com/v1", "sk-test", "")
	// Evolution on: wires the legacy scheduler (subscription goroutine +
	// shutdown watcher) before the failure point.
	cfg.Evolution.Enabled = true
	// Strict G3 gate with no suite configured → hard bootstrap failure in
	// wireGAEvolution (fail closed by design).
	cfg.Evolution.Gates.EvalStrict = true

	// The caller's context STAYS ALIVE after the failed bootstrap — the
	// leak the fix addresses only exists when the caller does not cancel.
	ctx := context.Background()

	_, err := Bootstrap(ctx, cfg, &BootstrapDeps{
		EventStore: ares_events.NewMemoryEventStore(),
		ExpRepo:    repositories.NewMemoryExperienceRepository(),
	})
	require.Error(t, err, "strict eval gate without a suite must fail bootstrap")

	// Give the cancelled workers a moment to observe bctx cancellation and
	// exit before goleak snapshots goroutines.
	time.Sleep(200 * time.Millisecond)
}
