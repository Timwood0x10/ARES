// nolint: errcheck // Test code may ignore return values
package builtin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/tools/resources/base"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

func TestToolRegistry(t *testing.T) {
	t.Run("register and get tool", func(t *testing.T) {
		registry := core.NewRegistry()

		// Use ToolFunc which implements Tool interface
		tool := base.NewToolFunc(
			"test_tool",
			"A test tool",
			nil,
			func(ctx context.Context, params map[string]interface{}) (core.Result, error) {
				return core.NewResult(true, nil), nil
			},
		)

		err := registry.Register(tool)
		if err != nil {
			t.Errorf("failed to register tool: %v", err)
		}

		retrieved, exists := registry.Get("test_tool")
		if !exists {
			t.Errorf("tool not found")
		}
		if retrieved.Name() != "test_tool" {
			t.Errorf("expected test_tool, got %s", retrieved.Name())
		}
	})

	t.Run("list tools", func(t *testing.T) {
		registry := core.NewRegistry()
		registry.Register(base.NewToolFunc("tool1", "desc1", nil, func(ctx context.Context, params map[string]interface{}) (core.Result, error) {
			return core.NewResult(true, nil), nil
		}))
		registry.Register(base.NewToolFunc("tool2", "desc2", nil, func(ctx context.Context, params map[string]interface{}) (core.Result, error) {
			return core.NewResult(true, nil), nil
		}))

		tools := registry.List()
		if len(tools) != 2 {
			t.Errorf("expected 2 tools, got %d", len(tools))
		}
	})

	t.Run("count tools", func(t *testing.T) {
		registry := core.NewRegistry()
		registry.Register(base.NewToolFunc("tool1", "desc1", nil, func(ctx context.Context, params map[string]interface{}) (core.Result, error) {
			return core.NewResult(true, nil), nil
		}))

		count := registry.Count()
		if count != 1 {
			t.Errorf("expected 1 tool, got %d", count)
		}
	})

	t.Run("unregister tool", func(t *testing.T) {
		registry := core.NewRegistry()
		registry.Register(base.NewToolFunc("tool1", "desc1", nil, func(ctx context.Context, params map[string]interface{}) (core.Result, error) {
			return core.NewResult(true, nil), nil
		}))

		err := registry.Unregister("tool1")
		if err != nil {
			t.Errorf("failed to unregister: %v", err)
		}

		_, exists := registry.Get("tool1")
		if exists {
			t.Errorf("tool should not exist after unregister")
		}
	})
}

func TestToolFunc(t *testing.T) {
	t.Run("create and execute function tool", func(t *testing.T) {
		tool := base.NewToolFunc(
			"adder",
			"Adds two numbers",
			nil,
			func(ctx context.Context, params map[string]interface{}) (core.Result, error) {
				return core.NewResult(true, params["a"]), nil
			},
		)

		if tool.Name() != "adder" {
			t.Errorf("expected adder, got %s", tool.Name())
		}

		result, err := tool.Execute(context.Background(), map[string]interface{}{"a": 1.0})
		if err != nil {
			t.Errorf("execute error: %v", err)
		}
		if !result.Success {
			t.Errorf("expected success")
		}
	})
}

func TestBaseTool(t *testing.T) {
	t.Run("create base tool", func(t *testing.T) {
		tool := base.NewBaseTool("my_tool", "A tool", nil)

		if tool.Name() != "my_tool" {
			t.Errorf("expected my_tool, got %s", tool.Name())
		}
		if tool.Description() != "A tool" {
			t.Errorf("expected A tool, got %s", tool.Description())
		}
	})
}

// TestRegisterGeneralTools_DuplicateNameContinues verifies that
// RegisterGeneralTools logs a warning and continues past a duplicate tool
// name instead of aborting all registration. This is the fix for TL-2: a
// single conflict must not prevent the remaining tools from registering.
func TestRegisterGeneralTools_DuplicateNameContinues(t *testing.T) {
	registry := core.NewRegistry()

	// Pre-register a tool whose name collides with the first tool registered
	// by RegisterGeneralTools (calculator). The pre-existing tool must win.
	pre := base.NewToolFunc(
		"calculator",
		"pre-registered duplicate sentinel",
		nil,
		func(ctx context.Context, params map[string]interface{}) (core.Result, error) {
			return core.NewResult(true, nil), nil
		},
	)
	require.NoError(t, registry.Register(pre))

	// With the OLD behavior this returned an error on the first duplicate;
	// with the NEW behavior it logs a warning and continues, registering the
	// rest of the general-purpose tools.
	require.NoError(t, RegisterGeneralTools(registry),
		"RegisterGeneralTools should continue past duplicate names")

	// The pre-registered duplicate must still be present (builtin lost the
	// conflict because the pre-existing registration wins).
	tool, ok := registry.Get("calculator")
	require.True(t, ok, "calculator tool should still be registered")
	require.Equal(t, "pre-registered duplicate sentinel", tool.Description(),
		"pre-registered tool should win the name conflict")

	// At least one other general tool should have been registered despite the
	// duplicate. "datetime" is registered right after "calculator" in the
	// general-tools list.
	_, ok = registry.Get("datetime")
	require.True(t, ok, "datetime should be registered after duplicate-name continuation")
}

// nolint: errcheck // Test code may ignore return values
// nolint: errcheck // Test code may ignore return values
