package builtin

import (
	"time"

	"github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/tools/resources/base"
	builtin_embedding "github.com/Timwood0x10/ares/internal/tools/resources/builtin/embedding"
	builtin_execution "github.com/Timwood0x10/ares/internal/tools/resources/builtin/execution"
	builtin_file "github.com/Timwood0x10/ares/internal/tools/resources/builtin/file"
	builtin_hash "github.com/Timwood0x10/ares/internal/tools/resources/builtin/hash"
	builtin_knowledge "github.com/Timwood0x10/ares/internal/tools/resources/builtin/knowledge"
	builtin_math "github.com/Timwood0x10/ares/internal/tools/resources/builtin/math"
	builtin_memory "github.com/Timwood0x10/ares/internal/tools/resources/builtin/memory"
	builtin_network "github.com/Timwood0x10/ares/internal/tools/resources/builtin/network"
	builtin_pdf "github.com/Timwood0x10/ares/internal/tools/resources/builtin/pdf"
	builtin_planning "github.com/Timwood0x10/ares/internal/tools/resources/builtin/planning"
	builtin_stringutils "github.com/Timwood0x10/ares/internal/tools/resources/builtin/stringutils"
	builtin_system "github.com/Timwood0x10/ares/internal/tools/resources/builtin/system"
	builtin_text "github.com/Timwood0x10/ares/internal/tools/resources/builtin/text"

	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// RegisterGeneralTools registers all general-purpose tools.
func RegisterGeneralTools() error {
	tools := []core.Tool{
		// Math capability
		base.WithToolTags(builtin_math.NewCalculator(), map[string]string{
			"domain": "math", "input_type": "text", "output_type": "number",
			"side_effects": "false",
		}),
		base.WithToolTags(builtin_math.NewDateTime(), map[string]string{
			"domain": "math", "input_type": "text", "output_type": "text",
			"side_effects": "false",
		}),
		base.WithToolTags(builtin_math.NewTextProcessor(), map[string]string{
			"domain": "math", "input_type": "text", "output_type": "text",
			"side_effects": "false",
		}),

		// Network capability
		base.WithToolTags(builtin_network.NewHTTPRequest(), map[string]string{
			"domain": "network", "input_type": "json", "output_type": "text",
			"side_effects": "true", "requires_network": "true",
		}),
		base.WithToolTags(
			builtin_network.NewWebScraper(builtin_network.NewWebFetcher(builtin_network.NewDefaultHTTPClient(30*time.Second))),
			map[string]string{"domain": "network", "input_type": "url", "output_type": "text",
				"side_effects": "false", "requires_network": "true"},
		),
		base.WithToolTags(builtin_network.NewWebSearch(), map[string]string{
			"domain": "network", "input_type": "text", "output_type": "text",
			"side_effects": "false", "requires_network": "true",
		}),

		// File capability
		base.WithToolTags(builtin_file.NewFileTools(), map[string]string{
			"domain": "file", "input_type": "text", "output_type": "text",
			"side_effects": "true", "mutates_state": "true",
		}),

		// Text capability
		base.WithToolTags(builtin_text.NewJSONTools(), map[string]string{
			"domain": "data", "input_type": "json", "output_type": "json",
			"side_effects": "false",
		}),
		base.WithToolTags(builtin_text.NewDataValidation(), map[string]string{
			"domain": "data", "input_type": "text", "output_type": "boolean",
			"side_effects": "false",
		}),
		base.WithToolTags(builtin_text.NewDataTransform(), map[string]string{
			"domain": "data", "input_type": "text", "output_type": "text",
			"side_effects": "false",
		}),
		base.WithToolTags(builtin_text.NewRegexTool(), map[string]string{
			"domain": "text", "input_type": "text", "output_type": "text",
			"side_effects": "false",
		}),
		base.WithToolTags(builtin_text.NewLogAnalyzer(), map[string]string{
			"domain": "text", "input_type": "text", "output_type": "text",
			"side_effects": "false",
		}),

		// Knowledge capability
		base.WithToolTags(builtin_knowledge.NewKnowledgeSearch(nil), map[string]string{
			"domain": "knowledge", "input_type": "text", "output_type": "json",
			"side_effects": "false",
		}),
		base.WithToolTags(builtin_knowledge.NewKnowledgeAdd(nil), map[string]string{
			"domain": "knowledge", "input_type": "json", "output_type": "boolean",
			"side_effects": "true", "mutates_state": "true",
		}),
		base.WithToolTags(builtin_knowledge.NewKnowledgeUpdate(nil), map[string]string{
			"domain": "knowledge", "input_type": "json", "output_type": "boolean",
			"side_effects": "true", "mutates_state": "true",
		}),
		base.WithToolTags(builtin_knowledge.NewKnowledgeDelete(nil), map[string]string{
			"domain": "knowledge", "input_type": "text", "output_type": "boolean",
			"side_effects": "true", "mutates_state": "true",
		}),
		base.WithToolTags(builtin_knowledge.NewCorrectKnowledge(nil), map[string]string{
			"domain": "knowledge", "input_type": "json", "output_type": "boolean",
			"side_effects": "true", "mutates_state": "true",
		}),

		// Memory capability
		base.WithToolTags(builtin_memory.NewMemorySearch(nil), map[string]string{
			"domain": "memory", "input_type": "text", "output_type": "json",
			"side_effects": "false",
		}),
		base.WithToolTags(builtin_memory.NewUserProfile(nil, nil), map[string]string{
			"domain": "memory", "input_type": "text", "output_type": "json",
			"side_effects": "false",
		}),
		base.WithToolTags(builtin_memory.NewDistilledMemorySearch(nil), map[string]string{
			"domain": "memory", "input_type": "text", "output_type": "json",
			"side_effects": "false",
		}),

		// System capability
		base.WithToolTags(builtin_system.NewIDGenerator(), map[string]string{
			"domain": "system", "input_type": "text", "output_type": "text",
			"side_effects": "false",
		}),

		// Execution capability
		base.WithToolTags(builtin_execution.NewCodeRunner(), map[string]string{
			"domain": "execution", "input_type": "text", "output_type": "text",
			"side_effects": "true",
		}),

		// Planning capability
		base.WithToolTags(builtin_planning.NewTaskPlanner(nil), map[string]string{
			"domain": "planning", "input_type": "text", "output_type": "json",
			"side_effects": "false",
		}),

		// Embedding capability
		base.WithToolTags(builtin_embedding.NewEmbeddingTool(""), map[string]string{
			"domain": "embedding", "input_type": "text", "output_type": "json",
			"side_effects": "false", "requires_network": "true",
		}),

		// Hash capability
		base.WithToolTags(builtin_hash.NewHashTool(), map[string]string{
			"domain": "crypto", "input_type": "text", "output_type": "text",
			"side_effects": "false",
		}),

		// String utils capability
		base.WithToolTags(builtin_stringutils.NewStringUtils(), map[string]string{
			"domain": "text", "input_type": "text", "output_type": "text",
			"side_effects": "false",
		}),

		// PDF capability
		base.WithToolTags(builtin_pdf.NewPDFTool(), map[string]string{
			"domain": "pdf", "input_type": "file", "output_type": "text",
			"side_effects": "false",
		}),
	}

	for _, tool := range tools {
		if err := core.Register(tool); err != nil {
			return errors.Wrap(err, "failed to register tool")
		}
	}

	return nil
}
