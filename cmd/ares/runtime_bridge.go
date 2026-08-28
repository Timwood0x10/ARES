package main

import (
	"context"
	"log"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/Timwood0x10/ares/internal/kernelscheduler"
)

// pluginBusHook adapts ares_runtime.PluginBus to the kernelscheduler.QuantumHook
// contract, so the runtime plugin ecosystem (observer/checkpoint/tool/...)
// participates in the Agent OS scheduling loop without the kernel depending on
// the runtime package (the adapter lives in the cmd assembly layer — the only
// place allowed to import both).
//
// Mapping: the bus speaks workflow Step/StepResult; a scheduling quantum is
// projected as a single-step workflow whose ID is the fabric task id.
type pluginBusHook struct {
	bus *ares_runtime.PluginBus
}

// newPluginBusHook wraps a started PluginBus as a scheduler QuantumHook.
func newPluginBusHook(bus *ares_runtime.PluginBus) *pluginBusHook {
	return &pluginBusHook{bus: bus}
}

// BeforeQuantum implements kernelscheduler.QuantumHook: projects the quantum
// onto the bus as a before-step hook invocation.
func (h *pluginBusHook) BeforeQuantum(ctx context.Context, taskID, agentID string) error {
	return h.bus.BeforeStep(ctx, taskID, &ares_runtime.Step{
		ID:        taskID,
		Name:      taskID,
		AgentType: agentID,
		Status:    ares_runtime.StepStatusRunning,
		StartedAt: time.Now(),
	})
}

// AfterQuantum implements kernelscheduler.QuantumHook: projects the quantum
// outcome onto the bus as an after-step hook invocation.
func (h *pluginBusHook) AfterQuantum(ctx context.Context, taskID, agentID string, qerr error) {
	res := &ares_runtime.StepResult{
		StepID:   taskID,
		Name:     taskID,
		Duration: 0,
		Metadata: map[string]string{"agent_id": agentID},
	}
	if qerr != nil {
		res.Status = ares_runtime.StepStatusFailed
		res.Error = qerr.Error()
	} else {
		res.Status = ares_runtime.StepStatusCompleted
	}
	_ = h.bus.AfterStep(ctx, taskID, res) // observational; bus already logs hook failures
}

// startPluginBus assembles the runtime plugin ecosystem and attaches it to the
// kernel scheduler's quantum boundary (Agent OS closure: the plugins observe
// every Schedule→Acquire→RunQuantum without the kernel importing the runtime).
//
// Args:
//
//	ctx   - lifetime of the serve process; cancelling stops the bus.
//	store - the shared event store the bus mirrors events into (may be nil).
//	sched - the kernel scheduler to hook; may be nil (no-op).
//
// Returns:
//
//	*ares_runtime.PluginBus - the started bus (nil when nothing to wire).
func startPluginBus(ctx context.Context, store ares_events.EventStore, sched *kernelscheduler.Scheduler) *ares_runtime.PluginBus {
	if sched == nil {
		return nil
	}
	bus := ares_runtime.NewPluginBus()
	_ = store // the bus subscribes via Subscribe(); store passthrough not needed
	if err := bus.Start(ctx); err != nil {
		log.Printf("peer mode: plugin bus start failed (scheduling continues without plugins): %v", err)
		return nil
	}
	sched.WithQuantumHook(newPluginBusHook(bus))
	log.Printf("peer mode: plugin bus wired to kernel quantum boundary")
	return bus
}
