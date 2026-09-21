# Code Review — ff328cee 外部单接口批次（2026-09-21）

> 目标提交：`ff328cee` feat(api): task 查询端点 + 外部最小提交（plan/external-simple-api-plan.md M1+M2+M3）。
> 门禁核验基线：gofmt/vet 干净；`internal/ares_config`（含 G2）、`internal/agentsyscall`、`sdk`、`cmd/ares` 全量测试通过；新外部面测试 `-race` 绿。
> 规范：`plan/rules/code_rules_v2.md`；review 格式遵循 review-agent（read-only、defect-first）。
> 状态：**修复完成，门禁全绿，修复批 review 通过**（未 commit——发令权在用户）。

## Findings

### [P1] GET/`?wait=` 的 result 不是会话答案，FAILED 不带错误信息

位置：`cmd/ares/agent_routes_task_read.go:52`（Result 映射）、`internal/kernel/scheduler_quantum.go:96-97,127-154`（错误被消费后丢弃；COMPLETED 时 StepCheckpoint 是量子步检查点 `{"result":"ok","items":...}` 而非会话回答）。

`taskStatusFromTask` 把 `result` 映射为 `env.StepCheckpoint`。调度器在 COMPLETED 时写入的是最后一个量子的步检查点，缺省形状甚至是纯 `"ok"`；用户真正的回答在 session/answer 侧（`sess/<sid>/…/answer#…` 任务的 `items[0].Content`）——SDK 路径经 `sdk/l2_submit.go` 等待 answer task，HTTP 路径没有答案通道。FAILED 更糟：`out.Result.Error` 在 RunQuantum 步错误路径被消费后从不落盘，Task 上无 error 字段，GET 只能返回 `state=FAILED`、result 为空。README 与 quick-start 都承诺「read the result」；e2e 只断言终态，不查 result 内容——「一个接口拿到结果」这一核心价值当前未被实现，也未被测试锁定。

**修复方案（已实施）**：
1. `CheckpointEnvelope`/`DecodedCheckpoint` 新增 `LastError`；`Fabric.Fail` 签名扩展为携带 `cause error`，终态失败时把错误原因盖章进 checkpoint（重试 requeue 路径不盖章）；唯一生产调用方 `RunQuantum` 传入 stepErr；全部测试调用方迁移。
2. HTTP 视图 `result` 优先返回会话答案（复用同包 `completedSessionAnswer` 的扫描语义），回退到 StepCheckpoint；新增 `error` 字段：来源为 envelope `LastError`，缺失时对级联失败派生 `dependency <id> failed`，会话 answer 节点 FAILED 时派生 `session answer task failed`。
3. `?wait=` 语义升级为「等待结果通道解析」（终态 + 会话答案可得/确认失败）；等待超时按计划合同降级 202，并在 body 中携带当前 `state`。
4. 测试锁定：fabric 错误落盘合同、handler 答案/error 契约、wait 超时降级契约；e2e 在 COMPLETED 时断言 result 非空。

### [P2] 默认 ollama 最小配置下金路径 POST 必 401，文档未给出出路

位置：`cmd/ares/agent.go:172-176`（write 门 deny-by-default）+ `configs/ares.yaml`（provider=ollama，api_key 注释掉）+ `README.md` 最小路径。

write 门 deny-by-default（无凭证层时 loopback 也 401）是已锁定的安全姿态，本身正确。但默认 ollama 配置 `llm.api_key` 为空 → `resolveServeAPIKey` 得空串 → 「ares serve & + curl POST」对默认配置用户直接不可用。plan Phase 4 验收项「minimal ares.yaml 冷启动全绿 + 一条 POST 入调度」与该姿态存在未调和的张力。

**修复方案（已实施）**：`ares init` 生成一次性 `security.api_key` 写入模板（保持 deny-by-default 且冷启动可 POST）；README / quick-start 最小配置段显式说明：`llm.api_key` 为空的 provider（如 ollama）必须设置 `security.api_key`，否则 write 门 401。

### [P3] README 重新引入不存在的环境变量名 `$ARES_HTTP_KEY`

位置：`README.md:73,83`。产品配置面已收敛到 ares.yaml、docs 环境变量引用刚清完（`d89f651a`）；`$ARES_HTTP_KEY` 在产品中不存在。**修复**：改为明确占位符 `<security.api_key 或 llm.api_key>`。

