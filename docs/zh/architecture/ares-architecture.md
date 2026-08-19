# ARES 运行时架构与数据流（v0.3.0）

> 本文档由 2026-08-19 的代码梳理生成，反映 `ares serve` 的**实际组件拓扑**，
> 与代码一致（非愿景文档）。配套阅读：`ares-runtime.md`（设计）、
> `docs/operator/README.md`（运维）。

---

## 1. 总体架构图

```mermaid
flowchart TB
    subgraph CLI["CLI 层 (cmd/ares)"]
        Serve["ares serve"]
        Auth["ares auth token"]
    end

    subgraph HTTP["HTTP 暴露面"]
        Console["monitoring console<br/>/console/* /api/*"]
        Dash["dashboard APIv2<br/>/evolution/* /observability/*"]
        AH["actionHandler<br/>/api/agents/:id/{kill,resume,retry}<br/>/api/chaos/* /api/tools/call"]
    end

    subgraph CORE["运行时核心"]
        Bs["ares_bootstrap.Bootstrap"]
        RS["ares_runtime.Manager<br/>(agent 注册/恢复链)"]
        EV["EventStore<br/>(archive-enabled)"]
        KQ["kernel 三循环<br/>quota / recovery / trace"]
        SCH["kernelScheduler<br/>(DAG→ReadyTasks→executor)"]
        IPC["agentipc<br/>bus + peer registry"]
        FAB["taskfabric<br/>(agentfabric 执行)"]
        AR["aresrecovery<br/>GlobalTracer / FeedbackStore<br/>QuotaManager / Recovery"]
    end

    subgraph SVC["支撑服务"]
        LLM["LLM adapter<br/>(fallback 链)"]
        MCP["MCP Manager + tool registry"]
        MEM["MemoryManager<br/>(事件/知识)"]
        DST["蒸馏 / AKG 知识环"]
        Evo["进化系统<br/>(GA genome / coordinator)"]
        Cfg["ConfigStore<br/>(热重载)"]
        Sec["ares_security<br/>JWT + RBAC + Audit"]
    end

    subgraph OBS["可观测"]
        TR["GlobalTracer<br/>/observability/spans"]
        FT["FeedbackStore<br/>/evolution/feedback"]
        ET["EvolutionTracer<br/>/evolution/trajectory"]
        HW["console 健康页"]
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

## 2. 任务执行主链路（数据流）

```mermaid
flowchart LR
    subgraph IN["入口"]
        API["actionHandler / console API"]
        AUTOP["autopilot 注入器<br/>(kernel.autopilot)"]
        IPCIN["peer IPC 消息"]
    end

    subgraph KERNEL["调度核心"]
        DISP["kernelTaskDispatcher"]
        DAG["DAG 就绪集 ReadyTasks"]
        SCH2["kernelScheduler.drain<br/>(并发 bounded goroutines)"]
        SUB["fabricTaskMeta 提交"]
    end

    subgraph EXEC["执行"]
        AG["sub/leader Agent"]
        TOOL["MCP tools / SkillCatalog"]
        LLM2["LLM 调用"]
    end

    subgraph OUT["产出"]
        RES["结果写回 task"]
        EVOUT["事件 EventStore"]
        TRACE["GlobalTracer span"]
        MEMOUT["memory / 知识蒸馏"]
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

    EVOUT -. "订阅" .-> KERNEL
    EVOUT --> AR["recovery loop<br/>(lease 过期/失败/抢占)"]
    AR --> DAG
```

---

## 3. 事件流

```mermaid
flowchart LR
    subgraph PROD["生产者"]
        T1["task 生命周期"]
        A1["agent 生命周期"]
        M1["memory 写入"]
        F1["flight recorder"]
    end

    STORE["EventStore<br/>(compactable + archive)"]

    subgraph CONS["消费者"]
        KQ["kernel recovery loop"]
        KTR["kernel trace loop"]
        BR["bridgeEvents → PluginBus"]
        FB["FlightRecorder → EvidenceStore"]
        DASH2["dashboard 订阅"]
        CON2["console 事件页"]
    end

    T1 & A1 & M1 & F1 --> STORE
    STORE --> KQ & KTR & BR & FB
    BR --> DASH2 & CON2
    FB --> EVID["进化证据"]
