// Command flight provides the Agent Flight Recorder CLI.
//
// Usage:
//
//	goagentx flight inspect <taskID> [--format=text|mermaid|dot|json] [--input=file]
//	goagentx flight replay <taskID> [--step=N] [--input=file]
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"goagentx/internal/events"
	"goagentx/internal/flight"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	sub := os.Args[1]
	args := os.Args[2:]

	switch sub {
	case "inspect":
		if err := runInspect(args); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "replay":
		if err := runReplay(args); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", sub)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage: goagentx flight <command> [options]\n\n")
	fmt.Fprintf(os.Stderr, "Commands:\n")
	fmt.Fprintf(os.Stderr, "  inspect   Show flight data for a task\n")
	fmt.Fprintf(os.Stderr, "  replay    Step-by-step replay of a task\n")
}

// runInspect handles the "inspect" subcommand.
func runInspect(args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	format := fs.String("format", "text", "Output format: text, mermaid, dot, json")
	input := fs.String("input", "", "Path to JSON events file (default: stdin)")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("taskID is required")
	}
	taskID := fs.Arg(0)

	evts, err := loadEvents(*input)
	if err != nil {
		return fmt.Errorf("load events: %w", err)
	}

	if len(evts) == 0 {
		return fmt.Errorf("no events found")
	}

	// Filter events by taskID (streamID).
	var taskEvts []*events.Event
	for _, e := range evts {
		if e.StreamID == taskID {
			taskEvts = append(taskEvts, e)
		}
	}
	if len(taskEvts) == 0 {
		return fmt.Errorf("no events found for task %s", taskID)
	}

	switch *format {
	case "text":
		return inspectText(taskID, taskEvts)
	case "mermaid":
		return inspectMermaid(taskEvts)
	case "dot":
		return inspectDOT(taskEvts)
	case "json":
		return inspectJSON(taskEvts)
	default:
		return fmt.Errorf("unknown format: %s (supported: text, mermaid, dot, json)", *format)
	}
}

// runReplay handles the "replay" subcommand.
func runReplay(args []string) error {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	step := fs.Int("step", -1, "Jump to a specific step (0-indexed)")
	input := fs.String("input", "", "Path to JSON events file (default: stdin)")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	if fs.NArg() < 1 {
		return fmt.Errorf("taskID is required")
	}
	taskID := fs.Arg(0)

	evts, err := loadEvents(*input)
	if err != nil {
		return fmt.Errorf("load events: %w", err)
	}

	// Load events into a memory store for the replay session.
	store := events.NewMemoryEventStore()
	defer store.Close()

	ctx := context.Background()

	// Group events by streamID and append to the store.
	streamEvents := make(map[string][]*events.Event)
	for _, e := range evts {
		streamEvents[e.StreamID] = append(streamEvents[e.StreamID], e)
	}
	for streamID, sevts := range streamEvents {
		if err := store.Append(ctx, streamID, sevts, 0); err != nil {
			return fmt.Errorf("append events for stream %s: %w", streamID, err)
		}
	}

	session, err := flight.NewReplaySession(ctx, store, taskID)
	if err != nil {
		return fmt.Errorf("create replay session: %w", err)
	}

	summary := session.Summary()
	fmt.Printf("Task: %s\n", summary.TaskID)
	fmt.Printf("Total steps: %d\n", summary.TotalSteps)
	fmt.Printf("Duration: %s\n", summary.Duration)
	fmt.Printf("Agents: %s\n", strings.Join(summary.Agents, ", "))
	fmt.Println("---")

	if *step >= 0 {
		// Jump to the specified step.
		rs, err := session.StepTo(*step)
		if err != nil {
			return fmt.Errorf("step to %d: %w", *step, err)
		}
		printReplayStep(rs)
	} else {
		// Step through all events.
		for {
			rs, err := session.Step()
			if err != nil {
				break
			}
			printReplayStep(rs)
		}
	}

	return nil
}

