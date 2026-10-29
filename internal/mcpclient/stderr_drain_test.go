package mcpclient

import (
	"bytes"
	"strings"
	"testing"
)

// TestStderrDrain_PrefixesAndTruncates: every forwarded line must say which server produced it, and
// a pathological line must not push everything else out of the log.
func TestStderrDrain_PrefixesAndTruncates(t *testing.T) {
	var out bytes.Buffer
	d := newStderrDrain("files", &out)

	if _, err := d.Write([]byte("starting\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := d.Write([]byte(strings.Repeat("x", maxStderrLine*2))); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "[mcp:files] starting") {
		t.Fatalf("line not attributed to its server: %q", got)
	}
	if !strings.Contains(got, "…(truncated)") {
		t.Fatalf("an over-long line must be truncated, not forwarded whole: %q", got)
	}
	if len(got) > maxStderrLine*2 {
		t.Fatalf("the drain forwarded %d bytes for a capped line", len(got))
	}
}

// TestStderrDrain_MutesAtTheVolumeCap: a child stuck in a print loop keeps writing into a pipe the
// parent must keep draining, or the child blocks. Draining and discarding is the only safe answer
// once the cap is reached.
func TestStderrDrain_MutesAtTheVolumeCap(t *testing.T) {
	var out bytes.Buffer
	d := newStderrDrain("noisy", &out)

	line := []byte(strings.Repeat("y", 1024) + "\n")
	for i := 0; i < (maxStderrBytes/1024)+16; i++ {
		if _, err := d.Write(line); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	got := out.String()
	if !strings.Contains(got, "stderr muted") {
		t.Fatal("reaching the cap must be announced once, not silently dropped")
	}
	if len(got) > maxStderrBytes+maxStderrLine*2 {
		t.Fatalf("the drain kept forwarding past its cap: %d bytes", len(got))
	}
}

// TestStderrDrain_HoldsPartialLines: exec writes in arbitrary chunks, so a partial line must not be
// emitted as if it were complete.
func TestStderrDrain_HoldsPartialLines(t *testing.T) {
	var out bytes.Buffer
	d := newStderrDrain("partial", &out)

	_, _ = d.Write([]byte("first half "))
	if got := out.String(); got != "" {
		t.Fatalf("a partial line was emitted early: %q", got)
	}
	_, _ = d.Write([]byte("second half\n"))

	if got := out.String(); !strings.Contains(got, "[mcp:partial] first half second half") {
		t.Fatalf("split line not reassembled: %q", got)
	}
}
