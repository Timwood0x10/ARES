# ARES Capability Fabric — Skill 系统设计文档

> 状态：**已实施（2026-08-15）**——第一批：SkillCatalog 核心（SourceManager/Indexer/Discovery/Loader/ToolResolver/Experience）+ Catalog 门面 + 生产接线（serve.go wireSkillCatalog → memoryManager 常驻技能块）。第二批：`~/.ares/config.toml` `[[skill_sources]]` 解析、MCP 懒连接（`Catalog.Activate` → `ConnectServer`）、Experience JSON 持久化、envcap 聚合桥接、hash 变更检测（`DetectIndexChanges` + `Refresh`）。第三批：Git 源（`SyncGitSource`）、HTTP/OCI 源（`FetchHTTPManifest`）、SQLite FTS5 全文检索（`FTS5Index` + Discovery 回退）、MCP listChanged 增量重索引（`MCPManager.SetToolChangeHandler` → `Catalog.Refresh`）。第四批（code_review 修复，本日）：`Refresh` 与 `Build` 语义对齐（重拉 http 源 + 重建 FTS5 关旧索引 + 重 seed registry）、Catalog 全量 RWMutex 并发保护（写锁 swap / 读锁 Search·Load·All·ResolveTools）、git sync 2 分钟超时防阻塞启动、并发 Refresh 与 Build→Refresh 一致性测试（-race）。30 组测试守护，`make fmt && make check` 全绿（166 包）。
> 目标：ARES 0.3.0 的 Skill 不是"又一个 Skill 系统"，而是 **Capability Package**——
> 一个把专业化 Agent、Handoff、Evidence、MCP、本机工具、Token 优化统一串起来的小抽象。
> 命名克制：实现层只有 `SkillCatalog` / `SkillLoader` / `ToolResolver`，不引入
> SkillManager / SkillOrchestrator / SkillMarketplace 等大名词。

---

## 一、核心定位

**ARES Skill = Capability Package（能力包）**

```
Skill
├── SKILL.md        ← 告诉 Agent「这是什么能力、什么时候该用」（指令）
├── references/     ← 按需读取的深层参考资料
└── tools/          ← 执行载体声明（MCP / Executable / Builtin）
```

**Skill ≠ Tool**：

| | Skill | Tool |
|---|-------|------|
| 职责 | 告诉 Agent **怎么解决某类问题** | **实际执行某个动作** |
| 内容 | instructions + references + tools | 可调用能力（MCP server / 进程 / 内置函数） |
| 加载 | 渐进披露（metadata → body → resources） | 激活 Skill 后按需解析 |

---

## 二、五条设计原则（不可协商）

1. **不扫盘，只扫"声明过的 Source"**。ARES 永远不 `find /`、不扫 PATH、不扫 executable。
   只发现"被声明的东西"，不寻找"可能存在的东西"。
2. **Skill 是能力包，Tool 是执行载体**。两者边界守住，MCP/Executable/Builtin 才能统一为
   ToolProvider 而无体系分裂。
3. **不启动所有 MCP**。Skill 是 MCP Server 的 **lazy-loading boundary**：任务匹配 Skill →
   加载 Skill → 连接其声明的 MCP → tools/list → 只暴露相关工具。1000 tools 永不进 context。
4. **不把所有 Skill 内容塞给 LLM**。三层渐进披露：metadata（常驻）→ SKILL.md（触发后）→
   resources/tools（执行时）。
5. **发现、加载、执行、信任是四件不同的事**。Skill 可以被发现 ≠ 可以执行；
   `Discovery ≠ Permission`。

---

## 三、模块结构（实现层）

```
ARES
│
├── Runtime
├── Agent
├── Skill
│   ├── SkillCatalog   ← 知道有什么（discover + index + match）
│   ├── SkillLoader    ← 负责加载（load SKILL.md / references）
│   └── ToolResolver   ← 负责执行（MCP / Process / Builtin）
├── Tool
│   ├── MCP
│   ├── Executable
│   └── Builtin
└── Evolution
```

