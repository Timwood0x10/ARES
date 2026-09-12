package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_config"
	memory "github.com/Timwood0x10/ares/internal/runtime/memory"
)

// newTestMemoryManager builds a real in-memory MemoryManager (no external
// dependencies) and registers its shutdown with the test lifecycle.
func newTestMemoryManager(t *testing.T) memory.MemoryManager {
	t.Helper()
	mgr, err := memory.NewMemoryManager(memory.DefaultMemoryConfig())
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = mgr.Stop(context.Background()) // best-effort test cleanup
	})
	return mgr
}

func testEnricher(t *testing.T, mgr memory.MemoryManager) *memoryPromptEnricher {
	t.Helper()
	return newMemoryPromptEnricher(mgr, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestMemoryPromptEnricherCarriesHistoryAcrossTurns: turn two's rewrite
// must contain turn one's question — cross-turn continuity on one L2
// session is the entire point of the serve memory hook — while a
// different L2 session stays isolated.
func TestMemoryPromptEnricherCarriesHistoryAcrossTurns(t *testing.T) {
	e := testEnricher(t, newTestMemoryManager(t))

	first := e.Enrich(context.Background(), "sess-1", "what is 2+2?")
	require.Contains(t, first, "what is 2+2?")

	second := e.Enrich(context.Background(), "sess-1", "and times 3?")
	require.Contains(t, second, "what is 2+2?", "second turn must carry the first turn's history")
	require.Contains(t, second, "and times 3?", "current turn must stay in the rewritten prompt")

	isolated := e.Enrich(context.Background(), "sess-2", "different topic")
	require.NotContains(t, isolated, "what is 2+2?", "sessions must not leak history into each other")
}

// failingCreateMemoryManager fails session creation on top of a working
// base manager (the embedded interface supplies every other method).
type failingCreateMemoryManager struct {
	memory.MemoryManager
}

func (failingCreateMemoryManager) CreateSession(context.Context, string) (string, error) {
	return "", errors.New("store offline")
}

// failingBuildContextMemoryManager fails context assembly after session
// creation succeeds.
type failingBuildContextMemoryManager struct {
	memory.MemoryManager
}

func (failingBuildContextMemoryManager) BuildContext(context.Context, string, string) (string, error) {
	return "", errors.New("retrieval broken")
}

// TestMemoryPromptEnricherFailsOpen: every memory failure degrades to the
// raw prompt — enrichments are additive context and must never block or
// empty a submission.
func TestMemoryPromptEnricherFailsOpen(t *testing.T) {
	base := newTestMemoryManager(t)
	tests := []struct {
		name string
		mgr  memory.MemoryManager
	}{
		{name: "session creation failure", mgr: failingCreateMemoryManager{MemoryManager: base}},
		{name: "context build failure", mgr: failingBuildContextMemoryManager{MemoryManager: base}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := testEnricher(t, tc.mgr).Enrich(context.Background(), "sess-1", "hello")
			require.Equal(t, "hello", got)
		})
	}
}

// countingMemoryManager counts CreateSession calls on top of a working base
// manager so the singleflight collapse is observable.
type countingMemoryManager struct {
	memory.MemoryManager
	creates atomic.Int64
}

func (c *countingMemoryManager) CreateSession(ctx context.Context, userID string) (string, error) {
	c.creates.Add(1)
	return c.MemoryManager.CreateSession(ctx, userID)
}

// TestMemoryPromptEnricherCollapsesConcurrentFirstTurns (P1): N goroutines
// racing on the SAME L2 session's first turn must produce exactly ONE store
// write. Before singleflight the map lock serialized them but each miss
// still reached CreateSession — and with the lock moved off the I/O path,
// without singleflight every caller would mint its own session and the
// losers' AddMessage calls would land in sessions nobody reads.
func TestMemoryPromptEnricherCollapsesConcurrentFirstTurns(t *testing.T) {
	mgr := &countingMemoryManager{MemoryManager: newTestMemoryManager(t)}
	e := testEnricher(t, mgr)

	const n = 16
	var wg sync.WaitGroup
	sessions := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// memorySession is the unit under test; Enrich would add a
			// BuildContext/AddMessage on top and blur the count.
			mem, err := e.memorySession(context.Background(), "sess-race")
			require.NoError(t, err)
			sessions[i] = mem
		}(i)
	}
	wg.Wait()

	require.Equal(t, int64(1), mgr.creates.Load(),
		"concurrent first turns on one L2 session must share a single CreateSession")
	for i, s := range sessions {
		require.Equal(t, sessions[0], s, "caller %d must resolve to the winner's memory session", i)
	}
}

