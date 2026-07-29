// Package workflow — WorkflowSpec validation.
//
// Phase: P1 — IR definition + Compiler + Validator.
// The Validator catches structural errors early (at Build/Compile time)
// rather than at execution time, fulfilling the §8 usability goal.

package workflow

import (
	"fmt"
)

// ValidationError describes a single validation failure within a WorkflowSpec.
type ValidationError struct {
	// NodeID identifies the node that caused the error (empty for global errors).
	NodeID NodeID `json:"node_id,omitempty"`
	// Field identifies the specific field that failed validation.
	Field string `json:"field,omitempty"`
	// Message describes the error in human-readable terms.
	Message string `json:"message"`
}

// Error returns a formatted validation error string.
func (ve *ValidationError) Error() string {
	if ve.NodeID != "" {
		return fmt.Sprintf("[%s] %s: %s", ve.NodeID, ve.Field, ve.Message)
	}
	return fmt.Sprintf("%s: %s", ve.Field, ve.Message)
}

// ValidationReport contains all errors and warnings found during validation.
type ValidationReport struct {
	Errors   []ValidationError `json:"errors"`
	Warnings []ValidationError `json:"warnings,omitempty"`
}

// Valid returns true when no errors were found.
func (r *ValidationReport) Valid() bool {
	return len(r.Errors) == 0
}

// Error returns a summary string of all validation errors.
func (r *ValidationReport) Error() string {
	if r.Valid() {
		return "no validation errors"
	}
	return fmt.Sprintf("%d validation error(s)", len(r.Errors))
}

// ──────────────────────────────────────────────────────────────────────
// Validator
// ──────────────────────────────────────────────────────────────────────

// Validate checks a WorkflowSpec for structural and semantic errors.
// It is designed to be called after CompileFromEngine/CompileFromGraph
// or after Builder.Build(), before the spec is passed to a Runner.
func Validate(spec *WorkflowSpec) *ValidationReport {
	report := &ValidationReport{}

	if spec == nil {
		report.Errors = append(report.Errors, ValidationError{
			Field:   "spec",
			Message: "workflow spec must not be nil",
		})
		return report
	}
	if spec.ID == "" {
		report.Errors = append(report.Errors, ValidationError{
			Field:   "id",
			Message: "workflow ID must not be empty",
		})
	}

	validateNodeIDs(spec, report)
	validateEdgeTargets(spec, report)
	validateCycle(spec, report)
	validateEntries(spec, report)
	validateBranchOne(spec, report)
	validateJoinPolicy(spec, report)
	validateLoop(spec, report)

	return report
}

// ──────────────────────────────────────────────────────────────────────
// Validation checks
// ──────────────────────────────────────────────────────────────────────

// validateNodeIDs checks for duplicate or empty node IDs.
func validateNodeIDs(spec *WorkflowSpec, r *ValidationReport) {
	seen := make(map[NodeID]bool)
	for _, n := range spec.Nodes {
		if n.ID == "" {
			r.Errors = append(r.Errors, ValidationError{
				Field:   "nodes[].id",
				Message: "node ID must not be empty",
			})
			continue
		}
		if seen[n.ID] {
			r.Errors = append(r.Errors, ValidationError{
				NodeID:  n.ID,
				Field:   "id",
				Message: "duplicate node ID",
			})
		}
		seen[n.ID] = true
	}
}

