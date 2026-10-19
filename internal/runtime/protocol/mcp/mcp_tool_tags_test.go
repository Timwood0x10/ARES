package ares_mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCapabilityTagsFor is the bridge from an MCP description to the kernel's routing
// vocabulary: without it an MCP tool declares no capability, so a task-tagged quantum can never
// select it and (before the untagged-tool fix) could drop it while keeping the builtins that
// happened to match.
func TestCapabilityTagsFor(t *testing.T) {
	// A weather server describes itself in HTTP terms — "weather" alone is not a tool domain.
	tags := capabilityTagsFor("weather.get", "Fetch current weather for a city over HTTP")
	assert.Equal(t, map[string]string{"cap_network": "true"}, tags)

	// Code search maps onto the file domain, the same one builtin grep declares. It ALSO picks
	// up cap_network because the shared table lists "search" under network (a task saying "搜索"
	// usually means the web). For a tool description that term is imprecise, and the direction of
	// the error is the safe one: the tool is advertised for more task classes, never hidden from
	// one it could serve.
	tags = capabilityTagsFor("code.search", "Search source code and return matching lines")
	assert.Equal(t, "true", tags["cap_file"])

	// Nothing recognisable: no tags at all, which means "not participating", not "matches none".
	assert.Nil(t, capabilityTagsFor("arena.echo", "Echoes its input back"))
}
