package workflow

import (
	"encoding/json"
	"fmt"
)

// MutationType identifies one serializable topology or policy change.
type MutationType string

const (
	MutationAddNode      MutationType = "add_node"
	MutationRemoveNode   MutationType = "remove_node"
	MutationReplaceNode  MutationType = "replace_node"
	MutationAddEdge      MutationType = "add_edge"
	MutationRemoveEdge   MutationType = "remove_edge"
	MutationUpdatePolicy MutationType = "update_policy"
)

// PolicyMutation replaces optional execution policies on one existing node.
type PolicyMutation struct {
	NodeID    NodeID         `json:"node_id"`
	Retry     *RetrySpec     `json:"retry,omitempty"`
	Recovery  *RecoverySpec  `json:"recovery,omitempty"`
	Interrupt *InterruptSpec `json:"interrupt,omitempty"`
}

// Mutation is one stable, typed, serializable WorkflowSpec change.
type Mutation struct {
	ID     string          `json:"id"`
	Type   MutationType    `json:"type"`
	NodeID NodeID          `json:"node_id,omitempty"`
	Node   *NodeSpec       `json:"node,omitempty"`
	Entry  bool            `json:"entry,omitempty"`
	Edge   *EdgeSpec       `json:"edge,omitempty"`
	Policy *PolicyMutation `json:"policy,omitempty"`
	Reason string          `json:"reason,omitempty"`
}

func applyMutations(spec *WorkflowSpec, mutations []Mutation) (*WorkflowSpec, error) {
	candidate, err := cloneWorkflowSpec(spec)
	if err != nil {
		return nil, err
	}
	for _, mutation := range mutations {
		if err := applyMutation(candidate, mutation); err != nil {
			return nil, fmt.Errorf("apply mutation %q (%s): %w", mutation.ID, mutation.Type, err)
		}
	}
	if report := Validate(candidate); !report.Valid() {
		return nil, fmt.Errorf("mutated workflow %q validation failed: %v", candidate.ID, report.Errors)
	}
	return candidate, nil
}

func cloneWorkflowSpec(spec *WorkflowSpec) (*WorkflowSpec, error) {
	if spec == nil {
		return nil, fmt.Errorf("workflow spec must not be nil")
	}
	payload, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal workflow %q for mutation: %w", spec.ID, err)
	}
	var clone WorkflowSpec
	if err := json.Unmarshal(payload, &clone); err != nil {
		return nil, fmt.Errorf("unmarshal workflow %q for mutation: %w", spec.ID, err)
	}
	return &clone, nil
}

func applyMutation(spec *WorkflowSpec, mutation Mutation) error {
	if mutation.ID == "" {
		return fmt.Errorf("mutation ID must not be empty")
	}
	switch mutation.Type {
	case MutationAddNode:
		return addMutationNode(spec, mutation.Node, mutation.Entry)
	case MutationRemoveNode:
		return removeMutationNode(spec, mutation.NodeID)
	case MutationReplaceNode:
		return replaceMutationNode(spec, mutation.NodeID, mutation.Node)
	case MutationAddEdge:
		return addMutationEdge(spec, mutation.Edge)
	case MutationRemoveEdge:
		return removeMutationEdge(spec, mutation.Edge)
	case MutationUpdatePolicy:
		return updateMutationPolicy(spec, mutation.Policy)
	default:
		return fmt.Errorf("unsupported mutation type %q", mutation.Type)
	}
}

func addMutationNode(spec *WorkflowSpec, node *NodeSpec, entry bool) error {
	if node == nil || node.ID == "" {
		return fmt.Errorf("added node and ID must not be empty")
	}
	if _, exists := nodeIndex(spec, node.ID); exists {
		return fmt.Errorf("node %q already exists", node.ID)
	}
	spec.Nodes = append(spec.Nodes, *node)
	if entry {
		spec.Entries = append(spec.Entries, node.ID)
	}
	return nil
}

