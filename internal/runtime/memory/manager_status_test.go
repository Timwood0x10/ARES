package memory

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuntimeStatusReportsWiredManager(t *testing.T) {
	cfg := DefaultMemoryConfig()
	cfg.SessionMaxHistory = 12
	cfg.DistillationThreshold = 4
	cfg.EnableRAG = true
	cfg.RAGTopK = 9

	mgr, mgrErr := NewMemoryManager(cfg)
	require.NoError(t, mgrErr)
	mm, ok := mgr.(*memoryManager)
	require.True(t, ok)

	st := mm.RuntimeStatus()
	require.False(t, st.DistillationEngineArmed, "engine-less manager must report unarmed")
	require.Equal(t, 12, st.SessionMaxHistory)
	require.Equal(t, 4, st.DistillationThreshold)
	require.Equal(t, cfg.MaxHistory, st.MaxHistory)
	require.Equal(t, cfg.MaxSessions, st.MaxSessions)
	require.True(t, st.EnableRAG)
	require.Equal(t, 9, st.RAGTopK)
	require.Equal(t, StorageMemory, st.Storage)

	require.NoError(t, mm.SetDistillationEngine(&testEmbedder{}, &testExpRepo{}))
	st2 := mm.RuntimeStatus()
	require.True(t, st2.DistillationEngineArmed, "injection must arm the engine")
}

// TestRuntimeStatusReportsEffectiveSessionCap pins the panel truthfulness
// contract: the status frame must report the ENFORCED store cap (clamped up
// to the read window), not the raw config knob that undercuts it.
func TestRuntimeStatusReportsEffectiveSessionCap(t *testing.T) {
	cfg := DefaultMemoryConfig()
	cfg.MaxHistory = 100
	cfg.SessionMaxHistory = 50 // raw knob below the read window
	mgr, mgrErr := NewMemoryManager(cfg)
	require.NoError(t, mgrErr)
	mm, ok := mgr.(*memoryManager)
	require.True(t, ok)

	st := mm.RuntimeStatus()
	require.Equal(t, 50, cfg.SessionMaxHistory, "constructor must not mutate the caller config")
	require.Equal(t, 100, st.SessionMaxHistory,
		"status must report the effective (clamped) cap, not the undercutting knob")
}

func TestSessionMaxHistoryCapsStoredMessages(t *testing.T) {
	ctx := context.Background()
	cfg := DefaultMemoryConfig()
	// Both knobs at 2: the effective store cap is max(session, read window),
	// so a matching pair exercises the cap without the floor raising it.
	cfg.MaxHistory = 2
	cfg.SessionMaxHistory = 2
	mgr, mgrErr := NewMemoryManager(cfg)
	require.NoError(t, mgrErr)

	sessionID, sessErr := mgr.CreateSession(ctx, "u1")
	require.NoError(t, sessErr)

	for i := 0; i < 5; i++ {
		require.NoError(t, mgr.AddMessage(ctx, sessionID, "user", "hello"))
	}
	msgs, msgErr := mgr.GetMessages(ctx, sessionID)
	require.NoError(t, msgErr)
	require.LessOrEqual(t, len(msgs), 2,
		"store must honor the effective session cap, not every appended turn")
}

// TestSessionStoreCapNeverUndercutsReadWindow pins the P2 invariant: a
// SessionMaxHistory below MaxHistory is clamped UP to the read window at
// construction, so BuildContext can always draw the configured depth (the
// component default in session.go exists for the same reason).
func TestSessionStoreCapNeverUndercutsReadWindow(t *testing.T) {
	ctx := context.Background()
	cfg := DefaultMemoryConfig()
	cfg.MaxHistory = 100
	cfg.SessionMaxHistory = 50 // below the read window — must be raised to 100
	mgr, mgrErr := NewMemoryManager(cfg)
	require.NoError(t, mgrErr)

	sessionID, sessErr := mgr.CreateSession(ctx, "u-cap")
	require.NoError(t, sessErr)
	for i := 0; i < 80; i++ {
		require.NoError(t, mgr.AddMessage(ctx, sessionID, "user", "hello"))
	}
	msgs, msgErr := mgr.GetMessages(ctx, sessionID)
	require.NoError(t, msgErr)
	require.Greater(t, len(msgs), 50,
		"store must retain more than the configured SessionMaxHistory when it undercuts MaxHistory")
	require.Equal(t, 100, effectiveSessionMaxHistory(cfg),
		"effective cap must clamp up to the read window")
}

func TestDistillationThresholdReachesDistillerConfig(t *testing.T) {
	snapshotThreshold := func(mm *memoryManager) int {
		mm.mu.RLock()
		defer mm.mu.RUnlock()
		if mm.distillConfig == nil {
			return -1
		}
		return mm.distillConfig.DistillationThreshold
	}

	cfg := DefaultMemoryConfig()
	cfg.DistillationThreshold = 7
	mgr, mgrErr := NewMemoryManagerWithDistiller(cfg, &testEmbedder{}, &testExpRepo{})
	require.NoError(t, mgrErr)
	mm, ok := mgr.(*memoryManager)
	require.True(t, ok)
	require.Equal(t, 7, snapshotThreshold(mm),
		"constructor must stamp DistillationThreshold into the distiller config")

	// Injected engine honors the same knob on the serve path.
	mgr2, mgr2Err := NewMemoryManager(cfg)
	require.NoError(t, mgr2Err)
	mm2, ok2 := mgr2.(*memoryManager)
	require.True(t, ok2)
	require.NoError(t, mm2.SetDistillationEngine(&testEmbedder{}, &testExpRepo{}))
	require.Equal(t, 7, snapshotThreshold(mm2),
		"SetDistillationEngine must stamp DistillationThreshold into the distiller config")
}

func TestApplyLiveConfigPushesSessionCapAndThreshold(t *testing.T) {
	ctx := context.Background()
	mgr, mgrErr := NewMemoryManagerWithDistiller(DefaultMemoryConfig(), &testEmbedder{}, &testExpRepo{})
	require.NoError(t, mgrErr)
	mm, ok := mgr.(*memoryManager)
	require.True(t, ok)

	patched := DefaultMemoryConfig()
	patched.MaxHistory = 2
	patched.SessionMaxHistory = 2
	patched.DistillationThreshold = 5
	mm.ApplyLiveConfig(patched)

	mm.mu.RLock()
	threshold := -1
	if mm.distillConfig != nil {
		threshold = mm.distillConfig.DistillationThreshold
	}
	mm.mu.RUnlock()
	require.Equal(t, 5, threshold, "ApplyLiveConfig must push the round gate into the live distiller")

	sessionID, sessErr := mgr.CreateSession(ctx, "u2")
	require.NoError(t, sessErr)
	for i := 0; i < 5; i++ {
		require.NoError(t, mgr.AddMessage(ctx, sessionID, "user", "hello"))
	}
	msgs, msgErr := mgr.GetMessages(ctx, sessionID)
	require.NoError(t, msgErr)
	require.LessOrEqual(t, len(msgs), 2,
		"ApplyLiveConfig must push the effective session cap into the live store")
}

func TestMemoryConfigValidateRejectsNegativeNewKnobs(t *testing.T) {
	cfg := DefaultMemoryConfig()
	cfg.SessionMaxHistory = -1
	require.Error(t, cfg.validate(), "negative SessionMaxHistory must be rejected")

	cfg = DefaultMemoryConfig()
	cfg.DistillationThreshold = -1
	require.Error(t, cfg.validate(), "negative DistillationThreshold must be rejected")
}
