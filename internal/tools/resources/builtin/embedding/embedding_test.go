package builtin

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/tools/resources/core"
	"github.com/stretchr/testify/require"
)

func TestNewEmbeddingTool(t *testing.T) {
	tool := NewEmbeddingTool("")
	require.NotNil(t, tool)
	require.Equal(t, "embedding", tool.Name())
	require.Equal(t, core.CategoryExternal, tool.Category())
}

func TestEmbeddingTool_UnknownAction(t *testing.T) {
	tool := NewEmbeddingTool("")
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"action": "invalid",
	})
	require.NoError(t, err)
	require.False(t, result.Success)
}

func TestEmbeddingTool_EmbedMissingText(t *testing.T) {
	tool := NewEmbeddingTool("")
	ctx := context.Background()

	// embed action without text should return error
	result, err := tool.Execute(ctx, map[string]interface{}{
		"action": "embed",
	})
	require.NoError(t, err)
	require.False(t, result.Success)
}

func TestEmbeddingTool_BatchMissingTexts(t *testing.T) {
	tool := NewEmbeddingTool("")
	ctx := context.Background()

	// embed_batch action without texts should return error
	result, err := tool.Execute(ctx, map[string]interface{}{
		"action": "embed_batch",
	})
	require.NoError(t, err)
	require.False(t, result.Success)
}

func TestEmbeddingTool_Parameters(t *testing.T) {
	tool := NewEmbeddingTool("http://test:8000")
	params := tool.Parameters()
	require.NotNil(t, params)
	require.Contains(t, params.Properties, "action")
	require.Contains(t, params.Properties, "text")
	require.Contains(t, params.Properties, "texts")
	require.Contains(t, params.Properties, "prefix")
	require.Contains(t, params.Required, "action")
}
