// Package elog provides structured logging with automatic module and method
// attribution. Each module creates a Logger once with its module name, then
// calls Debug/Info/Warn/Error with just the method name and message — no
// need to repeat "module", "genome", "method", "doEvolve" on every line.
package elog

import (
	"context"
	"log/slog"
)

// Logger wraps slog and prepends module+method attributes to every call.
type Logger struct {
	module string
}

// New creates a logger for the given module (e.g. "genome", "promotion", "adapter").
func New(module string) *Logger {
	return &Logger{module: module}
}

func (l *Logger) attrs(method string, extras []any) []any {
	r := make([]any, 0, len(extras)+4)
	r = append(r, "module", l.module, "method", method)
	r = append(r, extras...)
	return r
}

// Debug logs at Debug level with module+method attribution.
func (l *Logger) Debug(ctx context.Context, method, msg string, attrs ...any) {
	if ctx != nil {
		slog.DebugContext(ctx, msg, l.attrs(method, attrs)...)
	} else {
		slog.Debug(msg, l.attrs(method, attrs)...)
	}
}

// Info logs at Info level with module+method attribution.
func (l *Logger) Info(ctx context.Context, method, msg string, attrs ...any) {
	if ctx != nil {
		slog.InfoContext(ctx, msg, l.attrs(method, attrs)...)
	} else {
		slog.Info(msg, l.attrs(method, attrs)...)
	}
}

// Warn logs at Warn level with module+method attribution.
func (l *Logger) Warn(ctx context.Context, method, msg string, attrs ...any) {
	if ctx != nil {
		slog.WarnContext(ctx, msg, l.attrs(method, attrs)...)
	} else {
		slog.Warn(msg, l.attrs(method, attrs)...)
	}
}

// Error logs at Error level with module+method attribution.
// Unlike the other levels, Error requires an error parameter that is always attached.
func (l *Logger) Error(ctx context.Context, method, msg string, err error, attrs ...any) {
	all := make([]any, 0, len(attrs)+2)
	all = append(all, "error", err)
	all = append(all, attrs...)
	if ctx != nil {
		slog.ErrorContext(ctx, msg, l.attrs(method, all)...)
	} else {
		slog.Error(msg, l.attrs(method, all)...)
	}
}
