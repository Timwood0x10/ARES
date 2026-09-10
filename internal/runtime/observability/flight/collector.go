package flight

//nolint: errcheck // best-effort operations: ResponseWriter writes, cleanup Close/Wait, deferred shutdown
import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/evidence"
)

// keyFitnessValue is the JSON payload key that carries the normalized fitness
// value in [0, 1] for GA genome evidence (used by the workflow, recovery, and
// scheduler collectors).
const keyFitnessValue = "value"

// evidenceQueueSize bounds the buffered evidence emissions between the
// subscription loop and the background flusher. The enqueue side is
// non-blocking (drop + count on full): the subscription loop must never wait
// on evidence-store I/O, or a slow store plus the publisher's drop-on-full
// semantics would silently lose flight events while the loop was blocked.
const evidenceQueueSize = 256

// Collector subscribes to the EventStore and populates flight recorder data structures.
type Collector struct {
	eventStore         ares_events.EventStore
	evidenceStore      evidence.Store      // optional: unified Evidence Store
	evidenceCollector  *evidence.Collector // optional: evidence emitter (Source "flight")
	workflowCollector  *evidence.Collector // optional: workflow fitness evidence (Source "workflow")
	recoveryCollector  *evidence.Collector // optional: recovery fitness evidence (Source "recovery")
	schedulerCollector *evidence.Collector // optional: scheduler fitness evidence (Source "scheduler")
	timeline           *Timeline
	graph              *Graph
	decisions          *DecisionLog
	diag               *DiagnosticsEngine
	pipelines          map[string]*MemoryPipeline
	// agentStartIDs maps agentID → its most recent start event ID so
	// handleAgentEnd can set ParentID for robust timeline pairing.
	agentStartIDs map[string]string
	// evidenceCh carries buffered evidence emissions from the hot
	// subscription loop to the background flusher (nil when no evidence
	// store is wired — enqueue is then a no-op).
	evidenceCh      chan func(context.Context)
	evidenceDropped atomic.Uint64
	cancel          context.CancelFunc
	eg              errgroup.Group
	mu              sync.RWMutex
}

// maxPipelines is the ring cap for the pipelines map.
const maxPipelines = 100

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
		// Workflow fitness evidence is emitted under Source "workflow" so the
		// GA WorkflowGenome (which filters on that source) consumes it.
		c.workflowCollector = evidence.NewCollector(cfg.EvidenceStore, "workflow")
		// Recovery fitness evidence is emitted under Source "recovery" so the
		// GA RecoveryGenome (which filters on that source) consumes it.
		c.recoveryCollector = evidence.NewCollector(cfg.EvidenceStore, "recovery")
		// Scheduler fitness evidence: emitted under Source "scheduler" so the
		// GA SchedulerGenome (which filters on that source) consumes it.
		c.schedulerCollector = evidence.NewCollector(cfg.EvidenceStore, "scheduler")
		// Buffered evidence path: the subscription loop enqueues emissions
		// and a background goroutine performs the store I/O. A synchronous
		// emit on the hot path (the pre-fix behavior) let a slow evidence
		// store stall event consumption until the publisher's bounded
		// channel started dropping flight events.
		c.evidenceCh = make(chan func(context.Context), evidenceQueueSize)
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

	c.eg.Go(func() error {
		c.collectLoop(ctx, ch)
		return nil
	})
	if c.evidenceCh != nil {
		c.eg.Go(func() error {
			c.evidenceFlushLoop(ctx)
			return nil
		})
	}

	return nil
}

// Stop stops the collector. Buffered-but-unflushed evidence at shutdown is
// dropped (best-effort observability); the flush loop exits on the cancelled
// context and the enqueue side is non-blocking, so Stop cannot hang on store
// I/O.
func (c *Collector) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
	_ = c.eg.Wait()
}

// enqueueEvidence buffers one evidence emission for the background flusher.
// Never blocks: a full queue drops the emission and counts it (the
// subscription loop's liveness outranks any single evidence record).
func (c *Collector) enqueueEvidence(fn func(context.Context)) {
	if c.evidenceCh == nil {
		return
	}
	select {
	case c.evidenceCh <- fn:
	default:
		n := c.evidenceDropped.Add(1)
		// Log the first drop and then every 100th: visible, not spammy.
		if n == 1 || n%100 == 0 {
			log.Warn("flight: evidence queue full; dropping emission", "dropped_total", n)
		}
	}
}

// DroppedEvidence reports how many evidence emissions were dropped because
// the flush queue was full (the hot-path backpressure signal).
func (c *Collector) DroppedEvidence() uint64 {
	return c.evidenceDropped.Load()
}

// evidenceFlushLoop drains the buffered emissions on a background goroutine,
// performing the store I/O off the subscription path.
func (c *Collector) evidenceFlushLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case fn := <-c.evidenceCh:
			fn(ctx)
		}
	}
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
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			c.processEvent(ctx, evt)
		}
	}
}