// loadEvents reads events from a file or stdin.
func loadEvents(path string) ([]*events.Event, error) {
	var reader io.Reader

	if path == "" {
		reader = os.Stdin
	} else {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open file %s: %w", path, err)
		}
		defer f.Close()
		reader = f
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read input: %w", err)
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("input is empty")
	}

	var evts []*events.Event
	if err := json.Unmarshal(data, &evts); err != nil {
		return nil, fmt.Errorf("parse JSON events: %w", err)
	}

	return evts, nil
}

// inspectText prints a human-readable summary of the flight data.
func inspectText(taskID string, evts []*events.Event) error {
	tl := flight.NewTimeline()
	_ = flight.NewGraph()
	dl := flight.NewDecisionLog()
	de := flight.NewDiagnosticsEngine()

	for _, e := range evts {
		te := flight.TimelineEvent{
			ID:       e.ID,
			AgentID:  e.StreamID,
			Type:     mapEventType(e.Type),
			Name:     eventName(e),
			StartAt:  e.Timestamp,
			Metadata: e.Payload,
		}
		tl.Add(te)

		// Collect decisions from decision events.
		if e.Type == "decision" {
			d := flight.Decision{
				ID:        e.ID,
				AgentID:   e.StreamID,
				Type:      flight.DecisionType(stringOr(e.Payload, "type", "unknown")),
				Selected:  stringOr(e.Payload, "selected", ""),
				Reason:    stringOr(e.Payload, "reason", ""),
				Timestamp: e.Timestamp,
				Metadata:  e.Payload,
			}
			dl.Add(d)
		}

		// Collect diagnostics from error events.
		if e.Type == "error" {
			errMsg := stringOr(e.Payload, "error", "unknown error")
			cat := flight.ClassifyError(errMsg)
			suggestions := flight.SuggestFix(cat)
			suggestion := ""
			if len(suggestions) > 0 {
				suggestion = suggestions[0]
			}
			dr := flight.DiagnosticRecord{
				ID:         e.ID,
				AgentID:    e.StreamID,
				TaskID:     taskID,
				Category:   cat,
				RootCause:  errMsg,
				Suggestion: suggestion,
				Timestamp:  e.Timestamp,
			}
			de.Record(dr)
		}
	}

	// Print timeline summary.
	summary := tl.Summary()
	fmt.Printf("=== Flight Inspector: %s ===\n\n", taskID)
	fmt.Printf("Timeline Summary:\n")
	fmt.Printf("  Events:        %d\n", summary.EventCount)
	fmt.Printf("  Total Duration: %s\n", formatDuration(summary.TotalDuration))
	fmt.Printf("  Tool Duration:  %s (%.1f%%)\n", formatDuration(summary.ToolDuration), summary.ToolPercent)
	fmt.Printf("  LLM Duration:   %s (%.1f%%)\n", formatDuration(summary.LLMDuration), summary.LLMPercent)
	fmt.Printf("  Wait Duration:  %s (%.1f%%)\n", formatDuration(summary.WaitDuration), summary.WaitPercent)
	fmt.Println()

	// Print decisions.
	decisions := dl.All()
	if len(decisions) > 0 {
		fmt.Printf("Decisions (%d):\n", len(decisions))
		for _, d := range decisions {
			fmt.Printf("  [%s] agent=%s selected=%q reason=%q confidence=%.2f\n",
				d.Type, d.AgentID, d.Selected, d.Reason, d.Confidence)
		}
		fmt.Println()
	}

	// Print diagnostics.
	records := de.All()
	if len(records) > 0 {
		fmt.Printf("Diagnostics (%d):\n", len(records))
		for _, r := range records {
			fmt.Printf("  [%s] agent=%s cause=%q fix=%q\n",
				r.Category, r.AgentID, r.RootCause, r.Suggestion)
		}
		fmt.Println()
	}

	// Print event list.
	fmt.Printf("Events:\n")
	for i, e := range evts {
		fmt.Printf("  %d. [%s] %s (%s) @ %s\n",
			i, e.Type, eventName(e), e.StreamID, e.Timestamp.Format(time.RFC3339))
	}

	return nil
}

// inspectMermaid outputs the call graph as Mermaid.
func inspectMermaid(evts []*events.Event) error {
	g := buildGraph(evts)
	fmt.Println(g.ExportMermaid())
	return nil
}

