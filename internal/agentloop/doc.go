// Package agentloop implements the ReAct (reason+act) execution loop that
// drives an ARES agent through a sequence of LLM calls and tool executions.
//
// The Engine is the single execution path for agent runs: it owns the
// iterate→generate→tool-call→feed-back cycle that was previously inlined in
// sdk.Agent.Run. Extracting it here makes the loop independently testable with
// mock LLM, tool, memory, and event sinks, with no dependency on the sdk
// package (which depends on agentloop, so the reverse import would cycle).
//
// The Engine depends only on narrow consumer-side interfaces (LLMCaller,
// ToolExecutor, EventSink, MemorySink) so the concrete sdk.Runtime wiring can
// be substituted freely in tests. Behaviour — event payloads, version
// numbering, memory persistence, error wrapping, max-iteration handling —
// mirrors the original sdk.Agent.Run exactly.
package agentloop
