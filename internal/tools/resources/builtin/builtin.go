package builtin

import (
	"time"

	"github.com/Timwood0x10/goagent/internal/errors"
	builtin_execution "github.com/Timwood0x10/goagent/internal/tools/resources/builtin/execution"
	builtin_file "github.com/Timwood0x10/goagent/internal/tools/resources/builtin/file"
	builtin_knowledge "github.com/Timwood0x10/goagent/internal/tools/resources/builtin/knowledge"
	builtin_math "github.com/Timwood0x10/goagent/internal/tools/resources/builtin/math"
	builtin_memory "github.com/Timwood0x10/goagent/internal/tools/resources/builtin/memory"
	builtin_network "github.com/Timwood0x10/goagent/internal/tools/resources/builtin/network"
	builtin_planning "github.com/Timwood0x10/goagent/internal/tools/resources/builtin/planning"
	builtin_system "github.com/Timwood0x10/goagent/internal/tools/resources/builtin/system"
	builtin_text "github.com/Timwood0x10/goagent/internal/tools/resources/builtin/text"

	"github.com/Timwood0x10/goagent/internal/tools/resources/core"
)

// RegisterGeneralTools registers all general-purpose tools.
func RegisterGeneralTools() error {
	tools := []core.Tool{
		// Math capability
		builtin_math.NewCalculator(),
		builtin_math.NewDateTime(),
		builtin_math.NewTextProcessor(),

		// Network capability
		builtin_network.NewHTTPRequest(),
		builtin_network.NewWebScraper(builtin_network.NewWebFetcher(builtin_network.NewDefaultHTTPClient(30 * time.Second))),
		// File capability
		builtin_file.NewFileTools(),

		// Text capability
		builtin_text.NewJSONTools(),
		builtin_text.NewDataValidation(),
		builtin_text.NewDataTransform(),
		builtin_text.NewRegexTool(),
		builtin_text.NewLogAnalyzer(),

		// Knowledge capability
		builtin_knowledge.NewKnowledgeSearch(nil),
		builtin_knowledge.NewKnowledgeAdd(nil),
		builtin_knowledge.NewKnowledgeUpdate(nil),
		builtin_knowledge.NewKnowledgeDelete(nil),
		builtin_knowledge.NewCorrectKnowledge(nil),

		// Memory capability
		builtin_memory.NewMemorySearch(nil),
		builtin_memory.NewUserProfile(nil, nil),
		builtin_memory.NewDistilledMemorySearch(nil),

		// System capability
		builtin_system.NewIDGenerator(),

		// Execution capability
		builtin_execution.NewCodeRunner(),

		// Planning capability
		builtin_planning.NewTaskPlanner(nil),

		// Domain capability - removed: weather_check, style_recommend (not in 8 core capabilities)
	}

	for _, tool := range tools {
		if err := core.Register(tool); err != nil {
			return errors.Wrap(err, "failed to register tool")
		}
	}

	return nil
}