// processEvent routes a single event to the right handler.
func (c *Collector) processEvent(ctx context.Context, evt *ares_events.Event) {
	if evt == nil {
		return
	}

	// Emit evidence to the unified Evidence Store — buffered, never on this
	// hot path (see enqueueEvidence): a slow store must not stall event
	// consumption behind the publisher's drop-on-full channel.
	if c.evidenceCollector != nil {
		ec, evtType, streamID, version := c.evidenceCollector, evt.Type, evt.StreamID, evt.Version
		c.enqueueEvidence(func(fctx context.Context) {
			if err := ec.EmitWithMeta(fctx, evidence.KindExecutionTrace,
				map[string]any{
					"event_type": evtType,
					"stream_id":  streamID,
					"version":    version,
				},
				"event_type", string(evtType),
			); err != nil {
				log.Warn("flight: emit execution-trace evidence failed", "error", err, "event_type", evtType)
			}
		})
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
		// Emit workflow fitness evidence consumed by the GA WorkflowGenome:
		// the mean value across task outcomes (1.0 completed / 0.0 failed)
		// scores the DAG topology that actually executed. Emitted under
		// Source "workflow" so the genome's filter matches.
		successValue := 1.0
		if evt.Type == ares_events.EventTaskFailed {
			successValue = 0.0
		}
		if c.workflowCollector != nil {
			wc, value := c.workflowCollector, successValue
			c.enqueueEvidence(func(fctx context.Context) {
				if err := wc.Emit(fctx, evidence.KindFitness,
					map[string]any{keyFitnessValue: value},
				); err != nil {
					log.Warn("flight: emit workflow fitness evidence failed", "error", err)
				}
			})
		}
		// Scheduler fitness evidence: the GA SchedulerGenome consumes the mean
		// scheduling-outcome value to score the scheduler policy selected by
		// evolution. A completed task is a scheduling win (1.0); a failed task
		// is a loss (0.0).
		if c.schedulerCollector != nil {
			sc, value := c.schedulerCollector, successValue
			c.enqueueEvidence(func(fctx context.Context) {
				if err := sc.Emit(fctx, evidence.KindFitness,
					map[string]any{keyFitnessValue: value},
				); err != nil {
					log.Warn("flight: emit scheduler fitness evidence failed", "error", err)
				}
			})
		}
	case ares_events.EventFailoverTriggered, ares_events.EventFailoverCompleted:
		c.handleFailover(evt)
		// Emit recovery fitness evidence: the GA RecoveryGenome consumes the
		// mean value across failover outcomes (1.0 completed / 0.0 triggered
		// without completion) to score the recovery strategy actually used.
		recoveryValue := 0.0
		if evt.Type == ares_events.EventFailoverCompleted {
			recoveryValue = 1.0
		}
		if c.recoveryCollector != nil {
			rc, value := c.recoveryCollector, recoveryValue
			c.enqueueEvidence(func(fctx context.Context) {
				if err := rc.Emit(fctx, evidence.KindFitness,
					map[string]any{keyFitnessValue: value},
				); err != nil {
					log.Warn("flight: emit recovery fitness evidence failed", "error", err)
				}
			})
		}
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

	// Record the start event ID so handleAgentEnd can set ParentID,
	// enabling robust start→end pairing in Timeline.Add even with
	// out-of-order arrival or overlapping calls within one agent.
	c.mu.Lock()
	if c.agentStartIDs == nil {
		c.agentStartIDs = make(map[string]string)
	}
	c.agentStartIDs[agentID] = evt.ID
	c.mu.Unlock()

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

	// Set ParentID to the agent's start event ID so Timeline.Add can
	// pair the end event with the exact start event (not just the most
	// recent unpaired one — robust to out-of-order arrival).
	parentID := ""
	c.mu.RLock()
	parentID = c.agentStartIDs[agentID]
	c.mu.RUnlock()

	c.timeline.Add(TimelineEvent{
		ID:       evt.ID,
		ParentID: parentID,
		AgentID:  agentID,
		Type:     EventAgentEnd,
		Name:     string(evt.Type),
		StartAt:  evt.Timestamp,
		Metadata: evt.Payload,
	})

	// Update graph node status under the Graph write lock.
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
	inputCount := payloadInt(evt.Payload, "input_count")
	outputCount := payloadInt(evt.Payload, "output_count")

	c.mu.Lock()
	pipeline, ok := c.pipelines[sessionID]
	if !ok {
		pipeline = NewMemoryPipeline(sessionID)
		c.pipelines[sessionID] = pipeline
		// Cap the pipelines map — evict the oldest pipeline when
		// the cap is exceeded.
		if maxPipelines > 0 && len(c.pipelines) > maxPipelines {
			var oldestID string
			var oldestTime time.Time
			for id, p := range c.pipelines {
				if oldestID == "" {
					oldestID = id
					oldestTime = p.lastActivity()
					continue
				}
				t := p.lastActivity()
				if t.Before(oldestTime) {
					oldestID = id
					oldestTime = t
				}
			}
			if oldestID != "" && oldestID != sessionID {
				delete(c.pipelines, oldestID)
			}
		}
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

// payloadInt extracts an integer from an event payload, handling int,
// float64, int64, and string representations. Returns 0 when the key is
// absent or the value cannot be converted.
func payloadInt(payload map[string]any, key string) int {
	v, ok := payload[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		var i int
		_, err := fmt.Sscanf(n, "%d", &i)
		if err != nil {
			return 0
		}
		return i
	}
	return 0
}
