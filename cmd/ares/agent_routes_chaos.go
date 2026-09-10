// agent_routes_chaos — the /api/chaos surface (random-kill/kill-all/recover/
// stop) and the kernel-path chaos helpers it targets, split out of agent.go
// (M-C2). Handler bodies are moved verbatim; only the file boundary is new.
package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strings"

	"github.com/Timwood0x10/ares/internal/ares_security"
	"github.com/Timwood0x10/ares/internal/fabric/agent"
)

// routeChaos dispatches POST /api/chaos/{random-kill,kill-all,recover,stop};
// the admin-role RBAC and the X-Chaos-Token check live inside handleChaos.
func (h *actionHandler) routeChaos(w http.ResponseWriter, r *http.Request, princ *ares_security.Principal) {
	h.handleChaos(w, r, princ, strings.TrimPrefix(r.URL.Path, "/api/chaos/"))
}

// ── Chaos Engineering ────────────────────────────────────

func (h *actionHandler) handleChaos(w http.ResponseWriter, r *http.Request, princ *ares_security.Principal, chaosType string) {
	w.Header().Set("Content-Type", "application/json")

	// RBAC: destructive chaos (random-kill/kill-all/recover) requires the
	// admin permission; the emergency stop is guarded by its own X-Chaos-Token
	// below and is exempt. Previously every authenticated principal (operator
	// JWT or API key) could trigger them — the declared "chaos is RoleAdmin
	// only" policy was never enforced.
	if chaosType != "stop" && !ares_security.HasPermission(princ.Role, ares_security.PermAdmin) {
		h.auditAction("chaos-"+chaosType, "denied", princ, false)
		w.WriteHeader(http.StatusForbidden)
		writeJSON(w, map[string]any{"error": "insufficient role: chaos operations require admin permission"})
		return
	}

	switch chaosType {
	case "stop":
		// Emergency stop for the live chaos loop. The
		// endpoint is armed only when the process was started with a stop
		// token — an empty configured token means live chaos is not armed
		// and there is nothing to stop, so report 503 instead of silently
		// accepting.
		if h.chaosStopToken == "" {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprint(w, `{"error":"chaos stop endpoint not armed (stop_token empty)"}`) // best-effort body; status code carries the contract
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Chaos-Token")), []byte(h.chaosStopToken)) != 1 {
			h.auditAction("chaos-stop", "live-loop", princ, false)
			w.WriteHeader(http.StatusForbidden)
			_, _ = fmt.Fprint(w, `{"error":"invalid X-Chaos-Token"}`) // best-effort body
			return
		}
		liveChaosCtl.RequestStop()
		h.auditAction("chaos-stop", "live-loop", princ, true)
		_, _ = fmt.Fprint(w, `{"status":"stopping","message":"live chaos loop will exit"}`) // best-effort body

	case "random-kill":
		// Unified lifecycle: when the peer kernel exists, kill a fabric
		// agent so the death flows through the REAL kernel recovery chain
		// (agent.killed → lease expiry → requeue → replacement) instead of
		// the legacy runtime's resurrection.
		if h.kernel != nil {
			target, err := chaosKillRandomFabric(r.Context(), h.kernel)
			if err != nil {
				h.auditAction("chaos-random-kill", "unknown", princ, false)
				w.WriteHeader(http.StatusBadRequest)
				writeJSON(w, map[string]any{"error": err.Error()})
				return
			}
			h.auditAction("chaos-random-kill", target, princ, true)
			writeJSON(w, map[string]any{
				"chaos": "random-kill", "target": target, "success": true,
				"message": "chaos: killed fabric agent " + target + " (kernel recovery will resume its tasks)",
			})
			return
		}
		agents := h.mgr.ListAgents()
		if len(agents) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]any{"error": "no agents"})
			return
		}
		target := agents[rand.Intn(len(agents))]
		if err := h.mgr.StopAgent(r.Context(), target.ID); err != nil {
			h.auditAction("chaos-random-kill", target.ID, princ, false)
			w.WriteHeader(http.StatusInternalServerError)
			writeJSON(w, map[string]any{"error": err.Error()})
			return
		}
		h.auditAction("chaos-random-kill", target.ID, princ, true)
		writeJSON(w, map[string]any{
			"chaos": "random-kill", "target": target.ID, "success": true,
			"message": "chaos: killed random agent " + target.ID,
		})
	case "kill-all":
		if h.kernel != nil {
			killed, failed, err := chaosKillAllFabric(r.Context(), h.kernel)
			if err != nil {
				h.auditAction("chaos-kill-all", "unknown", princ, false)
				w.WriteHeader(http.StatusBadRequest)
				writeJSON(w, map[string]any{"error": err.Error()})
				return
			}
			h.auditAction("chaos-kill-all", strings.Join(killed, ","), princ, true)
			writeJSON(w, map[string]any{
				"chaos": "kill-all", "killed": killed, "failed": failed, "success": true,
			})
			return
		}
		agents := h.mgr.ListAgents()
		killed := make([]string, 0, len(agents))
		for _, a := range agents {
			if err := h.mgr.StopAgent(r.Context(), a.ID); err == nil {
				killed = append(killed, a.ID)
			}
		}
		// audit reflects whether ALL agents were stopped, not a blanket true.
		h.auditAction("chaos-kill-all", strings.Join(killed, ","), princ, len(killed) == len(agents))
		writeJSON(w, map[string]any{
			"chaos": "kill-all", "killed": killed, "success": len(killed) == len(agents),
		})
	case "recover":
		// Kernel semantics: what recovers is the TASK (durable intent), not
		// the agent (disposable cognition). Force one recovery sweep that
		// requeues every expired-lease task; the recovery loop spawns a
		// replacement executor on demand and resumes from checkpoint.
		if h.kernel != nil {
			requeued, err := chaosRecoverSweep(h.kernel)
			if err != nil {
				h.auditAction("chaos-recover", "unknown", princ, false)
				w.WriteHeader(http.StatusBadRequest)
				writeJSON(w, map[string]any{"error": err.Error()})
				return
			}
			h.auditAction("chaos-recover", strings.Join(requeued, ","), princ, true)
			writeJSON(w, map[string]any{
				"chaos": "recover", "recovered_tasks": requeued, "success": true,
				"message": "requeued expired-lease tasks; replacement executors resume from checkpoint",
			})
			return
		}
		agents := h.mgr.ListAgents()
		recovered := make([]string, 0, len(agents))
		needRecover := 0
		for _, a := range agents {
			if a.Status != "running" {
				needRecover++
				if err := h.mgr.RestartAgent(r.Context(), a.ID); err == nil {
					recovered = append(recovered, a.ID)
				}
			}
		}
		// audit reflects whether ALL down agents were recovered, not a blanket true.
		ok := needRecover == 0 || len(recovered) == needRecover
		h.auditAction("chaos-recover", strings.Join(recovered, ","), princ, ok)
		writeJSON(w, map[string]any{
			"chaos": "recover", "recovered": recovered, "success": ok,
		})
	default:
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, map[string]any{
			"error":     "unknown chaos type: " + chaosType,
			"available": []string{"random-kill", "kill-all", "recover"},
		})
	}
}

