package taskfabric

import "time"

// EventType enumerates the task lifecycle events (docs/zh/architecture/ares-runtime.md).
// The event log is the single source of truth: Scheduler / Task / Lease state
// can be fully rebuilt from it.
type EventType string

const (
	EventTaskCreated      EventType = "task.created"
	EventTaskReady        EventType = "task.ready"
	EventTaskAcquired     EventType = "task.acquired"
	EventTaskStarted      EventType = "task.started"
	EventTaskYielded      EventType = "task.yielded"
	EventTaskCheckpointed EventType = "task.checkpointed"
	EventTaskPreempted    EventType = "task.preempted"
	EventTaskReleased     EventType = "task.released"
	EventTaskCompleted    EventType = "task.completed"
	EventTaskFailed       EventType = "task.failed"
	EventTaskExpired      EventType = "task.expired"
	// EventTaskDeleted is the tombstone for Delete: the task was removed and
	// must stay removed across a restart.
	//
	// It is must-persist because the durable log already holds the task's
	// task.created — without a tombstone, RestoreFromStore folds that created
	// event back and the discarded work becomes READY again and re-executes.
	// It is deliberately NOT emitted by RestoreTask (the delete-then-restore
	// rollback primitive): re-installing a task from its in-memory snapshot
	// needs no durable record, and a recompile that outlives the rollback is
	// recovered by the graph reconcile (the live DAG is the source of truth).
	//
	// TODO(tech-debt): a task that is deleted and then reinstalled via
	// RestoreTask has no durable trace of the reinstall, so a restart in that
	// window drops it and relies on the DAG reconcile to re-create it. Emitting
	// a task.created on RestoreTask would close that window at the cost of
	// publishing a "created" event for a task that never had a create call.
	EventTaskDeleted EventType = "task.deleted"
	// EventTaskUpdated records an in-place rewrite of a task's scheduling
	// shape (Dependencies) or its payload by the incremental compiler: one
	// graph change moves one task instead of rebuilding the whole compiled
	// batch.
	//
	// It is observability-only BY DESIGN — absent from isMustPersistEvent
	// and unmapped in taskEventType, so nothing is written to the durable
	// store: after a restart the topology is rebuilt by re-compiling the
	// live DAG, not by folding these rewrites. Adding it to the
	// cross-restart protocol would be a protocol change, not a compiler
	// change, and belongs in its own review.
	EventTaskUpdated EventType = "task.updated"
)

// TaskEvent is one immutable lifecycle record. Replaying the log in order
// rebuilds the full task state (Evidence-Driven Autonomous Runtime).
type TaskEvent struct {
	Type    EventType
	TaskID  string
	AgentID string
	// Origin is the creating agent's ID ("" = root task). Captured on
	// task.created so provenance is auditable from the event log.
	Origin     string
	State      TaskState
	Checkpoint any
	At         time.Time
}
