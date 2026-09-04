// Package toolprojection projects raw tool-call events (EventToolCallCompleted)
// into a ToolStep DAG (Y1 方案C C2). A ToolStep is "one tool execution
// process" — the node granularity that makes a single agent's tool usage both
// observable and evolvable. It reads the C1-unified event contract
// (round/seq/success/error/arg_shape) that the three ReAct executors now emit.
//
// The projection is a pure read over events: no checkpoint schema change, no
// ReAct execution-model change. Edges express only "same-session execution
// order" (from round+seq), never semantic dependency, so they are for display /
// ordering statistics, not patch targets (plan §7.2).
package toolprojection

import (
	"context"
	"fmt"
	"sort"

	ares_events "github.com/Timwood0x10/ares/internal/ares_events"
)

// ToolStepID builds the aggregation key for a tool call: "toolName#argShape".
// argShape is the normalized argument-KEY set (values collapsed), so "the same
// tool used the same way" folds into one node and the trajectory does not
// fragment on parameter values (plan §11.0). The '#' separator is safe because
// tool names and shapes never contain it (names are identifiers; shapes are
// comma-joined key names).
func ToolStepID(toolName, argShape string) string {
	return toolName + "#" + argShape
}

// ToolStep is one aggregated tool execution process.
type ToolStep struct {
	// ToolStepID is the aggregation key (toolName#argShape).
	ToolStepID string `json:"tool_step_id"`
	// ToolName is the tool that was executed.
	ToolName string `json:"tool_name"`
	// ArgShape is the normalized argument-key set ("" when the tool took no args).
	ArgShape string `json:"arg_shape"`
	// Count is how many times this process occurred in the window.
	Count int `json:"count"`
	// SuccessCount is how many of those calls succeeded.
	SuccessCount int `json:"success_count"`
	// SuccessRate is SuccessCount/Count in [0,1] (0 when Count==0).
	SuccessRate float64 `json:"success_rate"`
}

// ToolStepEdge is an ordered (earlier -> later) execution edge within a session.
// It encodes "ran before", never semantic dependency (plan §7.2 — not a patch
// target).
type ToolStepEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Projection is the result of projecting a set of tool-call events.
type Projection struct {
	// Steps is a stable, name-ordered slice of aggregated ToolSteps.
	Steps []*ToolStep `json:"steps"`
	// Edges are the same-session execution-order edges (deduplicated, sorted).
	Edges []ToolStepEdge `json:"edges"`
	// TotalEvents is the number of EventToolCallCompleted events consumed.
	TotalEvents int `json:"total_events"`
}

// EventSource is the minimal read surface the projector needs. Declared at the
// consumer (code_rules §5.2); *ares_events.MemoryEventStore and friends satisfy
// it via ReadAll.
type EventSource interface {
	ReadAll(ctx context.Context, opts ares_events.ReadOptions) ([]*ares_events.Event, error)
}

// Options configures a projection.
type Options struct {
	// MinSamples is the minimum Count a ToolStep needs to be included (plan §7
	// pattern-aggregation threshold). 0 includes all steps.
	MinSamples int
}

// ProjectFromSource reads all events from src and projects them into ToolSteps.
// It is the eventStore-wired entry point (plan §11 C2 "从 EventStore 投影").
func ProjectFromSource(ctx context.Context, src EventSource, opts Options) (*Projection, error) {
	if src == nil {
		return nil, fmt.Errorf("toolprojection: event source is nil")
	}
	events, err := src.ReadAll(ctx, ares_events.ReadOptions{})
	if err != nil {
		return nil, fmt.Errorf("toolprojection: read events: %w", err)
	}
	return Project(events, opts), nil
}

// Project converts a slice of ares_events into a ToolStep projection. It is
// pure (no I/O) so tests can feed hand-built event streams directly.
func Project(events []*ares_events.Event, opts Options) *Projection {
	// toolStepID -> (count, successCount). Iteration is insertion-ordered (the
	// event order); we re-sort for a deterministic output.
	counts := map[string]int{}
	success := map[string]int{}

	// collect ordered calls so edges respect (stream, round, seq), not just
	// arrival order.
	type call struct {
		stream string
		round  int
		seq    int
		stepID string
	}
	var calls []call
	edgesSet := map[ToolStepEdge]bool{}
	total := 0

	for _, ev := range events {
		if ev == nil || ev.Type != ares_events.EventToolCallCompleted {
			continue
		}
		p := ev.Payload
		toolName := strField(p, ares_events.EventKeyToolName)
		if toolName == "" {
			toolName = strField(p, ares_events.EventKeyTool)
		}
		if toolName == "" {
			continue
		}
		argShape := strField(p, ares_events.EventKeyArgShape)
		stepID := ToolStepID(toolName, argShape)

		counts[stepID]++
		if boolField(p, ares_events.EventKeySuccess) {
			success[stepID]++
		}
		total++
		calls = append(calls, call{
			stream: streamOf(ev),
			round:  intField(p, ares_events.EventKeyRound),
			seq:    intField(p, ares_events.EventKeySeq),
			stepID: stepID,
		})
	}

	// Execution-order edges: for each session, chain "ran before" across calls
	// in (round, seq) order. This is an approximation (same-session ordering,
	// never semantic dependency — plan §7.2, edges are NOT patch targets).
	sort.SliceStable(calls, func(i, j int) bool {
		if calls[i].stream != calls[j].stream {
			return calls[i].stream < calls[j].stream
		}
		if calls[i].round != calls[j].round {
			return calls[i].round < calls[j].round
		}
		return calls[i].seq < calls[j].seq
	})
	for i := 1; i < len(calls); i++ {
		cur, prev := calls[i], calls[i-1]
		if cur.stream != prev.stream || cur.stepID == prev.stepID {
			continue
		}
		edgesSet[ToolStepEdge{From: prev.stepID, To: cur.stepID}] = true
	}

	// Build steps (sorted by ToolStepID for determinism).
	ids := make([]string, 0, len(counts))
	for id := range counts {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var steps []*ToolStep
	for _, id := range ids {
		if opts.MinSamples > 0 && counts[id] < opts.MinSamples {
			continue
		}
		name := id
		shape := ""
		// Recover toolName/shape from the id when the event carried no shape.
		if i := indexOf(id, '#'); i >= 0 {
			name = id[:i]
			shape = id[i+1:]
		}
		c := counts[id]
		steps = append(steps, &ToolStep{
			ToolStepID:   id,
			ToolName:     name,
			ArgShape:     shape,
			Count:        c,
			SuccessCount: success[id],
			SuccessRate:  float64(success[id]) / float64(max(c, 1)),
		})
	}

	// Sort edges deterministically.
	edges := make([]ToolStepEdge, 0, len(edgesSet))
	for e := range edgesSet {
		edges = append(edges, e)
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From != edges[j].From {
			return edges[i].From < edges[j].From
		}
		return edges[i].To < edges[j].To
	})

	return &Projection{Steps: steps, Edges: edges, TotalEvents: total}
}

func streamOf(ev *ares_events.Event) string {
	if ev == nil {
		return ""
	}
	return ev.StreamID
}

func strField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

func intField(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}

func boolField(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	if b, ok := m[key].(bool); ok {
		return b
	}
	return false
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func indexOf(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
