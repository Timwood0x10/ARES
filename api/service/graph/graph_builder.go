// Package graph provides graph building functionality from YAML configuration.

package graph

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Timwood0x10/goagent/internal/agents/base"
	"github.com/Timwood0x10/goagent/internal/errors"
	"github.com/Timwood0x10/goagent/internal/observability"
	"github.com/Timwood0x10/goagent/internal/tools/resources/core"
	wfgraph "github.com/Timwood0x10/goagent/internal/workflow/graph"
)

// GraphBuilder builds graph instances from configuration.
type GraphBuilder struct {
	agentRegistry map[string]base.Agent
	toolRegistry  map[string]interface{}
}

// NewGraphBuilder creates a new graph builder.
func NewGraphBuilder() *GraphBuilder {
	return &GraphBuilder{
		agentRegistry: make(map[string]base.Agent),
		toolRegistry:  make(map[string]interface{}),
	}
}

// RegisterAgent registers an agent for use in graph configuration.
func (b *GraphBuilder) RegisterAgent(agent base.Agent) {
	if b == nil || agent == nil {
		return
	}
	b.agentRegistry[agent.ID()] = agent
}

// RegisterTool registers a tool for use in graph configuration.
func (b *GraphBuilder) RegisterTool(id string, tool interface{}) {
	if b == nil || id == "" || tool == nil {
		return
	}
	b.toolRegistry[id] = tool
}

// Build builds a graph from configuration.
func (b *GraphBuilder) Build(config *GraphConfig) (*wfgraph.Graph, error) {
	if b == nil {
		return nil, fmt.Errorf("builder is nil")
	}
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	gdef := &config.Graph

	// Create new graph
	g := wfgraph.NewGraph(gdef.ID)

	// Build nodes
	for _, nodeConfig := range gdef.Nodes {
		node, err := b.buildNode(nodeConfig)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to build node '%s'", nodeConfig.ID)
		}
		g.Node(nodeConfig.ID, node)
	}

	// Build edges
	for _, edgeConfig := range gdef.Edges {
		if edgeConfig.Condition != "" {
			// Parse condition string and create conditional edge
			cond, err := b.parseCondition(edgeConfig.Condition)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to parse condition for edge %s -> %s", edgeConfig.From, edgeConfig.To)
			}
			g.Edge(edgeConfig.From, edgeConfig.To, cond)
		} else {
			// Unconditional edge
			g.Edge(edgeConfig.From, edgeConfig.To)
		}
	}

	// Set start node
	g.Start(gdef.StartNode)

	return g, nil
}

// buildNode builds a single node from configuration.
func (b *GraphBuilder) buildNode(config Node) (wfgraph.Node, error) {
	switch config.Type {
	case "function":
		return b.buildFuncNode(config)
	case "agent":
		return b.buildAgentNode(config)
	case "tool":
		return b.buildToolNode(config)
	default:
		return nil, fmt.Errorf("unsupported node type: %s", config.Type)
	}
}

// buildFuncNode builds a function node.
func (b *GraphBuilder) buildFuncNode(config Node) (wfgraph.Node, error) {
	if config.ID == "" {
		return nil, fmt.Errorf("node ID is required")
	}

	// Create a simple function that logs the node execution.
	fn := func(ctx context.Context, state *wfgraph.State) error {
		if ctx == nil {
			return fmt.Errorf("context is nil")
		}
		if state == nil {
			return fmt.Errorf("state is nil")
		}

		fmt.Printf("[Node %s] Executing...\n", config.ID)
		if config.Description != "" {
			fmt.Printf("  Description: %s\n", config.Description)
		}

		// Store node execution timestamp.
		state.Set(fmt.Sprintf("node.%s.timestamp", config.ID), "executed")
		state.Set(fmt.Sprintf("node.%s.status", config.ID), "success")

		return nil
	}

	return wfgraph.NewFuncNode(config.ID, fn), nil
}

func compareFloats(op string) func(a, b any) bool {
	return func(a, b any) bool {
		if aFloat, ok := a.(float64); ok {
			if bFloat, ok := b.(float64); ok {
				switch op {
				case ">":
					return aFloat > bFloat
				case "<":
					return aFloat < bFloat
				case ">=":
					return aFloat >= bFloat
				case "<=":
					return aFloat <= bFloat
				}
			}
		}
		af, ae := strconv.ParseFloat(fmt.Sprintf("%v", a), 64)
		bf, be := strconv.ParseFloat(fmt.Sprintf("%v", b), 64)
		if ae != nil || be != nil {
			return false
		}
		switch op {
		case ">":
			return af > bf
		case "<":
			return af < bf
		case ">=":
			return af >= bf
		case "<=":
			return af <= bf
		}
		return false
	}
}

