package workflow

import (
	"context"
	"fmt"
)

func (r *Runner) applyQueuedMutations(
	ctx context.Context,
	scope *ExecutionScope,
	scheduler *Scheduler,
	loopIteration int,
) (*Scheduler, bool, error) {
	if r.patchQueue == nil {
		return scheduler, false, nil
	}
	mutations := r.patchQueue.Pending(scope.ExecutionID)
	if len(mutations) == 0 {
		return scheduler, false, nil
	}
	if err := validateMutationSafety(scope, mutations); err != nil {
		return scheduler, false, err
	}
	candidate, err := applyMutations(scope.Spec, mutations)
	if err != nil {
		return scheduler, false, err
	}
	migrated, err := migrateScheduler(scheduler, candidate)
	if err != nil {
		return scheduler, false, fmt.Errorf("migrate scheduler after mutations: %w", err)
	}
	ids := mutationIDs(mutations)
	if err := r.commitMutationCheckpoint(ctx, scope, candidate, migrated, loopIteration, ids); err != nil {
		return scheduler, false, err
	}
	return migrated, true, nil
}

func validateMutationSafety(scope *ExecutionScope, mutations []Mutation) error {
	seen := make(map[string]bool, len(mutations))
	for _, mutation := range mutations {
		if seen[mutation.ID] {
			return fmt.Errorf("mutation %q is duplicated in one safe-point batch", mutation.ID)
		}
		seen[mutation.ID] = true
		switch mutation.Type {
		case MutationRemoveNode, MutationReplaceNode, MutationUpdatePolicy:
			id := mutation.NodeID
			if mutation.Policy != nil {
				id = mutation.Policy.NodeID
			}
			if scope.IsCompleted(id) || scope.NodeStatus(id) == NodeStatusRunning {
				return fmt.Errorf("mutation %q targets already executed node %q", mutation.ID, id)
			}
		case MutationAddEdge, MutationRemoveEdge:
			if mutation.Edge == nil {
				continue
			}
			if scope.IsCompleted(mutation.Edge.From) || scope.IsCompleted(mutation.Edge.To) {
				return fmt.Errorf("mutation %q changes edge adjacent to executed node", mutation.ID)
			}
		}
	}
	return nil
}

func migrateScheduler(current *Scheduler, candidate *WorkflowSpec) (*Scheduler, error) {
	next, err := NewScheduler(candidate, current.strategy)
	if err != nil {
		return nil, err
	}
	oldEdges := make(map[string]edgeActivation, len(current.spec.Edges))
	for index, edge := range current.spec.Edges {
		oldEdges[schedulerEdgeKey(edge)] = current.edgeStates[index]
	}
	for index, edge := range candidate.Edges {
		if state, exists := oldEdges[schedulerEdgeKey(edge)]; exists {
			next.edgeStates[index] = state
		}
	}
	next.completed = make(map[NodeID]bool)
	for id, completed := range current.completed {
		if completed && next.hasNode(id) {
			next.completed[id] = true
		}
	}
	next.readyQueue = nil
	next.readySet = make(map[NodeID]bool)
	for _, id := range current.readyQueue {
		if next.hasNode(id) && !next.completed[id] {
			next.readyQueue = append(next.readyQueue, id)
			if next.joinKind(id) != Merge {
				next.readySet[id] = true
			}
		}
	}
	next.pending = filterExistingNodes(current.pending, next)
	next.branchSkipped = filterNodeBoolMap(current.branchSkipped, next)
	next.activeBranches = make(map[branchGroupKey]bool)
	for key, active := range current.activeBranches {
		if next.hasNode(key.from) {
			next.activeBranches[key] = active
		}
	}
	for _, node := range candidate.Nodes {
		if next.completed[node.ID] || next.readySet[node.ID] {
			continue
		}
		if len(next.incoming[node.ID]) == 0 && candidateEntry(candidate, node.ID) {
			next.enqueue(node.ID)
			continue
		}
		next.evaluateTarget(node.ID)
	}
	return next, nil
}

func schedulerEdgeKey(edge EdgeSpec) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s", edge.From, edge.To, edge.Kind, edge.Group)
}

func candidateEntry(spec *WorkflowSpec, id NodeID) bool {
	if len(spec.Entries) == 0 {
		return true
	}
	for _, entry := range spec.Entries {
		if entry == id {
			return true
		}
	}
	return false
}

func filterExistingNodes(ids []NodeID, scheduler *Scheduler) []NodeID {
	result := make([]NodeID, 0, len(ids))
	for _, id := range ids {
		if scheduler.hasNode(id) {
			result = append(result, id)
		}
	}
	return result
}

func filterNodeBoolMap(source map[NodeID]bool, scheduler *Scheduler) map[NodeID]bool {
	result := make(map[NodeID]bool)
	for id, value := range source {
		if scheduler.hasNode(id) {
			result[id] = value
		}
	}
	return result
}

func mutationIDs(mutations []Mutation) []string {
	ids := make([]string, len(mutations))
	for index := range mutations {
		ids[index] = mutations[index].ID
	}
	return ids
}