### [P3] quick-start 排障项误导：peer capabilities 与外部任务面无关

位置：`docs/zh/guides/quick-start.md:119`。`Submitter.Submit`（`internal/agentruntime/submit.go:182-185`）把一切 capability 归一到 `ares/plan`，`agents.peers` 声明的 capabilities 从不为外部提交路由。**修复**：改写排障项指向真实判据（看 GET 的 state / error；capability 为审计性字段）。

### [P3] plan §2.2 的 `default_capability` 非空白校验未落地

位置：`internal/ares_config/config_validate.go`。`validateTasks` 只覆盖 `wait_timeout`。**修复**：`validateServer` 补「defaults 之后仍为空白即拒绝」；补测试。

### [P3] provenance 测试里有断言外观的死代码

位置：`internal/agentsyscall/syscall_from_provenance_test.go:55-79`。`sentinel` 读取 `payload["from"]` 后从未断言。**修复**：改为真实断言——payload 中的 `from` 保持 opaque 原值、bus 级 `from` 仍为 kernel 盖章（或空）。

## 非缺陷裁决（核查过、不报）

- `fabric.Task()` 返回快照（`fabric.go:274-289`），GET/wait 读路径无锁竞争。
- 守卫顺序正确：wait/query 校验先于 kernel 交互，非法 `?wait` 不会留下已提交任务。
- write-deadline 延长是真修复，且带反向对照测试（`write_timeout_test.go:63-81`）。
- `ares run "query"` 走 `sdk Agent.Run → submitThroughL2 → Submitter`，与 HTTP 面同一执行内核，code_rules_v2 §5 入口收敛满足。
- agentipc `from` 伪造：binder 不解码 `from`、Kernel 从 `kctx.CallerID` 盖章（`syscall.go:483`），测试走真实 `BindTools` 路径——M2 安全项真实落地。
- 鉴权 fail-closed 测试断言真实且覆盖完整（写门 401/403、读门 loopback 例外与凭证必带）。
- G2 契约门对 `Server.DefaultCapability`/`Tasks.WaitTimeout`/`Security.APIKey` 的消费判定通过，未入 knownDead。

## 总体评估

本批把「一个接口」的提交与鉴权面做扎实了：请求体最小化、GET 端点、wait 契约（400/202/200 降级）、ares.yaml 配置收敛、安全收口（专用 `security.api_key`、IPC provenance）质量都过硬，测试是真测试。缺口集中在结果通道：接口形状有了，但外部用户 POST 一个问题后拿不回答案——这是产品北极星与当前实现之间唯一的硬差距，本批修复即收口该项。

## 修复记录

### 变更（工作区未提交，HEAD 仍为 ff328cee）

**P1 结果通道**：
- `internal/fabric/task/checkpoint_schema.go`：`CheckpointEnvelope`/`DecodedCheckpoint` 新增 `LastError`（json `last_error`）；Decode/Encode 双向携带；map 形态解码补 `tenant_id`（修复 review P3：restore 后 re-wrap 丢租户归属）+ `last_error`；新增 `checkpointWithCause`（nil cause 原样返回；解码失败降级为仅含 cause 的新 envelope）。
- `internal/fabric/task/fabric_lifecycle.go`：`Fail(id, agentID, epoch, cause error)` — 终态失败时替换 checkpoint 指针盖章 cause（不原地变更，保持 off-lock 快照无竞争合同）；requeue 路径不盖章。
- `internal/fabric/task/quantum.go`：RunQuantum 步错误路径传 `stepErr` 作为 cause；全部 ~19 处测试调用方迁移为显式 `nil`。
- `cmd/ares/agent_routes_task_read.go`：视图 `result` 优先会话答案（`completedSessionAnswer`），回退 step checkpoint；新增 `error` 字段（优先级：持久化 cause → 会话 answer 节点失败 → 级联 `dependency <id> failed` → 停滞 `session stalled before an answer landed`）；`waitForTaskResult` 等待「结果通道解析」（终态 + 答案可得/确认不可得，`agentruntime.StallDetector` 连续 5 次确认防编译间隙误判）；wait 超时 202 携带当前 `state`。
- `cmd/ares/agent_routes_tasks.go`：`?wait=` 分支改走新等待语义。

