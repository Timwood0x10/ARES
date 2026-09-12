package main

// memory_enricher — prompt enrichment for the serve L2 submission path:
// folds conversation history into the submission prompt so serve sessions
// carry cross-turn memory, mirroring the SDK's Agent.composePrompt memory
// section without moving memory knowledge into the shared agentruntime core.

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/Timwood0x10/ares/internal/agentruntime"
	"github.com/Timwood0x10/ares/internal/ares_config"
	memory "github.com/Timwood0x10/ares/internal/runtime/memory"
)

// memorySessionTTL bounds how long an idle L2-session → memory-session
// mapping is retained. Serve is a long-lived process and sess-auto-N grows
// monotonically, so without a bound the mapping table accumulates one entry
// per conversation turn forever. An expired entry is only a mapping loss —
// the underlying memory session stays in the MemoryManager until ITS OWN
// idle TTL reclaims it — so the next turn simply re-resolves (re-creating a
// memory session) instead of failing.
const memorySessionTTL = 30 * time.Minute

// memorySessionEntry is one retained mapping plus the last time it was used.
type memorySessionEntry struct {
	memSession string
	lastUsed   time.Time
}

// memoryPromptEnricher adapts the serve Memory component to the shared L2
// submission path's agentruntime.PromptEnricher: each turn's prompt is
// rewritten to carry the session's history context (BuildContext), and the
// turn is recorded (AddMessage) so the next submission sees it.
//
// Memory sessions mint their own IDs, so the enricher keeps an L2-session →
// memory-session mapping: the first turn creates the memory session, later
// turns on the same L2 session reuse it. Every failure degrades to the raw
// prompt — enrichment is additive context and must never block submission.
//
// Concurrency: the mapping table is guarded by e.mu, but the memory store
// write that MINTS a session runs outside that lock — CreateSession is I/O
// (sessionMemory.Set + event emit) and holding e.mu across it would
// serialize every first-turn submission in the process, across unrelated
// sessions. Concurrent first turns on the SAME L2 session are collapsed to
// one store write by e.flight (singleflight); concurrent first turns on
// DIFFERENT sessions proceed in parallel.
type memoryPromptEnricher struct {
	mgr    memory.MemoryManager
	logger *slog.Logger

	// ttl bounds entry retention; zero means memorySessionTTL. Read once at
	// construction so the janitor and the lazy sweep agree on one value.
	ttl time.Duration

	mu       sync.Mutex                    // guards sessions
	sessions map[string]memorySessionEntry // L2 session ID → memory session

	// flight collapses concurrent first-turn creation for one L2 session.
	// The key is the L2 session ID (NOT the minted memory session ID): two
	// callers racing on the same chat thread must share one store write,
	// otherwise the loser's AddMessage lands in a session nobody reads.
	flight singleflight.Group
}

// newMemoryPromptEnricher builds an enricher over a MemoryManager. A nil
// manager or logger yields a nil-safe configuration (logger defaults to the
// process logger).
func newMemoryPromptEnricher(mgr memory.MemoryManager, logger *slog.Logger) *memoryPromptEnricher {
	if logger == nil {
		logger = slog.Default()
	}
	return &memoryPromptEnricher{
		mgr:      mgr,
		logger:   logger,
		ttl:      memorySessionTTL,
		sessions: make(map[string]memorySessionEntry),
	}
}

