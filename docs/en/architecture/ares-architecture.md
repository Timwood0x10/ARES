# ARES Runtime Architecture & Data Flows (v0.3.0)

> This document is generated from a 2026-08-19 code walkthrough and reflects
> the **actual component topology** of `ares serve` (not a vision doc). See also
> `ares-runtime.md` (design) and `docs/operator/README.en.md` (runbook).

---

## 1. Overall Architecture

```mermaid
flowchart TB
    subgraph CLI["CLI layer (cmd/ares)"]
        Serve["ares serve"]
        Auth["ares auth token"]
    end

    subgraph HTTP["HTTP surfaces"]
        Console["monitoring console<br/>/console/* /api/*"]
        Dash["dashboard APIv2<br/>/evolution/* /observability/*"]
        AH["actionHandler<br/>/api/agents/:id/{kill,resume,retry}<br/>/api/chaos/* /api/tools/call"]
    end

    subgraph CORE["Runtime core"]
        Bs["ares_bootstrap.Bootstrap"]
        RS["ares_runtime.Manager<br/>(agent registry / recovery chain)"]
        EV["EventStore<br/>(archive-enabled)"]
        KQ["kernel loops<br/>quota / recovery / trace"]
        SCH["kernelScheduler<br/>(DAG→ReadyTasks→executor)"]
        IPC["agentipc<br/>bus + peer registry"]
        FAB["taskfabric<br/>(agentfabric execution)"]
        AR["aresrecovery<br/>GlobalTracer / FeedbackStore<br/>QuotaManager / Recovery"]
    end

    subgraph SVC["Supporting services"]
        LLM["LLM adapter<br/>(fallback chain)"]
        MCP["MCP Manager + tool registry"]
        MEM["MemoryManager<br/>(events / knowledge)"]
        DST["Distillation / AKG knowledge loop"]
        Evo["Evolution system<br/>(GA genome / coordinator)"]
        Cfg["ConfigStore<br/>(hot-reload)"]
        Sec["ares_security<br/>JWT + RBAC + Audit"]
    end

    subgraph OBS["Observability"]
        TR["GlobalTracer<br/>/observability/spans"]
        FT["FeedbackStore<br/>/evolution/feedback"]
        ET["EvolutionTracer<br/>/evolution/trajectory"]
        HW["console health page"]
    end

    Serve --> Bs
    Bs --> RS & EV & MCP & LLM & MEM & DST & Evo
    Bs --> KQ
    KQ --> SCH & AR & EV
    SCH --> FAB & IPC
    FAB --> LLM & MCP
    IPC --> AR
    AR --> TR & FT & ET
    Console --> RS & AR & Cfg & Sec
    AH --> RS & MCP & Sec
    Dash --> AR & MCP
    Cfg --> Serve
    Sec --> Console & AH
```

---

## 2. Task Execution Pipeline (Data Flow)

```mermaid
flowchart LR
    subgraph IN["Entry points"]
        API["actionHandler / console API"]
        AUTOP["autopilot injector<br/>(kernel.autopilot)"]
        IPCIN["peer IPC messages"]
    end

    subgraph KERNEL["Scheduling core"]
        DISP["kernelTaskDispatcher"]
        DAG["DAG ready set ReadyTasks"]
        SCH2["kernelScheduler.drain<br/>(concurrent bounded goroutines)"]
        SUB["fabricTaskMeta submit"]
    end

    subgraph EXEC["Execution"]
        AG["sub/leader Agent"]
        TOOL["MCP tools / SkillCatalog"]
        LLM2["LLM calls"]
    end

    subgraph OUT["Outputs"]
        RES["result written to task"]
        EVOUT["events → EventStore"]
        TRACE["GlobalTracer span"]
        MEMOUT["memory / knowledge distillation"]
    end

    API --> DISP
    AUTOP --> DISP
    IPCIN --> DISP
    DISP --> SUB --> DAG
    DAG --> SCH2
    SCH2 --> AG
    AG --> TOOL & LLM2
    AG --> RES
    RES --> EVOUT
    SCH2 --> TRACE
    AG --> MEMOUT

    EVOUT -. "subscribe" .-> KERNEL
    EVOUT --> AR["recovery loop<br/>(lease expiry / failure / preemption)"]
    AR --> DAG
```

---

## 3. Event Flow

