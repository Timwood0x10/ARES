package builtin_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Timwood0x10/ares/compat/tool"
	"github.com/Timwood0x10/ares/compat/tool/builtin"
)

func TestNoopTool_Basic(t *testing.T) {
	t.Parallel()

	var n tool.Tool
	n, err := builtin.New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if n.Name() != "builtin.noop" {
		t.Fatalf("expected name=builtin.noop, got %q", n.Name())
	}

	result, err := n.Execute(context.Background(), json.RawMessage(`{"hello":"world"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result != `{"hello":"world"}` {
		t.Fatalf("unexpected result: %v", result)
	}
}

func TestNoopTool_EmptyArgs(t *testing.T) {
	t.Parallel()

	n, _ := builtin.New(nil)
	if _, err := n.Execute(context.Background(), json.RawMessage(``)); err == nil {
		t.Fatal("expected error on empty args")
	}
}
