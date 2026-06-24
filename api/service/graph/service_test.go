// Package graph provides tests for graph service.
package graph

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/observability"
	wfgraph "github.com/Timwood0x10/ares/internal/workflow/graph"
)

func TestNewService(t *testing.T) {
	config := &Config{
		RequestTimeout: 30 * time.Second,
		MaxRetries:     3,
		RetryDelay:     1 * time.Second,
	}

	service, err := NewService(config)
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	if service == nil {
		t.Error("expected non-nil service")
	}
}

func TestNewServiceWithNilConfig(t *testing.T) {
	_, err := NewService(nil)
	if err == nil {
		t.Error("expected error for nil config")
	}
	if err != ErrInvalidConfig {
		t.Errorf("expected ErrInvalidConfig, got %v", err)
	}
}

func TestExecute(t *testing.T) {
	config := &Config{
		RequestTimeout: 5 * time.Second,
	}

	service, err := NewService(config)
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	// Create a simple graph
	g, errBuild := wfgraph.NewGraph("test")
	if errBuild != nil {
		t.Fatalf("NewGraph failed: %v", errBuild)
	}
	n1, errBuild := wfgraph.NewFuncNode("node1", func(ctx context.Context, state *wfgraph.State) error {
		state.Set("result", "success")
		return nil
	})
	if errBuild != nil {
		t.Fatalf("NewFuncNode failed: %v", errBuild)
	}
	_, errBuild = g.Node("node1", n1)
	if errBuild != nil {
		t.Fatalf("Node failed: %v", errBuild)
	}
	_, errBuild = g.Start("node1")
	if errBuild != nil {
		t.Fatalf("Start failed: %v", errBuild)
	}

	request := &ExecuteRequest{
		GraphID: "test",
		State:   map[string]any{"input": "test"},
	}

	response, err := service.Execute(context.Background(), g, request)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	if response == nil {
		t.Error("expected non-nil response")
		return
	}

	if response.GraphID != "test" {
		t.Errorf("expected graph ID test, got %s", response.GraphID)
	}

	if response.Error != "" {
		t.Errorf("expected no error, got %s", response.Error)
	}

	if response.Duration == 0 {
		t.Error("expected non-zero duration")
	}
}

func TestExecuteWithTimeout(t *testing.T) {
	config := &Config{
		RequestTimeout: 5 * time.Second,
	}

	service, err := NewService(config)
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	// Create a graph with long-running node
	g, errBuild := wfgraph.NewGraph("timeout-test")
	if errBuild != nil {
		t.Fatalf("NewGraph failed: %v", errBuild)
	}
	n1, errBuild := wfgraph.NewFuncNode("node1", func(ctx context.Context, state *wfgraph.State) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
			return nil
		}
	})
	if errBuild != nil {
		t.Fatalf("NewFuncNode failed: %v", errBuild)
	}
	_, errBuild = g.Node("node1", n1)
	if errBuild != nil {
		t.Fatalf("Node failed: %v", errBuild)
	}
	_, errBuild = g.Start("node1")
	if errBuild != nil {
		t.Fatalf("Start failed: %v", errBuild)
	}

	request := &ExecuteRequest{
		GraphID: "timeout-test",
		Timeout: 10 * time.Millisecond, // very short timeout
	}

	response, err := service.Execute(context.Background(), g, request)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	require.NotNil(t, response)
	if response.Error == "" {
		t.Error("expected error message in response")
	}
}

func TestExecuteWithGraphBuilder(t *testing.T) {
	config := &Config{
		RequestTimeout: 5 * time.Second,
	}

	service, err := NewService(config)
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	request := &ExecuteRequest{
		GraphID: "builder-test",
		State:   map[string]any{"input": "test"},
	}

	response, err := service.ExecuteWithGraphBuilder(
		context.Background(),
		"builder-test",
		func(g *wfgraph.Graph) (*wfgraph.Graph, error) {
			n1, err := wfgraph.NewFuncNode("node1", func(ctx context.Context, state *wfgraph.State) error {
				state.Set("result", "builder-success")
				return nil
			})
			if err != nil {
				return nil, err
			}
			g, err = g.Node("node1", n1)
			if err != nil {
				return nil, err
			}
			return g.Start("node1")
		},
		request,
	)

	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	if response == nil {
		t.Error("expected non-nil response")
		return
	}

	if response.Error != "" {
		t.Errorf("expected no error, got %s", response.Error)
	}
}

