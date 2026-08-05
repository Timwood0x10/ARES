package toolsource

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	core "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// testTool is a minimal core.Tool implementation for tests. It also satisfies
// core.TaggableTool when tags is non-nil (Tags returns nil otherwise, but the
// method always exists so the type assertion succeeds).
type testTool struct {
	name         string
	description  string
	category     core.ToolCategory
	capabilities []core.Capability
	tags         map[string]string
}

func (t *testTool) Name() string                    { return t.name }
func (t *testTool) Description() string             { return t.description }
func (t *testTool) Category() core.ToolCategory     { return t.category }
func (t *testTool) Capabilities() []core.Capability { return t.capabilities }
func (t *testTool) Execute(context.Context, map[string]interface{}) (core.Result, error) {
	return core.NewResult(true, "ok"), nil
}
func (t *testTool) Parameters() *core.ParameterSchema { return nil }

// Tags satisfies core.TaggableTool; returns nil when no tags are set.
func (t *testTool) Tags() map[string]string {
	if t.tags == nil {
		return nil
	}
	cp := make(map[string]string, len(t.tags))
	for k, v := range t.tags {
		cp[k] = v
	}
	return cp
}

func TestRegistrySource_Tools(t *testing.T) {
	reg := core.NewRegistry()
	require.NoError(t, reg.Register(&testTool{name: "a"}))
	require.NoError(t, reg.Register(&testTool{name: "b"}))

	src := NewRegistrySource(reg)
	tools, err := src.Tools(context.Background())
	require.NoError(t, err)
	assert.Len(t, tools, 2)
	assert.Equal(t, "registry", src.Source())
}

func TestRegistrySource_NilRegistry(t *testing.T) {
	src := NewRegistrySource(nil)
	_, err := src.Tools(context.Background())
	assert.ErrorIs(t, err, ErrNilToolSource)
}

func TestRegistrySource_OnChangeForwardsToRegistry(t *testing.T) {
	reg := core.NewRegistry()
	src := NewRegistrySource(reg)
	var called bool
	src.OnChange(func() { called = true })

	require.NoError(t, reg.Register(&testTool{name: "x"}))
	assert.True(t, called, "OnChange callback should fire on Register")
}

func TestStaticSource_ToolsReturnsCopy(t *testing.T) {
	orig := []core.Tool{&testTool{name: "a"}}
	src := NewStaticSource(orig)

	out, err := src.Tools(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "static", src.Source())

	// Mutating the returned slice must not affect the source.
	out[0] = nil
	out2, _ := src.Tools(context.Background())
	require.Len(t, out2, 1)
	assert.Equal(t, "a", out2[0].Name())

	// Mutating the input slice after construction must not affect the source.
	orig[0] = nil
	out3, _ := src.Tools(context.Background())
	require.Len(t, out3, 1)
	assert.Equal(t, "a", out3[0].Name())
}

func TestMultiSource_DedupFirstWins(t *testing.T) {
	cases := []struct {
		name    string
		sources []ToolSource
		want    []string
		wantErr bool
	}{
		{
			name:    "no_collision_merges_all",
			sources: []ToolSource{NewStaticSource([]core.Tool{&testTool{name: "a"}}), NewStaticSource([]core.Tool{&testTool{name: "b"}})},
			want:    []string{"a", "b"},
		},
		{
			name:    "collision_first_source_wins",
			sources: []ToolSource{NewStaticSource([]core.Tool{&testTool{name: "dup", description: "from-static"}}), NewStaticSource([]core.Tool{&testTool{name: "dup", description: "from-registry"}})},
			want:    []string{"dup"},
		},
		{
			name:    "skips_nil_sources",
			sources: []ToolSource{nil, NewStaticSource([]core.Tool{&testTool{name: "a"}}), nil},
			want:    []string{"a"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ms := NewMultiSource(tc.sources...)
			tools, err := ms.Tools(context.Background())
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			names := make([]string, 0, len(tools))
			for _, tl := range tools {
				names = append(names, tl.Name())
			}
			assert.ElementsMatch(t, tc.want, names)
		})
	}
}

func TestMultiSource_CollisionKeepsFirstDescription(t *testing.T) {
	first := NewStaticSource([]core.Tool{&testTool{name: "dup", description: "from-static"}})
	second := NewStaticSource([]core.Tool{&testTool{name: "dup", description: "from-registry"}})
	ms := NewMultiSource(first, second)

	tools, err := ms.Tools(context.Background())
	require.NoError(t, err)
	require.Len(t, tools, 1)
	assert.Equal(t, "from-static", tools[0].Description(), "first source must win on name collision")
	assert.Equal(t, "multi", ms.Source())
}

func TestMultiSource_OnChangeFansOutToAllSources(t *testing.T) {
	reg := core.NewRegistry()
	regSrc := NewRegistrySource(reg)
	staticSrc := NewStaticSource(nil)
	ms := NewMultiSource(staticSrc, regSrc)

	var regCalled, staticCalled bool
	// StaticSource.OnChange is a no-op; this just proves fan-out doesn't panic.
	ms.OnChange(func() { regCalled = true })
	_ = staticCalled // static has no change notification; expect reg callback only

	require.NoError(t, reg.Register(&testTool{name: "x"}))
	assert.True(t, regCalled, "registry source callback should fire via MultiSource.OnChange")
}

func TestMultiSource_WrapsSourceError(t *testing.T) {
	ms := NewMultiSource(NewRegistrySource(nil))
	_, err := ms.Tools(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNilToolSource)
	assert.Contains(t, err.Error(), "source")
}

func TestMultiSource_NilReceiver(t *testing.T) {
	var ms *MultiSource
	_, err := ms.Tools(context.Background())
	assert.ErrorIs(t, err, ErrNilToolSource)
}