// ── Kernel-path chaos (unified lifecycle) ───────────────────────────
//
// The /api/chaos/* endpoints previously killed agents through the legacy
// ares_runtime manager pool, which has its own resurrection semantics — a
// SECOND lifecycle next to the kernel's agentfabric + aresrecovery pair.
// These helpers retarget chaos at the kernel fabric so an injected death
// exercises the REAL recovery chain: agent.killed → lease expiry → requeue →
// replacement executor resumes from checkpoint. The legacy mgr path remains
// only as a fallback for non-peer deployments.

// chaosKillRandomFabric kills one uniformly-chosen LIVE agent in the kernel's
// Agent Fabric and returns its id. Agents already dead (killed earlier) are
// skipped; killing an empty fabric is a caller-visible error.
func chaosKillRandomFabric(ctx context.Context, k *kernelHandle) (string, error) {
	if k == nil || k.agents == nil {
		return "", errors.New("peer mode: kernel agent fabric not wired")
	}
	live := liveFabricAgents(k.agents)
	if len(live) == 0 {
		return "", errors.New("peer mode: no live agents in the fabric")
	}
	target := live[rand.Intn(len(live))]
	if err := k.agents.Kill(ctx, target); err != nil {
		return "", fmt.Errorf("peer mode: kill %s: %w", target, err)
	}
	log.Info("peer mode: chaos killed agent — lease expiry + replacement recovery will follow", "target", target)
	return target, nil
}

// chaosKillAllFabric kills every LIVE agent in the kernel's Agent Fabric.
// It returns separate killed/failed lists because chaos engineering cares
// precisely about what did NOT die: a per-agent Kill error is logged AND
// surfaced instead of being silently skipped. err != nil is reserved for
// "the fabric itself is not wired", mirroring chaosKillRandomFabric.
func chaosKillAllFabric(ctx context.Context, k *kernelHandle) (killed, failed []string, err error) {
	if k == nil || k.agents == nil {
		return nil, nil, errors.New("peer mode: kernel agent fabric not wired")
	}
	killed = make([]string, 0)
	failed = make([]string, 0)
	for _, id := range liveFabricAgents(k.agents) {
		if kerr := k.agents.Kill(ctx, id); kerr != nil {
			log.Warn("peer mode: chaos kill-all failed", "id", id, "err", kerr)
			failed = append(failed, id)
			continue
		}
		killed = append(killed, id)
	}
	return killed, failed, nil
}

// chaosRecoverSweep forces one recovery sweep over the kernel's task fabric:
// every expired-lease task is requeued to READY so the scheduler (and, when
// no capable executor remains, the replacement factory) can pick it up.
//
// The two outcomes are deliberately distinct: an unwired recovery subsystem
// is an ERROR (operators must never see success with zero work done when the
// sweeper does not exist), while an empty result is a NORMAL response
// meaning nothing had expired. Returns the requeued task ids — the kernel
// recovers TASKS, not agents, because agents are disposable cognition and
// tasks are durable intent.
func chaosRecoverSweep(k *kernelHandle) ([]string, error) {
	if k == nil || k.recovery == nil {
		return nil, errors.New("peer mode: kernel recovery not wired")
	}
	requeued := k.recovery.RequeueExpiredLeases()
	if len(requeued) > 0 {
		log.Info("peer mode: chaos recover sweep requeued expired task(s)", "count", len(requeued))
	}
	return requeued, nil
}

// liveFabricAgents lists fabric ids that still resolve to a live agent
// (Get errors after Kill).
func liveFabricAgents(agents *agentfabric.Fabric) []string {
	live := make([]string, 0)
	for _, id := range agents.Agents() {
		if _, err := agents.Get(id); err == nil {
			live = append(live, id)
		}
	}
	return live
}
