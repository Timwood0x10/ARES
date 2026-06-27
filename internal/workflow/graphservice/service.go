// Package graph provides graph orchestration service implementation.
package graphservice

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_observability"
	"github.com/Timwood0x10/ares/internal/ares_ratelimit"
	wfgraph "github.com/Timwood0x10/ares/internal/workflow/graph"
)

// Service provides graph orchestration operations.
type Service struct {
	config  *Config
	tracer  ares_observability.Tracer
	limiter ares_ratelimit.Limiter
}

// Config represents service configuration.
type Config struct {
	// RequestTimeout is the default request timeout.
	RequestTimeout time.Duration
	// MaxRetries is the maximum number of retries.
	MaxRetries int
	// RetryDelay is the delay between retries.
	RetryDelay time.Duration
	// Tracer is the ares_observability tracer.
	Tracer ares_observability.Tracer
	// Limiter is the rate limiter.
	Limiter ares_ratelimit.Limiter
}

// NewService creates a new graph service instance.
// Args:
// config - service configuration.
// Returns new graph service instance or error.
func NewService(config *Config) (*Service, error) {
	if config == nil {
		return nil, ErrInvalidConfig
	}

	// Set default values
	if config.RequestTimeout == 0 {
		config.RequestTimeout = 30 * time.Second
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = 3
	}
	if config.RetryDelay == 0 {
		config.RetryDelay = 1 * time.Second
	}

	// Default to no-op tracer if not provided
	tracer := config.Tracer
	if tracer == nil {
		tracer = ares_observability.NewNoopTracer()
	}

	return &Service{
		config:  config,
		tracer:  tracer,
		limiter: config.Limiter,
	}, nil
}

// ExecuteRequest represents a graph execution request.
type ExecuteRequest struct {
	// GraphID is the graph identifier.
	GraphID string
	// State is the initial state for graph execution.
	State map[string]any
	// Timeout is the execution timeout.
	Timeout time.Duration
}

// ExecuteResponse represents a graph execution response.
type ExecuteResponse struct {
	// GraphID is the graph identifier.
	GraphID string
	// State is the final state after execution.
	State map[string]any
	// Duration is the execution duration.
	Duration time.Duration
	// Error is the execution error if any.
	Error string
}

// Execute executes a graph with the given request.
// Args:
// ctx - context for cancellation and timeout.
// graph - the graph to execute.
// request - execution parameters.
// Returns execution response or error.
func (s *Service) Execute(ctx context.Context, g *wfgraph.Graph, request *ExecuteRequest) (*ExecuteResponse, error) {
	if g == nil {
		return nil, ErrInvalidGraph
	}

	if request == nil {
		return nil, ErrInvalidRequest
	}

	// Apply timeout from request or config
	timeout := request.Timeout
	if timeout == 0 {
		timeout = s.config.RequestTimeout
	}

	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Apply service-level tracer and limiter if not set on graph
	if s.tracer != nil {
		if _, err := g.SetTracer(s.tracer); err != nil {
			return nil, fmt.Errorf("set tracer: %w", err)
		}
	}
	if s.limiter != nil {
		if _, err := g.SetLimiter(s.limiter); err != nil {
			return nil, fmt.Errorf("set limiter: %w", err)
		}
	}

	// Create initial state
	state := wfgraph.NewState()
	if request.State != nil {
		for key, value := range request.State {
			state.Set(key, value)
		}
	}

	// Execute graph
	result, err := g.Execute(ctx, state)
	if err != nil {
		slog.ErrorContext(ctx, "graph execution failed",
			"graph_id", g.ID(),
			"error", err)
		return &ExecuteResponse{
			GraphID:  g.ID(),
			State:    state.ToParams(),
			Duration: time.Duration(0),
			Error:    err.Error(),
		}, fmt.Errorf("execute graph %s: %w", g.ID(), err)
	}

	slog.InfoContext(ctx, "graph execution completed",
		"graph_id", g.ID(),
		"duration", result.Duration)

	return &ExecuteResponse{
		GraphID:  g.ID(),
		State:    result.State.ToParams(),
		Duration: result.Duration,
		Error:    "",
	}, nil
}

// ExecuteWithGraphBuilder executes a graph built with a builder function.
// Args:
// ctx - context for cancellation and timeout.
// graphID - graph identifier.
// builder - function to build the graph.
// request - execution parameters.
// Returns execution response or error.
func (s *Service) ExecuteWithGraphBuilder(
	ctx context.Context,
	graphID string,
	builder func(*wfgraph.Graph) (*wfgraph.Graph, error),
	request *ExecuteRequest,
) (*ExecuteResponse, error) {
	if builder == nil {
		return nil, ErrInvalidBuilder
	}

	// Build graph
	g, err := wfgraph.NewGraph(graphID)
	if err != nil {
		return nil, err
	}
	g, err = builder(g)
	if err != nil {
		return nil, err
	}

	return s.Execute(ctx, g, request)
}

// ValidateGraph validates a graph definition.
// Args:
// graph - the graph to validate.
// Returns validation error or nil if valid.
func (s *Service) ValidateGraph(g *wfgraph.Graph) error {
	if g == nil {
		return ErrInvalidGraph
	}

	// Check if graph has start node
	if g.ID() == "" {
		return ErrMissingGraphID
	}

	// Additional validation logic can be added here
	// e.g., check for cycles, validate node dependencies, etc.

	return nil
}

// GetGraphInfo returns information about a graph.
// Args:
// graph - the graph to inspect.
// Returns graph information.
func (s *Service) GetGraphInfo(g *wfgraph.Graph) *GraphInfo {
	if g == nil {
		return nil
	}

	return &GraphInfo{
		GraphID: g.ID(),
		// Additional info can be added here
	}
}

// GraphInfo represents graph information.
type GraphInfo struct {
	GraphID string
}

// Service errors.
var (
	ErrInvalidConfig  = errors.New("invalid configuration")
	ErrInvalidGraph   = errors.New("invalid graph")
	ErrInvalidRequest = errors.New("invalid request")
	ErrInvalidBuilder = errors.New("invalid builder function")
	ErrMissingGraphID = errors.New("missing graph ID")
)
