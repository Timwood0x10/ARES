// Package leader implements the legacy Leader-Agent orchestration model
// (planner → dispatcher → sub-agents).
//
// This package is the LEGACY model, superseded by the Peer Agent runtime
// (aresos-agentos-plan C1). All production paths run the peer runtime by
// default: a flat set of capability agents spawned into the Agent Fabric
// (agentfabric) and scheduled by the shared kernel scheduler. This package is
// retained ONLY behind the legacy gray switches (kernel.leader_enabled=true /
// kernel.policy=legacy) for backward compatibility and is frozen — it must
// not be extended (cmd/ares createAgents / createAndRegisterServeAgents are
// the only remaining callers).
//
// Removal milestone: scheduled for removal in v0.4.0 together with the leader
// gray switches. The migration path is fully exercised: cmd/ares serves the
// peer runtime by default, and the e2e suites (w2_peer_test,
// e2e_grand_loop_real_test) cover Leader OFF. Before removal, delete
// cmd/ares/agents.go createAgents, cmd/ares/serve_routine.go
// createAndRegisterServeAgents, the LeaderEnabled kernel config field, the
// LeaderEnabled gray switch in ares_config.KernelConfig, and this package's
// production references (its unit tests may be deleted or migrated
// alongside).
package leader
