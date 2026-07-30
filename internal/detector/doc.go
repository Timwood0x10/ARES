// Package detector discovers local environment services so the ARES SDK can
// bootstrap with zero configuration.
//
// # Boundaries
//
// Detection is strictly read-only: the package probes local ports and reads
// environment variables but starts no processes, writes no files, and performs
// no registration. It is safe to call from any bootstrap path, including
// short-lived CLI commands.
//
// # LLM provider selection
//
// Detect selects at most one LLM provider, in priority order:
//
//  1. Ollama reachable at http://localhost:11434/api/tags
//  2. A non-empty OPENAI_API_KEY environment variable
//  3. A non-empty ANTHROPIC_API_KEY environment variable
//
// The first match wins; later candidates are still recorded (their key flags
// are set) but never override the chosen provider.
//
// # Failure model
//
// Detect never panics and never hangs. Every HTTP probe runs under its own
// per-call timeout (capped at 2s) and the overall detection effort is bounded
// by the caller-supplied timeout. On timeout or error, Detect returns whatever
// it found so far; partial results are always usable.
package detector
