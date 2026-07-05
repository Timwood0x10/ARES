package main

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

func writeTestEvents(t *testing.T, path string) {
	t.Helper()

	now := time.Now()
	evts := []ares_events.Event{
		{ID: "e1", StreamID: "task-1", Type: ares_events.EventAgentStarted, Timestamp: now, Payload: map[string]any{"type": "leader"}},
		{ID: "e2", StreamID: "task-1", Type: "tool.call", Timestamp: now.Add(time.Second), Payload: map[string]any{"tool": "search"}},
		{ID: "e3", StreamID: "task-1", Type: "tool.result", Timestamp: now.Add(2 * time.Second), Payload: map[string]any{"result": "ok"}},
		{ID: "e4", StreamID: "task-1", Type: ares_events.EventAgentStopped, Timestamp: now.Add(3 * time.Second)},
	}

	data, err := json.Marshal(evts)
	if err != nil {
		t.Fatalf("marshal ares_events: %v", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write ares_events: %v", err)
	}
}

func TestRunInspectText(t *testing.T) {
	path := t.TempDir() + "/ares_events.json"
	writeTestEvents(t, path)

	err := runInspect([]string{"task-1", "--input", path, "--format", "text"})
	if err != nil {
		t.Fatalf("runInspect error: %v", err)
	}
}

func TestRunInspectMermaid(t *testing.T) {
	path := t.TempDir() + "/ares_events.json"
	writeTestEvents(t, path)

	err := runInspect([]string{"task-1", "--input", path, "--format", "mermaid"})
	if err != nil {
		t.Fatalf("runInspect error: %v", err)
	}
}

func TestRunInspectJSON(t *testing.T) {
	path := t.TempDir() + "/ares_events.json"
	writeTestEvents(t, path)

	err := runInspect([]string{"task-1", "--input", path, "--format", "json"})
	if err != nil {
		t.Fatalf("runInspect error: %v", err)
	}
}

func TestRunInspectDOT(t *testing.T) {
	path := t.TempDir() + "/ares_events.json"
	writeTestEvents(t, path)

	err := runInspect([]string{"task-1", "--input", path, "--format", "dot"})
	if err != nil {
		t.Fatalf("runInspect error: %v", err)
	}
}

func TestRunInspectMissingFile(t *testing.T) {
	err := runInspect([]string{"task-1", "--input", "/nonexistent"})
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestRunInspectNoTaskID(t *testing.T) {
	err := runInspect([]string{})
	if err == nil {
		t.Error("expected error for missing task ID")
	}
}

func TestRunReplayAll(t *testing.T) {
	path := t.TempDir() + "/ares_events.json"
	writeTestEvents(t, path)

	err := runReplay([]string{"task-1", "--input", path})
	if err != nil {
		t.Fatalf("runReplay error: %v", err)
	}
}

func TestRunReplayStep(t *testing.T) {
	path := t.TempDir() + "/ares_events.json"
	writeTestEvents(t, path)

	err := runReplay([]string{"task-1", "--input", path, "--step", "2"})
	if err != nil {
		t.Fatalf("runReplay error: %v", err)
	}
}

func TestRunReplayStepOutOfRange(t *testing.T) {
	path := t.TempDir() + "/ares_events.json"
	writeTestEvents(t, path)

	err := runReplay([]string{"task-1", "--input", path, "--step", "999"})
	if err == nil {
		t.Error("expected error for out-of-range step")
	}
}

func TestRunReplayNoEvents(t *testing.T) {
	path := t.TempDir() + "/empty.json"
	_ = os.WriteFile(path, []byte("[]"), 0644)

	err := runReplay([]string{"task-1", "--input", path})
	if err == nil {
		t.Error("expected error for no ares_events")
	}
}

func TestLoadEvents(t *testing.T) {
	path := t.TempDir() + "/ares_events.json"
	writeTestEvents(t, path)

	evts, err := loadEvents(path)
	if err != nil {
		t.Fatalf("loadEvents error: %v", err)
	}
	if len(evts) != 4 {
		t.Errorf("expected 4 ares_events, got %d", len(evts))
	}
}

func TestLoadEventsMissingFile(t *testing.T) {
	_, err := loadEvents("/nonexistent")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadEventsInvalidJSON(t *testing.T) {
	path := t.TempDir() + "/bad.json"
	_ = os.WriteFile(path, []byte("not json"), 0644)

	_, err := loadEvents(path)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestSeparateArgs(t *testing.T) {
	tests := []struct {
		name       string
		input      []string
		flags      []string
		positional []string
	}{
		{
			name:       "flags before positional",
			input:      []string{"--format", "text", "task-1"},
			flags:      []string{"--format", "text"},
			positional: []string{"task-1"},
		},
		{
			name:       "positional before flags",
			input:      []string{"task-1", "--format", "text"},
			flags:      []string{"--format", "text"},
			positional: []string{"task-1"},
		},
		{
			name:       "only positional",
			input:      []string{"task-1"},
			flags:      nil,
			positional: []string{"task-1"},
		},
		{
			name:       "only flags",
			input:      []string{"--format", "text"},
			flags:      []string{"--format", "text"},
			positional: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags, positional := separateArgs(tt.input)
			if len(flags) != len(tt.flags) {
				t.Errorf("flags len = %d, want %d", len(flags), len(tt.flags))
			}
			if len(positional) != len(tt.positional) {
				t.Errorf("positional len = %d, want %d", len(positional), len(tt.positional))
			}
		})
	}
}

func TestMapEventType(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"agent.started", "agent.start"},
		{"agent.stopped", "agent.end"},
		{"tool.call", "tool.call"},
		{"tool.result", "tool.result"},
		{"llm.call", "llm.call"},
		{"llm.result", "llm.result"},
		{"error", "error"},
		{"memory.distilled", "memory.op"},
		{"unknown.event", "unknown.event"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := mapEventType(ares_events.EventType(tt.input))
			if string(got) != tt.want {
				t.Errorf("mapEventType(%s) = %s, want %s", tt.input, got, tt.want)
			}
		})
	}
}
