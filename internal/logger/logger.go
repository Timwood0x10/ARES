// Package logger provides module-scoped structured logging helpers.
// Every module should create a logger via Module() to ensure all log entries
// include the module name for traceability.
package logger

import "log/slog"

// Module returns a slog.Logger that automatically includes the module name
// in every log entry. Use this in each module's init or constructor.
//
// Example:
//
//	var log = logger.Module("runtime")
//	log.Info("starting", "port", 8080)
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
