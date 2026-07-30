package toolsource

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	core "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// decodeDiscoverEntries unmarshals the discover_tools result Data (a JSON
// string of {name,description} objects) back into a slice for assertions.
func decodeDiscoverEntries(t *testing.T, res core.Result) []discoverToolEntry {
	t.Helper()
	raw, ok := res.Data.(string)
	require.True(t, ok, "Data must be a JSON string, got %T", res.Data)
	var entries []discoverToolEntry
	require.NoError(t, json.Unmarshal([]byte(raw), &entries))
	return entries
}

func TestDiscoverTools_Constants(t *testing.T) {
	assert.Equal(t, "discover_tools", DiscoverToolsName)
}

func TestDiscoverTools_InterfaceCompliance(t *testing.T) {
	// Assert the concrete type satisfies core.Tool. NewDiscoverToolsTool
	// already returns core.Tool by signature, so this guards the implementor
	// type directly (idiomatic compile-time check; avoids QF1011 on the
	// return-value form).
	var _ core.Tool = (*discoverToolsTool)(nil)
}

func TestDiscoverTools_MetaData(t *testing.T) {
	tool := NewDiscoverToolsTool(NewStaticSource(nil))
	assert.Equal(t, DiscoverToolsName, tool.Name())
	assert.Equal(t, core.CategorySystem, tool.Category())
	assert.Empty(t, tool.Capabilities())
	require.NotNil(t, tool.Parameters())
	assert.Contains(t, tool.Parameters().Required, "query")
}

func TestDiscoverTools_ExecuteMatching(t *testing.T) {
	src := NewStaticSource([]core.Tool{
		&testTool{name: "calc", description: "compute numbers", tags: map[string]string{"domain": "arithmetic"}},
		&testTool{name: "str", description: "string operations", tags: map[string]string{"domain": "text"}},
		&testTool{name: "http", description: "network calls", tags: map[string]string{"domain": "network"}},
	})
	tool := NewDiscoverToolsTool(src)

	cases := []struct {
		name      string
		query     string
		wantNames []string
		wantCount int
	}{
		{
			name:      "name_substring_match",
			query:     "calc",
			wantNames: []string{"calc"},
			wantCount: 1,
		},
		{
			name:      "description_substring_match",
			query:     "network",
			wantNames: []string{"http"},
			wantCount: 1,
		},
		{
			name:      "tag_value_match_only",
			query:     "arithmetic",
			wantNames: []string{"calc"},
			wantCount: 1,
		},
		{
			name:      "broad_match_multiple",
			query:     "e",
			wantNames: []string{"calc", "str", "http"},
			wantCount: 3,
		},
		{
			name:      "no_match_returns_empty",
			query:     "zzz",
			wantNames: []string{},
			wantCount: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := tool.Execute(context.Background(), map[string]interface{}{"query": tc.query})
			require.NoError(t, err)
			require.True(t, res.Success, "result must be success; got error %q", res.Error)

			entries := decodeDiscoverEntries(t, res)
			assert.Len(t, entries, tc.wantCount)

			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name)
				assert.NotEmpty(t, e.Description, "each entry must carry a description")
			}
			assert.ElementsMatch(t, tc.wantNames, names)
		})
	}
}

func TestDiscoverTools_ExecuteMissingQuery(t *testing.T) {
	tool := NewDiscoverToolsTool(NewStaticSource(nil))
	res, err := tool.Execute(context.Background(), map[string]interface{}{})
	require.NoError(t, err)
	assert.False(t, res.Success)
	assert.Contains(t, res.Error, "query")
}

func TestDiscoverTools_ExecuteNilParams(t *testing.T) {
	tool := NewDiscoverToolsTool(NewStaticSource(nil))
	// Reading from a nil map is safe in Go; query is "" → error result.
	res, err := tool.Execute(context.Background(), nil)
	require.NoError(t, err)
	assert.False(t, res.Success)
}

func TestDiscoverTools_ExecuteNilSource(t *testing.T) {
	tool := NewDiscoverToolsTool(nil)
	_, err := tool.Execute(context.Background(), map[string]interface{}{"query": "x"})
	assert.ErrorIs(t, err, ErrNilToolSource)
}

func TestDiscoverTools_ResultIsCompact(t *testing.T) {
	src := NewStaticSource([]core.Tool{
		&testTool{name: "calc", description: "compute", tags: map[string]string{"domain": "math"}},
	})
	tool := NewDiscoverToolsTool(src)
	res, err := tool.Execute(context.Background(), map[string]interface{}{"query": "calc"})
	require.NoError(t, err)

	entries := decodeDiscoverEntries(t, res)
	require.Len(t, entries, 1)
	// Only name + description fields exist on the entry (no full schema).
	assert.Equal(t, "calc", entries[0].Name)
	assert.Equal(t, "compute", entries[0].Description)
}

func TestDiscoverTools_ResultJSONShape(t *testing.T) {
	src := NewStaticSource([]core.Tool{
		&testTool{name: "calc", description: "compute"},
	})
	tool := NewDiscoverToolsTool(src)
	res, err := tool.Execute(context.Background(), map[string]interface{}{"query": "calc"})
	require.NoError(t, err)

	// Data is a JSON string of {name,description} objects (the wire format the
	// agentloop engine parses). Verify both the raw string and decoded form.
	raw, ok := res.Data.(string)
	require.True(t, ok)
	assert.Contains(t, raw, `"name":"calc"`)
	assert.Contains(t, raw, `"description":"compute"`)

	// The whole Result must also round-trip via ToJSON without losing the
	// nested JSON (data serializes as an escaped string).
	wire, mErr := res.ToJSON()
	require.NoError(t, mErr)
	assert.Contains(t, wire, `"data":"[{`)
	assert.Contains(t, wire, `\"name\":\"calc\"`)
}

func TestDiscoverTools_ResultCapAt20(t *testing.T) {
	var many []core.Tool
	for i := 0; i < 30; i++ {
		many = append(many, &testTool{
			name:        fmt.Sprintf("tool_%02d", i),
			description: "common match",
		})
	}
	src := NewStaticSource(many)
	tool := NewDiscoverToolsTool(src)

	res, err := tool.Execute(context.Background(), map[string]interface{}{"query": "match"})
	require.NoError(t, err)
	entries := decodeDiscoverEntries(t, res)
	assert.Len(t, entries, maxDiscoverResults, "must cap results at %d", maxDiscoverResults)
}

func TestDiscoverTools_NonTaggableToolMatchesByNameOrDescription(t *testing.T) {
	// plainTool (defined in selector_test.go) does NOT implement TaggableTool.
	src := NewStaticSource([]core.Tool{
		&plainTool{name: "alpha"},
	})
	tool := NewDiscoverToolsTool(src)

	res, err := tool.Execute(context.Background(), map[string]interface{}{"query": "alpha"})
	require.NoError(t, err)
	entries := decodeDiscoverEntries(t, res)
	require.Len(t, entries, 1, "non-taggable tool must still match by name")
	assert.Equal(t, "alpha", entries[0].Name)
}
