// observer.go provides the RuntimeObserver — the OBSERVE stage of the
// evolution control plane. It subscribes to task-completed/failed and
// agent-stopped events from the EventStore, converts them into normalized
// [0,1] StrategySample values, and fans the samples out to two consumers:
//
//  1. StrategyLifecycle — feeds RollbackPolicy.RecordScore for degradation
//     detection (B1 fix).
//  2. EvidenceStore — writes KindFitness evidence (source="strategy") so the
//     GA scorer and deployment staging can read real runtime fitness (B6 fix).
//
// The observer is deliberately passive: it never decides to promote or
// rollback. It only collects and forwards. Agent code is unaware that its
// execution outcomes are being scored here.
package evolution

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/evidence"
)

// StrategySample is one runtime execution observation of the currently active
// strategy. All values are normalized to [0,1] so they are dimensionally
// consistent with RollbackPolicy thresholds (B1 fix).
type StrategySample struct {
	// StrategyID is the ID of the active strategy when the sample was taken.
	StrategyID string

	// Success indicates whether the task completed successfully.
	Success bool

	// Score is the normalized fitness score in [0,1].
	Score float64

	// Latency is the observed execution duration.
	Latency time.Duration

	// CostUSD is the estimated dollar cost of the execution.
	CostUSD float64

	// TaskType categorizes the observed task (e.g. "chat", "workflow").
	TaskType string

	// At is when the sample was recorded.
	At time.Time
}

// SampleSink receives StrategySample values. StrategyLifecycle and the evidence
// store both implement this interface (the store via an adapter).
type SampleSink interface {
	// OnSample processes a single strategy sample.
	OnSample(sample StrategySample)
}

// RuntimeObserver subscribes to the EventStore for task and agent lifecycle
// events, converts them into StrategySample values, and fans them out to
// registered sinks. It is the sole producer of runtime fitness samples —
// agent code never calls it directly.
type RuntimeObserver struct {
	subscriber EventStoreSubscriber
	sinks      []SampleSink
	evStore    evidence.Store
	activeID   func() string
	mu         sync.Mutex
	cancel     context.CancelFunc
	eg         *errgroupAdapter
}

// errgroupAdapter is a minimal managed-goroutine wrapper so the observer
// does not import golang.org/x/sync/errgroup directly into its public API
// (it already transitively uses it via the scheduler package).
type errgroupAdapter struct {
	done chan struct{}
}

func newErrgroupAdapter() *errgroupAdapter {
	return &errgroupAdapter{done: make(chan struct{})}
}

func (e *errgroupAdapter) Wait() {
	<-e.done
}

// ObserverOption configures a RuntimeObserver.
type ObserverOption func(*RuntimeObserver)

// WithObserverEvidenceStore sets the evidence store so the observer writes
// KindFitness evidence (source="strategy") for each sample.
func WithObserverEvidenceStore(store evidence.Store) ObserverOption {
	return func(o *RuntimeObserver) {
		o.evStore = store
	}
}

// WithObserverActiveIDFunc sets a function that returns the currently active
// strategy ID. When nil, the observer uses the strategy ID from the event
// payload or falls back to "unknown".
func WithObserverActiveIDFunc(fn func() string) ObserverOption {
	return func(o *RuntimeObserver) {
		o.activeID = fn
	}
}

// WithObserverSink adds a SampleSink that receives every collected sample.
func WithObserverSink(sink SampleSink) ObserverOption {
	return func(o *RuntimeObserver) {
		if sink != nil {
			o.sinks = append(o.sinks, sink)
		}
	}
}

// NewRuntimeObserver creates an observer that subscribes to the given
// EventStoreSubscriber. The observer does not start until Start is called.
func NewRuntimeObserver(subscriber EventStoreSubscriber, opts ...ObserverOption) *RuntimeObserver {
	o := &RuntimeObserver{subscriber: subscriber}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// Start subscribes to task lifecycle events and begins collecting samples.
// It is idempotent: calling Start twice is a no-op. The subscription runs
// until ctx is cancelled or Stop is called.
func (o *RuntimeObserver) Start(ctx context.Context) error {
	if o.subscriber == nil {
		return nil
	}
	o.mu.Lock()
	if o.cancel != nil {
		o.mu.Unlock()
		return nil
	}
	subCtx, cancel := context.WithCancel(ctx)
	ch, err := o.subscriber.Subscribe(subCtx, ares_events.EventFilter{
		Types: []ares_events.EventType{
			ares_events.EventTaskCompleted,
			ares_events.EventTaskFailed,
		},
	})
	if err != nil {
		cancel()
		o.mu.Unlock()
		return err
	}
	eg := newErrgroupAdapter()
	o.cancel = cancel
	o.eg = eg
	o.mu.Unlock()

	go func() {
		defer close(eg.done)
		for {
			select {
			case evt, ok := <-ch:
				if !ok {
					return
				}
				if evt == nil {
					continue
				}
				o.processEvent(subCtx, evt)
			case <-subCtx.Done():
				return
			}
		}
	}()
	return nil
}

// Stop cancels the subscription and waits for the event loop to exit.
func (o *RuntimeObserver) Stop() {
	o.mu.Lock()
	cancel := o.cancel
	eg := o.eg
	o.cancel = nil
	o.eg = nil
	o.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if eg != nil {
		eg.Wait()
	}
}

// processEvent converts a single event into a StrategySample and dispatches
// it to all sinks and the evidence store.
func (o *RuntimeObserver) processEvent(ctx context.Context, evt *ares_events.Event) {
	sample := o.eventToSample(evt)
	for _, sink := range o.sinks {
		sink.OnSample(sample)
	}
	o.writeEvidence(ctx, sample)
}

// eventToSample converts a task lifecycle event into a normalized [0,1]
// StrategySample. Completed → 1.0, Failed → 0.0. The active strategy ID
// is resolved from the activeID func (if set) or the event payload.
func (o *RuntimeObserver) eventToSample(evt *ares_events.Event) StrategySample {
	score := 0.0
	success := false
	if evt.Type == ares_events.EventTaskCompleted {
		score = 1.0
		success = true
	}
	// Failed → 0.0 (already initialized).

	strategyID := "unknown"
	if o.activeID != nil {
		if id := o.activeID(); id != "" {
			strategyID = id
		}
	}
	if evt.Payload != nil {
		if id, ok := evt.Payload["strategy_id"].(string); ok && id != "" {
			strategyID = id
		}
	}
	taskType := ""
	if evt.Payload != nil {
		if t, ok := evt.Payload["task_type"].(string); ok {
			taskType = t
		}
	}
	return StrategySample{
		StrategyID: strategyID,
		Success:    success,
		Score:      score,
		TaskType:   taskType,
		At:         time.Now(),
	}
}

// writeEvidence writes a KindFitness evidence record (source="strategy") so
// the GA scorer and deployment staging can read real runtime fitness. This
// fixes B6: staging Evaluate now has a multi-dimensional evidence source
// including the "strategy" dimension.
func (o *RuntimeObserver) writeEvidence(ctx context.Context, sample StrategySample) {
	if o.evStore == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"value":       sample.Score,
		"success":     sample.Success,
		"strategy_id": sample.StrategyID,
		"task_type":   sample.TaskType,
	})
	if err != nil {
		return
	}
	_ = o.evStore.Append(ctx, evidence.Evidence{
		ID:        "strategy_" + sample.StrategyID + "_" + sample.At.Format("150405.000000"),
		Source:    "strategy",
		Kind:      evidence.KindFitness,
		Payload:   payload,
		Timestamp: sample.At,
	})
}