// TestMemoryPromptEnricherParallelSessionsDoNotBlock: two DIFFERENT L2
// sessions racing must each mint their own memory session. This is the
// complement of the collapse test — singleflight is keyed per L2 session,
// so it must not over-collapse across unrelated chat threads.
func TestMemoryPromptEnricherParallelSessionsDoNotBlock(t *testing.T) {
	mgr := &countingMemoryManager{MemoryManager: newTestMemoryManager(t)}
	e := testEnricher(t, mgr)

	var wg sync.WaitGroup
	results := make(map[string]string)
	var mu sync.Mutex
	for _, id := range []string{"sess-a", "sess-b", "sess-c"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			mem, err := e.memorySession(context.Background(), id)
			require.NoError(t, err)
			mu.Lock()
			results[id] = mem
			mu.Unlock()
		}(id)
	}
	wg.Wait()

	require.Equal(t, int64(3), mgr.creates.Load(), "each distinct L2 session mints its own memory session")
	require.Len(t, results, 3)
	require.NotEqual(t, results["sess-a"], results["sess-b"], "sessions must not share a memory session")
}

// TestMemoryPromptEnricherSweepsIdleEntries (P2): an entry idle past the TTL
// is dropped, so a long-lived serve process cannot accumulate one mapping
// per conversation turn forever.
func TestMemoryPromptEnricherSweepsIdleEntries(t *testing.T) {
	e := testEnricher(t, newTestMemoryManager(t))
	e.ttl = 20 * time.Millisecond

	mem, err := e.memorySession(context.Background(), "sess-old")
	require.NoError(t, err)
	require.NotEmpty(t, mem)
	require.Len(t, e.sessions, 1)

	time.Sleep(40 * time.Millisecond)

	// Any map touch sweeps; a different session's lookup is enough.
	_, err = e.memorySession(context.Background(), "sess-new")
	require.NoError(t, err)
	require.NotContains(t, e.sessions, "sess-old", "idle entry past TTL must be swept")
	require.Contains(t, e.sessions, "sess-new", "the fresh entry must survive the sweep")
}

// TestMemoryPromptEnricherActiveSessionSurvivesSweep: a session still in
// use refreshes its last-used stamp on every lookup, so an ongoing
// conversation is never swept out from under itself.
func TestMemoryPromptEnricherActiveSessionSurvivesSweep(t *testing.T) {
	e := testEnricher(t, newTestMemoryManager(t))
	e.ttl = 60 * time.Millisecond

	first, err := e.memorySession(context.Background(), "sess-live")
	require.NoError(t, err)

	// Keep it warm across more than one TTL window.
	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		again, err := e.memorySession(context.Background(), "sess-live")
		require.NoError(t, err)
		require.Equal(t, first, again, "an active session must keep its memory session")
		time.Sleep(20 * time.Millisecond)
	}
	require.Contains(t, e.sessions, "sess-live")
}

// TestMemoryPromptEnricherDefaultTTL: a zero ttl field must not disable
// sweeping — it falls back to memorySessionTTL rather than retaining
// entries forever.
func TestMemoryPromptEnricherDefaultTTL(t *testing.T) {
	e := testEnricher(t, newTestMemoryManager(t))
	e.ttl = 0
	e.store("sess-x", "mem-x")

	e.mu.Lock()
	e.sweepLocked()
	e.mu.Unlock()
	require.Contains(t, e.sessions, "sess-x", "a just-stored entry is younger than the default TTL")
}

// TestResolveServePromptEnricher: the wiring helper returns nil (pass-
// through admission) unless both the config enables memory and a manager
// was built — nil-safe on every argument so partial test kernels behave
// like memory-disabled deployments.
func TestResolveServePromptEnricher(t *testing.T) {
	mgr := newTestMemoryManager(t)
	enabledCfg := ares_config.NewMinimalConfig("", "", "")
	disabledCfg := ares_config.NewMinimalConfig("", "", "")
	off := false
	disabledCfg.Memory.Enabled = &off

	tests := []struct {
		name    string
		cfg     *ares_config.Config
		mgr     memory.MemoryManager
		wantNil bool
	}{
		{name: "nil config", cfg: nil, mgr: mgr, wantNil: true},
		{name: "nil manager", cfg: enabledCfg, mgr: nil, wantNil: true},
		{name: "memory disabled", cfg: disabledCfg, mgr: mgr, wantNil: true},
		{name: "enabled with manager", cfg: enabledCfg, mgr: mgr, wantNil: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveServePromptEnricher(tc.cfg, tc.mgr, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if tc.wantNil {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
		})
	}
}