// validateEdgeTargets checks for edges referencing non-existent or empty node IDs.
func validateEdgeTargets(spec *WorkflowSpec, r *ValidationReport) {
	nodeIndex := makeNodeIndex(spec.Nodes)
	for _, e := range spec.Edges {
		if e.From == "" {
			r.Errors = append(r.Errors, ValidationError{
				Field:   "edges[].from",
				Message: "edge 'from' must not be empty",
			})
		}
		if e.To == "" {
			r.Errors = append(r.Errors, ValidationError{
				Field:   "edges[].to",
				Message: "edge 'to' must not be empty",
			})
		}
		if e.From != "" && !nodeIndex[e.From] {
			r.Errors = append(r.Errors, ValidationError{
				NodeID:  e.From,
				Field:   "edges[].from",
				Message: fmt.Sprintf("edge references non-existent source node %q", e.From),
			})
		}
		if e.To != "" && !nodeIndex[e.To] {
			r.Errors = append(r.Errors, ValidationError{
				NodeID:  e.To,
				Field:   "edges[].to",
				Message: fmt.Sprintf("edge references non-existent target node %q", e.To),
			})
		}
	}
}

// validateCycle checks for cycles in the edge graph using DFS.
// A cycle makes the workflow non-executable.
func validateCycle(spec *WorkflowSpec, r *ValidationReport) {
	adjList := buildAdjacencyList(spec.Edges)

	// Build set of all node IDs referenced in edges
	seen := makeNodeIndex(spec.Nodes)
	for _, e := range spec.Edges {
		seen[e.From] = true
		seen[e.To] = true
	}

	visited := make(map[NodeID]bool)
	recStack := make(map[NodeID]bool)

	var dfs func(id NodeID) bool
	dfs = func(id NodeID) bool {
		visited[id] = true
		recStack[id] = true

		for _, neighbor := range adjList[id] {
			if !visited[neighbor] {
				if dfs(neighbor) {
					return true
				}
			} else if recStack[neighbor] {
				return true
			}
		}

		recStack[id] = false
		return false
	}

	for id := range seen {
		if !visited[id] {
			if dfs(id) {
				r.Errors = append(r.Errors, ValidationError{
					NodeID:  id,
					Field:   "edges",
					Message: "cycle detected in workflow graph",
				})
				return // one cycle error is sufficient
			}
		}
	}
}

// validateEntries checks that every node is reachable from at least one entry.
func validateEntries(spec *WorkflowSpec, r *ValidationReport) {
	if len(spec.Entries) == 0 {
		// No entries specified: all zero-in-degree nodes act as entries.
		// This is the legacy behaviour; it is valid but may produce unexpected
		// results (see §2.4). Emit a warning rather than an error.
		r.Warnings = append(r.Warnings, ValidationError{
			Field:   "entries",
			Message: "no explicit entry nodes; using all zero-in-degree nodes as entries (legacy behaviour)",
		})
		return
	}

	// Verify that entry nodes actually exist
	nodeIndex := makeNodeIndex(spec.Nodes)
	for _, e := range spec.Entries {
		if !nodeIndex[e] {
			r.Errors = append(r.Errors, ValidationError{
				NodeID:  e,
				Field:   "entries",
				Message: fmt.Sprintf("entry node %q not found in nodes", e),
			})
		}
	}

	// Compute reachable set from entries
	adjList := buildAdjacencyList(spec.Edges)
	reachable := make(map[NodeID]bool)
	bfs := func(ids []NodeID) {
		queue := ids
		for len(queue) > 0 {
			curr := queue[0]
			queue = queue[1:]
			if reachable[curr] {
				continue
			}
			reachable[curr] = true
			queue = append(queue, adjList[curr]...)
		}
	}
	bfs(spec.Entries)

	// Warn about nodes not reachable from any entry
	for _, n := range spec.Nodes {
		if !reachable[n.ID] {
			r.Warnings = append(r.Warnings, ValidationError{
				NodeID:  n.ID,
				Field:   "reachability",
				Message: "node is not reachable from any entry point; will not execute",
			})
		}
	}
}

