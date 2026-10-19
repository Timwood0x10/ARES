package sdk

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestClassifyMCPToolCapabilities pins the contract that makes MCP tools visible to the
// planner's capability routing: prose in, planner vocabulary out, and NO guess when the prose
// says nothing recognisable.
func TestClassifyMCPToolCapabilities(t *testing.T) {
	// A weather server describes itself in terms of HTTP; that is the capability it needs.
	assert.Equal(t, []string{"HTTPRequest"},
		classifyMCPToolCapabilities("weather.get", "Fetch current weather for a city over HTTP"))

	// Two categories at once, sorted so restarts do not shuffle the prompt.
	assert.Equal(t, []string{"CodeExecution", "WebSearch"},
		classifyMCPToolCapabilities("code.search", "Search source and run a query"))

	// Nothing recognisable: no caps at all. A wrong cap routes work to a tool that cannot do
	// it; a missing cap only leaves the tool unranked, which is the old behaviour.
	assert.Empty(t, classifyMCPToolCapabilities("arena.echo", "Echoes its input back"))
}

// TestMCPToolAdapter_CapabilitiesComeFromTheDescription keeps the two ends connected: the
// adapter must report what the classifier produced, not nil.
func TestMCPToolAdapter_CapabilitiesComeFromTheDescription(t *testing.T) {
	adapter := mcpToolAdapter{
		name: "weather.get",
		desc: "Fetch current weather for a city over HTTP",
		caps: classifyMCPToolCapabilities("weather.get", "Fetch current weather for a city over HTTP"),
	}
	assert.Equal(t, []string{"HTTPRequest"}, adapter.Capabilities())
}
