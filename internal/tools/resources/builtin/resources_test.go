// nolint: errcheck // Test code may ignore return values
package builtin

import (
	"context"
	"testing"

	"goagentx/internal/tools/resources/base"
	"goagentx/internal/tools/resources/core"
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

// nolint: errcheck // Test code may ignore return values
// nolint: errcheck // Test code may ignore return values