// parseCondition parses a condition string and returns a Condition function.
// Supports basic comparisons: "key == value", "key != value", "key > num", "key < num", "key >= num", "key <= num".
func (b *GraphBuilder) parseCondition(condition string) (wfgraph.Condition, error) {
	if condition == "" || condition == "true" {
		return func(state *wfgraph.State) bool {
			return true
		}, nil
	}

	condition = strings.TrimSpace(condition)

	parseCompare := func(op string) func(a, b any) bool {
		switch op {
		case "==":
			return func(a, b any) bool { return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b) }
		case "!=":
			return func(a, b any) bool { return fmt.Sprintf("%v", a) != fmt.Sprintf("%v", b) }
		case ">":
			return compareFloats(op)
		case "<":
			return compareFloats(op)
		case ">=":
			return compareFloats(op)
		case "<=":
			return compareFloats(op)
		default:
			return nil
		}
	}

	for _, op := range []string{"==", "!=", ">=", "<=", ">", "<"} {
		parts := strings.SplitN(condition, op, 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			cmp := parseCompare(op)
			if cmp != nil {
				return func(state *wfgraph.State) bool {
					if state == nil {
						return false
					}
					v, ok := state.Get(key)
					if !ok {
						return false
					}
					return cmp(v, value)
				}, nil
			}
		}
	}

	return func(state *wfgraph.State) bool {
		return true
	}, nil
}

// buildAgentNode builds an agent node.
func (b *GraphBuilder) buildAgentNode(config Node) (wfgraph.Node, error) {
	if b == nil {
		return nil, fmt.Errorf("builder is nil")
	}

	if config.ID == "" {
		return nil, fmt.Errorf("node ID is required")
	}

	// Get agent ID from config.
	agentID, ok := config.Config["agent_id"].(string)
	if !ok {
		// Use node ID as agent ID.
		agentID = config.ID
	}

	agent, exists := b.agentRegistry[agentID]
	if !exists {
		return nil, fmt.Errorf("agent '%s' not registered", agentID)
	}

	return wfgraph.NewAgentNode(agent), nil
}

// buildToolNode builds a tool node.
func (b *GraphBuilder) buildToolNode(config Node) (wfgraph.Node, error) {
	if b == nil {
		return nil, fmt.Errorf("builder is nil")
	}

	if config.ID == "" {
		return nil, fmt.Errorf("node ID is required")
	}

	// Get tool ID from config.
	toolID, ok := config.Config["tool_id"].(string)
	if !ok {
		toolID = config.ID
	}

	tool, exists := b.toolRegistry[toolID]
	if !exists {
		return nil, fmt.Errorf("tool '%s' not registered", toolID)
	}

	// Type assert tool to core.Tool interface
	toolImpl, ok := tool.(core.Tool)
	if !ok {
		return nil, fmt.Errorf("tool '%s' does not implement core.Tool interface", toolID)
	}

	return wfgraph.NewToolNode(toolImpl), nil
}

// BuildWithService creates a complete graph service from configuration.
func BuildWithService(configYAML []byte, builder *GraphBuilder) (*Service, *wfgraph.Graph, error) {
	if len(configYAML) == 0 {
		return nil, nil, fmt.Errorf("config YAML is empty")
	}
	if builder == nil {
		return nil, nil, fmt.Errorf("builder is nil")
	}

	// Parse configuration.
	graphConfig, err := ParseGraphConfig(configYAML)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to parse configuration")
	}

	// Build graph.
	g, err := builder.Build(graphConfig)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to build graph")
	}

	// Create service.
	serviceConfig := &Config{
		RequestTimeout: 30 * time.Second,
		Tracer:         observability.NewLogTracer(nil),
	}

	service, err := NewService(serviceConfig)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to create service")
	}

	return service, g, nil
}

// BuildSimple creates a simple graph from YAML without agent registration.
func BuildSimple(configYAML []byte) (*wfgraph.Graph, error) {
	if len(configYAML) == 0 {
		return nil, fmt.Errorf("config YAML is empty")
	}

	graphConfig, err := ParseGraphConfig(configYAML)
	if err != nil {
		return nil, err
	}

	builder := NewGraphBuilder()
	return builder.Build(graphConfig)
}
