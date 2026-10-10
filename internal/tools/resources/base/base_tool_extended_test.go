package base

import (
	"context"
	"errors"
	"testing"

	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// mockTool implements core.Tool for testing wrappers.
type mockTool struct {
	BaseTool
}

func (m *mockTool) Execute(_ context.Context, _ map[string]interface{}) (core.Result, error) {
	return core.NewResult(true, "mock"), nil
}

func TestBaseToolTagsEmptyMap(t *testing.T) {
	tool := NewBaseTool("tool", "desc", nil)
	tags := tool.Tags()
	if tags == nil {
		t.Fatal("Tags() returned nil")
	}
	if len(tags) != 0 {
		t.Errorf("Tags() len = %d, want 0", len(tags))
	}
}

func TestBaseToolSetAndGetTags(t *testing.T) {
	tool := NewBaseTool("tool", "desc", nil)
	tool.SetTags(map[string]string{"domain": "math", "input_type": "text"})
	tags := tool.Tags()
	if tags["domain"] != "math" {
		t.Errorf("Tags[domain] = %q, want math", tags["domain"])
	}
	if tags["input_type"] != "text" {
		t.Errorf("Tags[input_type] = %q, want text", tags["input_type"])
	}
}

func TestBaseToolTagsReturnsCopy(t *testing.T) {
	tool := NewBaseTool("tool", "desc", nil)
	tool.SetTags(map[string]string{"key": "val"})
	tags := tool.Tags()
	tags["key"] = "mutated"
	if tool.Tags()["key"] != "val" {
		t.Error("Tags() should return a copy, not the internal map")
	}
}

func TestBaseToolInitStop(t *testing.T) {
	tool := NewBaseTool("tool", "desc", nil)
	if err := tool.Init(context.Background()); err != nil {
		t.Errorf("Init: %v", err)
	}
	if err := tool.Stop(context.Background()); err != nil {
		t.Errorf("Stop: %v", err)
	}
}

func TestToolFuncExecuteReturnsData(t *testing.T) {
	fn := func(_ context.Context, params map[string]interface{}) (core.Result, error) {
		return core.NewResult(true, params["input"]), nil
	}
	tool := NewToolFunc("func-tool", "desc", nil, fn)
	result, err := tool.Execute(context.Background(), map[string]interface{}{"input": "hello"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Success {
		t.Error("result should be successful")
	}
	if result.Data != "hello" {
		t.Errorf("Data = %v, want hello", result.Data)
	}
}

func TestToolFuncExecuteReturnsError(t *testing.T) {
	testErr := errors.New("execution failed")
	fn := func(_ context.Context, _ map[string]interface{}) (core.Result, error) {
		return core.NewErrorResult("failed"), testErr
	}
	tool := NewToolFunc("func-tool", "desc", nil, fn)
	_, err := tool.Execute(context.Background(), nil)
	if err == nil {
		t.Error("expected error from fn")
	}
}

func TestWithToolTagsOnMockTool(t *testing.T) {
	inner := &mockTool{BaseTool: *NewBaseTool("inner-tool", "inner desc", nil)}
	tagged := WithToolTags(inner, map[string]string{
		"domain":      "knowledge",
		"input_type":  "json",
		"output_type": "json",
	})
	// tagged is core.Tool; Tags() is on TaggableTool interface.
	taggedTaggable, ok := tagged.(core.TaggableTool)
	if !ok {
		t.Fatal("tagged tool should implement TaggableTool")
	}
	tags := taggedTaggable.Tags()
	if tags["domain"] != "knowledge" {
		t.Errorf("Tags[domain] = %q, want knowledge", tags["domain"])
	}
	if tags["input_type"] != "json" {
		t.Errorf("Tags[input_type] = %q, want json", tags["input_type"])
	}
}

func TestWithToolTagsNilMap(t *testing.T) {
	inner := &mockTool{BaseTool: *NewBaseTool("inner-tool", "desc", nil)}
	tagged := WithToolTags(inner, nil)
	taggedTaggable, ok := tagged.(core.TaggableTool)
	if !ok {
		t.Fatal("tagged tool should implement TaggableTool")
	}
	tags := taggedTaggable.Tags()
	if len(tags) != 0 {
		t.Errorf("Tags() = %v, want empty map for nil tags", tags)
	}
}

func TestWithToolTagsDelegatesToInner(t *testing.T) {
	params := &core.ParameterSchema{Type: "object"}
	inner := &mockTool{BaseTool: *NewBaseTool("inner-tool", "inner desc", params)}
	tagged := WithToolTags(inner, map[string]string{"domain": "test"})

	if tagged.Name() != "inner-tool" {
		t.Errorf("Name() = %q, want inner-tool", tagged.Name())
	}
	if tagged.Description() != "inner desc" {
		t.Errorf("Description() = %q, want inner desc", tagged.Description())
	}
	if tagged.Parameters() != params {
		t.Error("Parameters() should delegate to inner tool")
	}

	// Execute should also delegate.
	result, err := tagged.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Data != "mock" {
		t.Errorf("Data = %v, want mock", result.Data)
	}
}

func TestWithMetadataWraps(t *testing.T) {
	inner := &mockTool{BaseTool: *NewBaseTool("tool", "desc", nil)}
	meta := core.ToolMetadata{Version: "1.0", Author: "test"}
	wrapped := WithMetadata(inner, meta)
	if wrapped.Name() != "tool" {
		t.Errorf("Name() = %q, want tool", wrapped.Name())
	}
}
