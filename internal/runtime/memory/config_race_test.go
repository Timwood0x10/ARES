package memory

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	patch "github.com/Timwood0x10/ares/internal/runtime/evolution/patch"
)

// TestMemoryConfigPatch_RacesWithHotPaths pins the config race fix: the
// MemoryPatchExecutor mutates MaxHistory/CleanOptions under the write lock,
// so the hot read paths (BuildPromptMessages / BuildContext) MUST take the
// read lock. Run under -race, the old unlocked reads failed this test.
func TestMemoryConfigPatch_RacesWithHotPaths(t *testing.T) {
	ctx := context.Background()
	mgr, err := NewMemoryManager(DefaultMemoryConfig())
	require.NoError(t, err)
	defer func() { _ = mgr.Stop(ctx) }()

	sessionID, err := mgr.CreateSession(ctx, "race-user")
	require.NoError(t, err)
	require.NoError(t, mgr.AddMessage(ctx, sessionID, "user", "hello"))

	reg := patch.NewRegistry()
	require.NoError(t, reg.RegisterComponent(NewMemoryPatchExecutor(castConcrete(t, mgr))))

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)

	// Writer: apply config patches in a loop (evolution ticker shape).
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			h := 3 + i%50
			_ = reg.Apply(ctx, patch.RuntimePatch{
				Type:   patch.PatchChangePlanner,
				Target: "memory",
				Value:  map[string]any{"max_history": h},
			})
			i++
		}
	}()

	// Reader: hammer the hot prompt/context paths concurrently.
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, _ = mgr.BuildPromptMessages(ctx, sessionID)
			_, _ = mgr.BuildContext(ctx, "query", sessionID)
		}
	}()

	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// TestMemoryConfigPatch_RacesWithStatusAndSessionCap extends the hot-path
// race contract to the surfaces added with the introspect Memory panel and
// the session-cap plumbing: RuntimeStatus must copy config under the read
// lock, and ApplyLiveConfig's session-cap push (WithMaxMessages) must not
// write the live store while AddMessage/GetMessages read it. A config with
// SessionMaxHistory>0 is required — with the zero default the push branch is
// skipped and the race stays hidden.
func TestMemoryConfigPatch_RacesWithStatusAndSessionCap(t *testing.T) {
	ctx := context.Background()
	cfg := DefaultMemoryConfig()
	cfg.SessionMaxHistory = 8
	cfg.DistillationThreshold = 3
	mgr, err := NewMemoryManagerWithDistiller(cfg, &testEmbedder{}, &testExpRepo{})
	require.NoError(t, err)
	defer func() { _ = mgr.Stop(ctx) }()
	mm := castConcrete(t, mgr)

	sessionID, err := mgr.CreateSession(ctx, "race-user")
	require.NoError(t, err)

	reg := patch.NewRegistry()
	require.NoError(t, reg.RegisterComponent(NewMemoryPatchExecutor(mm)))

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(3)

	// First non-nil loop error, recorded for a post-join assertion: a race
	// hammer that swallows Apply/AddMessage failures could pass while the
	// subsystem is actually broken (code_rules_v2 §3.1).
	var errMu sync.Mutex
	var loopErr error
	record := func(e error) {
		if e == nil {
			return
		}
		errMu.Lock()
		if loopErr == nil {
			loopErr = e
		}
		errMu.Unlock()
	}

	// Writer: evolution-style config patches. Each Apply re-enters
	// ApplyLiveConfig with the live config, whose SessionMaxHistory stays at
	// the boot value — exactly the branch under test.
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			record(reg.Apply(ctx, patch.RuntimePatch{
				Type:   patch.PatchChangePlanner,
				Target: "memory",
				Value:  map[string]any{"max_history": 3 + i%20},
			}))
			i++
		}
	}()

	// Reader: introspect panel pull loop (no error return by design).
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			mm.RuntimeStatus()
		}
	}()

	// Traffic: session store reads/writes racing the cap push.
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			record(mgr.AddMessage(ctx, sessionID, "user", "hello"))
			if _, getErr := mgr.GetMessages(ctx, sessionID); getErr != nil {
				record(getErr)
			}
		}
	}()

	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()

	errMu.Lock()
	finalErr := loopErr
	errMu.Unlock()
	if finalErr != nil {
		t.Fatalf("concurrent loop surfaced an error: %v", finalErr)
	}
}
