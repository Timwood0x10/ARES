package toolsource

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	core "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

func TestAllSelector_ReturnsAllSorted(t *testing.T) {
	tools := []core.Tool{
		&testTool{name: "zebra"},
		&testTool{name: "apple"},
		&testTool{name: "mango"},
	}
	sel := AllSelector{}

	out, err := sel.Select(context.Background(), "anything", tools)
	require.NoError(t, err)
	require.Len(t, out, 3)
	assert.Equal(t, "apple", out[0].Name())
	assert.Equal(t, "mango", out[1].Name())
	assert.Equal(t, "zebra", out[2].Name(), "must be sorted by Name for determinism")
}

func TestAllSelector_DoesNotMutateInput(t *testing.T) {
	tools := []core.Tool{&testTool{name: "b"}, &testTool{name: "a"}}
	sel := AllSelector{}
	_, err := sel.Select(context.Background(), "", tools)
	require.NoError(t, err)
	// Original slice order must be unchanged.
	assert.Equal(t, "b", tools[0].Name())
	assert.Equal(t, "a", tools[1].Name())
}

func TestAllSelector_Empty(t *testing.T) {
	sel := AllSelector{}
	out, err := sel.Select(context.Background(), "", nil)
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestTagSelector_MatchAndFallback(t *testing.T) {
	mathTool := &testTool{name: "calc", tags: map[string]string{"domain": "math"}}
	netTool := &testTool{name: "http", tags: map[string]string{"domain": "network"}}
	tools := []core.Tool{mathTool, netTool}
	sel := TagSelector{}

	cases := []struct {
		name      string
		input     string
		wantNames []string
	}{
		{
			name:      "keyword_matches_tag_value",
			input:     "do some math",
			wantNames: []string{"calc"},
		},
		{
			name:      "network_keyword_matches",
			input:     "network request",
			wantNames: []string{"http"},
		},
		{
			name:      "no_keywords_returns_all",
			input:     "",
			wantNames: []string{"calc", "http"},
		},
		{
			name:      "keywords_but_no_match_falls_back_to_all",
			input:     "cook dinner",
			wantNames: []string{"calc", "http"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := sel.Select(context.Background(), tc.input, tools)
			require.NoError(t, err)
			names := make([]string, 0, len(out))
			for _, tl := range out {
				names = append(names, tl.Name())
			}
			assert.ElementsMatch(t, tc.wantNames, names)
		})
	}
}

func TestTagSelector_SkipsNonTaggableTools(t *testing.T) {
	// plainTool does NOT implement core.TaggableTool.
	plain := &plainTool{name: "plain"}
	tagged := &testTool{name: "tagged", tags: map[string]string{"domain": "math"}}
	tools := []core.Tool{plain, tagged}
	sel := TagSelector{}

	out, err := sel.Select(context.Background(), "math", tools)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "tagged", out[0].Name(), "non-taggable tool must be excluded on match")
}

func TestTagSelector_EmptyAvailable(t *testing.T) {
	sel := TagSelector{}
	out, err := sel.Select(context.Background(), "math", nil)
	require.NoError(t, err)
	assert.Empty(t, out)
}

// plainTool implements core.Tool but NOT core.TaggableTool, for testing the
// non-taggable code path.
type plainTool struct {
	name string
}

func (p *plainTool) Name() string                    { return p.name }
func (p *plainTool) Description() string             { return "plain " + p.name }
func (p *plainTool) Category() core.ToolCategory     { return core.CategoryCore }
func (p *plainTool) Capabilities() []core.Capability { return nil }
func (p *plainTool) Execute(context.Context, map[string]interface{}) (core.Result, error) {
	return core.NewResult(true, "ok"), nil
}
func (p *plainTool) Parameters() *core.ParameterSchema { return nil }

func TestExtractKeywords(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"only_stopwords", "the a an do please", nil},
		{"single_char_tokens_dropped", "a b c", nil},
		{"mixed", "Please DO some Math", []string{"math"}},
		{"punctuation_split", "calc, sum! add?", []string{"calc", "sum", "add"}},
		{"duplicates_collapsed", "math math math", []string{"math"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractKeywords(tc.in)
			assert.Equal(t, tc.want, got)
		})
	}
}