// inspectDOT outputs the call graph as DOT.
func inspectDOT(evts []*events.Event) error {
	g := buildGraph(evts)
	fmt.Println(g.ExportDOT())
	return nil
}

// inspectJSON outputs the full event data as JSON.
func inspectJSON(evts []*events.Event) error {
	data, err := json.MarshalIndent(evts, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSON: %w", err)
	}
	fmt.Println(string(data))
	return nil
}

// buildGraph constructs a flight.Graph from events.
func buildGraph(evts []*events.Event) *flight.Graph {
	g := flight.NewGraph()
	nodeMap := make(map[string]*flight.GraphNode)

	for _, e := range evts {
		nodeID := e.ID
		if nodeID == "" {
			nodeID = fmt.Sprintf("evt-%d", e.Version)
		}

		nodeType := flight.NodeAgent
		switch e.Type {
		case "tool.call", "tool.result":
			nodeType = flight.NodeTool
		case "llm.call", "llm.result":
			nodeType = flight.NodeLLM
		}

		status := flight.StatusCompleted
		if e.Type == "error" {
			status = flight.StatusFailed
		}

		parentID := stringOr(e.Payload, "parent_id", "")

		node := &flight.GraphNode{
			ID:       nodeID,
			ParentID: parentID,
			Type:     nodeType,
			Name:     eventName(e),
			Status:   status,
			StartAt:  e.Timestamp,
			Metadata: e.Payload,
		}

		nodeMap[nodeID] = node
		g.AddNode(node)
	}

	// If no root was set (all nodes have parents), use the first event as root.
	if g.Root() == nil && len(nodeMap) > 0 {
		for _, n := range nodeMap {
			n.ParentID = ""
			g.AddNode(n)
			break
		}
	}

	return g
}

// mapEventType maps events.EventType to flight.EventType.
func mapEventType(t events.EventType) flight.EventType {
	switch t {
	case "agent.started":
		return flight.EventAgentStart
	case "agent.stopped":
		return flight.EventAgentEnd
	case "tool.call":
		return flight.EventToolCall
	case "tool.result":
		return flight.EventToolResult
	case "llm.call":
		return flight.EventLLMCall
	case "llm.result":
		return flight.EventLLMResult
	case "error":
		return flight.EventError
	case "memory.distilled", "memory.op":
		return flight.EventMemoryOp
	case "decision":
		return flight.EventDecision
	case "session.created", "task.created", "task.dispatched", "task.completed", "task.failed":
		return flight.EventType(t)
	default:
		return flight.EventType(t)
	}
}

// eventName extracts a human-readable name from an event.
func eventName(e *events.Event) string {
	if name, ok := e.Payload["name"].(string); ok && name != "" {
		return name
	}
	if tool, ok := e.Payload["tool"].(string); ok && tool != "" {
		return tool
	}
	if model, ok := e.Payload["model"].(string); ok && model != "" {
		return model
	}
	return string(e.Type)
}

// stringOr returns the string value of a key in m, or the fallback.
func stringOr(m map[string]any, key, fallback string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return fallback
}

// formatDuration formats a duration for human-readable output.
func formatDuration(d time.Duration) string {
	if d == 0 {
		return "0s"
	}
	if d < time.Millisecond {
		return fmt.Sprintf("%.0fus", float64(d.Microseconds()))
	}
	if d < time.Second {
		return fmt.Sprintf("%.1fms", float64(d.Milliseconds()))
	}
	return d.Truncate(time.Millisecond).String()
}

// printReplayStep prints a single replay step.
func printReplayStep(step *flight.ReplayStep) {
	fmt.Printf("Step %d: [%s] agent=%s @ %s\n",
		step.StepNum, step.EventType, step.AgentID,
		step.Timestamp.Format(time.RFC3339))
	if len(step.Payload) > 0 {
		data, err := json.MarshalIndent(step.Payload, "  ", "  ")
		if err == nil {
			fmt.Printf("  payload: %s\n", string(data))
		}
	}
}
