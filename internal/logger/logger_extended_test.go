package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
)

// captureHandler captures structured log entries into a buffer.
type captureHandler struct {
	buf   *bytes.Buffer
	level slog.Level
}

func newCaptureHandler(level slog.Level) (*captureHandler, *slog.Logger) {
	buf := &bytes.Buffer{}
	h := &captureHandler{buf: buf, level: level}
	return h, slog.New(h)
}

func (h *captureHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	entry := map[string]any{
		"level": r.Level.String(),
		"msg":   r.Message,
	}
	r.Attrs(func(a slog.Attr) bool {
		entry[a.Key] = a.Value.String()
		return true
	})
	b, marshalErr := json.Marshal(entry)
	if marshalErr != nil {
		return marshalErr
	}
	h.buf.Write(b)
	h.buf.WriteString("\n")
	return nil
}

func (h *captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	// Simple implementation: bake attrs into every subsequent record.
	return &attrHandler{parent: h, attrs: attrs}
}

func (h *captureHandler) WithGroup(_ string) slog.Handler { return h }

// attrHandler decorates a parent handler with fixed attributes.
type attrHandler struct {
	parent *captureHandler
	attrs  []slog.Attr
}

func (a *attrHandler) Enabled(_ context.Context, level slog.Level) bool {
	return a.parent.Enabled(context.Background(), level)
}

func (a *attrHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, attr := range a.attrs {
		r.AddAttrs(attr)
	}
	return a.parent.Handle(ctx, r)
}

func (a *attrHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	combined := append(append([]slog.Attr{}, a.attrs...), attrs...)
	return &attrHandler{parent: a.parent, attrs: combined}
}

func (a *attrHandler) WithGroup(_ string) slog.Handler { return a }

func parseLastEntry(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) == 0 || len(lines[len(lines)-1]) == 0 {
		t.Fatal("no log entries captured")
	}
	var entry map[string]any
	if err := json.Unmarshal(lines[len(lines)-1], &entry); err != nil {
		t.Fatalf("unmarshal log entry: %v", err)
	}
	return entry
}

// ── Module / ModuleWith ─────────────────────────────────────────────────────

func TestModuleReturnsLogger(t *testing.T) {
	l := Module("test-module")
	if l == nil {
		t.Fatal("Module returned nil")
	}
}

func TestModuleWithReturnsLogger(t *testing.T) {
	l := ModuleWith("test-module", "key", "value")
	if l == nil {
		t.Fatal("ModuleWith returned nil")
	}
}

// ── New / NewWithBase ───────────────────────────────────────────────────────

func TestNewCreatesLogger(t *testing.T) {
	l := New("test-module")
	if l == nil {
		t.Fatal("New returned nil")
	}
	if l.module != "test-module" {
		t.Errorf("module = %q, want test-module", l.module)
	}
	if l.base != nil {
		t.Error("base should be nil for New")
	}
}

func TestNewWithBaseCreatesLogger(t *testing.T) {
	_, slogLogger := newCaptureHandler(slog.LevelDebug)
	l := NewWithBase("test-module", slogLogger)
	if l == nil {
		t.Fatal("NewWithBase returned nil")
	}
	if l.module != "test-module" {
		t.Errorf("module = %q, want test-module", l.module)
	}
	if l.base == nil {
		t.Error("base should not be nil for NewWithBase")
	}
}

// ── attrs ───────────────────────────────────────────────────────────────────

func TestLoggerAttrsWithMethod(t *testing.T) {
	l := New("test-mod")
	attrs := l.attrs("TestMethod", []any{"key", "val"})
	// module, test-mod, method, TestMethod, key, val = 6 elements
	if len(attrs) != 6 {
		t.Fatalf("attrs len = %d, want 6", len(attrs))
	}
	if attrs[0] != "module" || attrs[1] != "test-mod" {
		t.Errorf("attrs[0:2] = %v, want [module test-mod]", attrs[0:2])
	}
	if attrs[2] != "method" || attrs[3] != "TestMethod" {
		t.Errorf("attrs[2:4] = %v, want [method TestMethod]", attrs[2:4])
	}
	if attrs[4] != "key" || attrs[5] != "val" {
		t.Errorf("attrs[4:6] = %v, want [key val]", attrs[4:6])
	}
}

func TestLoggerAttrsEmptyMethod(t *testing.T) {
	l := New("test-mod")
	attrs := l.attrs("", []any{"key", "val"})
	// module, test-mod, key, val = 4 elements (no method pair)
	if len(attrs) != 4 {
		t.Fatalf("attrs len = %d, want 4 (module + extras, no method)", len(attrs))
	}
	if attrs[0] != "module" || attrs[1] != "test-mod" {
		t.Errorf("attrs[0:2] = %v, want [module test-mod]", attrs[0:2])
	}
	// No "method" key should be present.
	for i := 0; i < len(attrs)-1; i++ {
		if attrs[i] == "method" {
			t.Error("method key should not be present for empty method")
		}
	}
}

func TestLoggerAttrsNoExtras(t *testing.T) {
	l := New("mod")
	attrs := l.attrs("Method", nil)
	if len(attrs) != 4 {
		t.Errorf("attrs len = %d, want 4", len(attrs))
	}
}

// ── Log levels ──────────────────────────────────────────────────────────────

func TestLoggerDebug(t *testing.T) {
	handler, slogLogger := newCaptureHandler(slog.LevelDebug)
	l := NewWithBase("test-mod", slogLogger)
	l.Debug(context.Background(), "TestMethod", "debug message", "key", "val")

	entry := parseLastEntry(t, handler.buf)
	if entry["msg"] != "debug message" {
		t.Errorf("msg = %v, want debug message", entry["msg"])
	}
	if entry["module"] != "test-mod" {
		t.Errorf("module = %v, want test-mod", entry["module"])
	}
	if entry["method"] != "TestMethod" {
		t.Errorf("method = %v, want TestMethod", entry["method"])
	}
}