```mermaid
flowchart LR
    subgraph PROD["Producers"]
        T1["task lifecycle"]
        A1["agent lifecycle"]
        M1["memory writes"]
        F1["flight recorder"]
    end

    STORE["EventStore<br/>(compactable + archive)"]

    subgraph CONS["Consumers"]
        KQ["kernel recovery loop"]
        KTR["kernel trace loop"]
        BR["bridgeEvents → PluginBus"]
        FB["FlightRecorder → EvidenceStore"]
        DASH2["dashboard subscription"]
        CON2["console events page"]
    end

    T1 & A1 & M1 & F1 --> STORE
    STORE --> KQ & KTR & BR & FB
    BR --> DASH2 & CON2
    FB --> EVID["evolution evidence"]
```

---

## 4. Auth & Audit Flow

```mermaid
flowchart TD
    REQ["Destructive request<br/>POST /api/agents/:id/kill etc."] --> AH2["actionHandler.checkAuth"]
    REQ --> GIN["gin requireAPIKey middleware<br/>(console /api destructive routes)"]

    AH2 --> JWT{"JWT valid?<br/>(HS256 + write perm)"}
    JWT -->|yes| PRIN["Principal(subject, role)"]
    JWT -->|no| APIK{"API key matches?"}
    APIK -->|yes| PRIN
    APIK -->|no| R401["401 reject"]
    PRIN --> ACT["execute action (kill/chaos/tool)"]
    ACT --> AUD["AuditLogger.Action<br/>(action/subject/target/ok)"]
```

---

## 5. Config Hot-Reload Flow (P1, snapshot-level)

```mermaid
flowchart LR
    YAML["ares.yaml"] --> WATCH["ConfigStore.Watch<br/>(fsnotify + 200ms debounce)"]
    WATCH -->|change| RELOAD["Reload(ctx, path)<br/>(validate + atomic replace)"]
    RELOAD -->|failure| HIST["record history ok:false<br/>keep last-good config"]
    RELOAD -->|success| CUR["Current() updated"]
    CUR --> ENDP["/runtime/config<br/>(redacted snapshot + history)"]
```

---

## 6. Component Responsibilities

| Component | Responsibility | Key dependencies |
|---|---|---|
| `ares_bootstrap` | Assembles all infrastructure (EventStore/Runtime/Memory/MCP/LLM/evolution/distillation) | cfg, EventStore |
| `ares_runtime.Manager` | Agent register/start/stop/recovery chain (lease/requeue) | EventStore, factories |
| `kernelTaskDispatcher` + `kernelScheduler` | Task submit → DAG ready → concurrent drain execution | EventStore, fabric |
| `taskfabric` | Task execution orchestration (preemption/retry/checkpoint) | agentfabric |
| `aresrecovery` | GlobalTracer / FeedbackStore / QuotaManager / recovery | EventStore |
| `agentipc` | Peer message bus (json+gzip evolution policy) | peer registry |
| `ares_security` | JWT(RBAC) + modular audit | stdlib, gin |
| `ares_config.ConfigStore` | Config snapshot + fsnotify hot-reload | fsnotify |
| `monitoring` | Console HTTP (health/actions/config/observability) | plugin, runtime, security |
| `dashboard` | APIv2 (evolution/observability WebSocket hub) | MCP, aresrecovery |

---

## 7. Mapping to the AgentOS Plan

| Plan item | Architecture element | Status |
|---|---|---|
| M1 Multi-agent collaboration | **Scheduler model (OS-thread semantics)**: shared ReadyTasks queue + capability-aware scoring + concurrent drain in `kernelScheduler`; agents are scheduled, not orchestrated, and never self-dispatch (self-dispatch removed in v0.3.0). The `agentipc` collaboration protocols (`DelegateToSpecialist`/`Pipeline`/`Orchestrate`) are wired into the production IPC bridge: `wireEvolutionIPC` dispatches by topic, so `delegate-task`/`pipeline-stage`/`orchestrate-worker` messages reaching a target agent run through its `Execute` capability and reply with the result. | ✅ both scheduler and collaboration protocols wired in prod |
| M2 Resource scheduling/isolation | `EvolutionAwareQuotaManager` + kernel quota loop | ✅ (cgroup deferred) |
| M3 Explainability/feedback | `EvolutionTracer` + `FeedbackStore` + dashboard endpoints | ✅ wired |
| M4 Global observability | `GlobalTracer` + `/observability/spans` + trace loop | ✅ wired |
| M6 Config hot-reload | `ConfigStore` + `/runtime/config` | ✅ (snapshot-level) |
| M7 Security/RBAC | `ares_security` (JWT+RBAC+Audit) | ✅ |
| M8 Versioning | `VERSION` + Makefile injection | ✅ |
| M10 Fault-injection e2e | arena→runtime recovery chain + `agentos_ci.yml` | ✅ (single-process) |
