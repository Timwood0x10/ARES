// Package introspect — memory subsystem read-model.
//
// The memory panel page renders a point-in-time frame of the runtime memory
// subsystem: live session/task counts, whether the distillation engine is
// armed (memory_search depends on it), RAG posture, and the effective
// config knobs. The frame is produced by the memory manager itself
// (memory.RuntimeStatus) and adapted on the serve side — this package stays
// decoupled from the memory implementation by defining its own DTO, the same
// way ChaosStatus decouples the chaos loops from the panel.
package introspect

// MemoryStatus is one frame of memory observability for the panel. Wired is
// false when the runtime was assembled without a memory component
// (memory.enabled=false): the other fields are then zero and the UI renders
// the disabled state instead of pretending the stores are empty.
type MemoryStatus struct {
	// Wired reports whether a memory manager is attached to the runtime.
	Wired bool `json:"wired"`
	// Sessions is the number of live sessions in the session store.
	Sessions int `json:"sessions"`
	// Tasks is the number of live tasks in the task store.
	Tasks int `json:"tasks"`
	// DistillationEngine reports whether the distillation engine is armed.
	// When false, memory_search / StoreDistilledTask fail closed.
	DistillationEngine bool `json:"distillation_engine"`
	// Retrievers is how many RAG ContextRetrievers are injected.
	Retrievers int `json:"retrievers"`
	// Skills is how many skills the resident registry advertises.
	Skills int `json:"skills"`
	// MaxHistory is the read-side context window used at BuildContext time.
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
	// Storage is the configured session/task store type.
	Storage string `json:"storage"`
	// Started reports whether the memory manager lifecycle is running.
	Started bool `json:"started"`
}