func TestExecuteWithNilGraph(t *testing.T) {
	config := &Config{}
	service, err := NewService(config)
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	request := &ExecuteRequest{
		GraphID: "test",
	}

	_, err = service.Execute(context.Background(), nil, request)
	if err == nil {
		t.Error("expected error for nil graph")
	}
	if err != ErrInvalidGraph {
		t.Errorf("expected ErrInvalidGraph, got %v", err)
	}
}

func TestExecuteWithNilRequest(t *testing.T) {
	config := &Config{}
	service, err := NewService(config)
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	g := newTestGraph(t, "test", func(ctx context.Context, state *wfgraph.State) error {
		return nil
	})

	_, err = service.Execute(context.Background(), g, nil)
	if err == nil {
		t.Error("expected error for nil request")
	}
	if err != ErrInvalidRequest {
		t.Errorf("expected ErrInvalidRequest, got %v", err)
	}
}

func TestValidateGraph(t *testing.T) {
	config := &Config{}
	service, err := NewService(config)
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	// Test valid graph
	g := newTestGraph(t, "test", func(ctx context.Context, state *wfgraph.State) error {
		return nil
	})

	err = service.ValidateGraph(g)
	if err != nil {
		t.Errorf("expected no error for valid graph, got %v", err)
	}

	// Test nil graph
	err = service.ValidateGraph(nil)
	if err == nil {
		t.Error("expected error for nil graph")
	}
	if err != ErrInvalidGraph {
		t.Errorf("expected ErrInvalidGraph, got %v", err)
	}
}

func TestGetGraphInfo(t *testing.T) {
	config := &Config{}
	service, err := NewService(config)
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	g := newTestGraph(t, "test", func(ctx context.Context, state *wfgraph.State) error {
		return nil
	})

	info := service.GetGraphInfo(g)
	if info == nil {
		t.Error("expected non-nil info")
		return
	}

	if info.GraphID != "test" {
		t.Errorf("expected graph ID test, got %s", info.GraphID)
	}

	// Test nil graph
	info = service.GetGraphInfo(nil)
	if info != nil {
		t.Error("expected nil info for nil graph")
	}
}

func TestExecuteWithObservability(t *testing.T) {
	tracer := observability.NewLogTracer(&observability.LogTracerConfig{})

	config := &Config{
		RequestTimeout: 5 * time.Second,
		Tracer:         tracer,
	}

	service, err := NewService(config)
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	g := newTestGraph(t, "observability-test", func(ctx context.Context, state *wfgraph.State) error {
		state.Set("result", "success")
		return nil
	})

	request := &ExecuteRequest{
		GraphID: "observability-test",
	}

	response, err := service.Execute(context.Background(), g, request)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	if response.Error != "" {
		t.Errorf("expected no error, got %s", response.Error)
	}
}

// newTestGraph is a test helper that builds a single-node graph.
func newTestGraph(t *testing.T, id string, fn func(context.Context, *wfgraph.State) error) *wfgraph.Graph {
	t.Helper()
	g, err := wfgraph.NewGraph(id)
	if err != nil {
		t.Fatalf("NewGraph failed: %v", err)
	}
	n, err := wfgraph.NewFuncNode("node1", fn)
	if err != nil {
		t.Fatalf("NewFuncNode failed: %v", err)
	}
	_, err = g.Node("node1", n)
	if err != nil {
		t.Fatalf("Node failed: %v", err)
	}
	_, err = g.Start("node1")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	return g
}
