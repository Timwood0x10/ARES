# Compactor 负数 KeepRecent 进程崩溃；Memory 配置数据竞争；Planner 证据分裂

日期：2026-08-24
范围：internal/ares_events、internal/ares_memory、api/tools + internal/tools/planner

## 1. Compactor：负数 KeepRecent 崩掉整个进程

`NewCompactor` 只净化 `Threshold`。当 `KeepRecent < 0` 且流为空时，
`compactStream` 计算 `candidates := allEvents[:total-KeepRecent]` →
切片越界 → panic 发生在 store 的 errgroup worker 里（`errgroup.Go` 不会
recover）→ 整个进程退出。YAML 配置的 `CompactionConfig` 经 `ForceCompact`
可直接触达该路径。

修复：构造器把 `KeepRecent < 0` 净化为默认值；`compactStream` 内再增加一道
防御性守卫。

测试：`TestCompactStream_NegativeKeepRecent_NoPanic`（空流 + 绕过构造器的
配置）与 `..._WithEvents`。

## 2. Memory 管理器：配置补丁与热路径读竞争

`MemoryPatchExecutor.Apply` 在写锁下修改 `MaxHistory`/`MaxTasks`/
`CleanOptions`，但 `BuildPromptMessages`、`BuildContext`、蒸馏路径以及
`ProductionMemoryManager` 的读取（`SessionTTL`、`MaxHistory`）完全无锁——
进化补丁在流量中落地即触发数据竞争（`runRetrieval` 一直做对了，其余路径
不一致）。

修复：所有热路径在 `RLock` 下快照所需字段；为 ProductionMemoryManager 增加
`snapshotTuning()` 辅助函数。

测试：`TestMemoryConfigPatch_RacesWithHotPaths`——并发补丁写入者 vs
prompt/context 读取者，旧代码在 `-race` 下必失败。

## 3. Planner 证据仓库精神分裂

`api/tools.NewPlanner` 创建证据仓库 A（打分器查询它）；`api/tools.NewBridge`
又创建了第二个仓库 B（bridge 把执行结果写进 B）。证据永远到不了打分器——
通过公共 API 无法让工具选择适应真实的失败/延迟。

修复：新增 `Planner.EvidenceStore()` 访问器；NewBridge 接线 planner 自己的
仓库，读写共用一个实例。
