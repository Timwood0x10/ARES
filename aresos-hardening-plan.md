# ARES Agent OS 收尾加固计划（v1 · 冻结架构后的硬化）

> 基准：`aresos-plan.md`（W1-W5 全绿）、`aresos-agentos-plan.md`（v3 单一调度，里程碑 1-5 全绿）
> 前提：两份计划的功能项均已完成并对齐；本计划**不新增功能**，只做收尾、加固、清债。
> 指导原则：不违反 `aresos-agentos-plan.md` §0 的冻结规则（Rule 1-7）；遵循 `plan/rules/code_rules_v2.md`（修 bug 先写复现测试、禁裸 goroutine、goroutine 必 recover、不擅自 git commit、破坏性变更走灰度）。
> 日期：2026-08-22 | 分支：dev

---

## 0. 背景与动机

最近一次开发（标题同名的两个 commit `c3371b0c` → `3aabb450`）冻结了 Agent OS parity 架构并修复了 F1 调度反馈。功能已闭环，但 code review 暴露出四类"债"，本计划逐一清偿：

1. **提交历史脏**：`c3371b0c` 引入了 `.orig`/临时文件（`serve_routine.orig.go` +720、`serve_dag.go` +285），`3aabb450` 又把它们删掉（−11384 行），两个 commit 同名、内容互补。历史里存在过备份文件入库。
2. **关键 bug fix 缺回归测试**：`LoadTracker.End()` 的 `load--`（防调度饥饿）是 F1 的核心修复，但 `internal/kernelscheduler/` 下**没有**任何针对 load 释放的单元测试（`grep 'func Test' | grep -i load` 为空）。将来有人误删 `load--` 不会有测试报警。
3. **未做全量竞态验证**：这次删除了 ~11384 行（含 leader 整包），只验证了核心 4 个包。未跑全量 `go test -race ./...`，无法确认没有悬空引用/竞态。
4. **leader 残留符号（休眠债）**：`aresos-agentos-plan.md` C1 明确留待后续的重命名项仍在生产代码中：
   - `models.AgentTypeLeader = "leader"`（`internal/core/models/types.go:52`）+ 转发别名（`api/agent/agent.go:31`）
   - `AresMemoryManager.GetLatestSessionForLeader` 接口方法 + 两处实现 + 调用点（`internal/ares_runtime/manager_lifecycle.go:308`）
   - Postgres `leader_checkpoints` 表（现仅存在于 `internal/ares_integration/*_test.go`）
   - `agentipc.PolicyLegacyLeader` 常量（现仅存在于 `internal/agentipc/bus_test.go`）

---

## 1. 任务分解

### 阶段 H1 — 零风险加固（先做，不碰历史、不改语义）

- [ ] **H1.1 补 F1 load 释放回归测试**
  - 文件：新建 `internal/kernelscheduler/load_tracker_test.go`
  - 断言 1：`Begin` 后 `Load==1`，`End` 后 `Load==0`（跑完一个 quantum 槽位必须释放）。
  - 断言 2：多轮 `Begin/End` 后 `Load` 不单调递增（复现被修的饥饿 bug：删掉 `load--` 时此断言必须失败）。
  - 断言 3：`End` 在 `load==0` 时不会下溢到负数（`if t.load[agentID] > 0` 分支）。
  - 断言 4：`SetAgentConfidence(id, -1)` 清除 override 回落历史成功率；`SetAgentConfidence(id, 0.0)` 保留为有效 0（0% 成功率排底，F1 GA 契约）。
  - 断言 5：`SetCapabilityConfidence` 负值清除、`ConfidenceFor` 的 capability > agent > 历史 > 1.0 回退顺序。
  - 断言 6（并发）：`-race` 下并发 `Begin/End/SetAgentConfidence` 无数据竞争。

- [ ] **H1.2 补 Score×Load 集成断言（防回归的第二道门）**
  - 文件：`internal/kernelscheduler/` 新增或复用现有 test。
  - 断言：同一 agent 连续被调度多轮后，`taskfabric.Score` 因 `load` 已释放仍 `> 0`（端到端复现 F1："later rounds get no capable candidate" 不再发生）。

- [ ] **H1.3 全量竞态与测试**
  - `go test -race ./...`，记录结果。
  - 若有 flaky/竞态，逐个定位修复（修复前先写复现测试，遵循 code_rules_v2）。
  - `gofmt -l .` 空、`go vet ./...` 通过、`go build ./...` 通过（重新全量确认）。

- [ ] **H1.4 CHANGELOG 补记**
  - 在 `## [Unreleased]` 下补一条 `### Fixed`：记录 F1 `LoadTracker.End()` load 释放修复 + 新增回归测试。

### 阶段 H2 — 提交历史整理（破坏性，需用户拍板）

