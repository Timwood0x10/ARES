// Package callbacks tests.
package callbacks

import (
	"testing"
)

func TestNewRegistry(t *testing.T) {
	r := New()
	if r == nil {
		t.Fatal("expected non-nil registry")
	}
}
