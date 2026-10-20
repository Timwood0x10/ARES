package ares_bootstrap

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/discovery"
	ares_mcp "github.com/Timwood0x10/ares/internal/runtime/protocol/mcp"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// newJoinTestManager builds a manager configured with ONE stdio server whose
// config name ("search") deliberately differs from everything discovery will call
// it ("grep-mcp", the normalized endpoint).
func newJoinTestManager(t *testing.T, name, command, url string) *ares_mcp.MCPManager {
	t.Helper()
	sc := ares_mcp.MCPServerConfig{Name: name, Enabled: true}
	switch {
	case command != "":
		sc.Transport = ares_mcp.TransportConfig{Type: "stdio", Stdio: &ares_mcp.StdioConfig{Command: command}}
	case url != "":
		sc.Transport = ares_mcp.TransportConfig{Type: "sse", SSE: &ares_mcp.SSEConfig{URL: url}}
	}
	mgr, err := ares_mcp.NewMCPManager(&ares_mcp.MCPManagerConfig{Servers: []ares_mcp.MCPServerConfig{sc}}, core.NewRegistry())
	require.NoError(t, err)
	return mgr
}

// discovered builds the service identity discovery produces for an endpoint:
// ID/Name are both the canonical endpoint key (see mergeRecords).
func discovered(endpoint string) *discovery.DiscoveredService {
	key := discovery.ServiceKey(endpoint)
	return &discovery.DiscoveredService{Identity: discovery.ServiceIdentity{ID: key, Name: key, Endpoint: endpoint}}
}

// TestMCPHealthJoin_AppliesVerdictForAutoDiscoveredStdioServer is the regression
// for the namespace break S-mcp-S4 was "fixed" with: discovery identifies a
// service by its ENDPOINT (here "grep-mcp"), the operator names the config entry
// whatever they like ("search"), and nothing mapped between the two — so the one
// signal that can see a half-dead server was dropped for every automatically
// discovered server.
//
// The assertion goes through the public status API on purpose: it is what an
// operator (and the model's tool list) actually reads.
func TestMCPHealthJoin_AppliesVerdictForAutoDiscoveredStdioServer(t *testing.T) {
	mgr := newJoinTestManager(t, "search", "/usr/local/bin/grep-mcp", "")
	join := &mcpHealthJoin{mgr: mgr, warned: make(map[string]bool)}

	join.handle(discovery.Event{
		Type:    discovery.EventHealthChanged,
		Service: discovered("/usr/local/bin/grep-mcp"),
		Message: "probe timed out",
	})

	statuses := mgr.ListServers()
	require.Len(t, statuses, 1)
	assert.Equal(t, "search", statuses[0].Name)
	assert.True(t, strings.Contains(statuses[0].HealthNote, "probe timed out"),
		"the health verdict must reach the managed server the discovered endpoint belongs to; got %q", statuses[0].HealthNote)

	recovered := discovered("/usr/local/bin/grep-mcp")
	recovered.Healthy = true // the verdict rides the service, not the event
	join.handle(discovery.Event{Type: discovery.EventHealthChanged, Service: recovered})
	assert.Empty(t, mgr.ListServers()[0].HealthNote, "a recovered server must clear the note")
}

// TestMCPHealthJoin_AppliesVerdictForAutoDiscoveredSSEServer pins the same join
// for the URL namespace (a remote server whose config name is unrelated to its
// URL).
func TestMCPHealthJoin_AppliesVerdictForAutoDiscoveredSSEServer(t *testing.T) {
	mgr := newJoinTestManager(t, "web-search", "", "https://search.example/mcp")
	join := &mcpHealthJoin{mgr: mgr, warned: make(map[string]bool)}

	join.handle(discovery.Event{
		Type:    discovery.EventHealthChanged,
		Service: discovered("https://search.example/mcp"),
		Message: "probe timed out",
	})

	assert.Contains(t, mgr.ListServers()[0].HealthNote, "probe timed out")
}

// TestMCPHealthJoin_KeepsDirectNameMatch: an operator who named the config entry
// after the discovered service keeps working — the direct name match is a
// supported join, not an accident of the old implementation.
func TestMCPHealthJoin_KeepsDirectNameMatch(t *testing.T) {
	mgr := newJoinTestManager(t, "grep-mcp", "/opt/bin/grep-mcp", "")
	join := &mcpHealthJoin{mgr: mgr, warned: make(map[string]bool)}

	name, ok := join.resolve(&discovery.DiscoveredService{
		Identity: discovery.ServiceIdentity{ID: "grep-mcp", Name: "grep-mcp"},
	})
	require.True(t, ok)
	assert.Equal(t, "grep-mcp", name)
}

// TestMCPHealthJoin_UnmatchedServerIsReportedAndNotApplied is the (c) half: a
// discovered MCP server this manager does not run must be reported — once, not
// per cycle — and must never be applied to some other server.
func TestMCPHealthJoin_UnmatchedServerIsReportedAndNotApplied(t *testing.T) {
	mgr := newJoinTestManager(t, "search", "/usr/local/bin/grep-mcp", "")
	join := &mcpHealthJoin{mgr: mgr, warned: make(map[string]bool)}
	svc := discovered("/usr/local/bin/other-mcp")

	name, ok := join.resolve(svc)
	assert.False(t, ok, "an unmanaged service must not resolve to a managed server")
	assert.Empty(t, name)

	join.handle(discovery.Event{Type: discovery.EventHealthChanged, Service: svc, Message: "probe timed out"})
	join.handle(discovery.Event{Type: discovery.EventHealthChanged, Service: svc, Message: "probe timed out"})

	assert.Equal(t, 1, len(join.warned), "an unmatched service is reported exactly once")
	assert.Empty(t, mgr.ListServers()[0].HealthNote,
		"and its verdict must not be applied to an unrelated managed server")
}

// TestMCPHealthJoin_StopsWarningPastTheBound keeps the reporting bounded: a
// deployment that discovers more MCP servers than this manager runs must not turn
// the log into a sink, and the suppression itself is announced once.
func TestMCPHealthJoin_StopsWarningPastTheBound(t *testing.T) {
	mgr := newJoinTestManager(t, "search", "/usr/local/bin/grep-mcp", "")
	join := &mcpHealthJoin{mgr: mgr, warned: make(map[string]bool)}

	for i := 0; i < maxUnmatchedHealthWarnings+10; i++ {
		join.handle(discovery.Event{
			Type:    discovery.EventHealthChanged,
			Service: discovered("/tmp/unmanaged-" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + "-mcp"),
		})
	}
	assert.LessOrEqual(t, len(join.warned), maxUnmatchedHealthWarnings)
	assert.True(t, join.suppressed, "hitting the bound must be recorded, not silent")
}
