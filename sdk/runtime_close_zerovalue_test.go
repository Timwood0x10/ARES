package sdk

import "testing"

// TestRuntimeClose_ZeroValueDoesNotPanic: Close is public, and the documented flow panics in New so
// callers never receive a half-built Runtime — but the type is exported, so a zero value is
// constructible and Close must be a no-op rather than a panic. Every other optional subsystem in
// Close is already guarded; llmSvc was the one that was not.
func TestRuntimeClose_ZeroValueDoesNotPanic(t *testing.T) {
	var r Runtime

	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("Close on a zero-value Runtime panicked: %v", rec)
		}
	}()
	r.Close()
}
