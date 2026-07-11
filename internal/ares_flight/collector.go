package flight

import (
	"context"
	"sync"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/evidence"
)

// Collector subscribes to the EventStore and populates flight recorder data structures.
type Collector struct {
	eventStore        ares_events.EventStore
	evidenceStore     evidence.Store      // optional: unified Evidence Store
	evidenceCollector *evidence.Collector // optional: evidence emitter
	timeline          *Timeline
	graph             *Graph
	decisions         *DecisionLog
	diag              *DiagnosticsEngine
	pipelines         map[string]*MemoryPipeline
	cancel            context.CancelFunc
	wg                sync.WaitGroup
	mu                sync.RWMutex
}

// CollectorConfig holds dependencies for the collector.
type CollectorConfig struct {
	EventStore    ares_events.EventStore
	EvidenceStore evidence.Store // optional: unified Evidence Store
}

// NewCollector creates a new flight data collector.
func NewCollector(cfg CollectorConfig) *Collector {
	c := &Collector{
		eventStore:    cfg.EventStore,
		evidenceStore: cfg.EvidenceStore,
		timeline:      NewTimeline(),
		graph:         NewGraph(),
		decisions:     NewDecisionLog(),
		diag:          NewDiagnosticsEngine(),
		pipelines:     make(map[string]*MemoryPipeline),
	}
	if cfg.EvidenceStore != nil {
		c.evidenceCollector = evidence.NewCollector(cfg.EvidenceStore, "flight")
	}
	return c
}

// Start begins collecting ares_events from the event store.
func (c *Collector) Start(ctx context.Context) error {
	if c.eventStore == nil {
		return nil
	}

	ctx, c.cancel = context.WithCancel(ctx)

	ch, err := c.eventStore.Subscribe(ctx, ares_events.EventFilter{})
	if err != nil {
		return err
	}

	c.wg.Add(1)
	go c.collectLoop(ctx, ch)

	return nil
}

// Stop stops the collector.
func (c *Collector) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
	c.wg.Wait()
}

// Timeline returns the execution timeline.
func (c *Collector) Timeline() *Timeline {
	return c.timeline
}

// Graph returns the call graph.
func (c *Collector) Graph() *Graph {
	return c.graph
}

// Decisions returns the decision log.
func (c *Collector) Decisions() *DecisionLog {
	return c.decisions
}

// Diagnostics returns the diagnostics engine.
func (c *Collector) Diagnostics() *DiagnosticsEngine {
	return c.diag
}

// Pipeline returns the memory pipeline for a session.
func (c *Collector) Pipeline(sessionID string) *MemoryPipeline {
	c.mu.RLock()
	p, ok := c.pipelines[sessionID]
	c.mu.RUnlock()
	if !ok {
		return nil
	}
	return p
}

// collectLoop reads ares_events and routes them to the appropriate data structure.
func (c *Collector) collectLoop(ctx context.Context, ch <-chan *ares_events.Event) {
	defer c.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			c.processEvent(evt)
		}
	}
}

// processEvent routes a single event to the right handler.
func (c *Collector) processEvent(evt *ares_events.Event) {
	if evt == nil {
		return
	}

	// Emit evidence to the unified Evidence Store.
	if c.evidenceCollector != nil {
		_ = c.evidenceCollector.EmitWithMeta(context.Background(), evidence.KindExecutionTrace,
			map[string]any{
				"event_type": evt.Type,
				"stream_id":  evt.StreamID,
				"version":    evt.Version,
			},
			"event_type", string(evt.Type),
		)
	}

	switch evt.Type {
	case ares_events.EventAgentStarted:
		c.handleAgentStart(evt)
	case ares_events.EventAgentStopped:
		c.handleAgentEnd(evt)
	case ares_events.EventTaskCreated, ares_events.EventTaskDispatched:
		c.handleTaskStart(evt)
	case ares_events.EventTaskCompleted, ares_events.EventTaskFailed:
		c.handleTaskEnd(evt)
	case ares_events.EventFailoverTriggered, ares_events.EventFailoverCompleted:
		c.handleFailover(evt)
	case ares_events.EventMemoryDistilled:
		c.handleMemoryDistilled(evt)
	case ares_events.EventLLMCall:
		c.handleLLMCall(evt)
	}

	// Check for tool-related ares_events (custom types).
	if isToolEvent(evt) {
		c.handleToolEvent(evt)
	}

	// Check for decision ares_events.
	if isDecisionEvent(evt) {
		c.handleDecisionEvent(evt)
	}
}

func (c *Collector) handleAgentStart(evt *ares_events.Event) {
	agentID := evt.StreamID
	c.timeline.Add(TimelineEvent{
		ID:       evt.ID,
		AgentID:  agentID,
		Type:     EventAgentStart,
		Name:     string(evt.Type),
		StartAt:  evt.Timestamp,
		Metadata: evt.Payload,
	})

	// Use agentID (evt.StreamID) as the graph node ID so handleAgentEnd can
	// look up the node by the same agentID. Using evt.ID here caused the
	// lookup in handleAgentEnd to always miss.
	c.graph.AddNode(&GraphNode{
		ID:       agentID,
		Type:     NodeAgent,
		Name:     agentID,
		Status:   StatusRunning,
		StartAt:  evt.Timestamp,
		Metadata: evt.Payload,
	})
}

