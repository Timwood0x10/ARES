// Package adapter provides bridges between existing ARES subsystems and AKF
// (the Agent Knowledge Graph).
//
// It implements KnowledgeRetriever — an adapter that exposes the AKG
// via the ContextRetriever interface so the chat-loop context builder
// can inject AKG knowledge into the LLM prompt — along with adapters
// for distillation, evolution, and memory integration.
package adapter
