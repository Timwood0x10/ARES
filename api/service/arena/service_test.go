// Package arena tests.
package arena

import (
	"testing"
)

func TestServiceNew(t *testing.T) {
	s := New(nil, nil, nil)
	if s == nil {
		t.Fatal("expected non-nil service")
	}
}

func TestServiceStats(t *testing.T) {
	s := New(nil, nil, nil)
	st := s.Stats()
	if st.TotalActions != 0 {
		t.Fatalf("expected 0, got %d", st.TotalActions)
	}
}

func TestServiceMetrics(t *testing.T) {
	s := New(nil, nil, nil)
	m := s.Metrics()
	if m.FailoverCount != 0 {
		t.Fatalf("expected 0, got %d", m.FailoverCount)
	}
}

func TestServiceReset(t *testing.T) {
	s := New(nil, nil, nil)
	s.Reset()
}

func TestServiceHistory(t *testing.T) {
	s := New(nil, nil, nil)
	h := s.History()
	if len(h) != 0 {
		t.Fatalf("expected empty, got %d", len(h))
	}
}

func TestActionTypeValues(t *testing.T) {
	tests := []struct {
		name string
		at   ActionType
	}{
		{"KillLeader", ActionKillLeader},
		{"KillAgent", ActionKillAgent},
		{"PauseAgent", ActionPauseAgent},
		{"NetworkPartition", ActionNetworkPartition},
		{"LLMFailure", ActionLLMFailure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.at) == "" {
				t.Fatal("action type should not be empty")
			}
		})
	}
}
