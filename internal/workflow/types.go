// Package workflow defines the unified types and IR for DAG-based workflow
// execution. It serves as the single source of truth for node status, edge
// kinds, branching strategies, and join policies — shared by all execution
// runtimes (engine.Executor, engine.DynamicExecutor, graph.Graph).
//
// Phase: P0 — semantic contract freezing.
// All types defined here are frozen during P0 and serve as the foundation
// for conformance tests and the future single Runner.
package workflow

// NodeStatus represents the execution status of a workflow node.
type NodeStatus string

const (
	// NodeStatusPending indicates the node has not been evaluated yet.
	NodeStatusPending NodeStatus = "pending"
	// NodeStatusReady indicates all dependencies are satisfied; the node is
	// eligible for scheduling.
	NodeStatusReady NodeStatus = "ready"
	// NodeStatusRunning indicates the node is currently executing.
	NodeStatusRunning NodeStatus = "running"
	// NodeStatusCompleted indicates the node executed successfully.
	NodeStatusCompleted NodeStatus = "completed"
	// NodeStatusFailed indicates the node execution failed and was not recovered.
	NodeStatusFailed NodeStatus = "failed"
	// NodeStatusInterrupted indicates the node is paused waiting for human
	// approval (HITL).
	NodeStatusInterrupted NodeStatus = "interrupted"
	// NodeStatusCancelled indicates the node was cancelled by user or context.
	NodeStatusCancelled NodeStatus = "cancelled"

	// --- P1 extensions (declared here for forward compatibility) ---

	// NodeStatusNotSelected indicates the node was skipped because a branch
	// chose a different path.
	NodeStatusNotSelected NodeStatus = "not_selected"
	// NodeStatusUnreachable indicates all control-flow paths to this node
	// are blocked by unsatisfied conditions; the node will never execute.
	NodeStatusUnreachable NodeStatus = "unreachable"
	// NodeStatusBlocked indicates a required upstream node failed, making
	// this node unable to proceed.
	NodeStatusBlocked NodeStatus = "blocked"
)

// EdgeKind classifies the type of relationship between two nodes.
type EdgeKind string

const (
	// EdgeDataDependency indicates the target node requires the source node's
	// output as input. A data-dependency edge always implies execution order:
	// the source must complete before the target can start.
	EdgeDataDependency EdgeKind = "data_dependency"
	// EdgeControlFlow indicates the source node's execution result determines
	// whether the target node should be activated. Control-flow edges do not
	// carry data; they only affect reachability.
	EdgeControlFlow EdgeKind = "control_flow"
)

// BranchKind classifies how a node's outgoing control-flow edges are evaluated.
type BranchKind string

const (
	// BranchOne indicates exactly one outgoing control-flow edge must be
	// selected. If multiple conditions match, it is a validation error.
	// If no condition matches and no Otherwise fallback exists, it is an error.
	BranchOne BranchKind = "branch_one"
	// BranchMany indicates zero or more outgoing control-flow edges may be
	// activated independently. Each edge whose condition evaluates to true
	// activates its target node.
	BranchMany BranchKind = "branch_many"
)

// JoinKind classifies how a node with multiple incoming edges is activated.
type JoinKind string

const (
	// JoinAll indicates the node must wait for ALL activated predecessors
	// to complete before it becomes ready. This is the classic AND-join.
	JoinAll JoinKind = "join_all"
	// JoinAny indicates the node becomes ready when ANY activated predecessor
	// completes. The first completion triggers execution; subsequent completions
	// are ignored.
	JoinAny JoinKind = "join_any"
	// Merge indicates the node is triggered each time an activated predecessor
	// completes, allowing the same node to execute multiple times.
	Merge JoinKind = "merge"
)
