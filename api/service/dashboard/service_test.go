// Package dashboard tests.
package dashboard

import (
	"testing"
)

func TestNew(t *testing.T) {
	d := New(nil, nil)
	if d == nil {
		t.Fatal("expected non-nil dashboard")
	}
}