内部（SkillCatalog 子系统，就这些，不膨胀）：

```
SkillCatalog
├── SourceManager   ← 4 类 Source（project / user / registered / experience）
├── Indexer         ← Source → SkillIndexEntry（只存 metadata，不读 body）
├── Discovery       ← Index 检索（关键词 / 未来 FTS5）
├── Loader          ← SkillLoader：按 ID 加载 SKILL.md / references
└── ToolResolver    ← Skill 的 tools 声明 → MCP / Process / Builtin 可调用能力
```

明确**不要**（0.3.0）：SkillGraph、SkillRegistryService、SkillMarketplace、
SkillDependencyResolver、SkillOrchestrator、SkillLifecycleController。

---

## 四、Discovery Source（0.3.0 只支持 4 类）

```
                    Skill Discovery
                          │
        ┌─────────────────┼─────────────────┐
        ↓                 ↓                 ↓
   Explicit Source    Project Source    Learned Source
        │                 │                 │
   ~/.ares/skills      .ares/skills       Memory
   Registry            Manifest           Experience
        │                 │                 │
        └─────────────────┼─────────────────┘
                          ↓
                    Skill Index
                          ↓
                    Semantic Match
                          ↓
                     Load Skill
```

| Source | 位置 | 说明 |
|--------|------|------|
| **Project**（最重要） | `<project>/.ares/skills/<name>/SKILL.md` | 项目自声明；只读目录 metadata，**不执行 Skill** |
| **User** | `~/.ares/skills/<name>/SKILL.md` | 用户安装的全局能力；ARES 只知 `~/.ares/skills`，不知 Downloads/opt |
| **Registered** | `~/.ares/config.toml` 的 `[[skill_sources]]` | 显式声明的额外目录（未来可扩展 git/http/oci） |
| **Experience**（Learned） | 任务经验记录 | `{skill, task_pattern, success_rate}` relevance prior；**不是 LLM 生成 Skill** |

0.3.0 实现范围：**Project + User**（+ config.toml 的 directory 类型）；Git/HTTP/OCI 留接口。

`~/.ares/config.toml`：

```toml
[[skill_sources]]
type = "directory"
path = "~/.ares/skills"

[[skill_sources]]
type = "directory"
path = "~/my-company/ares-skills"

# 未来：
# [[skill_sources]]
# type = "git"
# url = "https://..."
```

---

## 五、Skill Index（解决"管理混乱"的关键）

**不要让 Runtime 每次 find/read/parse**。Indexer 在启动/变更时构建一次，只存 metadata：

```go
// SkillIndexEntry is the metadata-only index record (Level 0 of progressive
// disclosure). The SKILL.md body is deliberately NOT loaded here.
type SkillIndexEntry struct {
	ID           string   `json:"id"`           // e.g. "rust-review"
	Name         string   `json:"name"`         // "Rust Code Review"
	Description  string   `json:"description"`  // one-liner, always resident
	Keywords     []string `json:"keywords"`     // ["rust","ownership","unsafe"]
	Source       string   `json:"source"`       // "project" | "user" | "registered"
	Path         string   `json:"path"`         // ".ares/skills/rust-review"
	Version      string   `json:"version"`      // "1.0.0"
	Capabilities []string `json:"capabilities"` // e.g. ["code-review","security"]
	ToolTypes    []string `json:"tool_types"`   // ["mcp","executable"] — 来自 manifest
	Hash         string   `json:"hash"`         // content hash，变更检测
}
```

- 0.3.0 存储：**内存 index + JSON 持久化**（skill 数量小，不造向量库）；
  查询走关键词匹配，接口预留 FTS5。
- 查询示例：`"rust unsafe audit"` → `rust-review 0.92 / unsafe-audit 0.89 / cargo-security 0.51`，
  只把 **top-K metadata** 给 LLM。