**P2 凭证金路径**：
- `cmd/ares/main.go`：`ares init` 生成随机 `security.api_key`（crypto/rand 192-bit hex）写入模板；ares.yaml 以 **0600** 写盘（review P3 修复）；模板含 `server.default_capability`（注明 audit-only）。
- README / docs/zh+en quick-start / configs/ares.yaml / SECURITY.md / examples 01-quickstart：删除不存在的 `$ARES_HTTP_KEY`；明确「llm.api_key 为空的 provider 必须设置 security.api_key，否则 write 门 401」；wait/result/error 契约描述更新；zh/en 排障项改为「capability 审计性，与 agents.peers 无关」；SECURITY.md 补结果通道段。

**P3**：`validateServer` 拒绝显式空白 `default_capability`（空值仍合法，setDefaults 回填）；`syscall_from_provenance_test.go` 死 sentinel 改为真实断言（payload `from` 保持 opaque、bus 级 `from` 为 kernel 盖章）；e2e COMPLETED 时断言 result 非空。

### 测试（全部走真实生产符号）

- `internal/fabric/task/checkpoint_cause_test.go`：终态 Fail 盖章 cause、requeue 不盖章、RunQuantum 落盘步错误、context.Canceled 不落盘、map round-trip 保留 tenant_id+last_error。
- `cmd/ares/agent_routes_external_test.go`：GET 结果通道 5 个子合同（答案优先/回退/失败 cause/answer 失败优先级/停滞 error）、级联派生 error、waitForTaskResult 4 个解析合同、wait 超时 202 带 state。
- `cmd/ares/main_test.go`：init 模板生成 key（≥32 字符）+ YAML 可解析 + 0600 权限。
- `internal/ares_config/config_tasks_test.go`：default_capability 空白拒绝。

### 门禁（2026-09-21）

- `make fmt`：绿（golangci-lint fmt + gofmt -s）
- `make check`：**EXIT=0**（golangci-lint 0 issues + `go test -short -cover ./...` 全绿）
- `go test -race -count=1`：`cmd/ares`、`internal/fabric/task`、`internal/ares_config`、`internal/agentsyscall`、`sdk` 全绿
- `gofmt -l .` / `go vet ./...` / `git diff --check`：干净
- G2 契约门：绿（随 ares_config 测试）

### 修复批 code review（第二轮，review-agent）

对未提交变更跑 defect-first review，3 条 findings **全部当批修复并补锁**：
1. **[P2] 停滞解析的 200 会把 step 占位符当成功结果** → COMPLETED 且会话无答案且 `SessionStalled` 时视图携带 `error: session stalled before an answer landed`（答案后到时 GET 自愈——答案扫描优先）；测试 `TestGetTaskStallResolvedViewSurfacesError`。
2. **[P3] init ares.yaml 0644 世界可读** → 改 0600；测试断言权限位。
3. **[P3] checkpointWithCause 经 map 解码丢 TenantID** → map 解码路径补 `tenant_id`；测试 `TestDecodeCheckpointMapRoundTripKeepsScope`。

Review 结论：变更集可信关闭原 findings；Fail 签名迁移完整；LastError 不会漏进成功完成路径（scheduler re-wrap 显式构造 DecodedCheckpoint 不含 LastError）；测试非敷衍。残留观察（非阻塞）：wait 轮询每次至多 3 次全 fabric IDs() 扫描（沿用 sdk 模式，低并发可接受）。

### 已知门禁外事项（非本批引入，已处置/待拍板）

- ~~`make gate` G1 失败：`internal/llm/output` 不可达~~ — **已处置**（见下方第二批）。
- 0.3.x GA/MutableDAG 深度线：9-18 批 8 findings 已关账，继续推进需新立计划（产品级，待用户拍板批次内容）。

---

## 第二批：e2e 验收 + 死包删除 + 顺手项（2026-09-21，未提交）

用户指令「都做吧」→ 三件事：真实凭证 e2e 验收、G1 收口（删 `internal/llm/output`）、memory P3 顺手项。

### 1. 真实凭证 e2e 验收 — 金路径闭环 ✅

根目录 `ares.yaml`（openai + 真实 key）下 `go test -tags=e2e ./cmd/ares/`：

- **`TestExternalSimpleQueryE2E` PASS**：POST `{"query":"reply with the single word pong"}` → GET 轮询 → `state=COMPLETED result=pong`——「一个接口提交、同一接口拿回答案」首次真实闭环（P1 结果通道生产验证通过）。
- **`TestServeProductionE2E` PASS**：显式 capability 提交（payload 双 key）→ GET 轮询 → `COMPLETED`。

