// Package graph provides the public API for dynamic agent orchestration
// with pluggable scheduling.
//
// This package exposes the dynamic DAG (Graph/Node/Edge/State/Scheduler)
// to external modules. The internal implementation lives in
// internal/workflow/graph; this file re-exports its public contract
// via type aliases so external callers can build and execute dynamic
// graphs without importing internal packages.
//
// Key capabilities:
//   - Dynamic node/edge addition and removal at runtime
//   - Pluggable scheduling strategies (FIFO, Priority, ShortJob, etc.)
//   - Conditional edges with predicate functions
//   - Checkpoint-based resume for fault tolerance
//   - Dynamic routing via NodeRouter callback
package graph

import (
	"github.com/Timwood0x10/ares/internal/workflow/graph"
)

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
// External modules can implement this to provide custom scheduling.
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