// validateBranchOne checks that all BranchOne groups have non-overlapping
// conditions and at most one unconditional edge (fallback).
func validateBranchOne(spec *WorkflowSpec, r *ValidationReport) {
	// Group edges by (from, group) for BranchOne analysis
	type branchGroupKey struct {
		from  NodeID
		group string
	}
	groups := make(map[branchGroupKey][]EdgeSpec)

	for _, e := range spec.Edges {
		if e.Branch == BranchOne {
			key := branchGroupKey{from: e.From, group: e.Group}
			groups[key] = append(groups[key], e)
		}
	}

	for key, edges := range groups {
		_ = edges // the actual condition overlap check requires condition evaluation,
		// which is not possible at IR level since conditions may be closures.
		// For P1, we check structural constraints only.

		if len(edges) < 2 {
			r.Warnings = append(r.Warnings, ValidationError{
				NodeID:  key.from,
				Field:   fmt.Sprintf("branch_one[group=%q]", key.group),
				Message: "BranchOne with fewer than 2 outgoing edges is redundant; use an unconditional edge",
			})
		}

		// Count unconditional edges
		unconditionalCount := 0
		for _, e := range edges {
			if e.Cond == nil {
				unconditionalCount++
			}
		}
		if unconditionalCount > 1 {
			r.Errors = append(r.Errors, ValidationError{
				NodeID:  key.from,
				Field:   fmt.Sprintf("branch_one[group=%q]", key.group),
				Message: fmt.Sprintf("BranchOne group has %d unconditional edges; at most one fallback allowed", unconditionalCount),
			})
		}
	}
}

// validateJoinPolicy checks that nodes with multiple incoming edges have an
// explicit Join policy set (rather than relying on the default AND-join).
func validateJoinPolicy(spec *WorkflowSpec, r *ValidationReport) {
	inDegree := make(map[NodeID]int)
	for _, e := range spec.Edges {
		if e.Kind == EdgeDataDependency {
			inDegree[e.To]++
		}
	}

	joinIndex := make(map[NodeID]JoinKind)
	for _, n := range spec.Nodes {
		if n.Join != "" {
			joinIndex[n.ID] = n.Join
		}
	}

	for _, n := range spec.Nodes {
		deg := inDegree[n.ID]
		if deg > 1 {
			if _, ok := joinIndex[n.ID]; !ok {
				r.Warnings = append(r.Warnings, ValidationError{
					NodeID:  n.ID,
					Field:   validationFieldJoin,
					Message: fmt.Sprintf("node has %d incoming data-dependency edges but no explicit Join policy; defaulting to JoinAll", deg),
				})
			}
		}
	}
}

// validateLoop checks loop configuration for validity.
func validateLoop(spec *WorkflowSpec, r *ValidationReport) {
	if spec.Loop == nil {
		return
	}
	if spec.Loop.MaxIterations <= 0 && spec.Loop.MaxIterations != 0 {
		r.Errors = append(r.Errors, ValidationError{
			Field:   "loop.max_iterations",
			Message: "loop MaxIterations must be > 0; 0 means run once (no loop)",
		})
	}

	nodeIndex := makeNodeIndex(spec.Nodes)
	for _, ln := range spec.Loop.LoopNodes {
		if !nodeIndex[ln] {
			r.Errors = append(r.Errors, ValidationError{
				NodeID:  ln,
				Field:   "loop.loop_nodes",
				Message: fmt.Sprintf("loop node %q not found in workflow nodes", ln),
			})
		}
	}
	if len(spec.Loop.LoopNodes) == 0 {
		r.Errors = append(r.Errors, ValidationError{
			Field:   "loop.loop_nodes",
			Message: "loop must have at least one loop node",
		})
	}
}

// ──────────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────────

// makeNodeIndex builds a set of all node IDs for O(1) lookup.
func makeNodeIndex(nodes []NodeSpec) map[NodeID]bool {
	idx := make(map[NodeID]bool, len(nodes))
	for _, n := range nodes {
		idx[n.ID] = true
	}
	return idx
}

// buildAdjacencyList builds a forward adjacency list from edges.
func buildAdjacencyList(edges []EdgeSpec) map[NodeID][]NodeID {
	adj := make(map[NodeID][]NodeID)
	for _, e := range edges {
		adj[e.From] = append(adj[e.From], e.To)
	}
	return adj
}
