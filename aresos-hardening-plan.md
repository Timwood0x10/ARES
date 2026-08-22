# ARES Agent OS 收尾加固计划（v1 · 冻结架构后的硬化）

> 基准：`aresos-plan.md`（W1-W5 全绿）、`aresos-agentos-plan.md`（v3 单一调度，里程碑 1-5 全绿）
> 前提：两份计划的功能项均已完成并对齐；本计划**不新增功能**，只做收尾、加固、清债。
> 指导原则：不违反 `aresos-agentos-plan.md` §0 的冻结规则（Rule 1-7）；遵循 `plan/rules/code_rules_v2.md`（修 bug 先写复现测试、禁裸 goroutine、goroutine 必 recover、不擅自 git commit、破坏性变更走灰度）。
> 日期：2026-08-22 | 分支：dev

> **状态（2026-08-22 复核）**：H1 ✅ 全部完成；H2 ❌ 关闭（已 push，按规则不 rebase）；H3 🟡 降级为可选 backlog（理由见 §1.3）。计划与实况对齐。

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

### 阶段 H1 — 零风险加固（✅ 已完成 2026-08-22）

- [x] **H1.1 补 F1 load 释放回归测试**
  - 文件：`internal/kernelscheduler/load_tracker_test.go`（283 行）
  - 断言 1：`Begin` 后 `Load==1`，`End` 后 `Load==0`（跑完一个 quantum 槽位必须释放）。
  - 断言 2：多轮 `Begin/End` 后 `Load` 不单调递增（复现被修的饥饿 bug：删掉 `load--` 时此断言必须失败）。
  - 断言 3：`End` 在 `load==0` 时不会下溢到负数（`if t.load[agentID] > 0` 分支）。
  - 断言 4：`SetAgentConfidence(id, -1)` 清除 override 回落历史成功率；`SetAgentConfidence(id, 0.0)` 保留为有效 0（0% 成功率排底，F1 GA 契约）。
  - 断言 5：`SetCapabilityConfidence` 负值清除、`ConfidenceFor` 的 capability > agent > 历史 > 1.0 回退顺序。
  - 断言 6（并发）：`-race` 下并发 `Begin/End/SetAgentConfidence` 无数据竞争。

- [x] **H1.2 补 Score×Load 集成断言（防回归的第二道门）**
  - 文件：`internal/kernelscheduler/load_tracker_test.go`
  - 断言：`TestLoadTracker_ScoreStaysPositiveAfterMultipleRounds` — 同一 agent 连续被调度多轮后，`taskfabric.Score` 因 `load` 已释放仍 `> 0`（端到端复现 F1："later rounds get no capable candidate" 不再发生）。

- [x] **H1.3 全量竞态与测试**
  - `go test -race ./...` 全绿：169 个包 ok（2026-08-22 复核）。
  - `gofmt -l .` 空、`go vet ./...` 通过、`go build ./...` 通过。

- [x] **H1.4 CHANGELOG 补记**
  - `CHANGELOG.md` `## [Unreleased]` 下已补 `### Fixed`：F1 `LoadTracker.End()` load 释放修复 + `TestLoadTracker_ScoreStaysPositiveAfterMultipleRounds` 回归测试（见 CHANGELOG.md:85-93）。

### 阶段 H2 — 提交历史整理（❌ 关闭：已 push，不改历史）

- [x] **H2.1 确认 push 状态**
  - 判定结果：`c3371b0c`/`3aabb450` **已 push 到 `pd/dev`**（`git branch -r --contains 3aabb450` 命中 pd/dev）。
  - 按 H2.1 规则：**已 push → 不 rebase，不改历史**。脏 commit（`.orig` 备份文件入库后删除）留档；`7b044ca6`（docs: Update multi-agent documentation and finalize the reinforcement plan.）即"说明现状"的 clean commit。
  - 结论：本阶段无剩余动作，无需用户拍板。

### 阶段 H3 — leader 残留符号清债（🟡 降级为可选 backlog）

> 降级理由（2026-08-22 复核）：
> 1. **纯改名/注释债**：`AgentTypeLeader`、`GetLatestSessionForLeader`、`PolicyLegacyLeader` 均为休眠符号，不影响功能、不激活路径，无运行时风险。
> 2. **DB 迁移风险高**：`leader_checkpoints` 表清理需生产 DB 迁移（H3.3），收益（叙事干净）远低于风险（迁移事故），且表当前无生产数据依赖（仅测试引用 + migrate.go schema）。
> 3. **对演进叙事有正面价值**：这些符号是"从 leader 分层 → 扁平 peer 内核"演进路径的活化石，保留并注释比删除更能证明架构迁移的真实性（面试/文档叙事价值）。
> 4. 若未来确需清理，影响面比 v1 描述更大：`leader_checkpoints` 在 `migrate.go:77` + `base_repository.go:44`（非"仅测试"）、`PolicyLegacyLeader` 在 `policy.go:55` `IsLeader()`（非"仅测试"）——需更新影响面清单后单独排期。

- [ ] **H3.1 影响面盘点**（暂缓：仅当 H3 被重新激活时执行）
- [ ] **H3.2 `GetLatestSessionForLeader` → 去 leader 化**（暂缓）
- [ ] **H3.3 `leader_checkpoints` 表迁移**（暂缓：生产 DB 变更，需用户确认）
- [ ] **H3.4 `AgentTypeLeader` / `PolicyLegacyLeader` 收敛**（暂缓）
- [ ] **H3.5 回归**（随上述任一执行时配套）

---

## 2. 执行顺序与风险

| 阶段 | 风险 | 是否需用户确认 | 状态 |
|------|------|----------------|------|
| H1（加固/测试/CHANGELOG） | 无（纯新增测试） | 否 | ✅ 已完成 |
| H2（历史整理） | 破坏性（改 git 历史） | ~~是~~ → 已判定已 push，**不 rebase** | ❌ 关闭，无剩余动作 |
| H3（leader 清债） | 中-高（DB 迁移 + 跨包改名） | 是（尤其 H3.3） | 🟡 可选 backlog，暂缓 |

**执行记录（2026-08-22）**：H1 已执行完毕（load_tracker 回归测试 + Score×Load 集成 + 全量 `go test -race ./...` 169 包全绿 + CHANGELOG 补记）→ H2 经 `git branch -r --contains 3aabb450` 判定已 push 到 pd/dev，按规则不 rebase、脏 commit 留档 → H3 降级为可选 backlog（纯改名债 / DB 迁移风险高 / 残留符号对演进叙事有正面价值，理由见 §1.3）。

## 3. 完成定义（DoD）

- H1：✅ 达成 — `go test -race ./...` 全绿（169 包），load_tracker 回归测试覆盖 6 条断言，CHANGELOG 补记。
- H2：✅ 按"不改历史"分支达成 — 已 push 到 pd/dev，明确记录为不改历史（同名 commit 留档），`7b044ca6` 为说明现状的 clean commit。
- H3：🟡 未执行（可选 backlog）— 残留符号仍在生产代码（`AgentTypeLeader`、`GetLatestSessionForLeader`、`leader_checkpoints`、`PolicyLegacyLeader`），清理收益低于风险，暂缓。

## 4. 不做什么（防 scope 蔓延）

- 不新增任何 Agent OS 功能（两份计划已完成）。
- 不重构无关大文件（code_rules_v2：聚焦最小改动）。
- 不违反 §0 冻结规则，不以任何形式恢复 leader/planner 作为 Kernel 角色。
