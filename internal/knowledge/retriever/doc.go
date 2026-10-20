// Package retriever implements intent-driven knowledge retrieval.
//
// Unlike traditional TopK vector search, the Retriever uses the full AKF
// pipeline (Plan → Load → Pipeline → Link → Reduce → Compile) to produce
// LLM-ready context from a natural language query. Embedding is used only
// as a fallback signal, not the primary retrieval mechanism.
//
// Flow:
//
//	Query + Intent
//	    │
//	    ▼
//	KnowledgePlanner (generates requirements from intent)
//	    │
//	    ▼
//	SourceDiscovery (maps requirements to providers)
//	    │
//	    ▼
//	KnowledgeRuntime (Load → Pipeline → Link → Reduce)
//	    │
//	    ▼
//	WorkingGraph
//	    │
//	    ▼
//	Compiler (Prompt / Markdown / JSON / XML / ToolSchema)
//	    │
//	    ▼
//	CompiledContext
package retriever
