package compat

import (
	"testing"

	"github.com/Timwood0x10/ares/compat/llm"
	"github.com/Timwood0x10/ares/compat/loader"
	"github.com/Timwood0x10/ares/compat/protocol"
	"github.com/Timwood0x10/ares/compat/tool"
	"github.com/Timwood0x10/ares/compat/vector"
)

func TestDefaultRegistries(t *testing.T) {
	t.Parallel()

	if Default.LLM() == nil {
		t.Fatal("LLM registry must not be nil")
	}
	if Default.Vector() == nil {
		t.Fatal("Vector registry must not be nil")
	}
	if Default.Loader() == nil {
		t.Fatal("Loader registry must not be nil")
	}
	if Default.Protocol() == nil {
		t.Fatal("Protocol registry must not be nil")
	}
	if Default.Tool() == nil {
		t.Fatal("Tool registry must not be nil")
	}
}

func TestRegisterLLM(t *testing.T) {
	t.Parallel()

	noop := llm.Factory(func(_ map[string]any) (llm.LLMProvider, error) { return nil, nil })

	if err := RegisterLLM("__test_llm", noop); err != nil {
		t.Fatalf("RegisterLLM: %v", err)
	}
	if err := RegisterLLM("__test_llm", noop); err == nil {
		t.Fatal("expected error on duplicate registration")
	}
	if _, err := Default.LLM().Lookup("__test_llm"); err != nil {
		t.Fatalf("Lookup after register: %v", err)
	}
	if _, err := Default.LLM().Lookup("nonexistent"); err == nil {
		t.Fatal("expected ErrNotFound for unknown LLM provider")
	}
}

func TestRegisterVector(t *testing.T) {
	t.Parallel()

	noop := vector.Factory(func(_ map[string]any) (vector.VectorStore, error) { return nil, nil })

	if err := RegisterVector("__test_vec", noop); err != nil {
		t.Fatalf("RegisterVector: %v", err)
	}
	if err := RegisterVector("__test_vec", noop); err == nil {
		t.Fatal("expected error on duplicate registration")
	}
}

func TestRegisterLoader(t *testing.T) {
	t.Parallel()

	noop := loader.Factory(func(_ map[string]any) (loader.DocumentLoader, error) { return nil, nil })

	if err := RegisterLoader("__test_loader", noop); err != nil {
		t.Fatalf("RegisterLoader: %v", err)
	}
	if err := RegisterLoader("__test_loader", noop); err == nil {
		t.Fatal("expected error on duplicate registration")
	}
}

func TestRegisterProtocol(t *testing.T) {
	t.Parallel()

	noop := protocol.Factory(func(_ map[string]any) (protocol.ProtocolAdapter, error) { return nil, nil })

	if err := RegisterProtocol("__test_proto", noop); err != nil {
		t.Fatalf("RegisterProtocol: %v", err)
	}
	if err := RegisterProtocol("__test_proto", noop); err == nil {
		t.Fatal("expected error on duplicate registration")
	}
}

func TestRegisterTool(t *testing.T) {
	t.Parallel()

	noop := tool.Factory(func(_ map[string]any) (tool.Tool, error) { return nil, nil })

	if err := RegisterTool("__test_tool", noop); err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}
	if err := RegisterTool("__test_tool", noop); err == nil {
		t.Fatal("expected error on duplicate registration")
	}
}
