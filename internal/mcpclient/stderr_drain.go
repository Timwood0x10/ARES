package mcpclient

import (
	"bytes"
	"fmt"
	"io"
	"sync"
)

const (
	// maxStderrLine bounds one forwarded line. A stack dump or a serializer that prints a whole
	// payload on one line would otherwise push everything else out of view.
	maxStderrLine = 4 << 10
	// maxStderrBytes bounds the total volume forwarded per server per process lifetime. Past it the
	// child is muted: a server stuck in a print loop must not be able to drown the parent's log.
	maxStderrBytes = 1 << 20
)

// stderrDrain forwards a child process's stderr to the parent with the server name attached.
//
// Without it, `cmd.Stderr = os.Stderr` mixed several servers' logs and stack traces into the
// parent's structured log stream with no attribution and no limit — during an incident that is the
// stream someone is trying to read. Attribution and a hard cap are the two things a child process
// cannot be trusted to provide itself.
type stderrDrain struct {
	name string
	out  io.Writer

	mu      sync.Mutex
	partial []byte
	written int
	muted   bool
}

func newStderrDrain(name string, out io.Writer) *stderrDrain {
	return &stderrDrain{name: name, out: out}
}

// Write implements io.Writer as required by exec.Cmd. Writes arrive line-buffered only by accident,
// so a partial line is held until its newline arrives.
func (d *stderrDrain) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.muted {
		return len(p), nil
	}
	d.partial = append(d.partial, p...)
	for {
		idx := bytes.IndexByte(d.partial, '\n')
		if idx < 0 {
			// A single line longer than the cap: forward what we have, keep draining the rest so the
			// child never blocks on a full pipe.
			if len(d.partial) > maxStderrLine {
				d.emit(d.partial[:maxStderrLine], true)
				d.partial = d.partial[:0]
			}
			break
		}
		d.emit(d.partial[:idx], false)
		d.partial = d.partial[idx+1:]
	}
	return len(p), nil
}

// emit writes one line, truncating it and muting the drain when the volume cap is reached.
func (d *stderrDrain) emit(line []byte, truncated bool) {
	if len(line) == 0 && !truncated {
		return
	}
	text := string(line)
	if len(line) > maxStderrLine {
		text = string(line[:maxStderrLine])
		truncated = true
	}
	if truncated {
		text += " …(truncated)"
	}
	prefix := fmt.Sprintf("[mcp:%s] ", d.name)
	// The prefix counts toward the cap: the cap exists to protect the parent's log, and the bytes
	// that reach it are the framed ones.
	if d.written+len(prefix)+len(text) > maxStderrBytes {
		d.muted = true
		_, _ = fmt.Fprintf(d.out, "%sstderr muted after %d bytes\n", prefix, d.written)
		return
	}
	d.written += len(prefix) + len(text)
	_, _ = fmt.Fprintf(d.out, "%s%s\n", prefix, text)
}
