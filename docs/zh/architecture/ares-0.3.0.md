# ARES 0.3.0 架构定义
**Verified Agent Evolution · 克制而坚固**

---

## 一、核心哲学

```
SELF-EVOLVING AGENT          →  ❌ 太危险，太难解释
VERIFIED AGENT EVOLUTION     →  ✅ 改了能证明变好了
```

### 一条原则

> 一个明确、反复出现、能定位到单一组件的问题，
> 应优先做最小、可审计、容易验证和回滚的局部修改。
> 只有局部修改长期解决不了问题，才上升到 Workflow / Harness / Optimizer。
> — 《AI Agents in Depth》第 8 章

### 四条不做

| 不做 | 原因 |
|------|------|
| ❌ 复杂 Agent Registry | `map[string]AgentProfile` 够用 |
| ❌ Message Bus | Handoff → Runtime State 即可 |
| ❌ A2A 协议 | 跨进程/跨组织协作，0.3.0 不用碰 |
| ❌ GA Evolution | 先 Evidence → Patch → Verify |
| ❌ Agent 自修改 Verifier/Evaluator/Safety Gate | 可信根必须在进化控制范围之外 |
| ❌ 自动创造新 Agent | 专业 Agent 由开发者定义（researcher/coder/reviewer） |

---

## 二、核心闭环

```
                 ┌─────────────┐
                 │    Task     │
                 └──────┬──────┘
                        ↓
              ┌───────────────────┐
              │  Agent Profile × N │   ← 专业化角色
              │  (Role/Instr/Tools)│
              └─────────┬─────────┘
                        ↓
              ┌───────────────────┐
              │   Handoff         │   ← 显式交接
              │   From → To       │
              └─────────┬─────────┘
                        ↓
                   Execution
                        ↓
                     Trace        ← 发生了什么
                        ↓
                   Evidence      ← 带证据的评价（非标量）
                  ┌────┴────┐
                  ↓         ↓
             Evaluation   Handoff
                  │
                  ↓
               Experience   ← 这次说明了什么
                  │
                  ↓
            Experience Distillation  ← Memory Distillation 归位
                  │
                  ↓
               Candidate    ← 候选修改（最小 diff）
                  │
           ┌──────┴──────┐
           ↓             ↓
      Verification    Reject
           │
      ┌────┴────┐
      ↓         ↓
  Promote    Stay Candidate
      │
      ↓
  Next Round Task
```

---

## 三、四个新概念

### ① AgentProfile（简单结构体）

```go
// internal/agents/profile.go

type AgentProfile struct {
    ID           string
    Role         string           // "researcher" / "coder" / "reviewer"
    Instructions string         // 角色指令（系统提示词）
    Tools        []string        // 该角色可用的工具名列表
}

// 用法：map 足够，不需要 Registry
var Profiles = map[string]*AgentProfile{
    "researcher": {
        Role:         "researcher",
        Instructions: "You find facts and verify claims...",
        Tools:        []string{"web_search", "read_webpage", "calculate"},
    },
    "coder": {
        Role:         "coder", 
        Instructions: "You write and test code...",
        Tools:        []string{"write_file", "execute_code", "run_tests"},
    },
    "reviewer": {
        Role:         "reviewer",
        Instructions: "You review code for correctness...",
        Tools:        []string{"read_file", "run_linter", "analyze"},
    },
}
```

**为什么简单就够：**
- Role 决定 System Prompt 切换点（第 10 章的多阶段角色转换）
- Tools 决定每个阶段可用工具集
- Instructions 是唯一需要进化的载体（短期）

---

### ② Handoff（显式交接）

```go
// internal/agents/handoff.go

type ArtifactRef struct {
    Path    string  // 文件路径或内存引用
    Type    string  // "file" / "data" / "summary"
    Summary string  // 一句话摘要（给下游 Agent 快速理解）
}

type Handoff struct {
    From      string         // 发送方 Role ID
    To        string         // 接收方 Role ID
    Task      string         // 当前任务描述
    Context   map[string]any // 显式传递的上下文（非全量历史）
    Artifacts []ArtifactRef  // 交接的产物
}
```

**关键设计决策：Handoff 不是 RPC，是状态转移。**

```
// 旧方式（隐式共享）：
Agent A 的完整对话历史 → 自动传给 Agent B
→ 上下文爆炸，信息污染

// 新方式（显式交接）：
Agent A 完成研究
→ 创建 Handoff{To: "coder", Context: {research_findings: ...}, Artifacts: [...]}
→ Runtime 切换到 Agent B 的 Profile
→ Agent B 只看到 Handoff 中的内容
```

**集成到现有 Dynamic DAG：**

