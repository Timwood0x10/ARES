package ares_bootstrap

import (
	"github.com/Timwood0x10/ares/internal/llm"
	builtintools "github.com/Timwood0x10/ares/internal/tools/resources/builtin"
	builtin_knowledge "github.com/Timwood0x10/ares/internal/tools/resources/builtin/knowledge"
)

// ToolDepsFromComponents builds the GeneralToolsDeps for
// builtintools.RegisterGeneralTools from the wired bootstrap components, so
// the knowledge / memory / planning tools receive real backends instead of
// nil guards:
//
//   - MemoryMgr        ← comp.Memory (always wired)
//   - KnowledgeSearcher / KnowledgeService ← StoreAdapter over comp.KnowledgeStore
//     (the AKG store; nil when AKG is disabled)
//   - LLMClient        ← comp.LLM.Client, when it is a *llm.Client
//
// KnowledgeRepo and DistilledRepo remain nil here: they require a
// PostgreSQL connection (repositories.KnowledgeRepositoryInterface /
// DistilledMemoryRepositoryInterface), which bootstrap only creates when
// distillation/storage is configured. Tools backed by those fields
// (correct_knowledge, distilled_memory_search) keep their nil guards until
// such a repo is wired explicitly.
func ToolDepsFromComponents(comp *Components) builtintools.GeneralToolsDeps {
	deps := builtintools.GeneralToolsDeps{
		MemoryMgr: comp.Memory,
	}
	if comp.KnowledgeStore != nil {
		adapter := builtin_knowledge.NewStoreAdapter(comp.KnowledgeStore)
		deps.KnowledgeSearcher = adapter
		deps.KnowledgeService = adapter
	}
	if comp.LLM != nil {
		if c, ok := comp.LLM.Client.(*llm.Client); ok {
			deps.LLMClient = c
		}
	}
	return deps
}