func (c *Collector) handleAgentEnd(evt *ares_events.Event) {
	agentID := evt.StreamID
	c.timeline.Add(TimelineEvent{
		ID:       evt.ID,
		AgentID:  agentID,
		Type:     EventAgentEnd,
		Name:     string(evt.Type),
		StartAt:  evt.Timestamp,
		Metadata: evt.Payload,
	})

	// Update graph node status under the Graph write lock (P0-2).
	c.graph.UpdateNodeStatus(agentID, StatusCompleted, evt.Timestamp)
}

func (c *Collector) handleTaskStart(evt *ares_events.Event) {
	c.timeline.Add(TimelineEvent{
		ID:       evt.ID,
		AgentID:  evt.StreamID,
		Type:     EventWaiting,
		Name:     string(evt.Type),
		StartAt:  evt.Timestamp,
		Metadata: evt.Payload,
	})
}

func (c *Collector) handleTaskEnd(evt *ares_events.Event) {
	var evtType EventType
	switch evt.Type {
	case ares_events.EventTaskCompleted:
		evtType = EventTaskEnd
	case ares_events.EventTaskFailed:
		evtType = EventError

		// Auto-diagnose failures.
		errMsg := ""
		if e, ok := evt.Payload["error"].(string); ok {
			errMsg = e
		}
		suggestions := SuggestFix(ClassifyError(errMsg))
		suggestion := ""
		if len(suggestions) > 0 {
			suggestion = suggestions[0]
		}
		c.diag.Record(DiagnosticRecord{
			ID:         evt.ID,
			AgentID:    evt.StreamID,
			Category:   ClassifyError(errMsg),
			RootCause:  errMsg,
			Suggestion: suggestion,
			Timestamp:  evt.Timestamp,
		})
	}

	c.timeline.Add(TimelineEvent{
		ID:       evt.ID,
		AgentID:  evt.StreamID,
		Type:     evtType,
		Name:     string(evt.Type),
		StartAt:  evt.Timestamp,
		Metadata: evt.Payload,
	})
}

func (c *Collector) handleFailover(evt *ares_events.Event) {
	c.timeline.Add(TimelineEvent{
		ID:       evt.ID,
		AgentID:  evt.StreamID,
		Type:     EventError,
		Name:     string(evt.Type),
		StartAt:  evt.Timestamp,
		Metadata: evt.Payload,
	})
}

func (c *Collector) handleMemoryDistilled(evt *ares_events.Event) {
	sessionID := evt.StreamID
	inputCount := 0
	outputCount := 0
	if v, ok := evt.Payload["input_count"].(float64); ok {
		inputCount = int(v)
	}
	if v, ok := evt.Payload["output_count"].(float64); ok {
		outputCount = int(v)
	}

	c.mu.Lock()
	pipeline, ok := c.pipelines[sessionID]
	if !ok {
		pipeline = NewMemoryPipeline(sessionID)
		c.pipelines[sessionID] = pipeline
	}
	c.mu.Unlock()

	pipeline.AddStage(PipelineStage{
		Name:        "distill",
		InputCount:  inputCount,
		OutputCount: outputCount,
		Timestamp:   evt.Timestamp,
	})

	c.timeline.Add(TimelineEvent{
		ID:       evt.ID,
		AgentID:  sessionID,
		Type:     EventMemoryOp,
		Name:     "memory.distilled",
		StartAt:  evt.Timestamp,
		Metadata: evt.Payload,
	})
}

func (c *Collector) handleLLMCall(evt *ares_events.Event) {
	c.timeline.Add(TimelineEvent{
		ID:       evt.ID,
		AgentID:  evt.StreamID,
		Type:     EventLLMCall,
		Name:     "llm.call",
		StartAt:  evt.Timestamp,
		Metadata: evt.Payload,
	})

	c.graph.AddNode(&GraphNode{
		ID:       evt.ID,
		ParentID: evt.StreamID,
		Type:     NodeLLM,
		Name:     "LLM Call",
		Status:   StatusCompleted,
		StartAt:  evt.Timestamp,
		Metadata: evt.Payload,
	})
}

func (c *Collector) handleToolEvent(evt *ares_events.Event) {
	c.timeline.Add(TimelineEvent{
		ID:       evt.ID,
		AgentID:  evt.StreamID,
		Type:     EventToolCall,
		Name:     string(evt.Type),
		StartAt:  evt.Timestamp,
		Metadata: evt.Payload,
	})

	c.graph.AddNode(&GraphNode{
		ID:       evt.ID,
		ParentID: evt.StreamID,
		Type:     NodeTool,
		Name:     string(evt.Type),
		Status:   StatusCompleted,
		StartAt:  evt.Timestamp,
		Metadata: evt.Payload,
	})
}

func (c *Collector) handleDecisionEvent(evt *ares_events.Event) {
	d := Decision{
		ID:        evt.ID,
		AgentID:   evt.StreamID,
		Type:      DecisionToolSelect,
		Timestamp: evt.Timestamp,
		Metadata:  evt.Payload,
	}

	if reason, ok := evt.Payload["reason"].(string); ok {
		d.Reason = reason
	}
	if selected, ok := evt.Payload["selected"].(string); ok {
		d.Selected = selected
	}
	if confidence, ok := evt.Payload["confidence"].(float64); ok {
		d.Confidence = confidence
	}

	c.decisions.Add(d)
}

func isToolEvent(evt *ares_events.Event) bool {
	s := string(evt.Type)
	return len(s) > 5 && s[:5] == "tool."
}

func isDecisionEvent(evt *ares_events.Event) bool {
	s := string(evt.Type)
	return len(s) > 9 && s[:9] == "decision."
}
