// Package engine ...
package engine

import (
	"context"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
)

type ApplyMode int

const (
	ApplyAtCheckpoint ApplyMode = iota
	ApplyImmediate
)

type ExecutorOption func(*Executor)

func WithMaxParallel(n int) ExecutorOption {
	return func(e *Executor) { e.maxParallel = n }
}

func WithStepTimeout(d time.Duration) ExecutorOption {
	return func(e *Executor) { e.stepTimeout = d }
}

func WithCheckpointStore(store ares_runtime.CheckpointStore) ExecutorOption {
	return func(e *Executor) { e.checkpointStore = store }
}

type DynamicExecutor struct {
	*Executor
	applyMode          ApplyMode
	hitlHandler        InterruptHandler
	hitlStore          InterruptStore
	recoveryHandler    StepRecoveryHandler
	recoveryEventSink  func(ctx context.Context, eventType ares_events.EventType, payload map[string]any)
	pluginBus          *ares_runtime.PluginBus
	checkpointStore    ares_runtime.CheckpointStore
	executionCollector *ares_runtime.ExecutionCollector
}

func NewDynamicExecutor(registry *AgentRegistry, applyMode ApplyMode, opts ...ExecutorOption) *DynamicExecutor {
	e := &Executor{
		registry:    registry,
		maxParallel: 1,
	}
	for _, opt := range opts {
		opt(e)
	}
	return &DynamicExecutor{Executor: e, applyMode: applyMode}
}

func (e *DynamicExecutor) WithHitlHandler(handler InterruptHandler) *DynamicExecutor {
	e.hitlHandler = handler
	return e
}
func (e *DynamicExecutor) WithHitlStore(store InterruptStore) *DynamicExecutor {
	e.hitlStore = store
	return e
}
func (e *DynamicExecutor) WithRecoveryHandler(handler StepRecoveryHandler) *DynamicExecutor {
	e.recoveryHandler = handler
	return e
}
func (e *DynamicExecutor) WithRecoveryEventSink(sink func(ctx context.Context, eventType ares_events.EventType, payload map[string]any)) *DynamicExecutor {
	e.recoveryEventSink = sink
	return e
}
func (e *DynamicExecutor) WithPluginBus(bus *ares_runtime.PluginBus) *DynamicExecutor {
	e.pluginBus = bus
	return e
}
func (e *DynamicExecutor) WithCheckpointStore(store ares_runtime.CheckpointStore) *DynamicExecutor {
	e.checkpointStore = store
	return e
}
func (e *DynamicExecutor) WithExecutionCollector(c *ares_runtime.ExecutionCollector) *DynamicExecutor {
	e.executionCollector = c
	return e
}