```

---

## 4. 认证与审计流

```mermaid
flowchart TD
    REQ["破坏性请求<br/>POST /api/agents/:id/kill 等"] --> AH2["actionHandler.checkAuth"]
    REQ --> GIN["gin requireAPIKey 中间件<br/>(console /api 破坏性路由)"]

    AH2 --> JWT{"JWT 有效?<br/>(HS256 + write 权限)"}
    JWT -->|是| PRIN["Principal(subject, role)"]
    JWT -->|否| APIK{"API key 匹配?"}
    APIK -->|是| PRIN
    APIK -->|否| 401["401 拒绝"]
    PRIN --> ACT["执行动作 (kill/chaos/tool)"]
    ACT --> AUD["AuditLogger.Action<br/>(action/subject/target/ok)"]
```

---

## 5. 配置热重载流（P1，快照级）

```mermaid
flowchart LR
    YAML["ares.yaml"] --> WATCH["ConfigStore.Watch<br/>(fsnotify + 200ms debounce)"]
    WATCH -->|变更| RELOAD["Reload(ctx, path)<br/>(校验 + 原子替换)"]
    RELOAD -->|失败| HIST["记录历史 ok:false<br/>保留上次有效配置"]
    RELOAD -->|成功| CUR["Current() 更新"]
    CUR --> ENDP["/runtime/config<br/>(脱敏快照 + history)"]
```

---

## 6. 组件职责速查

| 组件 | 职责 | 关键依赖 |
|---|---|---|
| `ares_bootstrap` | 装配所有基础设施（EventStore/Runtime/Memory/MCP/LLM/进化/蒸馏） | cfg, EventStore |
| `ares_runtime.Manager` | agent 注册/启动/停止/恢复链（lease/requeue） | EventStore, factories |
| `kernelTaskDispatcher` + `kernelScheduler` | 任务提交 → DAG 就绪 → 并发 drain 执行 | EventStore, fabric |
| `taskfabric` | 任务执行编排（预占/重试/checkpoint） | agentfabric |
| `aresrecovery` | GlobalTracer / FeedbackStore / QuotaManager / recovery | EventStore |
| `agentipc` | peer 消息总线（json+gzip 演进策略） | peer registry |
| `ares_security` | JWT(RBAC) + 模块化审计 | stdlib, gin |
| `ares_config.ConfigStore` | 配置快照 + fsnotify 热重载 | fsnotify |
| `monitoring` | console HTTP（健康/动作/配置/可观测） | plugin, runtime, security |
| `dashboard` | APIv2（evolution/observability WebSocket hub） | MCP, aresrecovery |

---

## 7. 与 AGENTOS 计划的映射

| 计划项 | 对应架构元素 | 状态 |
|---|---|---|
| M1 多 Agent 协作 | **调度器模型（OS 线程语义）**：`kernelScheduler` 共享 ReadyTasks 队列 + capability 感知打分 + 并发 drain；agent 被调度而非编排，**从不自调度**（v0.3.0 移除自调度）。`agentipc` 协作协议（`DelegateToSpecialist`/`Pipeline`/`Orchestrate`）已接入生产 IPC bridge：`wireEvolutionIPC` 按 topic 分发，`delegate-task`/`pipeline-stage`/`orchestrate-worker` 到达目标 agent 时经其 `Execute` 执行并回 reply | ✅ 调度器 + 协作协议均生产已接线 |
| M2 资源调度/隔离 | `EvolutionAwareQuotaManager` + kernel quota loop | ✅（cgroup 暂缓） |
| M3 可解释性/反馈 | `EvolutionTracer` + `FeedbackStore` + dashboard 端点 | ✅ 已接线 |
| M4 全局可观测 | `GlobalTracer` + `/observability/spans` + trace loop | ✅ 已接线 |
| M6 配置热加载 | `ConfigStore` + `/runtime/config` | ✅（快照级） |
| M7 安全/RBAC | `ares_security`（JWT+RBAC+Audit） | ✅ |
| M8 版本化 | `VERSION` + Makefile 注入 | ✅ |
| M10 故障注入 e2e | arena→runtime 恢复链 + `agentos_ci.yml` | ✅（单进程） |
