// Package logger provides module-scoped structured logging helpers.
// Every module should create a logger to ensure all log entries include
// the module name and (when using the Logger type) the method name.
//
// Usage (recommended — includes method attribution):
//
//	var log = logger.New("genome")
//	slog.Info(ctx, "doEvolve", "evolution completed", "generation", 5)
//
// Simple usage (backward compatible — module only):
//
//	var log = logger.Module("runtime")
//	slog.Info("starting", "port", 8080)
package logger

import (
	"context"
	"log/slog"
)

// ---------------------------------------------------------------------------
// Module / ModuleWith — backward-compatible slog.Logger wrappers (module only)
// ---------------------------------------------------------------------------

// Module returns a slog.Logger that automatically includes the module name
// in every log entry. Use this in each module's init or constructor.
//
// Example:
//
//	var log = logger.Module("runtime")
//	slog.Info("starting", "port", 8080)
//	// Output: {"level":"INFO","msg":"starting","module":"runtime","port":8080}
func Module(name string) *slog.Logger {
	return slog.Default().With("module", name)
}

// ModuleWith returns a slog.Logger with the module name and additional
// context fields already attached.
//
// Example:
//
//	var log = logger.ModuleWith("memory", "tenant_id", tid)
func ModuleWith(name string, attrs ...any) *slog.Logger {
	attrs = append([]any{"module", name}, attrs...)
	return slog.Default().With(attrs...)
}

// ---------------------------------------------------------------------------
// Logger — structured logger with automatic module + method attribution
// ---------------------------------------------------------------------------

// Logger wraps slog and prepends module+method attributes to every call.
// Create one via New() — one instance per module is sufficient.
//
// The base slog.Logger is resolved lazily (slog.Default() at call time) so a
// later slog.SetDefault is picked up; tests inject a fixed base via
// NewWithBase (3.13#9: the Logger was previously
// untestable because every method resolved the package-level default with no
// injection seam).
type Logger struct {
	module string
	base   *slog.Logger // optional override; nil = slog.Default() at call time
}

// New creates a logger for the given module (e.g. "genome", "promotion",
// "scheduler", "runtime", "memory"). All log entries produced by this
// logger will automatically include "module" and "method" attributes.
func New(module string) *Logger {
	return &Logger{module: module}
}

// NewWithBase creates a module logger bound to a specific slog.Logger —
// the testability seam: point it at a logger backed by a capturing handler
// and every call site is observable without touching the global default.
func NewWithBase(module string, base *slog.Logger) *Logger {
	return &Logger{module: module, base: base}
}

// slog resolves the effective base logger.
func (l *Logger) slog() *slog.Logger {
	if l.base != nil {
		return l.base
	}
	return slog.Default()
}

func (l *Logger) attrs(method string, extras []any) []any {
	// An empty method (the *Context drop-in variants) omits the pair
	// entirely — "method":"" carried no information and polluted every
	// structured log line from those call sites.
	if method == "" {
		r := make([]any, 0, len(extras)+2)
		r = append(r, "module", l.module)
		return append(r, extras...)
	}
	r := make([]any, 0, len(extras)+4)
	r = append(r, "module", l.module, "method", method)
	r = append(r, extras...)
	return r
}

// Debug logs at Debug level with module+method attribution.
func (l *Logger) Debug(ctx context.Context, method, msg string, attrs ...any) {
	if ctx != nil {
		l.slog().DebugContext(ctx, msg, l.attrs(method, attrs)...)
	} else {
		l.slog().Debug(msg, l.attrs(method, attrs)...)
	}
}

// DebugContext logs at Debug level with context and module attribution.
// This mirrors slog.Logger.DebugContext for drop-in compatibility.
// Use Debug(ctx, method, msg, ...) when a method name is available.
func (l *Logger) DebugContext(ctx context.Context, msg string, attrs ...any) {
	l.slog().DebugContext(ctx, msg, l.attrs("", attrs)...)
}

// Info logs at Info level with module+method attribution.
func (l *Logger) Info(ctx context.Context, method, msg string, attrs ...any) {
	if ctx != nil {
		l.slog().InfoContext(ctx, msg, l.attrs(method, attrs)...)
	} else {
		l.slog().Info(msg, l.attrs(method, attrs)...)
	}
}

// InfoContext logs at Info level with context and module attribution.
// This mirrors slog.Logger.InfoContext for drop-in compatibility.
// Use Info(ctx, method, msg, ...) when a method name is available.
func (l *Logger) InfoContext(ctx context.Context, msg string, attrs ...any) {
	l.slog().InfoContext(ctx, msg, l.attrs("", attrs)...)
}

// Warn logs at Warn level with module+method attribution.
func (l *Logger) Warn(ctx context.Context, method, msg string, attrs ...any) {
	if ctx != nil {
		l.slog().WarnContext(ctx, msg, l.attrs(method, attrs)...)
	} else {
		l.slog().Warn(msg, l.attrs(method, attrs)...)
	}
}

// WarnContext logs at Warn level with context and module attribution.
// This mirrors slog.Logger.WarnContext for drop-in compatibility.
// Use Warn(ctx, method, msg, ...) when a method name is available.
func (l *Logger) WarnContext(ctx context.Context, msg string, attrs ...any) {
	l.slog().WarnContext(ctx, msg, l.attrs("", attrs)...)
}

// Error logs at Error level with module+method attribution.
// Unlike the other levels, Error requires an error parameter that is always attached.
func (l *Logger) Error(ctx context.Context, method, msg string, err error, attrs ...any) {
	all := make([]any, 0, len(attrs)+2)
	all = append(all, "error", err)
	all = append(all, attrs...)
	if ctx != nil {
		l.slog().ErrorContext(ctx, msg, l.attrs(method, all)...)
	} else {
		l.slog().Error(msg, l.attrs(method, all)...)
	}
}

// ErrorContext logs at Error level with context, error, and module attribution.
// This mirrors slog.Logger.ErrorContext for drop-in compatibility.
// Use Error(ctx, method, msg, err, ...) when a method name is available.
func (l *Logger) ErrorContext(ctx context.Context, msg string, err error, attrs ...any) {
	all := make([]any, 0, len(attrs)+2)
	all = append(all, "error", err)
	all = append(all, attrs...)
	l.slog().ErrorContext(ctx, msg, l.attrs("", all)...)
}