```go
// 现有 DAG Node 增强
type DAGNode struct {
    ID       string
    Role     string       // 对应 AgentProfile.Role
    Tools    []string     // 继承 Profile.Tools
    Validator *Validator  // 可选的执行后验证
}

// 节点间边支持 Handoff 声明
type DAGEdge struct {
    From      string
    To        string
    HandoffFn func(result any) Handoff  // 自定义交接逻辑
}
```

---

### ③ Evidence（评价结果）

**核心改动：Evaluation 不再返回标量分数，返回带证据的结构化诊断。**

```go
// internal/eval/evidence.go

// Evidence 是一等公民，替代现有的 Score 概念
type Evidence struct {
    TaskID      string
    Role        string            // 哪个角色的执行被评估
    Verdict     Verdict           // pass / fail / uncertain
    Dimensions  []DimensionScore
    
    // 关键字段：每个维度的硬证据
    EvidenceChain []EvidenceItem
    
    Confidence  float64           // 整体置信度
    Source      string            // "result_verifier" / "process_verifier" / "rubric_judge"
}

type DimensionScore struct {
    Name     string
    Score    int
    Max      int
    Pass     bool
    Evidence []EvidenceItem  // 支撑证据
    Flag     string          // 如果有问题，这里是问题描述
}

type EvidenceItem struct {
    Type   string  // "test" / "tool_call" / "db_state" / "file" / "llm_quote"
    Name   string  // "unit_test_auth" / "refund_tool_called" / "turn-12"
    Status string  // "passed" / "failed" / "missing"
    Detail string  // 具体内容
}

type Verdict int
const (
    VerdictPass Verdict = iota
    VerdictFail
    VerdictUncertain
)
```

**对比现有 DimensionJudge：**

```json
// 现有（标量，无证据）
{"correctness": 3, "completeness": 2, "efficiency": 1, "safety": 2}

// 新版 Evidence（结构化，有证据链）
{
  "verdict": "fail",
  "confidence": 0.85,
  "dimensions": [
    {
      "name": "task_result",
      "score": 2, "max": 3,
      "pass": false,
      "evidence": [
        {"type": "tool_call", "name": "refund", "status": "called", 
         "detail": "调用成功但缺少身份验证前置检查"}
      ],
      "flag": "身份验证流程缺失"
    },
    {
      "name": "rule_compliance", 
      "score": 1, "max": 3,
      "pass": false,
      "evidence": [
        {"type": "tool_call", "name": "verify_identity", "status": "missing"}
      ],
      "flag": "未调用 verify_identity 工具即承诺退款"
    }
  ]
}
```

---

### ④ Candidate → Verify → Promote（安全进化管道）

```go
// internal/evolution/candidate.go

type CandidateKind int
const (
    CandidateInstruction CandidateKind = iota  // 修改 Role 指令
    CandidateSkill                                // 新增/修改 Skill
    CandidateTool                                 // 新增工具
)

type Candidate struct {
    ID          string
    Kind        CandidateKind
    TargetRole  string             // 影响哪个角色
    Diff        string             // 最小 diff（old → new）
    Reason      string             // 为什么需要这个修改
    EvidenceIDs []string           // 支撑证据的 Trace ID 列表
    CreatedAt   time.Time
    
    // 状态机
    Status      CandidateStatus
}

type CandidateStatus string
const (
    StatusCandidate  CandidateStatus = "candidate"    // 刚生成
    StatusVerified   CandidateStatus = "verified"     // 通过验证
    StatusRejected   CandidateStatus = "rejected"     // 验证失败
    StatusPromoted   CandidateStatus = "promoted"     // 已发布到稳定版
)
```

**五状态机（极简）：**

```
Candidate ──verify──→ Verified ──promote──→ Promoted
    │                                       │
    └──reject──→ Rejected ←─────────────────┘（失败案例回退到这里）
```

**Verify 门禁（三道关）：**

```go
type CandidateVerifier struct{}

func (v *CandidateVerifier) Verify(ctx context.Context, c *Candidate) error {
    // 关1: 静态检查（语法/结构）
    if err := v.staticCheck(c); err != nil {
        return fmt.Errorf("static check failed: %w", err)
    }
    
    // 关2: 失败案例回放（应该在边界案例上改善）
    if ok, err := v.replayFailureCases(c); err != nil || !ok {
        return fmt.Errorf("failure case replay failed")
    }
    
    // 关3: 保留案例回放（旧场景不能退化）
    if regressions := v.checkRegression(c); regressions > 0 {
        return fmt.Errorf("%d regression cases detected", regressions)
    }
    
    return nil
}
```

---

## 四、Experience Distillation（Memory Distillation 归位）

**重新定义三个概念：**

| 概念 | 含义 | 生命周期 |
|------|------|---------|
| **Trace** | 发生了什么（不可变事件流） | 持久化，用于审计 |
| **Experience** | 这次说明了什么（可迭代分析） | 可重新分析 |
| **Memory** | 以后应该记住什么（正式知识） | 经过验证后生效 |

**Distillation 管道：**

