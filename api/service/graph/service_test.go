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
	g := wfgraph.NewGraph("test").
		Node("node1", wfgraph.NewFuncNode("node1", func(ctx context.Context, state *wfgraph.State) error {
			state.Set("result", "success")
			return nil
		})).
		Start("node1")

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
	g := wfgraph.NewGraph("timeout-test").
		Node("node1", wfgraph.NewFuncNode("node1", func(ctx context.Context, state *wfgraph.State) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(10 * time.Second):
				return nil
			}
		})).
		Start("node1")

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
		func(g *wfgraph.Graph) *wfgraph.Graph {
			return g.Node("node1", wfgraph.NewFuncNode("node1", func(ctx context.Context, state *wfgraph.State) error {
				state.Set("result", "builder-success")
				return nil
			})).
				Start("node1")
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

	g := wfgraph.NewGraph("test").
		Node("node1", wfgraph.NewFuncNode("node1", func(ctx context.Context, state *wfgraph.State) error {
			return nil
		})).
		Start("node1")

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
	g := wfgraph.NewGraph("test").
		Node("node1", wfgraph.NewFuncNode("node1", func(ctx context.Context, state *wfgraph.State) error {
			return nil
		})).
		Start("node1")

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

	g := wfgraph.NewGraph("test").
		Node("node1", wfgraph.NewFuncNode("node1", func(ctx context.Context, state *wfgraph.State) error {
			return nil
		})).
		Start("node1")

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

	g := wfgraph.NewGraph("observability-test").
		Node("node1", wfgraph.NewFuncNode("node1", func(ctx context.Context, state *wfgraph.State) error {
			state.Set("result", "success")
			return nil
		})).
		Start("node1")

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