func removeMutationNode(spec *WorkflowSpec, id NodeID) error {
	index, exists := nodeIndex(spec, id)
	if !exists {
		return fmt.Errorf("node %q does not exist", id)
	}
	for _, edge := range spec.Edges {
		if edge.From == id || edge.To == id {
			return fmt.Errorf("node %q still has edge %q -> %q", id, edge.From, edge.To)
		}
	}
	spec.Nodes = append(spec.Nodes[:index], spec.Nodes[index+1:]...)
	for index := len(spec.Entries) - 1; index >= 0; index-- {
		if spec.Entries[index] == id {
			spec.Entries = append(spec.Entries[:index], spec.Entries[index+1:]...)
		}
	}
	return nil
}

func replaceMutationNode(spec *WorkflowSpec, id NodeID, node *NodeSpec) error {
	index, exists := nodeIndex(spec, id)
	if !exists {
		return fmt.Errorf("node %q does not exist", id)
	}
	if node == nil || node.ID == "" {
		return fmt.Errorf("replacement node and ID must not be empty")
	}
	if node.ID != id {
		if _, duplicate := nodeIndex(spec, node.ID); duplicate {
			return fmt.Errorf("replacement node %q already exists", node.ID)
		}
		for edgeIndex := range spec.Edges {
			if spec.Edges[edgeIndex].From == id {
				spec.Edges[edgeIndex].From = node.ID
			}
			if spec.Edges[edgeIndex].To == id {
				spec.Edges[edgeIndex].To = node.ID
			}
		}
		for entryIndex := range spec.Entries {
			if spec.Entries[entryIndex] == id {
				spec.Entries[entryIndex] = node.ID
			}
		}
	}
	spec.Nodes[index] = *node
	return nil
}

func addMutationEdge(spec *WorkflowSpec, edge *EdgeSpec) error {
	if edge == nil {
		return fmt.Errorf("added edge must not be nil")
	}
	if _, exists := nodeIndex(spec, edge.From); !exists {
		return fmt.Errorf("source node %q does not exist", edge.From)
	}
	if _, exists := nodeIndex(spec, edge.To); !exists {
		return fmt.Errorf("target node %q does not exist", edge.To)
	}
	if edgeIndex(spec, edge) >= 0 {
		return fmt.Errorf("edge %q -> %q already exists", edge.From, edge.To)
	}
	spec.Edges = append(spec.Edges, *edge)
	return nil
}

func removeMutationEdge(spec *WorkflowSpec, edge *EdgeSpec) error {
	if edge == nil {
		return fmt.Errorf("removed edge must not be nil")
	}
	index := edgeIndex(spec, edge)
	if index < 0 {
		return fmt.Errorf("edge %q -> %q does not exist", edge.From, edge.To)
	}
	spec.Edges = append(spec.Edges[:index], spec.Edges[index+1:]...)
	return nil
}

func updateMutationPolicy(spec *WorkflowSpec, policy *PolicyMutation) error {
	if policy == nil {
		return fmt.Errorf("policy mutation must not be nil")
	}
	index, exists := nodeIndex(spec, policy.NodeID)
	if !exists {
		return fmt.Errorf("node %q does not exist", policy.NodeID)
	}
	spec.Nodes[index].Retry = policy.Retry
	spec.Nodes[index].Recovery = policy.Recovery
	spec.Nodes[index].Interrupt = policy.Interrupt
	return nil
}

func nodeIndex(spec *WorkflowSpec, id NodeID) (int, bool) {
	for index := range spec.Nodes {
		if spec.Nodes[index].ID == id {
			return index, true
		}
	}
	return -1, false
}

func edgeIndex(spec *WorkflowSpec, target *EdgeSpec) int {
	for index := range spec.Edges {
		edge := spec.Edges[index]
		if edge.From == target.From && edge.To == target.To && edge.Kind == target.Kind && edge.Group == target.Group {
			return index
		}
	}
	return -1
}
