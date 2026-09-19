package feedback

import (
	"testing"
	"time"
)

// ── Outcome constants ───────────────────────────────────────────────────────

func TestOutcomeConstants(t *testing.T) {
	tests := []struct {
		name   string
		give   Outcome
		want   string
		wantOb bool
		wantSu bool
		wantSc float64
	}{
		{"success", OutcomeSuccess, "success", true, true, 1},
		{"failure", OutcomeFailure, "failure", true, false, 0},
		{"timeout", OutcomeTimeout, "timeout", true, false, 0},
		{"not_found", OutcomeNotFound, "not_found", true, false, 0},
		{"unobserved", OutcomeUnobserved, "", false, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.give) != tt.want {
				t.Errorf("string = %q, want %q", string(tt.give), tt.want)
			}
			if got := tt.give.Observable(); got != tt.wantOb {
				t.Errorf("Observable() = %v, want %v", got, tt.wantOb)
			}
			if got := tt.give.Succeeded(); got != tt.wantSu {
				t.Errorf("Succeeded() = %v, want %v", got, tt.wantSu)
			}
			if got := tt.give.Score(); got != tt.wantSc {
				t.Errorf("Score() = %v, want %v", got, tt.wantSc)
			}
		})
	}
}

// ── Outcome.Observable ──────────────────────────────────────────────────────

func TestOutcomeObservable(t *testing.T) {
	observable := []Outcome{OutcomeSuccess, OutcomeFailure, OutcomeTimeout, OutcomeNotFound}
	for _, o := range observable {
		if !o.Observable() {
			t.Errorf("%q should be observable", o)
		}
	}
	if OutcomeUnobserved.Observable() {
		t.Error("OutcomeUnobserved should NOT be observable")
	}
}

// ── Outcome.Succeeded ───────────────────────────────────────────────────────

func TestOutcomeSucceeded(t *testing.T) {
	if !OutcomeSuccess.Succeeded() {
		t.Error("OutcomeSuccess should succeed")
	}
	failed := []Outcome{OutcomeFailure, OutcomeTimeout, OutcomeNotFound, OutcomeUnobserved}
	for _, o := range failed {
		if o.Succeeded() {
			t.Errorf("%q should NOT succeed", o)
		}
	}
}

// ── Outcome.Score ───────────────────────────────────────────────────────────

func TestOutcomeScore(t *testing.T) {
	if s := OutcomeSuccess.Score(); s != 1 {
		t.Errorf("Score(success) = %v, want 1", s)
	}
	nonSuccess := []Outcome{OutcomeFailure, OutcomeTimeout, OutcomeNotFound, OutcomeUnobserved}
	for _, o := range nonSuccess {
		if s := o.Score(); s != 0 {
			t.Errorf("Score(%q) = %v, want 0", o, s)
		}
	}
}

// ── CollaborationKind constants ─────────────────────────────────────────────

func TestCollaborationKindConstants(t *testing.T) {
	if string(CollabRequest) != "request" {
		t.Errorf("CollabRequest = %q", CollabRequest)
	}
	if string(CollabSend) != "send" {
		t.Errorf("CollabSend = %q", CollabSend)
	}
	if CollabRequest == CollabSend {
		t.Error("CollabRequest and CollabSend should differ")
	}
}

// ── CollaborationOutcome struct ─────────────────────────────────────────────

func TestCollaborationOutcomeFields(t *testing.T) {
	latency := 250 * time.Millisecond
	co := CollaborationOutcome{
		Initiator: "agent-a",
		Target:    "agent-b",
		Topic:     "handoff-task",
		Kind:      CollabRequest,
		Outcome:   OutcomeSuccess,
		Latency:   latency,
	}

	if co.Initiator != "agent-a" {
		t.Errorf("Initiator = %q", co.Initiator)
	}
	if co.Target != "agent-b" {
		t.Errorf("Target = %q", co.Target)
	}
	if co.Topic != "handoff-task" {
		t.Errorf("Topic = %q", co.Topic)
	}
	if co.Kind != CollabRequest {
		t.Errorf("Kind = %q", co.Kind)
	}
	if co.Outcome != OutcomeSuccess {
		t.Errorf("Outcome = %q", co.Outcome)
	}
	if co.Latency != latency {
		t.Errorf("Latency = %v", co.Latency)
	}
}

// ── ToolCallOutcome struct ──────────────────────────────────────────────────

func TestToolCallOutcomeFields(t *testing.T) {
	latency := 100 * time.Millisecond
	tc := ToolCallOutcome{
		Tool:       "web_search",
		Caller:     "agent-x",
		Outcome:    OutcomeFailure,
		Latency:    latency,
		ToolStepID: "web_search#query=string",
	}

	if tc.Tool != "web_search" {
		t.Errorf("Tool = %q", tc.Tool)
	}
	if tc.Caller != "agent-x" {
		t.Errorf("Caller = %q", tc.Caller)
	}
	if tc.Outcome != OutcomeFailure {
		t.Errorf("Outcome = %q", tc.Outcome)
	}
	if tc.Latency != latency {
		t.Errorf("Latency = %v", tc.Latency)
	}
	if tc.ToolStepID != "web_search#query=string" {
		t.Errorf("ToolStepID = %q", tc.ToolStepID)
	}
}

func TestToolCallOutcomeEmptyCaller(t *testing.T) {
	tc := ToolCallOutcome{
		Tool:    "calculator",
		Outcome: OutcomeSuccess,
	}
	if tc.Caller != "" {
		t.Errorf("Caller = %q, want empty", tc.Caller)
	}
	if tc.ToolStepID != "" {
		t.Errorf("ToolStepID = %q, want empty", tc.ToolStepID)
	}
}

// ── Outcome type satisfies string ───────────────────────────────────────────

func TestOutcomeIsString(t *testing.T) {
	var o Outcome = "custom_outcome"
	if string(o) != "custom_outcome" {
		t.Errorf("custom outcome = %q", o)
	}
	// Custom outcomes are observable but not success
	if !o.Observable() {
		t.Error("custom outcome should be observable")
	}
	if o.Succeeded() {
		t.Error("custom outcome should not succeed")
	}
	if o.Score() != 0 {
		t.Errorf("Score = %v, want 0", o.Score())
	}
}