```
Trace (ares_events.Event[])
  ↓  单次运行分析
Experience (problem + solution + constraints)
  ↓  跨轨迹聚类 + 对比
Candidate Knowledge
  ↓  迁移测试通过
正式 Memory（写入 Experience Repository）
```

**关键约束（来自第 8 章）：**
- 一条经验必须得到 ≥2 条非失败轨迹支持才能成为正式知识
- 候选知识与正式知识分库存放，可随时重新归纳
- 原始 Trace 永远不可变

---

## 五、零碎东西的新位置

### Memory Distillation MCP → 进化管道的一部分

之前它是孤立的记忆模块，现在：

```
Execution → Trace → Evaluation → Experience
                                          ↓
                              Memory Distillation
                              (从 Experience 提取 Candidate Knowledge)
                                          ↓
                                    Verify → Promote
```

### Evolution Engine (DreamCycle/GA) → 降级为可选高级功能

- 0.3.0 不做 GA
- DreamCycle 暂时保留但不作为主力
- 主力进化模式：**Failure → Diagnosis → Patch → Verify**

### Dynamic DAG → 核心载体

DAG Node 直接承载 Agent Profile：

```yaml
# ares.yaml 示例
nodes:
  - id: planner
    role: planner
    tools: [ask_clarifying_question, save_requirement]
    
  - id: researcher  
    role: researcher
    tools: [web_search, read_webpage]
    on_exit: handoff_to(coder)
    
  - id: coder
    role: coder
    tools: [write_file, execute_code, run_tests]
    validator: code_quality_checker
    
  - id: reviewer
    role: reviewer
    tools: [read_file, run_linter]
    on_failure: handoff_back_to(coder)
```

### AHP / MCP / Knowledge → 继续作为基础设施

不变。它们是 Execution 层的依赖，不是 0.3.0 的主角。

---

## 六、0.3.0 交付物清单

### 新增（4个概念）

| # | 概念 | 文件 | 行数估计 |
|---|------|------|---------|
| 1 | AgentProfile | `internal/agents/profile.go` | ~50 |
| 2 | Handoff | `internal/agents/handoff.go` | ~80 |
| 3 | Evidence | `internal/eval/evidence.go` | ~120 |
| 4 | Candidate Pipeline | `internal/evolution/candidate.go` | ~200 |

### 改造（已有）

| # | 改动 | 文件 | 性质 |
|---|------|------|------|
| 1 | 三层验证器接入 | `internal/eval/result_verifier.go` | 新增底层+中层 |
| 2 | DimensionJudge → Evidence 格式 | `internal/eval/dimension_judge.go` | 格式升级 |
| 3 | Memory Distillation → Experience Distillation | `internal/ares_experience/` | 归位重命名 |
| 4 | DAG Node 支持 Role | `internal/workflow/` | 扩展 |
| 5 | Handoff 集成到 Leader Process | `internal/agents/leader/agent.go` | 增强 |
| 6 | Candidate 状态机 | `internal/evolution/candidate_store.go` | 新增 |

### 不做

- ❌ Agent Registry
- ❌ Message Bus
- ❌ A2A 协议
- ❌ GA Evolution
- ❌ Agent 自修改 Runtime
- ❌ 自动创建新 Agent
- ❌ VFS（推迟到 0.4.0）

---

## 七、验收标准

### P0 功能验收

- [ ] 能在 ares.yaml 中定义 3 个角色（researcher/coder/reviewer）
- [ ] DAG 执行时角色按顺序切换，每次切换携带 Handoff
- [ ] 执行完成后生成带证据链的 Evaluation（非标量）
- [ ] 连续 2 次以上同类失败能生成 Candidate Patch
- [ ] Candidate 必须通过 Verify（失败回放+保留回放）才能 Promote
- [ ] Promote 后下一轮任务使用新的 Profile

### P0 安全验收

- [ ] Evaluator/Verifier/Audit Log 不能被 Candidate 修改
- [ ] Stable Profile 目录与 Candidate 目录物理隔离
- [ ] 每次 Promote 写入不可变审计日志
- [ ] 支持回滚到上一版本 Profile

---

## 八、与 0.2.x 的关系

```
0.2.x: Runtime + MCP + Memory + Evolution(GA) + Chaos
                    ↓ 压缩 + 归位
0.3.0: Specialized Agent + Evidence + Verified Evolution
                    ↓
0.4.0: VFS + 外部挂载 + A2A + GA Evolution（可选）
```

**不变的：** Runtime、AHP、MCP、Knowledge Fabric、Chaos/Recovery
**压缩的：** Evolution Engine（GA 降级为可选）
**新增的：** AgentProfile、Handoff、Evidence、Candidate Pipeline
**归位的：** Memory Distillation → Experience Distillation

---

**维护者**：GoAgent Core Team
**版本**：0.3.0
**状态**：📝 架构冻结
