package graphservice

import (
	"context"
	"testing"
	"time"

	wfgraph "github.com/Timwood0x10/ares/internal/workflow/graph"
)

func TestNewService(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		s, err := NewService(nil)
		if err == nil {
			t.Fatal("expected error for nil config")
		}
		if s != nil {
			t.Errorf("expected nil service, got %v", s)
		}
	})

	t.Run("default config", func(t *testing.T) {
		s, err := NewService(&Config{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if s == nil {
			t.Fatal("expected non-nil service")
		}
	})
}

func TestExecute_Validation(t *testing.T) {
	ctx := context.Background()
	t.Run("nil graph", func(t *testing.T) {
		s, _ := NewService(&Config{})
		_, err := s.Execute(ctx, nil, &ExecuteRequest{GraphID: "test"})
		if err != ErrInvalidGraph {
			t.Errorf("expected ErrInvalidGraph, got %v", err)
		}
	})

	t.Run("nil request", func(t *testing.T) {
		s, _ := NewService(&Config{})
		g, _ := wfgraph.NewGraph("test")
		_, _ = g.Start("a")
		_, err := s.Execute(ctx, g, nil)
		if err != ErrInvalidRequest {
			t.Errorf("expected ErrInvalidRequest, got %v", err)
		}
	})
}

func TestExecute_HappyPath(t *testing.T) {
	ctx := context.Background()
	s, _ := NewService(&Config{})
	g, _ := wfgraph.NewGraph("test-graph")
	fn, _ := wfgraph.NewFuncNode("a", func(_ context.Context, st *wfgraph.State) error {
		st.Set("result", 42)
		return nil
	})
	_, _ = g.Node("a", fn)
	_, _ = g.Start("a")

	resp, err := s.Execute(ctx, g, &ExecuteRequest{GraphID: "test-graph"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.GraphID != "test-graph" {
		t.Errorf("expected graph ID test-graph, got %s", resp.GraphID)
	}
	if resp.Duration <= 0 {
		t.Errorf("expected positive duration, got %v", resp.Duration)
	}
}

func TestExecute_Timeout(t *testing.T) {
	ctx := context.Background()
	s, _ := NewService(&Config{})
	g, _ := wfgraph.NewGraph("test")
	fn, _ := wfgraph.NewFuncNode("a", func(_ context.Context, _ *wfgraph.State) error {
		time.Sleep(50 * time.Millisecond)
		return nil
	})
	_, _ = g.Node("a", fn)
	_, _ = g.Start("a")

	_, err := s.Execute(ctx, g, &ExecuteRequest{
		GraphID: "test",
		Timeout: time.Nanosecond,
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}
