// Package evolution tests.
package evolution

import (
	"testing"

	internal "github.com/Timwood0x10/ares/internal/ares_evolution/service"
)

func TestNewService(t *testing.T) {
	cfg := &Config{
		PopulationSize: 20,
		EliteCount:     3,
		MutationRate:   0.3,
		BaseStrategy:   &internal.Strategy{ID: "base", Params: map[string]any{"model": "llama3.2"}},
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil service")
	}
	s.Shutdown()
}

func TestNewServiceNilConfig(t *testing.T) {
	_, err := New(nil)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestReportPath(t *testing.T) {
	cfg := &Config{
		PopulationSize: 10,
		EliteCount:     2,
		MutationRate:   0.1,
		BaseStrategy:   &internal.Strategy{ID: "base", Params: map[string]any{"model": "llama3.2"}},
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	path := s.ReportPath()
	if path != "" {
		t.Fatalf("expected empty path, got %s", path)
	}
	s.Shutdown()
}