- [ ] **H2.1 确认 push 状态**
  - `git log origin/dev..dev` 判断 `c3371b0c`/`3aabb450` 是否已推到共享分支。
  - **若已 push**：不 rebase，改用一个新的 clean commit 说明现状（不改历史）。
  - **若未 push**：向用户确认后 `git rebase -i`，把两个同名 commit 合并为一个，标题重写为能反映真实意图的内容（例：`refactor(ares): remove legacy leader package (~11k LOC) and fix F1 load-release scheduling starvation`）。
  - **本阶段任何 git 历史改写操作必须先经用户明确同意**（code_rules_v2：不擅自 git commit / 破坏性变更走灰度）。

### 阶段 H3 — leader 残留符号清债（休眠债，需 DB 迁移，谨慎灰度）

> 冻结规则约束：这些符号是 Rule 4 允许的 migration boundary 遗留，清理时**不得**重新引入 leader 作为 Kernel 角色。目标是"改名去角色化"，不是"恢复功能"。

- [x] **H3.1 影响面盘点**
  - 输出一份清单：每个符号的定义点、实现点、调用点、测试点、DB schema 依赖。
  - 判定每项是"可直接删"还是"需保留但改名/去 leader 语义"。
  - **结果（2026-08-24）**：`GetLatestSessionForLeader`（接口+2 实现+1 调用点+Err 常量+7 个测试点）→ 改名；`leader_checkpoints` 表（migrate+base_repository+SQL+集成测试）→ 改名；`AgentTypeLeader`（定义+别名+31 处测试引用，生产零引用）→ 直接删；`PolicyLegacyLeader`（定义+Dispatch/IsLegacy 休眠分支+6 处测试引用）→ 改名保留休眠分支。

- [x] **H3.2 `GetLatestSessionForLeader` → 去 leader 化**
  - 接口 `MemoryManager`（`internal/ares_memory/manager.go:71`）+ 实现（`manager_impl.go`、`production_manager.go`）+ 调用点（`manager_lifecycle.go:308`）。
  - 已改名为语义中性的 `GetLatestSessionForAgent`。
  - 同步 `ErrLeaderCheckpointNotSupported` → `ErrAgentCheckpointNotSupported`（`manager.go:164`）。

- [x] **H3.3 `leader_checkpoints` 表迁移**
  - 表、列、索引全量改名 `agent_checkpoints` / `agent_id` / `idx_agent_checkpoints_status`。
  - `migrate.go` 增加幂等 DO 块：已有库 `ALTER TABLE ... RENAME`（含列/索引改名）不丢数据；新库全为 no-op。
  - 同步 `base_repository.go` 白名单、`production_manager.go` SQL、集成测试。
  - 生产 DB 变更：随 `Migrate` 启动自动应用（幂等），无需停机。

- [x] **H3.4 `AgentTypeLeader` / `PolicyLegacyLeader` 收敛**
  - `models.AgentTypeLeader`（`types.go`）+ `api/agent/agent.go` 别名：生产零引用，直接删除；测试改用 `AgentTypeTop`（31 处）。
  - `agentipc.PolicyLegacyLeader` → `PolicyLegacy`（`Dispatch`/`IsLegacy` 休眠分支保留，kernel 仍只注册 TaskFabric track）；清理 `bus_test.go`（6 处）。

- [x] **H3.5 回归**
  - `gofmt -l .` 空、`go vet` 相关包、`go build ./...`、`go test ./...` 全绿；`go test -race` 相关包全绿。

---

## 2. 执行顺序与风险

| 阶段 | 风险 | 是否需用户确认 | 依赖 |
|------|------|----------------|------|
| H1（加固/测试/CHANGELOG） | 无（纯新增测试） | 否 | 无 |
| H2（历史整理） | 破坏性（改 git 历史） | **是** | H1 完成后 |
| H3（leader 清债） | 中-高（DB 迁移 + 跨包改名） | **是**（尤其 H3.3） | 独立，可最后做 |

**推荐路径**：立即执行 H1（零风险硬化）→ 汇报 H2 的 push 状态供你拍板 → H3 作为独立后续（涉及 DB，单独排期）。

## 3. 完成定义（DoD）

- H1：`go test -race ./...` 全绿，新增 load_tracker 回归测试覆盖上述 6 条断言，CHANGELOG 补记。
- H2：提交历史无同名 commit、无 `.orig` 备份文件痕迹（或明确记录为不改历史）。
- H3：生产代码无 `leader` 角色语义符号（保留项均已改名或标 Deprecated 并说明原因），DB 迁移脚本就绪并通过集成测试。**✅ 完成于 2026-08-24**：`GetLatestSessionForAgent` / `agent_checkpoints` 表（幂等 DO 迁移）/ `AgentTypeLeader` 删除 / `PolicyLegacy` 改名；`go build ./...` + `go test ./...` + 相关包 `-race` 全绿。

## 4. 不做什么（防 scope 蔓延）

- 不新增任何 Agent OS 功能（两份计划已完成）。
- 不重构无关大文件（code_rules_v2：聚焦最小改动）。
- 不违反 §0 冻结规则，不以任何形式恢复 leader/planner 作为 Kernel 角色。
