// Package graph provides YAML configuration parsing for graph workflows.
package graph

import (
	"fmt"
	"time"

	"goagentx/internal/errors"

	"gopkg.in/yaml.v3"
)

// GraphConfig represents the complete graph configuration from YAML
type GraphConfig struct {
	Graph GraphDefinition `yaml:"graph"`
}

// GraphDefinition defines a graph structure
type GraphDefinition struct {
	ID        string  `yaml:"id"`
	StartNode string  `yaml:"start_node"`
	Nodes     []Node  `yaml:"nodes"`
	Edges     []Edge  `yaml:"edges"`
	Agents    []Agent `yaml:"agents,omitempty"`
}

// Node defines a graph node
type Node struct {
	ID          string                 `yaml:"id"`
	Type        string                 `yaml:"type"` // function, agent, tool
	Description string                 `yaml:"description,omitempty"`
	Config      map[string]interface{} `yaml:"config,omitempty"`
}

// Edge defines a connection between nodes
type Edge struct {
	From      string `yaml:"from"`
	To        string `yaml:"to"`
	Condition string `yaml:"condition,omitempty"` // expression or condition ID
}

// Agent defines an agent configuration
type Agent struct {
	ID     string                 `yaml:"id"`
	Type   string                 `yaml:"type"`
	Name   string                 `yaml:"name"`
	Config map[string]interface{} `yaml:"config,omitempty"`
}

// ServiceConfig represents service-level configuration
type ServiceConfig struct {
	RequestTimeout time.Duration `yaml:"request_timeout"`
	TracerType     string        `yaml:"tracer_type"`
	LogLevel       string        `yaml:"log_level"`
	RateLimit      *RateLimit    `yaml:"rate_limit,omitempty"`
}

// RateLimit defines rate limiting configuration
type RateLimit struct {
	Enabled bool           `yaml:"enabled"`
	Type    string         `yaml:"type"` // token_bucket
	Config  map[string]any `yaml:"config,omitempty"`
}

// ParseGraphConfig parses a YAML configuration file
func ParseGraphConfig(data []byte) (*GraphConfig, error) {
	var config GraphConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, errors.Wrap(err, "failed to parse YAML")
	}

	// Validate configuration
	if err := validateGraphConfig(&config); err != nil {
		return nil, errors.Wrap(err, "invalid configuration")
	}

	return &config, nil
}

// validateGraphConfig validates the graph configuration
func validateGraphConfig(config *GraphConfig) error {
	g := &config.Graph

	// Check required fields
	if g.ID == "" {
		return fmt.Errorf("graph ID is required")
	}

	if g.StartNode == "" {
		return fmt.Errorf("start node is required")
	}

	if len(g.Nodes) == 0 {
		return fmt.Errorf("at least one node is required")
	}

	// Check node uniqueness
	nodeIDs := make(map[string]bool)
	for _, node := range g.Nodes {
		if node.ID == "" {
			return fmt.Errorf("node ID is required")
		}
		if nodeIDs[node.ID] {
			return fmt.Errorf("duplicate node ID: %s", node.ID)
		}
		nodeIDs[node.ID] = true

		// Validate node type
		if node.Type != "function" && node.Type != "agent" && node.Type != "tool" {
			return fmt.Errorf("invalid node type '%s' for node '%s', must be one of: function, agent, tool", node.Type, node.ID)
		}
	}

	// Validate start node exists
	if !nodeIDs[g.StartNode] {
		return fmt.Errorf("start node '%s' does not exist", g.StartNode)
	}

	// Validate edges
	for _, edge := range g.Edges {
		if edge.From == "" {
			return fmt.Errorf("edge source node is required")
		}
		if edge.To == "" {
			return fmt.Errorf("edge target node is required")
		}
		if !nodeIDs[edge.From] {
			return fmt.Errorf("edge source node '%s' does not exist", edge.From)
		}
		if !nodeIDs[edge.To] {
			return fmt.Errorf("edge target node '%s' does not exist", edge.To)
		}
	}

	return nil
}

// GetNodeByID retrieves a node by its ID
func (g *GraphDefinition) GetNodeByID(id string) (*Node, bool) {
	for i := range g.Nodes {
		if g.Nodes[i].ID == id {
			return &g.Nodes[i], true
		}
	}
	return nil, false
}

// GetAgentByID retrieves an agent by its ID
func (g *GraphDefinition) GetAgentByID(id string) (*Agent, bool) {
	for i := range g.Agents {
		if g.Agents[i].ID == id {
			return &g.Agents[i], true
		}
	}
	return nil, false
}
