// Package memory tests.
package memory

import (
	"testing"
)

func TestNewNilConfig(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil service")
	}
}
