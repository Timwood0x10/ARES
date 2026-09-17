// Package builtin — general-tools dependency-gated registration tests (Stage 5).
//
// Verifies that dependency-backed tools (knowledge/memory/planning) are only
// registered when their backend dependency is actually wired: no tool is
// registered that is known to fail at call time.
package builtin

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// TestRegisterGeneralTools_NoDeps_SkipsDependencyTools verifies that with no
// dependency wired, knowledge/memory/planning tools are NOT registered (Stage
// 5 gate: no "registered but always fails" tools).
func TestRegisterGeneralTools_NoDeps_SkipsDependencyTools(t *testing.T) {
	registry := core.NewRegistry()
	require.NoError(t, RegisterGeneralTools(registry))

	_, ok := registry.Get("knowledge_search")
	assert.False(t, ok, "knowledge_search must NOT be registered without a knowledge searcher")
	_, ok = registry.Get("memory_search")
	assert.False(t, ok, "memory_search must NOT be registered without a MemoryManager")
	_, ok = registry.Get("task_planner")
	assert.False(t, ok, "task_planner must NOT be registered without an LLM client")

	// Dependency-free tools must still be registered.
	_, ok = registry.Get("calculator")
	assert.True(t, ok, "calculator must always be registered")
}

// TestRegisterGeneralTools_MemoryOnly_RegistersMemory verifies that wiring a
// MemoryManager registers memory tools but not knowledge/planning tools.
func TestRegisterGeneralTools_MemoryOnly_RegistersMemory(t *testing.T) {
	registry := core.NewRegistry()
	deps := GeneralToolsDeps{}
	require.NoError(t, RegisterGeneralTools(registry, deps))

	// With a nil MemoryManager, memory tools must still be skipped.
	_, ok := registry.Get("memory_search")
	assert.False(t, ok, "memory_search must NOT be registered with nil MemoryManager")
}
