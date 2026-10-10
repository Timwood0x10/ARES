// Package memory — runtime status read-model for observability surfaces.
package memory

// RuntimeStatus is a read-only point-in-time view of the memory subsystem,
// produced by memoryManager.RuntimeStatus for the introspect panel. It is a
// pure read of already-guarded state — no I/O, no locking beyond the config/
// registry snapshots the method takes internally — so the panel's 2s pull
// loop can call it freely.
type RuntimeStatus struct {
	// Sessions is the number of live sessions in the session store.
	Sessions int `json:"sessions"`
	// Tasks is the number of live tasks in the task store.
	Tasks int `json:"tasks"`
	// DistillationEngineArmed reports whether pipeline+expRepo are attached
	// (via NewMemoryManagerWithDistiller or SetDistillationEngine). When
	// false, memory_search and StoreDistilledTask fail closed.
	DistillationEngineArmed bool `json:"distillation_engine_armed"`
	// Retrievers is how many ContextRetrievers are injected (RAG read side).
	Retrievers int `json:"retrievers"`
	// Skills is how many skills the resident registry advertises (0 when no
	// registry is attached).
	Skills int `json:"skills"`
	// MaxHistory is the read-side context window (BuildContext truncation).
	MaxHistory int `json:"max_history"`
	// SessionMaxHistory is the per-session stored-message cap (0 = component
	// default).
	SessionMaxHistory int `json:"session_max_history"`
	// DistillationThreshold is the configured round gate (0 = ungated).
	DistillationThreshold int `json:"distillation_threshold"`
	// MaxSessions is the session-store capacity.
	MaxSessions int `json:"max_sessions"`
	// EnableRAG reports whether retrieval-augmented prompt injection is on.
	EnableRAG bool `json:"enable_rag"`
	// RAGTopK / RAGMinScore tune retrieval when EnableRAG is true.
	RAGTopK     int     `json:"rag_top_k"`
	RAGMinScore float64 `json:"rag_min_score"`
	// Storage is the configured session/task store type ("memory"/"postgres").
	Storage string `json:"storage"`
	// Started reports whether Start has run and Stop has not.
	Started bool `json:"started"`
}

// RuntimeStatus returns the current subsystem status. It satisfies the
// consumer-side statser interface the introspect panel wiring type-asserts
// for — kept off the public MemoryManager interface so existing
// implementations are not broken.
//
// Locking: every config field is copied INSIDE the RLock — the patch
// executor mutates those fields under the write lock, so dereferencing the
// config pointer after release would race with a concurrent Apply. Store
// counts and the skills registry List run outside m.mu on purpose: Size()
// and List() guard their own state and must not nest under the manager lock.
func (m *memoryManager) RuntimeStatus() RuntimeStatus {
	m.mu.RLock()
	engineArmed := m.pipeline != nil && m.expRepo != nil
	retrieverCount := len(m.retrievers)
	reg := m.skillsRegistry
	started := m.started && !m.stopped
	st := RuntimeStatus{
		DistillationEngineArmed: engineArmed,
		Retrievers:              retrieverCount,
		Started:                 started,
	}
	if m.config != nil {
		cfg := m.config
		st.MaxHistory = cfg.MaxHistory
		// Report the EFFECTIVE store cap, not the raw knob: the runtime
		// clamps SessionMaxHistory up to MaxHistory (see
		// effectiveSessionMaxHistory), so the raw config value can understate
		// what the session store actually retains. Zero still means
		// "component default".
		st.SessionMaxHistory = effectiveSessionMaxHistory(cfg)
		st.DistillationThreshold = cfg.DistillationThreshold
		st.MaxSessions = cfg.MaxSessions
		st.EnableRAG = cfg.EnableRAG
		st.RAGTopK = cfg.RAGTopK
		st.RAGMinScore = cfg.RAGMinScore
		st.Storage = cfg.Storage
	}
	m.mu.RUnlock()

	st.Sessions = m.sessionMemory.Size()
	st.Tasks = m.taskMemory.Size()
	if reg != nil {
		st.Skills = len(reg.List())
	}
	return st
}
