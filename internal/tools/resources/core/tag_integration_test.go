package core

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockTaggableTool implements Tool + TaggableTool for testing.
type mockTaggableTool struct {
	name string
	cat  ToolCategory
	tags map[string]string
}

func (m *mockTaggableTool) Name() string               { return m.name }
func (m *mockTaggableTool) Description() string        { return "mock " + m.name }
func (m *mockTaggableTool) Category() ToolCategory     { return m.cat }
func (m *mockTaggableTool) Capabilities() []Capability { return nil }
func (m *mockTaggableTool) Execute(ctx context.Context, params map[string]interface{}) (Result, error) {
	return Result{Success: true}, nil
}
func (m *mockTaggableTool) Parameters() *ParameterSchema { return nil }
func (m *mockTaggableTool) Tags() map[string]string      { return m.tags }

func TestFindByTags_ExactMatch(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, reg.Register(&mockTaggableTool{
		name: "math-calc",
		cat:  CategoryCore,
		tags: map[string]string{"domain": "math", "input_type": "text", "side_effects": "false"},
	}))
	require.NoError(t, reg.Register(&mockTaggableTool{
		name: "network-http",
		cat:  CategoryCore,
		tags: map[string]string{"domain": "network", "input_type": "json", "side_effects": "true"},
	}))

	// Find by exact domain.
	result := reg.FindByTags(map[string]string{"domain": "math"})
	require.Len(t, result, 1)
	assert.Equal(t, "math-calc", result[0].Name())
}

func TestFindByTags_MultipleKeys(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, reg.Register(&mockTaggableTool{
		name: "math-calc",
		tags: map[string]string{"domain": "math", "input_type": "text", "side_effects": "false"},
	}))
	require.NoError(t, reg.Register(&mockTaggableTool{
		name: "file-read",
		tags: map[string]string{"domain": "file", "input_type": "text", "side_effects": "true"},
	}))

	result := reg.FindByTags(map[string]string{"domain": "math", "side_effects": "false"})
	require.Len(t, result, 1)
	assert.Equal(t, "math-calc", result[0].Name())
}

func TestFindByTags_WildcardValue(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, reg.Register(&mockTaggableTool{
		name: "calc",
		tags: map[string]string{"domain": "math"},
	}))
	require.NoError(t, reg.Register(&mockTaggableTool{
		name: "search",
		tags: map[string]string{"domain": "network"},
	}))

	// "*" matches any value.
	result := reg.FindByTags(map[string]string{"domain": "*"})
	require.Len(t, result, 2)
}

func TestFindByTags_NoMatch(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, reg.Register(&mockTaggableTool{
		name: "calc",
		tags: map[string]string{"domain": "math"},
	}))

	result := reg.FindByTags(map[string]string{"domain": "network"})
	assert.Empty(t, result)
}

func TestFindByTags_NonTaggableTool(t *testing.T) {
	reg := NewRegistry()
	// Register a tool that does NOT implement TaggableTool.
	require.NoError(t, reg.Register(&mockNonTaggableTool{name: "basic"}))

	result := reg.FindByTags(map[string]string{"domain": "math"})
	assert.Empty(t, result)
}

// mockNonTaggableTool implements Tool but not TaggableTool.
type mockNonTaggableTool struct {
	name string
}

func (m *mockNonTaggableTool) Name() string               { return m.name }
func (m *mockNonTaggableTool) Description() string        { return "mock " + m.name }
func (m *mockNonTaggableTool) Category() ToolCategory     { return CategoryCore }
func (m *mockNonTaggableTool) Capabilities() []Capability { return nil }
func (m *mockNonTaggableTool) Execute(ctx context.Context, params map[string]interface{}) (Result, error) {
	return Result{Success: true}, nil
}
func (m *mockNonTaggableTool) Parameters() *ParameterSchema { return nil }

func TestGetSchemas_IncludesTags(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, reg.Register(&mockTaggableTool{
		name: "calc",
		cat:  CategoryCore,
		tags: map[string]string{"domain": "math", "input_type": "text"},
	}))

	schemas := reg.GetSchemas()
	require.Len(t, schemas, 1)
	assert.Equal(t, "calc", schemas[0].Name)
	// Verify tags are included in schema.
	assert.Equal(t, "math", schemas[0].Tags["domain"])
	assert.Equal(t, "text", schemas[0].Tags["input_type"])
}

func TestGetSchemas_NonTaggableToolHasEmptyTags(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, reg.Register(&mockNonTaggableTool{name: "basic"}))

	schemas := reg.GetSchemas()
	require.Len(t, schemas, 1)
	assert.Empty(t, schemas[0].Tags)
}
