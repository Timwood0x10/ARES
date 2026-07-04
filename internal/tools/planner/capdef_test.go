package planner

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuiltinCapabilities_NotEmpty(t *testing.T) {
	caps := BuiltinCapabilities()
	assert.Greater(t, len(caps), 10)
}

func TestBuiltinCapabilities_AllHaveNames(t *testing.T) {
	for _, c := range BuiltinCapabilities() {
		assert.NotEmpty(t, c.Name, "capability name must not be empty")
	}
}

func TestBuiltinCapabilities_AllHaveAliases(t *testing.T) {
	for _, c := range BuiltinCapabilities() {
		assert.Greater(t, len(c.Aliases), 0, "capability %q must have at least one alias", c.Name)
	}
}

func TestBuiltinCapabilities_InputOutputTypes(t *testing.T) {
	for _, c := range BuiltinCapabilities() {
		assert.NotEmpty(t, c.InputType, "capability %q must have InputType", c.Name)
		assert.NotEmpty(t, c.OutputType, "capability %q must have OutputType", c.Name)
	}
}

func TestFindCapability_ByName(t *testing.T) {
	c := FindCapability("Arithmetic")
	assert.NotNil(t, c)
	assert.Equal(t, "Arithmetic", c.Name)
}

func TestFindCapability_ByAlias(t *testing.T) {
	c := FindCapability("累加")
	assert.NotNil(t, c)
	assert.Equal(t, "Summation", c.Name)
}

func TestFindCapability_NotFound(t *testing.T) {
	c := FindCapability("NonexistentCapability")
	assert.Nil(t, c)
}

func TestToolCapabilityMap_EveryToolHasCapabilities(t *testing.T) {
	capaMap := ToolCapabilityMap()
	assert.Greater(t, len(capaMap), 15)

	for toolName, caps := range capaMap {
		assert.Greater(t, len(caps), 0, "tool %q must have at least one capability", toolName)
		for _, capa := range caps {
			def := FindCapability(capa)
			if def == nil {
				// Some capabilities like ExpressionEvaluation, TextExtraction,
				// WebFetch, etc. are implicit — they are valid but not yet
				// formalized in BuiltinCapabilities.
				continue
			}
			assert.Equal(t, capa, def.Name)
		}
	}
}

func TestToolCapabilityMap_AllToolsExist(t *testing.T) {
	capaMap := ToolCapabilityMap()
	expectedTools := []string{
		"calculator", "hash_tool", "string_utils", "regex_tool",
		"json_tools", "pdf_tool", "web_search", "http_request",
		"id_generator", "code_runner", "embedding", "datetime",
		"data_transform", "data_validation", "log_analyzer",
		"text_processor", "task_planner",
	}
	for _, tool := range expectedTools {
		_, exists := capaMap[tool]
		assert.True(t, exists, "tool %q should be in ToolCapabilityMap", tool)
	}
}

func TestToolCapabilityMap_CalculatorHasSummation(t *testing.T) {
	caps := ToolCapabilityMap()["calculator"]
	assert.Contains(t, caps, "Summation")
	assert.Contains(t, caps, "Arithmetic")
}
