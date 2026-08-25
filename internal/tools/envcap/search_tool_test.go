package envcap

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge/skills"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRegistry is a minimal toolRegistry: a name→tool map exposing List + Get.
type fakeRegistry struct {
	tools map[string]core.Tool
}

func (r *fakeRegistry) List() []string {
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	return names
}

func (r *fakeRegistry) Get(name string) (core.Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

func TestRegistryLister_ResolvesNamesToTools(t *testing.T) {
	reg := &fakeRegistry{tools: map[string]core.Tool{
		"web_search": &staticTool{name: "web_search", desc: "Search the web"},
		"calculator": &staticTool{name: "calculator", desc: "Do math"},
	}}
	lister := NewRegistryLister(reg)

	tools, err := lister.Tools(context.Background())
	require.NoError(t, err)
	require.Len(t, tools, 2)

	// The lister feeds the searcher, so a query over it must match by name.
	s := NewSearcher(lister, nil, nil)
	results, err := s.Search(context.Background(), "web", 0)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "web_search", results[0].Name)
}

func TestSearchTool_ExecuteReturnsRankedResults(t *testing.T) {
	reg := skills.NewRegistry()
	require.NoError(t, reg.Register(skills.Skill{Name: "git-writer", Description: "Write git commits"}))
	lister := &fakeToolLister{tools: []core.Tool{
		&staticTool{name: "git_status", desc: "Show git status"},
	}}
	tool := NewSearchTool(NewSearcher(lister, reg, nil))

	assert.Equal(t, SearchToolName, tool.Name())

	res, err := tool.Execute(context.Background(), map[string]interface{}{"query": "git"})
	require.NoError(t, err)
	require.True(t, res.Success)

	data, ok := res.Data.(map[string]interface{})
	require.True(t, ok, "result data must be a map")
	assert.Equal(t, "git", data["query"])
	assert.Equal(t, 2, data["count"])

	items, ok := data["results"].([]map[string]interface{})
	require.True(t, ok)
	require.Len(t, items, 2)
	// Rank order: tool before skill.
	assert.Equal(t, string(KindTool), items[0]["kind"])
	assert.Equal(t, "git_status", items[0]["name"])
	assert.Equal(t, string(KindSkill), items[1]["kind"])
	assert.Equal(t, "git-writer", items[1]["name"])
}

func TestSearchTool_ExecuteRespectsLimit(t *testing.T) {
	lister := &fakeToolLister{tools: []core.Tool{
		&staticTool{name: "a_tool", desc: "match"},
		&staticTool{name: "b_tool", desc: "match"},
		&staticTool{name: "c_tool", desc: "match"},
	}}
	tool := NewSearchTool(NewSearcher(lister, nil, nil))

	// limit arrives as float64 from JSON decoding.
	res, err := tool.Execute(context.Background(), map[string]interface{}{
		"query": "tool",
		"limit": float64(2),
	})
	require.NoError(t, err)
	data := res.Data.(map[string]interface{})
	assert.Equal(t, 2, data["count"])
}

func TestSearchTool_ExecuteRequiresQuery(t *testing.T) {
	tool := NewSearchTool(NewSearcher(&fakeToolLister{}, nil, nil))

	res, err := tool.Execute(context.Background(), map[string]interface{}{})
	require.NoError(t, err)
	assert.False(t, res.Success, "missing query should yield an error result, not a transport error")
}

func TestSearchTool_NilSkillRegistrySkipped(t *testing.T) {
	// Skills disabled (nil registry) must not panic; the tool source still works.
	lister := &fakeToolLister{tools: []core.Tool{
		&staticTool{name: "only_tool", desc: "the sole capability"},
	}}
	tool := NewSearchTool(NewSearcher(lister, nil, nil))

	res, err := tool.Execute(context.Background(), map[string]interface{}{"query": "sole"})
	require.NoError(t, err)
	require.True(t, res.Success)
	data := res.Data.(map[string]interface{})
	assert.Equal(t, 1, data["count"])
}