过程中修正的 e2e 缺陷（全部当批处理）：
- **启动证据字符串过时**：serve 日志已无 `"serve: Leader OFF mode"` / `"kernel: live flip to policy=taskfabric"`，改为实际存在的 `"peer agents registered, Kernel scheduler started (no leader)"` + `"kernel scheduler:"`。
- **legacy payload 缺 `input` 键**：`submitTaskViaHTTP` 原先只发 `task_desc`，planner cognition 报 `payload has no string "input"`——补 `taskPayloadInput`+`taskPayloadDesc` 双键。
- **成功路径日志不可观测**：scheduler 成功执行在 Info 级零日志（只有失败才打 `execute task failed`），原「日志扫 scheduler 活动」断言对健康行为永远失败（E-13 类缺陷）——改为 GET 轮询到终态作为执行证明。
- **prompt 长度影响预算**：开放式 prompt 让真实 LLM planner 长时间不终态；wiring 测试改用应答型 prompt + 120s 预算 + 超时携带 last-state 诊断。
- e2e 新增合同：COMPLETED 必须携带非空 result（第一轮 e2e 空 result 断言真实抓到过问题形态）。

### 2. 删除 `internal/llm/output`（F-07）— G1 门禁收口 ✅

- 事实核验：全仓（Go/脚本/CI/Makefile/examples）零 import；`cmd/ares/serve.go:126` 注释早已定性「zero callers left, 0.4 deletion candidate (independent-review F-07)」；docs 里「evolution 侧 Parse 消费者」的说法已过时（grep 证伪）。
- **引用方先行更新**（删除前）：`serve.go` 注释（过去时）、`internal/core/models/recommend.go` 注释（去 `llm/output/schema.go` 指向）、`docs/reference/serve-walkthrough.md` §5.2（包本体已删）、`docs/articles/en+zh/20-llm-client-layer.md`（cut output 章节 + 删除记录 + 工具类型改述到 `internal/llmcore`）。CHANGELOG / docs/reviews / plan/archive 的历史记录按规则保留。
- `rm -rf internal/llm/output`（21 文件）；`go build ./...` OK；**G1 reachability gate PASSED**；G2 绿。

### 3. memory P3 顺手项 — 核验为已落地 ✅

- `PatchChangePlanner` 已支持 `session_max_history` / `distillation_threshold`（`memory_patcher.go:176-186` + `memory_patcher_test.go` 覆盖）。
- SDK `Memory.Session.MaxHistory` 已接入（`sdk/config.go` 解析/校验/ToOptions + 测试）。
- 结论：memory-arc review-round-2 已收口，checkpoint 里的 OPEN 标记过时，无需改动。

### 第二批门禁（2026-09-21）

- `make fmt` EXIT=0；`make check` EXIT=0；**`make gate` EXIT=0（G1+G2+事件契约全绿）**
- e2e：两测试 PASS（真实凭证）；`go vet -tags=e2e ./cmd/ares/` 干净
- 残留引用扫描：live 文件中 `llm/output` 仅剩 4 处刻意的删除记录；CI/lint/coverage/examples 零引用

### 第二批 review 结论

review-agent 子任务连续三次被运行时中断，改为主代理按 review-agent 方法学直接核查（diff 级 + 引用扫描 + 门禁验证）：**No findings.** 核查面：悬空引用（无误导性残留）、e2e 断言可失败性（启动证据/终态/非空 result 均可红）、CI 配置孤儿引用（无）、code_rules_v2（注释英文、测试非敷衍）。残留观察（非阻塞）：金路径 e2e 把 FAILED 也接受为终态（测试注释明言——回答质量断言留给凭证可信的部署；内容合同由单元测试锁定）。

### 状态汇总（全部未提交，commit 发令权在用户）

| 项 | 状态 |
|---|---|
| ff328cee review + P1/P2/P3 修复 | ✅ 门禁绿，二轮 review 3 findings 已闭 |
| e2e 真实凭证验收（金路径闭环） | ✅ 双测试 PASS，result=pong |
| `internal/llm/output` 删除 + G1 | ✅ gate EXIT=0 |
| memory P3（patch keys / SDK session.max_history） | ✅ 核验已落地，零改动 |
| 0.3.x GA/MutableDAG 深度线 | ⏸ 需新立计划（产品级，待拍板） |
