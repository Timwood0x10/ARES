package agentfabric

import "sync"

// ContextLayer identifies the three context tiers (design §13: Context three
// layers — do not share one brain).
type ContextLayer int

const (
	// ContextTaskShared is the shared task state (goal / constraints /
	// artifacts / decisions / dependencies / checkpoints). Objective; all
	// agents working on the task must see it.
	ContextTaskShared ContextLayer = iota
	// ContextAgentPrivate is the agent's private state (reasoning /
	// observations / hypotheses / scratchpad). Per-agent; NEVER leaks to
	// other agents or the task layer.
	ContextAgentPrivate
	// ContextIPC is the message channel between agents ("I found X" /
	// "help me verify Y" / "your conclusion conflicts with mine"). Handled
	// by the IPC pillar (P4); this layer is the storage surface.
	ContextIPC
)

// ContextView is a read-only snapshot of one agent's three-layer context.
// It is used to verify isolation (Private never bleeds into Task).
type ContextView struct {
	TaskShared map[string]any
	Private    map[string]any
}

// SetTaskContext replaces the agent's Task Shared State. Called by the
// Scheduler/Runtime when binding a Task to the agent. The agent receives a
// copy so it never mutates the caller's map.
func (f *Fabric) SetTaskContext(agentID string, taskCtx map[string]any) error {
	f.mu.Lock()
	a, ok := f.agents[agentID]
	if !ok {
		f.mu.Unlock()
		return ErrAgentNotFound
	}
	a.mu.Lock()
	a.taskContext = cloneMap(taskCtx)
	a.mu.Unlock()
	f.mu.Unlock()
	return nil
}

// TaskContext returns a copy of the agent's Task Shared State.
func (f *Fabric) TaskContext(agentID string) (map[string]any, error) {
	f.mu.Lock()
	a, ok := f.agents[agentID]
	if !ok {
		f.mu.Unlock()
		return nil, ErrAgentNotFound
	}
	a.mu.RLock()
	out := cloneMap(a.taskContext)
	a.mu.RUnlock()
	f.mu.Unlock()
	return out, nil
}

// SetPrivate stores a key in the agent's Private State (scratchpad). This
// layer NEVER leaks to the Task Shared State or to other agents (§13
// invariant #5 + #6).
func (f *Fabric) SetPrivate(agentID, key string, val any) error {
	f.mu.Lock()
	a, ok := f.agents[agentID]
	if !ok {
		f.mu.Unlock()
		return ErrAgentNotFound
	}
	a.mu.Lock()
	a.privateContext[key] = val
	a.mu.Unlock()
	f.mu.Unlock()
	return nil
}

// Private returns a value from the agent's Private State.
func (f *Fabric) Private(agentID, key string) (any, error) {
	f.mu.Lock()
	a, ok := f.agents[agentID]
	if !ok {
		f.mu.Unlock()
		return nil, ErrAgentNotFound
	}
	a.mu.RLock()
	val := a.privateContext[key]
	a.mu.RUnlock()
	f.mu.Unlock()
	return val, nil
}

// ContextView returns a snapshot of the agent's Task Shared + Private
// layers (IPC is P4). Used to verify isolation: Private must not appear in
// TaskShared.
func (f *Fabric) ContextView(agentID string) (ContextView, error) {
	f.mu.Lock()
	a, ok := f.agents[agentID]
	if !ok {
		f.mu.Unlock()
		return ContextView{}, ErrAgentNotFound
	}
	a.mu.RLock()
	v := ContextView{
		TaskShared: cloneMap(a.taskContext),
		Private:    cloneMap(a.privateContext),
	}
	a.mu.RUnlock()
	f.mu.Unlock()
	return v, nil
}

// CognitiveState returns a copy of the agent's cognitive state.
func (f *Fabric) CognitiveState(agentID string) (CognitiveState, error) {
	f.mu.Lock()
	a, ok := f.agents[agentID]
	if !ok {
		f.mu.Unlock()
		return CognitiveState{}, ErrAgentNotFound
	}
	a.mu.RLock()
	cs := a.cognitive
	a.mu.RUnlock()
	f.mu.Unlock()
	return cs, nil
}

// SetCognitiveState replaces the agent's cognitive state (used by Recover
// and by the agent itself to checkpoint progress).
func (f *Fabric) SetCognitiveState(agentID string, cs CognitiveState) error {
	f.mu.Lock()
	a, ok := f.agents[agentID]
	if !ok {
		f.mu.Unlock()
		return ErrAgentNotFound
	}
	a.mu.Lock()
	a.cognitive = cs
	a.mu.Unlock()
	f.mu.Unlock()
	return nil
}

// CheckpointCognitive returns a snapshot of the agent's cognitive state for
// durable storage (the Runtime does NOT depend on hidden CoT — only on this
// checkpointable state; §13 invariant #5). The snapshot is a copy: mutating
// it does not affect the live agent.
func (f *Fabric) CheckpointCognitive(agentID string) (CognitiveState, error) {
	return f.CognitiveState(agentID)
}

// cloneMap returns a shallow copy of m (nil → empty map). The copy is safe
// for the caller to mutate without affecting the source.
func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Ensure sync is referenced (agent.go uses it, but this file may be compiled
// standalone in tooling).
var _ = sync.RWMutex{}
