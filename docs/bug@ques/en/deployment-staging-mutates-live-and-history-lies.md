# Deployment Pipeline: Staging Mutated Live State; Coordinator History Lied

Date: 2026-08-24
Scope: internal/ares_bootstrap, internal/evolution/deployment, patch.Registry

## Symptom

With `evolution.deployment.enabled=true` the staging→live safety gate was
decorative:

1. `deploymentStagingRuntime` and `deploymentLiveRuntime` shared the SAME
   `comp.NewEvolution.PatchReg`. Staging `Apply` invoked `reg.Apply` on the
   live registry — memory config, knowledge runtime and DAG recovery policies
   were mutated by patches that were then REJECTED.
2. Staging "rollback" re-applied the identical patch (`return &p` as its own
   rollback) — a no-op for idempotent patches, double-apply otherwise.
3. For ID-bearing patches the staging apply poisoned the shared idempotency
   map (`applied[patch.ID]=true`), so the later live promotion SILENTLY
   no-op'd — nothing was ever promoted.
4. `deploymentAdapter.Deploy` discarded `rec.Status`: a pipeline rejection is
   not an error, so the Coordinator recorded `PatchResult{Error: nil}` —
   operator-facing decision history reported success for rejected patches.

## Fix

- `patch.Registry.CanApply(target)`: read-only preflight.
- Staging Apply = preflight only (no state mutation); Rollback = bookkeeping
  no-op. Unknown targets still fail staging (same rejection class).
- `deploymentAdapter.Deploy` returns an error for any non-promoted outcome so
  the Coordinator's history reflects reality.

## Regression Tests

- `TestDeploymentStaging_DoesNotMutateLiveRegistry`
  (internal/ares_bootstrap/deployment_wiring_test.go): real MemoryPatchExecutor;
  after staging Apply the live MaxHistory is unchanged; orphan target rejected.

## Related closed-loop fixes (same review)

- `recovery.strategy` was never registered as a patch target while both
  `RecoveryDiffer` and the LLM adapter emit it → every recovery-strategy patch
  failed with "no executor registered". Registered alongside the other three
  recovery keys in `ProvideNewEvolution` and `UpdateLiveDAG`.
- `cmd/ares serve` passed `nil` StrategySource: GA-deployed strategies in
  `NewEvolution.StrategyStore` were consumed by NOTHING. Serve now wires
  `ares_bootstrap.NewStrategySource(...)` into every agent executor.
- Bootstrap's dashboard server was assembled with the shared M3/M4
  observability adapters but `Start` was never called anywhere: the endpoints
  (/evolution/trajectory, /evolution/feedback, /observability/spans) fed a
  server no one could reach. serve now starts it on cfg.Dashboard.Addr and
  stops it during graceful shutdown.
