package evolution

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// FileExperimentLog is an append-only ExperimentLog backed by a JSONL file.
//
// It exists because the only implementation was in-memory, and an experiment log that empties
// on restart cannot answer the question it was built for ("what did we try, and what
// happened"). JSONL rather than a database on purpose: one line per record is append-only by
// construction, human-inspectable when something looks wrong, and needs no migration.
//
// Append-only is enforced by the file mode (O_APPEND) and by never rewriting: the type exposes
// no Update/Delete, so a record that was written stays as written.
type FileExperimentLog struct {
	mu   sync.Mutex // guards f
	f    *os.File
	path string
}

// OpenFileExperimentLog opens (creating if needed) the log at path for appending.
func OpenFileExperimentLog(path string) (*FileExperimentLog, error) {
	if path == "" {
		return nil, fmt.Errorf("experiment log: path is required")
	}
	// The path is operator configuration (where this deployment keeps its experiment log),
	// never request input, and the file is opened by the process that owns it — this adds no
	// path-injection surface. Flagged only because the argument is a variable.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // operator-configured log path
	if err != nil {
		return nil, fmt.Errorf("experiment log: open %s: %w", path, err)
	}
	return &FileExperimentLog{f: f, path: path}, nil
}

// Append implements ExperimentLog. The record is fsynced before returning: a record that is
// only in the page cache is exactly the one a crash loses, and the crash is often the reason
// someone reads the log.
func (l *FileExperimentLog) Append(_ context.Context, record ExperimentRecord) error {
	if record.CandidateID == "" {
		return fmt.Errorf("experiment log: record has no candidate id")
	}
	line, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("experiment log: marshal record for %s: %w", record.CandidateID, err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("experiment log: append to %s: %w", l.path, err)
	}
	if err := l.f.Sync(); err != nil {
		return fmt.Errorf("experiment log: sync %s: %w", l.path, err)
	}
	return nil
}

// List implements ExperimentLog by reading the file back.
//
// A malformed line is an ERROR naming its number, not a skipped line: a record that cannot be
// parsed is either corruption or a writer bug, and silently dropping it would make the log
// answer "we never ran that" about an experiment it did run.
func (l *FileExperimentLog) List(_ context.Context) ([]ExperimentRecord, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	f, err := os.Open(l.path)
	if err != nil {
		return nil, fmt.Errorf("experiment log: read %s: %w", l.path, err)
	}
	defer func() { _ = f.Close() }() // read-only: nothing to flush

	var out []ExperimentRecord
	scanner := bufio.NewScanner(f)
	// Records carry a whole dimension map plus params; the default 64KB line limit is close
	// enough to a real record that a long one would be reported as a scanner error.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for n := 1; scanner.Scan(); n++ {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec ExperimentRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return nil, fmt.Errorf("experiment log: %s line %d is not a record: %w", l.path, n, err)
		}
		out = append(out, rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("experiment log: scan %s: %w", l.path, err)
	}
	return out, nil
}

// Close releases the file handle.
func (l *FileExperimentLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	if err != nil {
		return fmt.Errorf("experiment log: close %s: %w", l.path, err)
	}
	return nil
}