---

## 六、三阶段渐进披露（与业界 Agent Skills 一致）

```
Level 0  Metadata    常驻：name / description / keywords        (~100 tokens / skill)
        ↓
Level 1  SKILL.md    任务匹配后加载：完整指令 + 何时使用         (按需)
        ↓
Level 2  Resources   执行时加载：references / tools 声明          (真正需要才读)
```

100 skills ≠ 100 × full instructions；而是 100 × ~100 tokens metadata → 3 × SKILL.md → 1 × tool。

---

## 七、Skill 结构（保持克制）

```
skills/
└── rust-review/
    ├── SKILL.md
    ├── references/
    │   └── rust-checklist.md
    └── tools/
        └── cargo-audit
```

SKILL.md 只负责：告诉 Agent **这个能力是什么、什么时候应该使用它**。

Skill manifest（tools 声明，统一 ToolProvider）：

```yaml
# .ares/skills/security-audit/skill.yaml
id: security-audit
name: Security Audit
description: Audit a codebase for OWASP Top 10 vulnerabilities.
keywords: [security, owasp, audit]
version: 1.0.0
tools:
  - id: semgrep
    type: executable
    command: semgrep
    args: ["--json"]
  - id: dependency-check
    type: executable
    command: dependency-check
  - id: github
    type: mcp
    server: github        # Skill 激活后才连接该 MCP server
  - id: filesystem
    type: builtin
    name: filesystem
```

---

## 八、ToolResolver：MCP / Executable / Builtin 统一

ARES 只需知道"我要一个可调用能力"，来源是 Provider 的事：

```go
// ToolKind classifies a resolved tool provider.
type ToolKind string

const (
	ToolMCP        ToolKind = "mcp"
	ToolExecutable ToolKind = "executable"
	ToolBuiltin    ToolKind = "builtin"
)

// ResolvedTool is a skill-declared tool bound to a runnable provider.
type ResolvedTool struct {
	ID    string
	Kind  ToolKind
	// MCP: server name; Executable: command; Builtin: registry name.
	Target string
	Args  []string
}
```

**MCP**：复用现有 `internal/ares_mcp` 的 tools/list 发现（标准协议，支持分页与
listChanged，ARES 不重新发明 MCP 内部发现）。Skill 激活后才连接。

**Executable**：来源是 **Skill 声明**，不是机器扫描。`cargo-audit` 由 manifest 声明，
ARES 验证存在性后 lazy spawn。

---

## 九、安全模型（提前留一个非常小的信任门）

```
Discovered → Declared → Trusted? → Allowed? → Executable
```

| source | 默认信任 | 说明 |
|--------|----------|------|
| builtin | trusted | 框架内置 |
| project | **trusted after project approval** | clone 的仓库可能 untrusted，仍需门控 |
| user | trusted（用户显式安装） | 可配 ask/allow/deny |
| external | untrusted | 发现可，执行需显式允许 |
| learned | **never executable automatically** | 只做 relevance prior，不自动执行 |

配置（最小，不做复杂 sandbox）：

```toml
[tools]
allow_local_executables = true
# 未来：ask / allow / deny
```

`~/.ares/skills/foo` 声明 `/usr/local/bin/random-script` 时，至少要有 permission/trust 检查；
project-local `./tools/foo` 可默认允许（项目内）。

---

## 十、主流程（与 0.3.0 主循环统一）

```
                User Task
                    │
                    ↓
             ┌─────────────┐
             │ SkillCatalog│   ← metadata search（只查 Index）
             └──────┬──────┘
                    │
              Top-K Skills
                    ↓
             ┌─────────────┐
             │ SkillLoader │   ← load SKILL.md（Level 1）
             └──────┬──────┘
                    ↓
               Agent Context
                    ↓
              Need Tool?
               /        \
             no          yes
             │            │
             │      ┌─────┴─────┐
             │      │ ToolResolver │
             │      └─────┬─────┘
             │            │
             │       ┌────┴────┐
             │       ↓         ↓
             │      MCP     Executable
             │       │         │
             └───────┴────┬────┘
                          ↓
                       Execute
                          ↓
                       Evidence
                          ↓
                      Experience
                          ↓
                       Verify → Improve
```

