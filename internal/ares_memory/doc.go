// Package memory provides unified memory management for the StyleAgent framework.
//
// TODO(tech-debt): report generation from distilled knowledge was removed.
// The internal/ares_memory/report package (ReportGenerator/Formatter that
// rendered distillation output into a human-readable evolution report) and its
// sole consumer, the memory.Pipeline coordinator (pipeline.go), were deleted
// because both were dead code: memory.NewPipeline was never invoked on any
// serve/production path (the live path uses compiler.NewPipeline). If a
// human-readable evolution report is ever required, re-add a ReportGenerator
// and wire it into the live distillation path.
package memory