func TestLoggerDebugNilContext(t *testing.T) {
	handler, slogLogger := newCaptureHandler(slog.LevelDebug)
	l := NewWithBase("test-mod", slogLogger)
	var nilCtx context.Context
	l.Debug(nilCtx, "Method", "msg")
	entry := parseLastEntry(t, handler.buf)
	if entry["msg"] != "msg" {
		t.Errorf("msg = %v, want msg", entry["msg"])
	}
}

func TestLoggerDebugContext(t *testing.T) {
	handler, slogLogger := newCaptureHandler(slog.LevelDebug)
	l := NewWithBase("test-mod", slogLogger)
	l.DebugContext(context.Background(), "debug ctx message", "key", "val")
	entry := parseLastEntry(t, handler.buf)
	if entry["msg"] != "debug ctx message" {
		t.Errorf("msg = %v", entry["msg"])
	}
	if entry["module"] != "test-mod" {
		t.Errorf("module = %v, want test-mod", entry["module"])
	}
}

func TestLoggerInfo(t *testing.T) {
	handler, slogLogger := newCaptureHandler(slog.LevelInfo)
	l := NewWithBase("test-mod", slogLogger)
	l.Info(context.Background(), "TestMethod", "info message", "key", "val")

	entry := parseLastEntry(t, handler.buf)
	if entry["msg"] != "info message" {
		t.Errorf("msg = %v, want info message", entry["msg"])
	}
	if entry["module"] != "test-mod" {
		t.Errorf("module = %v, want test-mod", entry["module"])
	}
}

func TestLoggerInfoNilContext(t *testing.T) {
	handler, slogLogger := newCaptureHandler(slog.LevelInfo)
	l := NewWithBase("test-mod", slogLogger)
	var nilCtx context.Context
	l.Info(nilCtx, "Method", "msg")
	entry := parseLastEntry(t, handler.buf)
	if entry["msg"] != "msg" {
		t.Errorf("msg = %v, want msg", entry["msg"])
	}
}

func TestLoggerInfoContext(t *testing.T) {
	handler, slogLogger := newCaptureHandler(slog.LevelInfo)
	l := NewWithBase("test-mod", slogLogger)
	l.InfoContext(context.Background(), "info ctx message", "key", "val")
	entry := parseLastEntry(t, handler.buf)
	if entry["msg"] != "info ctx message" {
		t.Errorf("msg = %v", entry["msg"])
	}
}

func TestLoggerWarn(t *testing.T) {
	handler, slogLogger := newCaptureHandler(slog.LevelWarn)
	l := NewWithBase("test-mod", slogLogger)
	l.Warn(context.Background(), "TestMethod", "warn message", "key", "val")

	entry := parseLastEntry(t, handler.buf)
	if entry["msg"] != "warn message" {
		t.Errorf("msg = %v, want warn message", entry["msg"])
	}
}

func TestLoggerWarnNilContext(t *testing.T) {
	handler, slogLogger := newCaptureHandler(slog.LevelWarn)
	l := NewWithBase("test-mod", slogLogger)
	var nilCtx context.Context
	l.Warn(nilCtx, "Method", "msg")
	entry := parseLastEntry(t, handler.buf)
	if entry["msg"] != "msg" {
		t.Errorf("msg = %v, want msg", entry["msg"])
	}
}

func TestLoggerWarnContext(t *testing.T) {
	handler, slogLogger := newCaptureHandler(slog.LevelWarn)
	l := NewWithBase("test-mod", slogLogger)
	l.WarnContext(context.Background(), "warn ctx message", "key", "val")
	entry := parseLastEntry(t, handler.buf)
	if entry["msg"] != "warn ctx message" {
		t.Errorf("msg = %v", entry["msg"])
	}
}

func TestLoggerError(t *testing.T) {
	handler, slogLogger := newCaptureHandler(slog.LevelError)
	l := NewWithBase("test-mod", slogLogger)
	testErr := errors.New("test error")
	l.Error(context.Background(), "TestMethod", "error message", testErr, "key", "val")

	entry := parseLastEntry(t, handler.buf)
	if entry["msg"] != "error message" {
		t.Errorf("msg = %v, want error message", entry["msg"])
	}
	if entry["error"] == nil {
		t.Error("error field should be present")
	}
}

func TestLoggerErrorNilContext(t *testing.T) {
	handler, slogLogger := newCaptureHandler(slog.LevelError)
	l := NewWithBase("test-mod", slogLogger)
	testErr := errors.New("test error")
	var nilCtx context.Context
	l.Error(nilCtx, "Method", "msg", testErr)
	entry := parseLastEntry(t, handler.buf)
	if entry["msg"] != "msg" {
		t.Errorf("msg = %v, want msg", entry["msg"])
	}
}

func TestLoggerErrorContext(t *testing.T) {
	handler, slogLogger := newCaptureHandler(slog.LevelError)
	l := NewWithBase("test-mod", slogLogger)
	testErr := errors.New("ctx error")
	l.ErrorContext(context.Background(), "error ctx message", testErr, "key", "val")
	entry := parseLastEntry(t, handler.buf)
	if entry["msg"] != "error ctx message" {
		t.Errorf("msg = %v", entry["msg"])
	}
}

// ── slog fallback ───────────────────────────────────────────────────────────

func TestLoggerSlogFallback(t *testing.T) {
	// New() without base — slog() should return slog.Default().
	l := New("test-mod")
	if l.slog() == nil {
		t.Error("slog() returned nil for New()")
	}
}
