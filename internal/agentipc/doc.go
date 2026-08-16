// Package agentipc implements the ARES Kernel IPC pillar (P4 of
// docs/ares-runtime.md): peer-to-peer agent communication primitives.
//
// Design invariants (ares-runtime.md §13):
//   - Agents are same-level cognitive processes — A ≡ B ≡ C; parent/child
//     does NOT restrict communication.
//   - IPC is the third context layer (Task Shared / Agent Private / IPC
//     Messages): "I found X" / "help me verify Y" / "your conclusion
//     conflicts with mine".
//   - Agents express intent (Send / Request / Delegate / Handoff / Subscribe);
//     the Kernel enforces delivery.
//
// This package provides the full IPC primitive set (Send / Request / Reply /
// Delegate / Handoff / Subscribe) as a peer-mesh message bus. It complements
// the existing agents/peer.Registry (a direct-delivery Send path) without
// replacing it: the legacy leader-dispatched path and the new peer IPC run
// side-by-side under a feature flag (P4 D4: parallel + gradual cutover).
package agentipc
