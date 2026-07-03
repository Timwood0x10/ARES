// Package agent tests.
package agent

import (
	"testing"
)

func TestNew(t *testing.T) {
	s := New()
	if s == nil {
		t.Fatal("expected non-nil service")
	}
}

func TestListAgents(t *testing.T) {
	s := New()
	agents := s.ListAgents()
	if agents != nil {
		t.Fatal("expected nil from stub")
	}
}
