package ares_mcp

import (
	"fmt"
	"sync"
	"time"
)

// ServerHealth is the availability of ONE MCP server as last observed. There is
// exactly one cell per server name, shared by the manager (which owns it), the
// client (which detects its own transport dying) and every tool of that server
// (which reads it). One cell means one answer to "can I use this server", instead
// of the three half-answers the code used to have: tools silently absent when the
// server never connected (S-mcp-S2), tools silently present as zombies after the
// transport died (S-mcp-S5), and a health flag updated by discovery that no one
// read (S-mcp-S4).
//
// State transitions. "Removed" is not a state here: closing a connection on
// purpose (Stop / DisconnectServer / config reload) goes through the manager's
// existing unregister path, which removes the tools entirely — an operator's
// deliberate disconnect is not an outage to mark.
//
//	available   --transport dies--        --> unavailable(reason)
//	available   --connect fails--         --> unavailable(reason)
//	available   --health check: unhealthy-> unavailable(reason)
//	unavailable --reconnect succeeds--    --> available
//	unavailable --health check: healthy-- > available
//
// The unavailable→available edges matter as much as the reverse ones: without
// them a server that recovered stayed marked forever, which is the same silent
// inconsistency pointing the other way.
//
// All methods are safe on a nil *ServerHealth and report available: a tool built
// outside the manager (SDK callers, tests) has no cell and must not be treated as
// broken. The cell is also deliberately NOT a liveness probe — it records what
// somebody actually observed; see MCPManager.SetServerHealth.
type ServerHealth struct {
	mu        sync.RWMutex
	available bool
	reason    string
	since     time.Time
	// now supplies the since stamp. It is a field rather than a direct time.Now
	// call so a test can pin the clock and assert a DURATION, matching the
	// fabric's WithClock discipline; it is deliberately unexported because no
	// production caller needs to control it (exports are for callers that need
	// them, not for symmetry). Guarded by mu.
	now func() time.Time
}

// NewServerHealth creates an available cell. A fresh client has not observed any
// failure yet, and reporting an unproven outage would be its own bug.
func NewServerHealth() *ServerHealth {
	return &ServerHealth{available: true, now: time.Now}
}

// MarkUnavailable records that the server cannot be used, with the reason (a
// transport error, a health-check message, a failed connect). Re-marking an
// already-unavailable server keeps the FIRST failure's time: the since stamp
// answers "how long has this been broken", which a refresh of the reason must
// not restart.
func (h *ServerHealth) MarkUnavailable(reason string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.available {
		h.reason = reason
		return
	}
	now := h.now
	if now == nil {
		now = time.Now // a cell built as a bare struct literal still works
	}
	h.available = false
	h.reason = reason
	h.since = now()
}

// MarkAvailable records that the server is usable again, clearing the reason.
func (h *ServerHealth) MarkAvailable() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.available = true
	h.reason = ""
	h.since = time.Time{}
}

// IsAvailable reports whether the server was usable as of the last observation.
func (h *ServerHealth) IsAvailable() bool {
	if h == nil {
		return true
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.available
}

// State returns the last observation: whether the server is usable, why not, and
// since when. The zero time means "available".
func (h *ServerHealth) State() (bool, string, time.Time) {
	if h == nil {
		return true, "", time.Time{}
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.available, h.reason, h.since
}

// UnavailableNote renders the state as one short sentence for a tool description,
// or "" when the server is usable. It goes into the tool's description on
// purpose: the tool list is what the model is handed, so this is the one place a
// notice reaches the model WITHOUT new prompt plumbing, and it is the difference
// between "the tool is gone, I must have misremembered it" and "the tool is here
// but its server is down" (S-mcp-S2).
func (h *ServerHealth) UnavailableNote() string {
	if h == nil {
		return ""
	}
	h.mu.RLock()
	available, reason, since := h.available, h.reason, h.since
	h.mu.RUnlock()
	if available {
		return ""
	}
	note := "UNAVAILABLE: this tool's MCP server is not usable right now"
	if reason != "" {
		note += fmt.Sprintf(" (%s)", reason)
	}
	if !since.IsZero() {
		note += fmt.Sprintf(" since %s", since.UTC().Format(time.RFC3339))
	}
	return note + "; do not call it, use another tool"
}
