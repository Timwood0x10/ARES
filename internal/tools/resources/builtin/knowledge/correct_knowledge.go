package builtin

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
	"github.com/Timwood0x10/ares/internal/tools/resources/base"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// CorrectKnowledge corrects knowledge base content.
type CorrectKnowledge struct {
	*base.BaseTool
	repo repositories.KnowledgeRepositoryInterface
}

// NewCorrectKnowledge creates a new CorrectKnowledge tool.
func NewCorrectKnowledge(repo repositories.KnowledgeRepositoryInterface) *CorrectKnowledge {
	params := &core.ParameterSchema{
		Type: "object",
		Properties: map[string]*core.Parameter{
			"chunk_id": {
				Type:        "string",
				Description: "Knowledge chunk ID to correct",
			},
			"corrected_content": {
				Type:        "string",
				Description: "Corrected content",
			},
		},
		Required: []string{"chunk_id", "corrected_content"},
	}

	ck := &CorrectKnowledge{
		repo: repo,
	}
	ck.BaseTool = base.NewBaseToolWithCapabilities("correct_knowledge", "Correct knowledge base content", core.CategoryKnowledge, []core.Capability{core.CapabilityKnowledge}, params)

	return ck
}

// Execute corrects knowledge content.
func (t *CorrectKnowledge) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	chunkID, ok := params["chunk_id"].(string)
	if !ok || chunkID == "" {
		return core.NewErrorResult("chunk_id is required"), nil
	}

	correctedContent, ok := params["corrected_content"].(string)
	if !ok || correctedContent == "" {
		return core.NewErrorResult("corrected_content is required"), nil
	}

	// Get existing chunk
	chunk, err := t.repo.GetByID(ctx, chunkID)
	if err != nil {
		return core.NewErrorResult(fmt.Sprintf("failed to get chunk: %v", err)), nil
	}

	if chunk == nil {
		return core.NewErrorResult("chunk not found"), nil
	}

	// Update content
	chunk.Content = correctedContent
	chunk.UpdatedAt = time.Now()

	// Add metadata
	if chunk.Metadata == nil {
		chunk.Metadata = make(map[string]interface{})
	}
	chunk.Metadata["corrected_at"] = time.Now()
	chunk.Metadata["correction"] = true

	// Perform update
	if err := t.repo.Update(ctx, chunk); err != nil {
		return core.NewErrorResult(fmt.Sprintf("update failed: %v", err)), nil
	}

	return core.NewResult(true, map[string]interface{}{
		"chunk_id":         chunk.ID,
		"corrected":        true,
		"corrected_at":     chunk.UpdatedAt,
		"original_content": "updated",
	}), nil
}
