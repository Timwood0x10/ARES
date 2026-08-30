package envcap

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/knowledge/skills"
	"github.com/Timwood0x10/ares/internal/tools/discovery"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// staticTool is a minimal core.Tool for tests.
type staticTool struct {
	name string
	desc string
}

func (t *staticTool) Name() string        { return t.name }
func (t *staticTool) Description() string { return t.desc }
func (t *staticTool) Category() core.ToolCategory {
	return core.CategoryCore
}
func (t *staticTool) Capabilities() []core.Capability { return nil }
func (t *staticTool) Parameters() *core.ParameterSchema {
	return &core.ParameterSchema{Type: "object"}
}
func (t *staticTool) Execute(context.Context, map[string]interface{}) (core.Result, error) {
	return core.NewResult(true, nil), nil
}

// fakeToolLister returns a fixed tool snapshot.
type fakeToolLister struct {
	tools []core.Tool
}

func (f *fakeToolLister) Tools(context.Context) ([]core.Tool, error) {
	return f.tools, nil
}

// fakeCommandDiscoverer probes the allowlist with a fake exec that makes both
// commands "installed" and returns a stable help line.
func fakeCommandDiscoverer() *discovery.Discoverer {
	fakeExec := func(_ context.Context, name string, args []string) ([]byte, error) {
		return []byte("usage: " + name + " — a native tool\n"), nil
	}
	fakeLookup := func(name string) (string, error) {
		return "/usr/bin/" + name, nil
	}
	return discovery.NewDiscoverer(
		[]string{"git", "jq"},
		discovery.WithExec(fakeExec),
		discovery.WithLookup(fakeLookup),
	)
}

func TestSearcher_SearchAcrossSources(t *testing.T) {
	lister := &fakeToolLister{tools: []core.Tool{
		&staticTool{name: "web_search", desc: "Search the web"},
		&staticTool{name: "calculator", desc: "Do math"},
	}}
	reg := skills.NewRegistry()
	require.NoError(t, reg.Register(skills.Skill{Name: "shell", Description: "Run shell commands"}))
	require.NoError(t, reg.Register(skills.Skill{Name: "git-writer", Description: "Write git commits"}))
	cmds := fakeCommandDiscoverer()

	s := NewSearcher(lister, reg, cmds)
	results, err := s.Search(context.Background(), "git", 0)
	require.NoError(t, err)
	require.NotEmpty(t, results, "search 'git' should hit the git skill and git command")

	// Rank order: tool < skill < command.
	require.Len(t, results, 2)
	assert.Equal(t, KindSkill, results[0].Kind)
	assert.Equal(t, "git-writer", results[0].Name)
	assert.Equal(t, KindCommand, results[1].Kind)
	assert.Equal(t, "git", results[1].Name)
}

func TestSearcher_SearchToolNameMatch(t *testing.T) {
	lister := &fakeToolLister{tools: []core.Tool{
		&staticTool{name: "web_search", desc: "Search the web"},
		&staticTool{name: "calculator", desc: "Do math"},
	}}
	s := NewSearcher(lister, nil, nil)
	results, err := s.Search(context.Background(), "web", 0)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, KindTool, results[0].Kind)
	assert.Equal(t, "web_search", results[0].Name)
}

func TestSearcher_SearchDescriptionMatch(t *testing.T) {
	reg := skills.NewRegistry()
	require.NoError(t, reg.Register(skills.Skill{Name: "code-review", Description: "Review Go code carefully"}))
	s := NewSearcher(nil, reg, nil)
	results, err := s.Search(context.Background(), "carefully", 0)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, KindSkill, results[0].Kind)
	assert.Equal(t, "code-review", results[0].Name)
}

func TestSearcher_SearchLimitAndEmpty(t *testing.T) {
	lister := &fakeToolLister{tools: []core.Tool{
		&staticTool{name: "a_tool", desc: "first"},
		&staticTool{name: "b_tool", desc: "second"},
	}}
	s := NewSearcher(lister, nil, nil)

	limited, err := s.Search(context.Background(), "tool", 1)
	require.NoError(t, err)
	require.Len(t, limited, 1)

	empty, err := s.Search(context.Background(), "zzz-no-match", 0)
	require.NoError(t, err)
	assert.Empty(t, empty)

	blank, err := s.Search(context.Background(), "  ", 0)
	require.NoError(t, err)
	assert.Nil(t, blank)
}

func TestSearcher_CaseInsensitive(t *testing.T) {
	reg := skills.NewRegistry()
	require.NoError(t, reg.Register(skills.Skill{Name: "ShellMaster", Description: "Advanced shell"}))
	s := NewSearcher(nil, reg, nil)
	results, err := s.Search(context.Background(), "shellmaster", 0)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "ShellMaster", results[0].Name)
}
