// Package adapter provides bridges between existing ARES subsystems and AKF
// (the Agent Knowledge Graph).
//
// It implements KnowledgeRetriever — an adapter that exposes the AKG
// via the ContextRetriever interface so the chat-loop context builder
// can inject AKG knowledge into the LLM prompt — along with adapters
// for distillation, evolution, and memory integration.
//
// Beta: this package is part of the AKG (Autonomous Knowledge Graph)
// subsystem and is currently BETA. The API is not yet stable and may
// change between minor releases. Do not depend on it in production
// without pinning a version. Feedback welcome.
package adapter
