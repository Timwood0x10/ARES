package ares_mcp

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// TestServerHealth_StateTransitions pins the cell's contract, including the two
// edges that are easy to get wrong: re-marking an unavailable server must not
// restart its "since" clock (the stamp answers "how long has this been broken"),
// and a recovered server must really clear the reason — otherwise it stays marked
// forever, which is the same silent inconsistency pointing the other way.
func TestServerHealth_StateTransitions(t *testing.T) {
	cell := NewServerHealth()
	assert.True(t, cell.IsAvailable())
	assert.Empty(t, cell.UnavailableNote())

	cell.MarkUnavailable("transport closed: EOF")
	available, reason, since := cell.State()
	assert.False(t, available)
	assert.Equal(t, "transport closed: EOF", reason)
	require.False(t, since.IsZero())
	assert.Contains(t, cell.UnavailableNote(), "UNAVAILABLE")
	assert.Contains(t, cell.UnavailableNote(), "transport closed: EOF")

	firstSince := since
	cell.MarkUnavailable("probe timed out")
	_, reason, since = cell.State()
	assert.Equal(t, "probe timed out", reason, "the freshest reason wins")
	assert.Equal(t, firstSince, since, "re-marking must not restart the since clock")

	cell.MarkAvailable()
	assert.True(t, cell.IsAvailable())
	assert.Empty(t, cell.UnavailableNote(), "recovery must clear the note")

	// The since stamp is injectable, mirroring the fabric's WithClock discipline,
	// so a duration can be asserted instead of merely "something was recorded".
	clocked := NewServerHealth()
	fixed := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	clocked.now = func() time.Time { return fixed }
	clocked.MarkUnavailable("probe timed out")
	_, _, stamped := clocked.State()
	assert.Equal(t, fixed, stamped)

	// A tool built outside a manager has no cell; it must not look broken.
	var absent *ServerHealth
	assert.True(t, absent.IsAvailable())
	absent.MarkUnavailable("ignored")
	assert.True(t, absent.IsAvailable())
}

// TestToolReportsUnavailableServer is the S-mcp-S5 + S-mcp-S2 contract at the tool
// boundary: while the server is down the tool must (a) say so in its DESCRIPTION,
// which is what the model reads, and (b) refuse a call with a message naming the
// server and the reason — not the raw "connection closed" that reads like a blip,
// and not a "tool not found" that makes the model doubt its own memory.
func TestToolReportsUnavailableServer(t *testing.T) {
	def := buildSimpleToolDef("query", "Search the corpus")

	available := NewMCPClient(MCPClientConfig{ServerName: "search", Health: NewServerHealth()})
	healthy, err := NewMCPTool(available, &def)
	require.NoError(t, err)
	assert.Equal(t, "Search the corpus", healthy.Description(),
		"a usable server's description must stay exactly as the server declared it")

	cell := NewServerHealth()
	cell.MarkUnavailable("probe timed out")
	client := NewMCPClient(MCPClientConfig{ServerName: "search", Health: cell})
	tool, err := NewMCPTool(client, &def)
	require.NoError(t, err)

	assert.Contains(t, tool.Description(), "Search the corpus")
	assert.Contains(t, tool.Description(), "UNAVAILABLE")

	res, err := tool.Execute(context.Background(), nil)
	require.NoError(t, err, "an outage is a tool-level result, not a dispatcher error")
	assert.False(t, res.Success)
	assert.Contains(t, res.Error, "mcp.search.query", "the refusal names the tool")
	assert.Contains(t, res.Error, "probe timed out", "and the reason")
}

// TestTransportDeathMarksServerUnavailable is the S-mcp-S5 fix end to end: the
// subprocess's stream closes, and the server's tools must stop looking usable.
// Before this, the receive loop cleared a private flag and nobody else heard:
// the tools stayed in the registry and only failed when a model called one.
func TestTransportDeathMarksServerUnavailable(t *testing.T) {
	tr := newTestServer(lifecycleTestTools, nil)
	m := newTestManager(t, &MCPManagerConfig{}, core.NewRegistry())
	sc := &MCPServerConfig{Name: "mock", Enabled: true, Timeout: 2 * time.Second}
	require.NoError(t, m.connectWithTransport(context.Background(), "mock", sc, tr))

	cell := m.serverHealth("mock")
	require.True(t, cell.IsAvailable(), "a fresh connection is usable")

	changed := make(chan struct{}, 1)
	m.SetToolChangeHandler(func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})

	// The child's stdout closes: Receive fails permanently, exactly like a killed
	// subprocess (the receive loop's error path).
	close(tr.respCh)

	deadline := time.Now().Add(2 * time.Second)
	for cell.IsAvailable() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond) // poll, with a deadline (no sleep-for-sync)
	}
	assert.False(t, cell.IsAvailable(), "a dead transport must mark the server unavailable")
	_, reason, since := cell.State()
	assert.Contains(t, reason, "transport closed")
	assert.False(t, since.IsZero())

	select {
	case <-changed:
	case <-time.After(2 * time.Second):
		t.Fatal("consumers must be told to re-read the tool list when a server dies")
	}
}

// TestSetServerHealth_ConsumesExternalVerdict is the MANAGER half of S-mcp-S4:
// the manager must act on a health verdict from outside (discovery's
// MCPHealthChecker is the only probe that can see a half-dead server), and must
// reject verdicts about services it does not run rather than inventing state.
//
// It deliberately hands SetServerHealth the ALREADY-RESOLVED config name. The
// other half — joining discovery's endpoint-derived identity onto that config
// name — is a separate contract tested in ares_bootstrap
// (provide_mcp_health_join_test.go). Keeping them apart is the point: an earlier
// cut of this test hand-aligned the two names, which is exactly what hid the
// production break where no automatic-discovered server ever joined.
func TestSetServerHealth_ConsumesExternalVerdict(t *testing.T) {
	registry := core.NewRegistry()
	m := newTestManager(t, &MCPManagerConfig{Servers: []MCPServerConfig{{Name: "search", Enabled: true}}}, registry)

	var notified int
	m.SetToolChangeHandler(func() { notified++ })

	require.NoError(t, m.SetServerHealth("search", false, "probe timed out"))
	cell := m.serverHealth("search")
	assert.False(t, cell.IsAvailable())
	assert.Contains(t, cell.UnavailableNote(), "probe timed out")
	assert.Equal(t, 1, notified, "the published tool list must be refreshed on a health flip")

	require.NoError(t, m.SetServerHealth("search", true, ""))
	assert.True(t, cell.IsAvailable(), "a recovered server must be usable again")
	assert.Equal(t, 2, notified)

	err := m.SetServerHealth("not-ours", false, "some other service")
	assert.ErrorIs(t, err, ErrServerNotManaged,
		"discovery reports on every service; a foreign one is not a fault")
}