// Enrich rewrites prompt to carry the session's history context and records
// the turn. It satisfies agentruntime.PromptEnricher: any memory failure
// returns the original prompt unchanged (fail-open).
func (e *memoryPromptEnricher) Enrich(ctx context.Context, sessionID, prompt string) string {
	memSession, err := e.memorySession(ctx, sessionID)
	if err != nil {
		e.logger.WarnContext(ctx, "serve: memory session unavailable; submitting without history context",
			"session_id", sessionID, "error", err)
		return prompt
	}
	built, err := e.mgr.BuildContext(ctx, prompt, memSession)
	if err != nil {
		e.logger.WarnContext(ctx, "serve: memory context build failed; submitting without history context",
			"session_id", sessionID, "error", err)
		return prompt
	}
	if strings.TrimSpace(built) == "" {
		return prompt
	}
	// Best-effort history write: the context build already succeeded, so a
	// record failure degrades only this turn's memory, never the submission
	// (same contract as the SDK composePrompt's AddMessage call).
	if err := e.mgr.AddMessage(ctx, memSession, "user", prompt); err != nil {
		e.logger.WarnContext(ctx, "serve: memory turn record failed",
			"session_id", sessionID, "error", err)
	}
	return built
}

// memorySession resolves (creating on first turn) the memory session that
// backs an L2 session.
//
// The fast path is a map read under e.mu; only a genuine miss reaches the
// store, and that write is taken through singleflight so N concurrent first
// turns on one L2 session produce ONE CreateSession call. The slow path
// re-checks the map after the flight lands — a losing caller may find the
// winner already stored the entry.
func (e *memoryPromptEnricher) memorySession(ctx context.Context, sessionID string) (string, error) {
	if mem, ok := e.lookup(sessionID); ok {
		return mem, nil
	}
	// Keyed by the L2 session ID so concurrent first turns on the SAME
	// thread share one mint; different sessions never block each other.
	v, err, _ := e.flight.Do(sessionID, func() (any, error) {
		// Re-check under the flight: another caller may have minted between
		// our map miss and this call being admitted.
		if mem, ok := e.lookup(sessionID); ok {
			return mem, nil
		}
		mem, err := e.mgr.CreateSession(ctx, sessionID)
		if err != nil {
			return "", fmt.Errorf("create memory session for %s: %w", sessionID, err)
		}
		e.store(sessionID, mem)
		return mem, nil
	})
	if err != nil {
		return "", err
	}
	mem, _ := v.(string)
	return mem, nil
}

// lookup returns the retained memory session for sessionID, refreshing its
// last-used stamp so an actively conversing session is never swept. Expired
// entries are dropped lazily on read, so the janitor is a safety net for
// sessions that go silent rather than the only reclaimer.
func (e *memoryPromptEnricher) lookup(sessionID string) (string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sweepLocked()
	entry, ok := e.sessions[sessionID]
	if !ok {
		return "", false
	}
	entry.lastUsed = time.Now()
	e.sessions[sessionID] = entry
	return entry.memSession, true
}

// store records a freshly minted mapping.
func (e *memoryPromptEnricher) store(sessionID, memSession string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sweepLocked()
	e.sessions[sessionID] = memorySessionEntry{memSession: memSession, lastUsed: time.Now()}
}

// sweepLocked drops entries idle past the TTL. Caller must hold e.mu.
//
// The sweep is O(n) on every map touch, which is deliberate: the table is
// small (one entry per live conversation), and sweeping inline removes the
// need for a background goroutine whose lifecycle would have to be tied to
// the serve shutdown path.
func (e *memoryPromptEnricher) sweepLocked() {
	if len(e.sessions) == 0 {
		return
	}
	ttl := e.ttl
	if ttl <= 0 {
		ttl = memorySessionTTL
	}
	cutoff := time.Now().Add(-ttl)
	for id, entry := range e.sessions {
		if entry.lastUsed.Before(cutoff) {
			delete(e.sessions, id)
		}
	}
}

// resolveServePromptEnricher returns the memory enricher for the serve
// submission path, or nil when memory is disabled or not built — a nil
// enricher leaves admission unchanged, and tests that build the kernel
// without a Memory component keep pass-through semantics.
func resolveServePromptEnricher(
	cfg *ares_config.Config,
	mgr memory.MemoryManager,
	logger *slog.Logger,
) agentruntime.PromptEnricher {
	if cfg == nil || mgr == nil || !cfg.Memory.IsEnabled() {
		return nil
	}
	return newMemoryPromptEnricher(mgr, logger).Enrich
}
