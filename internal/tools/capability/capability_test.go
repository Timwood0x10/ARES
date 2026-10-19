package capability

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/tools/resources/builtin"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// TestKeywordsCoverEveryDeclaredCapability is the drift guard between the routing table and the
// tools: if a tool declares `cap_foo` and no keyword maps to "foo", no task can ever select it by
// description — the tool is advertised always or narrowed away, never routed.
//
// The two lists live in different packages and are edited by different people, which is exactly
// why this has to fail loudly instead of being reviewed by eye.
// Scope note: this scans the DEFAULT registration (RegisterGeneralTools). "knowledge" is a
// reserved domain — no tool registered here declares cap_knowledge (knowledge tools need
// injected deps), so the guard cannot see it. It stays in the table deliberately, as the name a
// knowledge tool would use; it routes nothing today.
func TestKeywordsCoverEveryDeclaredCapability(t *testing.T) {
	reg := core.NewRegistry()
	require.NoError(t, builtin.RegisterGeneralTools(reg))

	declared := make(map[string]bool)
	for _, schema := range reg.GetSchemas() {
		for key := range schema.Tags {
			if strings.HasPrefix(key, "cap_") {
				declared[strings.TrimPrefix(key, "cap_")] = true
			}
		}
	}
	require.NotEmpty(t, declared,
		"the builtin registry must actually declare capabilities, otherwise this guard proves nothing")

	known := make(map[string]bool, len(KnownDomains()))
	for _, domain := range KnownDomains() {
		known[domain] = true
	}
	for domain := range declared {
		assert.True(t, known[domain],
			"tool capability %q has no keywords in the routing table: tasks mentioning it can never reach those tools", domain)
	}
}
