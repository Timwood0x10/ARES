package ares_archive

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Timwood0x10/ares/internal/logger"
)

// fileArchiveWriter persists RoundRecords as round_N.json files in a directory.
// Writes are atomic (temp file + rename) and safe for concurrent use.
type fileArchiveWriter struct {
	dir       string
	maxRounds int
	mu        sync.Mutex
	log       *slog.Logger
}

// NewFileArchiveWriter creates a writer that stores round files in dir.
//
// Validates that dir is non-empty (returning ErrEmptyDir otherwise) and
// creates the directory with MkdirAll (mode 0o755) when it does not yet exist.
// When maxRounds <= 0, rotation is disabled and unlimited rounds are retained.
//
// Args:
//   - dir: directory path for round_N.json files. Must be non-empty.
//   - maxRounds: maximum number of round files to retain. <= 0 disables rotation.
//
// Returns:
//   - *fileArchiveWriter: a ready-to-use writer.
//   - error: ErrEmptyDir when dir is empty, or a wrapped error when MkdirAll fails.
func NewFileArchiveWriter(dir string, maxRounds int) (*fileArchiveWriter, error) {
	if dir == "" {
		return nil, fmt.Errorf("new archive writer: %w", ErrEmptyDir)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("new archive writer: mkdir %q: %w", dir, err)
	}
	return &fileArchiveWriter{
		dir:       dir,
		maxRounds: maxRounds,
		log:       logger.Module("archive.writer"),
	}, nil
}

// RecordRound writes round_N.json atomically. The record must Validate.
// When MaxRounds is exceeded, the oldest rounds are deleted (rotation).
//
// Rotation errors are logged but never returned: rotation is best-effort
// housekeeping and must not fail an otherwise-successful write. Validation,
// context, and I/O errors ARE returned, wrapped with the round number.
func (w *fileArchiveWriter) RecordRound(ctx context.Context, record RoundRecord) error {
	if err := record.Validate(); err != nil {
		return fmt.Errorf("record round %d: %w", record.Round, err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("record round %d: %w", record.Round, err)
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.writeAtomic(record); err != nil {
		return fmt.Errorf("record round %d: %w", record.Round, err)
	}
	if w.maxRounds > 0 {
		if err := w.rotate(ctx); err != nil {
			w.log.Warn("record round: rotation failed (non-fatal)",
				"round", record.Round, "error", err)
		}
	}
	return nil
}

// Flush waits for any pending writes to complete. The file writer is
// synchronous, so this is a no-op except for honoring context cancellation.
func (w *fileArchiveWriter) Flush(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("flush archive: %w", err)
	}
	return nil
}

// writeAtomic marshals the record and writes it via temp-file + rename so
// readers never observe a partial file. Caller must hold w.mu.
//
// On any error the temp file is removed before returning, so no .tmp file
// is ever left behind on disk.
func (w *fileArchiveWriter) writeAtomic(record RoundRecord) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	tmp := filepath.Join(w.dir, fmt.Sprintf("round_%d.json.tmp", record.Round))
	final := filepath.Join(w.dir, fmt.Sprintf("round_%d.json", record.Round))

	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write temp %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %q -> %q: %w", tmp, final, err)
	}
	return nil
}

// rotate deletes the oldest round files when the count exceeds maxRounds.
// Caller must hold w.mu.
//
// Only fully-renamed round_N.json files are considered (round_N.json.tmp is
// excluded by the glob), so a file being written is never deleted. Parse
// failures on filenames are logged and skipped. Removal errors are logged but
// not returned — rotation is best-effort housekeeping. A glob error is
// returned to the caller, which logs it (non-fatal) but never returns it to
// the RecordRound caller.
func (w *fileArchiveWriter) rotate(_ context.Context) error {
	matches, err := filepath.Glob(filepath.Join(w.dir, "round_*.json"))
	if err != nil {
		return fmt.Errorf("glob rounds: %w", err)
	}

	rounds := make([]int, 0, len(matches))
	for _, m := range matches {
		n, ok := parseRoundFromName(m)
		if !ok {
			w.log.Warn("rotate: skipping unparseable filename", "file", m)
			continue
		}
		rounds = append(rounds, n)
	}
	sort.Ints(rounds)

	excess := len(rounds) - w.maxRounds
	for i := range excess {
		path := filepath.Join(w.dir, fmt.Sprintf("round_%d.json", rounds[i]))
		if err := os.Remove(path); err != nil {
			w.log.Debug("rotate: remove old round failed",
				"file", path, "error", err)
		}
	}
	return nil
}

// parseRoundFromName extracts the integer N from a "round_N.json" path.
// Returns (0, false) when the filename does not match the expected shape
// (e.g. "round_abc.json" or "round_1.json.tmp"). A non-positive N is rejected
// to avoid surfacing manually-placed bogus files via List/rotate.
func parseRoundFromName(path string) (int, bool) {
	base := filepath.Base(path)
	const prefix = "round_"
	const suffix = ".json"
	if !strings.HasPrefix(base, prefix) || !strings.HasSuffix(base, suffix) {
		return 0, false
	}
	middle := strings.TrimSuffix(strings.TrimPrefix(base, prefix), suffix)
	n, err := strconv.Atoi(middle)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}
