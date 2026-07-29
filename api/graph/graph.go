// Package graph provides the public API for dynamic agent orchestration
// with pluggable scheduling, and the unified Runner for DAG execution.
//
// This package re-exports types from:
//   - internal/workflow/graph (legacy graph builder — keep for migration)
//   - internal/workflow (unified Runner — new production path)
//
// External callers should prefer the unified types below for new code.
package graph

import (
	"github.com/Timwood0x10/ares/internal/workflow"
	"github.com/Timwood0x10/ares/internal/workflow/graph"
)

// ── Legacy graph builder types (internal/workflow/graph) ─────────────

// State represents the shared runtime state for graph execution.
type State = graph.State

// Condition defines a predicate function for edge traversal.
type Condition = graph.Condition

// NodeRouter is a callback for dynamic routing decisions during graph
// execution. After a node completes, the router is called with the
// just-executed node ID and current state. If it returns a non-empty
// node ID, that node is enqueued for execution next (bypassing the
// DAG's static edge traversal). Return "" to let the DAG decide the
// next node via in-degree BFS as usual.
type NodeRouter = graph.NodeRouter

// Scheduler defines the interface for node scheduling.
type Scheduler = graph.Scheduler

// DefaultScheduler provides FIFO scheduling.
type DefaultScheduler = graph.DefaultScheduler

// PriorityScheduler provides priority-based scheduling.
type PriorityScheduler = graph.PriorityScheduler

// ShortJobScheduler provides shortest-job-first scheduling.
type ShortJobScheduler = graph.ShortJobScheduler

// RoundRobinScheduler cycles through ready nodes in order.
type RoundRobinScheduler = graph.RoundRobinScheduler

// WeightedFairScheduler distributes execution proportionally.
type WeightedFairScheduler = graph.WeightedFairScheduler

// Edge represents a connection between two nodes with optional condition.
type Edge = graph.Edge

// Result represents the outcome of a graph execution.
type Result = graph.Result

// Node represents an executable unit in the graph.
type Node = graph.Node

// Graph represents a DAG of nodes with conditional edges.
type Graph = graph.Graph

// NewState creates a new empty state instance.
var NewState = graph.NewState

// NewGraph creates a new graph with the given ID.
var NewGraph = graph.NewGraph

// NewDefaultScheduler creates a new default (FIFO) scheduler.
var NewDefaultScheduler = graph.NewDefaultScheduler

// NewPriorityScheduler creates a new priority scheduler.
var NewPriorityScheduler = graph.NewPriorityScheduler

// NewShortJobScheduler creates a new short-job scheduler.
var NewShortJobScheduler = graph.NewShortJobScheduler

// NewRoundRobinScheduler creates a new round-robin scheduler.
var NewRoundRobinScheduler = graph.NewRoundRobinScheduler

// NewWeightedFairScheduler creates a weighted fair scheduler.
var NewWeightedFairScheduler = graph.NewWeightedFairScheduler

// IfFunc creates a condition from a function.
var IfFunc = graph.IfFunc

// ── Unified Runner types (internal/workflow) ─────────────────────────

// WorkflowSpec is the unified intermediate representation for a workflow.
type WorkflowSpec = workflow.WorkflowSpec

// NodeSpec defines a single node in a WorkflowSpec.
type NodeSpec = workflow.NodeSpec

// EdgeSpec defines a directed edge between two nodes.
type EdgeSpec = workflow.EdgeSpec

// ConditionExpr is a serializable condition expression.
type ConditionExpr = workflow.ConditionExpr

// NodeID is a unique identifier for a workflow node.
type NodeID = workflow.NodeID

// NodeStatus represents the execution status of a workflow node.
type NodeStatus = workflow.NodeStatus

// EdgeKind classifies the type of relationship between two nodes.
type EdgeKind = workflow.EdgeKind

// BranchKind classifies how outgoing edges are evaluated.
type BranchKind = workflow.BranchKind

// JoinKind classifies how multi-incoming-edge nodes are activated.
type JoinKind = workflow.JoinKind

// LoopSpec defines controlled loop behaviour for a workflow.
type LoopSpec = workflow.LoopSpec

// ScheduleSpec defines execution constraints.
type ScheduleSpec = workflow.ScheduleSpec

// RetrySpec defines retry behaviour on transient failures.
type RetrySpec = workflow.RetrySpec

// RecoverySpec defines recovery behaviour on hard failures.
type RecoverySpec = workflow.RecoverySpec

// InterruptSpec marks a node as requiring human approval.
type InterruptSpec = workflow.InterruptSpec

// ScheduleStrategy selects the order of ready node execution.
type ScheduleStrategy = workflow.ScheduleStrategy

// NodeStatusValue tracks the runtime execution status of a single node.
type NodeStatusValue = workflow.NodeStatusValue

// NodeExecutor resolves and executes a single node.
type NodeExecutor = workflow.NodeExecutor

// Runner is the unified single execution engine.
type Runner = workflow.Runner

// NewRunner creates a new Runner.
var NewRunner = workflow.NewRunner

// RunWorkflow is the simplest entry point for workflow execution.
var RunWorkflow = workflow.RunWorkflow

// NewWorkflow creates a new workflow spec builder.
var NewWorkflow = workflow.NewWorkflow

// NewFuncNodeExecutor creates an executor from a function map.
var NewFuncNodeExecutor = workflow.NewFuncNodeExecutor

// Validate checks a WorkflowSpec for structural errors.
var Validate = workflow.Validate

// TopologicalSort returns nodes in topological order.
var TopologicalSort = workflow.TopologicalSort

// CompileFromEngine compiles an engine.Workflow and preserves runtime bindings.
var CompileFromEngine = workflow.CompileFromEngineWithBindings

// CompileFromGraph compiles executable graph bindings for the unified Runner.
var CompileFromGraph = graph.CompileBound

// ScheduleStrategy constants.
const (
	ScheduleFIFO     = workflow.ScheduleFIFO
	SchedulePriority = workflow.SchedulePriority
)

// NodeStatus constants.
const (
	NodeStatusPending     = workflow.NodeStatusPending
	NodeStatusReady       = workflow.NodeStatusReady
	NodeStatusRunning     = workflow.NodeStatusRunning
	NodeStatusCompleted   = workflow.NodeStatusCompleted
	NodeStatusFailed      = workflow.NodeStatusFailed
	NodeStatusInterrupted = workflow.NodeStatusInterrupted
	NodeStatusCancelled   = workflow.NodeStatusCancelled
	NodeStatusNotSelected = workflow.NodeStatusNotSelected
	NodeStatusUnreachable = workflow.NodeStatusUnreachable
	NodeStatusBlocked     = workflow.NodeStatusBlocked
)

// EdgeKind constants.
const (
	EdgeDataDependency = workflow.EdgeDataDependency
	EdgeControlFlow    = workflow.EdgeControlFlow
)

// BranchKind constants.
const (
	BranchOne  = workflow.BranchOne
	BranchMany = workflow.BranchMany
)

// JoinKind constants.
const (
	JoinAll = workflow.JoinAll
	JoinAny = workflow.JoinAny
	Merge   = workflow.Merge
)
