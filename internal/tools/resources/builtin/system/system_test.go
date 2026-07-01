package builtin

import (
	"context"
	"strings"
	"testing"

	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

func TestNewIDGenerator(t *testing.T) {
	gen := NewIDGenerator()
	if gen == nil {
		t.Fatal("expected non-nil IDGenerator")
	}
	if gen.Name() != "id_generator" {
		t.Errorf("expected name id_generator, got %s", gen.Name())
	}
	if gen.Category() != core.CategorySystem {
		t.Errorf("expected category system, got %s", gen.Category())
	}
}

func TestIDGenerator_Execute(t *testing.T) {
	ctx := context.Background()
	gen := NewIDGenerator()

	t.Run("missing operation", func(t *testing.T) {
		result, err := gen.Execute(ctx, map[string]interface{}{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Success {
			t.Error("expected failure for missing operation")
		}
	})

	t.Run("generate_uuid default count", func(t *testing.T) {
		result, err := gen.Execute(ctx, map[string]interface{}{
			"operation": "generate_uuid",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.Success {
			t.Fatalf("expected success, got error: %s", result.Error)
		}
		data, ok := result.Data.(map[string]interface{})
		if !ok {
			t.Fatal("expected map data")
		}
		if data["operation"] != "generate_uuid" {
			t.Errorf("expected generate_uuid operation, got %v", data["operation"])
		}
		ids, ok := data["ids"].([]string)
		if !ok || len(ids) != 1 {
			t.Errorf("expected 1 ID, got %v", ids)
		}
		singleID, ok := data["id"].(string)
		if !ok || singleID != ids[0] {
			t.Errorf("expected id to match first ids entry")
		}
		if !strings.Contains(singleID, "-") {
			t.Errorf("expected UUID format, got %s", singleID)
		}
	})

	t.Run("generate_uuid multiple", func(t *testing.T) {
		result, err := gen.Execute(ctx, map[string]interface{}{
			"operation": "generate_uuid",
			"count":     3,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.Success {
			t.Fatalf("expected success, got error: %s", result.Error)
		}
		data := result.Data.(map[string]interface{})
		ids := data["ids"].([]string)
		if len(ids) != 3 {
			t.Errorf("expected 3 IDs, got %d", len(ids))
		}
		if len(ids[0]) != 36 {
			t.Errorf("expected UUID length 36, got %d", len(ids[0]))
		}
	})

	t.Run("generate_short_id", func(t *testing.T) {
		result, err := gen.Execute(ctx, map[string]interface{}{
			"operation": "generate_short_id",
			"count":     2,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.Success {
			t.Fatalf("expected success, got error: %s", result.Error)
		}
		data := result.Data.(map[string]interface{})
		ids := data["ids"].([]string)
		if len(ids) != 2 {
			t.Errorf("expected 2 short IDs, got %d", len(ids))
		}
		if len(ids[0]) != 8 {
			t.Errorf("expected short ID length 8, got %d", len(ids[0]))
		}
	})

	t.Run("count zero defaults to 1", func(t *testing.T) {
		result, err := gen.Execute(ctx, map[string]interface{}{
			"operation": "generate_uuid",
			"count":     0,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.Success {
			t.Fatalf("expected success, got error: %s", result.Error)
		}
		data := result.Data.(map[string]interface{})
		ids := data["ids"].([]string)
		if len(ids) != 1 {
			t.Errorf("expected 1 ID for count=0, got %d", len(ids))
		}
	})

	t.Run("count negative defaults to 1", func(t *testing.T) {
		result, err := gen.Execute(ctx, map[string]interface{}{
			"operation": "generate_uuid",
			"count":     -5,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.Success {
			t.Fatalf("expected success, got error: %s", result.Error)
		}
		data := result.Data.(map[string]interface{})
		ids := data["ids"].([]string)
		if len(ids) != 1 {
			t.Errorf("expected 1 ID for negative count, got %d", len(ids))
		}
	})

	t.Run("count exceeds 100", func(t *testing.T) {
		result, err := gen.Execute(ctx, map[string]interface{}{
			"operation": "generate_uuid",
			"count":     101,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Success {
			t.Fatal("expected failure for count > 100")
		}
	})

	t.Run("unsupported operation", func(t *testing.T) {
		result, err := gen.Execute(ctx, map[string]interface{}{
			"operation": "unknown_op",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Success {
			t.Fatal("expected failure for unknown operation")
		}
	})
}

func TestIDGenerator_BaseToolMethods(t *testing.T) {
	gen := NewIDGenerator()
	if gen.Description() == "" {
		t.Error("expected non-empty description")
	}
	if gen.Parameters() == nil {
		t.Error("expected non-nil parameters")
	}
	if len(gen.Capabilities()) == 0 {
		t.Error("expected non-empty capabilities")
	}
}

func TestIDGenerator_IsIdempotent(t *testing.T) {
	gen := NewIDGenerator()
	if !gen.IsIdempotent() {
		t.Error("expected IsIdempotent to return true")
	}
}

func TestGetInt(t *testing.T) {
	t.Run("float64 to int", func(t *testing.T) {
		if v := getInt(map[string]interface{}{"k": 3.0}, "k", 1); v != 3 {
			t.Errorf("expected 3, got %d", v)
		}
	})
	t.Run("int", func(t *testing.T) {
		if v := getInt(map[string]interface{}{"k": 5}, "k", 1); v != 5 {
			t.Errorf("expected 5, got %d", v)
		}
	})
	t.Run("string", func(t *testing.T) {
		if v := getInt(map[string]interface{}{"k": "7"}, "k", 1); v != 7 {
			t.Errorf("expected 7, got %d", v)
		}
	})
	t.Run("missing key returns default", func(t *testing.T) {
		if v := getInt(map[string]interface{}{}, "k", 42); v != 42 {
			t.Errorf("expected 42, got %d", v)
		}
	})
	t.Run("invalid string returns default", func(t *testing.T) {
		if v := getInt(map[string]interface{}{"k": "notanumber"}, "k", 10); v != 10 {
			t.Errorf("expected 10, got %d", v)
		}
	})
}
