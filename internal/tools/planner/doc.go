// Package planner implements a capability-driven tool selection and execution
// planning layer for autonomous agents.
//
// # Pipeline
//
// The planning pipeline has 6 stages:
//
//	User Request
//	    │
//	    ▼
//	 1. SemanticAnalyzer  — parse request into structured Intent
//	    │
//	    ▼
//	 2. CapabilityPlanner — decompose Intent into capability requirements
//	    │
//	    ▼
//	 3. ToolResolver      — map requirements to candidate tools
//	    │
//	    ▼
//	 4. ToolScorer        — rank candidates by metadata + historical evidence
//	    │
//	    ▼
//	 5. ExecutionPlanner  — build single-step or multi-step DAG plan
//	    │
//	    ▼
//	 6. ParameterExtractor — fill tool params from natural language request
//
// Planner does NOT execute tools. It only produces plans.
// For execution, use ToolExecutionBridge.
//
// # ToolExecutionBridge
//
// The bridge wraps a tool registry (core.Registry) with planner-based fallback:
//
//	Direct execution:
//	  tool found → execute directly
//	  tool not found → Planner.Plan() → execute plan
//
//	After every execution, the bridge saves evidence (latency, success/failure)
//	to the EvidenceStore so future scoring can use historical data.
//
// # EvidenceStore Plugin System
//
// EvidenceStore is a plugin interface. The default implementation is
// NewMemoryEvidenceStore() (in-memory, lost on restart). External
// implementations can replace it with any backend:
//
//	type MyPostgresStore struct { db *pgx.Pool }
//	func (s *MyPostgresStore) Save(ctx context.Context, ev *ToolEvidence) error { ... }
//	func (s *MyPostgresStore) Query(ctx context.Context, ...) ([]ToolEvidence, error) { ... }
//	func (s *MyPostgresStore) Aggregate(ctx context.Context, ...) (map[string]ToolScore, error) { ... }
//
//	store := &MyPostgresStore{db: pool}
//	planner, err := NewPlanner(analyzer, capPlanner, resolver, scorer, execPlan, store)
//
// # ToolProvider Interface
//
// The planner discovers tools through the ToolProvider interface. The
// RegistryProvider adapter wraps core.Registry and performs broad-to-granular
// capability expansion:
//
//   - Tool declares CapabilityText ("text")
//   - RegistryProvider expands to: StringManipulation, Regex, Hashing,
//     Base64, JSONProcessing, LogAnalysis, TextProcessor, ...
//   - Planner resolver matches these against requirement names
//
// # Broad → Granular Capability Mapping
//
// | Broad (tool declares)    | Granular (planner uses)                                |
// |--------------------------|--------------------------------------------------------|
// | math                     | Arithmetic, Summation, DiscreteMath, Probability, ...  |
// | text                     | StringManipulation, Regex, Hashing, Base64, ...        |
// | network                  | WebSearch, HTTPRequest, WebFetch                       |
// | file                     | PDFParsing, TextExtraction                             |
// | time                     | DateTime                                               |
// | knowledge                | WebSearch                                              |
// | external                 | CodeExecution, IDGeneration                            |
// | memory                   | Embedding                                              |
//
// # Integration with sub.ToolBinder
//
// The production server (cmd/ares) wires the planner bridge into the agent
// tool binder:
//
//	internalReg := setupMCP(ctx, cfg, registry)
//	binder := newToolBinder(internalReg)
//	bridge := newPlannerBridge(internalReg)
//	binder.WithPlannerBridge(bridge)
//
// When an agent calls an unknown tool, the binder falls back to the planner
// bridge which resolves the intent and auto-selects the best tool.
package planner