对应 0.3.0 主循环：**Specialize → Discover Skill → Load on Demand → Execute → Evidence →
Experience → Verify → Improve**。

---

## 十一、Experience（Learned Source）—— 与 Evidence-Driven 路线统一

不造新机制，只记录 relevance prior：

```go
// ExperienceRecord maps a task pattern to a useful skill with a success rate.
type ExperienceRecord struct {
	Skill       string  `json:"skill"`        // "pdf-generation"
	TaskPattern string  `json:"task_pattern"` // "document-to-pdf"
	SuccessRate float64 `json:"success_rate"` // 0.94
}
```

流程：Agent 发现任务需要能力 → 无匹配 skill → 用户安装 → 成功 → 记录
`{skill, task_pattern, success_rate}`。以后同任务模式优先匹配该 skill。
（可复用现有 `internal/ares_evolution/refine` / feedback_recorder 的持久化模式。）

---

## 十二、与现有代码的衔接（不推倒重来）

| 现有组件 | 演进方向 |
|----------|----------|
| `internal/knowledge/skills.Registry`（渐进披露 List/LoadDetail/Search） | → SkillCatalog 的 Level-0 metadata 存储 + Index 查询 |
| `internal/tools/envcap.Searcher`（统一检索工具/skills/命令） | → ToolResolver 的查询前端（匹配后 resolve 到 provider） |
| `internal/tools/discovery`（本机命令 allowlist） | → Executable Provider 的验证/惰性 spawn 基础（**必须只读声明来源，不扫盘**） |
| `internal/ares_mcp`（MCP tools/list） | → MCP Provider（Skill 激活后连接） |
| `internal/ares_evolution/refine` / feedback_recorder | → Experience 记录的持久化模式参考 |

---

## 十三、0.3.0 范围与后续

**本次（0.3.0）**：
- 4 类 Source 中的 **Project + User + Registered(directory)**；Experience 记录最小版
- `SkillCatalog`（SourceManager + Indexer + Discovery）+ `SkillLoader` + `ToolResolver`
- JSON 内存 Index；关键词匹配；`~/.ares/config.toml`
- 信任门控：`allow_local_executables` + source trust 分级

**后续（不阻塞 0.3.0）**：
- Git / HTTP / OCI / GitHub / Plugin Registry 源（SourceManager 已留 type 扩展点）
- SQLite FTS5 全文检索
- MCP listChanged 增量重索引
- skill 版本升级 / 变更检测（Index 已有 hash 字段）

---

## 十四、验收标准

1. ARES 启动只读 4 类声明 Source，**零扫盘**（无 find/glob 全盘、无 PATH 扫描）。
2. 100 skills 场景下 LLM context 只含 metadata（Level 0），SKILL.md 按需加载。
3. MCP server 仅在 Skill 激活后连接，tools/list 结果不预塞 context。
4. Executable 仅来自 Skill manifest 声明 + 信任门控，拒绝未声明可执行文件。
5. `Discovery ≠ Permission`：learned/external skill 可被索引到但不可自动执行。
6. `make fmt && make check` 全绿。

---

## 十五、设计原则速记（最终）

> ① 不扫盘，只扫声明过的 Source。
> ② 不把 Skill 当 Tool：Skill 是能力包，Tool 是执行载体。
> ③ 不启动所有 MCP：Skill 激活后才连接。
> ④ 不塞全部内容：metadata → body → resources 渐进披露。
> ⑤ 不让自主发现绕过安全：发现、加载、执行、信任四件事分离。
