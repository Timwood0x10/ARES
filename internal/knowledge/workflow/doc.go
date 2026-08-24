// Package workflow integrates AKF (ARES Knowledge Fabric) with the DAG
// workflow engine. It wraps KnowledgeRuntime as a base.Agent (Process /
// ProcessStream) so that AKF pipelines can be registered as workflow steps.
//
// Since the retired workflow engine was removed (fusion plan Phase A/B), the
// current DAG surface is sdk.Graph, whose nodes are *sdk.Agent, pure
// func(ctx, state) error nodes, or nested *sdk.Graph. KnowledgeAgent therefore
// also exposes AsGraphNode(): a func(ctx, state) error adapter so AKF steps
// (build_graph / compile) can be added directly to a sdk.Graph without going
// through the kernel scheduling path. The adapter reads its input from
// state["input"] and writes the step output to state["output"].
//
// Beta: this package is part of the AKG (Autonomous Knowledge Graph)
// subsystem and is currently BETA. The API is not yet stable and may
// change between minor releases. Do not depend on it in production
// without pinning a version. Feedback welcome.
package workflow
