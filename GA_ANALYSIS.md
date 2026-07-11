# 遗传算法（GA）实现现状分析与改进计划

## 一、现有实现全景

### 1.1 内部引擎 (`internal/ares_evolution/`)

```
internal/ares_evolution/
├── genome/                    # 核心 GA 引擎
│   ├── population.go          # 种群管理：初始化、进化、快照、最佳追踪
│   ├── population_config.go   # 配置结构体 + 默认值
│   ├── population_options.go  # 函数式选项（WithPopulationSize, WithEliteCount 等 20+ 选项）
│   ├── selection.go           # 6 种选择算子
│   ├── crossover.go           # 交叉算子（uniform参数 + prompt模板）
│   ├── adaptive.go            # 自适应机制：多样性测量、变异率调整、停滞恢复
│   ├── multi_objective.go     # 多目标优化：Pareto支配、Pareto前沿、NSGA-II拥挤距离
│   ├── meta_evolution.go      # 元进化控制器：参数自调优
│   ├── diversity_config.go    # 多样性配置
│   ├── hypothesis.go          # 进化假设生成
│   ├── reflection.go          # LLM反思分析
│   ├── guided_pipeline.go     # 反思→假设→知识蒸馏全管道
│   ├── knowledge.go           # 知识库：从进化历史中学习
│   ├── spatial_index.go       # 空间索引：网格哈希加速邻近查询
│   ├── score.go               # 评分常量与工具
│   ├── errors.go              # 错误定义
│   └── population_guard.go    # 守卫：验证、多样性恢复、注入
│
├── mutation/                  # 变异引擎
│   ├── mutator.go             # 参数/提示/工具 三种变异
│   ├── types.go               # Strategy、MutationType、ParamRange
│   ├── option.go              # 函数式选项
│   ├── adaptive_distribution.go  # 自适应变异类型概率分布
│   ├── guided_mutator.go      # 经验引导的变异
│   ├── llm_hint_provider.go  # LLM提示生成器
│   └── errors.go
│
├── scoring/                   # 评分系统
│   ├── tiered_scorer.go       # 分层评分器
│   ├── cache.go               # 评分缓存
│   ├── budget.go              # LLM调用预算控制
│   ├── memory_aware_scorer.go # 记忆感知评分
│   └── hash.go                # 策略哈希
│
├── dream_cycle.go             # 自主进化编排器
├── scheduler/                 # 进化调度器
├── promotion/                 # 策略晋升/降级系统
├── experience/                # 经验系统（从失败中学习）
├── guardrails.go              # 进化安全守卫
└── genome_wiring.go           # 内部连接适配器
```

### 1.2 已实现的功能

| 类别 | 功能 | 状态 |
|------|------|------|
| **种群** | 初始化、精英保留、生存率选择 | ✅ |
| **种群** | 自适应变异率 (0.05~0.5) | ✅ |
| **种群** | 停滞检测与恢复机制 | ✅ |
| **种群** | 新鲜突变注入（多样性坍塌时） | ✅ |
| **种群** | 适应度共享（Fitness Sharing） | ✅ |
| **种群** | 基于年龄的个体淘汰 | ✅ |
| **种群** | 谱系精英保留 | ✅ |
| **选择** | 锦标赛选择 (Tournament) | ✅ |
| **选择** | 排名选择 (Rank) | ✅ |
| **选择** | 随机通用采样 (SUS) | ✅ |
| **选择** | 轮盘赌选择 (Roulette Wheel) | ✅ |
| **选择** | 截断选择 (Truncation) | ✅ |
| **选择** | 谱系排名选择 (LineageRank) | ✅ |
| **交叉** | 均匀参数交叉 | ✅ |
| **交叉** | Prompt模板交叉（单点/半拆分） | ✅ |
| **变异** | 参数变异 | ✅ |
| **变异** | Prompt模板变异 | ✅ |
| **变异** | 工具配置变异 | ✅ |
| **变异** | 自适应变异类型分布 | ✅ |
| **变异** | LLM引导变异 | ✅ |
| **多目标** | Pareto支配判断 | ✅ |
| **多目标** | Pareto前沿提取 | ✅ |
| **多目标** | NSGA-II拥挤距离 | ✅ |
| **多目标** | Pareto排名（非支配排序） | ✅ |
| **多目标** | 维度聚合与归一化 | ✅ |
| **元进化** | 种群参数自调优 | ✅ |
| **知识** | 反思→假设→知识蒸馏 | ✅ |
| **知识** | 经验引导变异 | ✅ |
| **评分** | 分层评分（快速/标准/深度） | ✅ |
| **评分** | LLM调用预算控制 | ✅ |
| **评分** | 评分缓存 | ✅ |
| **编排** | DreamCycle自主进化 | ✅ |
| **晋升** | 策略晋升/降级 | ✅ |

### 1.3 公共 API 层 (`api/evolution/`)

```
api/evolution/
├── evolution.go          # 公共接口 + 适配器
├── evolution_test.go     # 测试
├── genome/               # ❌ 空目录
└── mutation/             # ❌ 空目录
```

**已知问题（TODO标记）：**
1. `NewMutator()` → 返回 `ErrNotImplemented`
2. `Promoter.Evaluate()` → 返回 `ErrNotImplemented`
3. `Promoter.Demote()` → 返回 `ErrNotImplemented`
4. `genome/` 和 `mutation/` 子目录为空

另有一个独立 GA 服务 `api/service/ga/service.go`，功能正常但不是通过 `api/evolution/` 走的。

---

## 二、与 PyGAD 功能对比

| 功能 | PyGAD | 本项目 | 差距 |
|------|-------|--------|------|
| **交叉算子** | single_point, two_points, uniform, scattered, segment | uniform params + prompt crossover | 缺少 classical 交叉类型 |
| **变异算子** | random, swap, inversion, scramble, adaptive | param/prompt/tool | 缺少 swap/inversion/scramble |
| **选择算子** | sss, tournament, roulette_wheel, rank, random | 6种（含全部） | 无差距，甚至更多 |
| **多目标** | NSGA-II, NSGA-III | Pareto基础 + 拥挤距离 | 缺少完整 NSGA-II/III 实现 |
| **稳态GA** | `parent_selection_type="sss"` | 无 | 缺少稳态进化模式 |
| **基因空间控制** | init_range_low/init_range_high, gene_type, allow_duplicate | ParamRange | 缺少基因类型约束和重复控制 |
| **回调函数** | on_generation, on_fitness, on_mutation, on_crossover | 无公共回调 | 缺少公共回调系统 |
| **停止条件** | reach_target_fitness, max_generations | max generations | 缺少目标适应度停止 |
| **历史追踪** | best_solutions_generations | BestEver | 缺少每代详细历史 |
| **可视化** | plot_fitness, plot_genes, plot_new_solution | 无 | 缺少可视化支持 |
| **自适应变异** | mutation_type="adaptive" | adaptive_distribution.go | 已有，但未暴露到公共API |
| **并行评估** | 无内置支持 | 无 | 两者都缺 |
| **迁移学习** | 无 | knowledge.go + reflection | 本项目领先 |

---

## 三、改进计划

### 阶段一：修复公共API TODO ❌→✅

| 任务 | 文件 | 描述 |
|------|------|------|
| P1.1 | `api/evolution/mutation/mutator.go` | 新建，实现 `Mutator` 接口 |
| P1.2 | `api/evolution/genome/genome.go` | 新建，实现基因操作 |
| P1.3 | `api/evolution/evolution.go` | 修复 `NewMutator()`, `Promoter.Evaluate()`, `Promoter.Demote()` |

### 阶段二：新增交叉算子

| 任务 | 文件 | 描述 |
|------|------|------|
| P2.1 | `internal/ares_evolution/genome/crossover.go` | 新增两点交叉 (two_point) |
| P2.2 | 同上 | 新增分散交叉 (scattered) |
| P2.3 | 同上 | 新增分段交叉 (segment) |

### 阶段三：新增变异算子

| 任务 | 文件 | 描述 |
|------|------|------|
| P3.1 | `internal/ares_evolution/mutation/mutator.go` | 新增交换变异 (swap) |
| P3.2 | 同上 | 新增反转变异 (inversion) |
| P3.3 | 同上 | 新增打乱变异 (scramble) |

### 阶段四：稳态GA模式

| 任务 | 文件 | 描述 |
|------|------|------|
| P4.1 | `internal/ares_evolution/genome/population.go` | 新增 `EvolveSteadyState()` 方法 |
| P4.2 | `api/evolution/evolution.go` | 暴露稳态模式配置 |

### 阶段五：完整NSGA-II多目标优化

| 任务 | 文件 | 描述 |
|------|------|------|
| P5.1 | `internal/ares_evolution/genome/multi_objective.go` | 实现完整NSGA-II（非支配排序+拥挤距离选择） |
| P5.2 | 同上 | 实现NSGA-II选择算子 |

### 阶段六：回调系统

| 任务 | 文件 | 描述 |
|------|------|------|
| P6.1 | `internal/ares_evolution/genome/population.go` | 新增 on_generation 回调 |
| P6.2 | 同上 | 新增 on_fitness 回调 |
| P6.3 | `api/evolution/evolution.go` | 暴露回调配置 |

### 阶段七：历史追踪与数据分析

| 任务 | 文件 | 描述 |
|------|------|------|
| P7.1 | `internal/ares_evolution/genome/population_history.go` | 增强每代历史记录 |
| P7.2 | `api/evolution/evolution.go` | 暴露历史数据接口 |

### 阶段八：可视化数据导出

| 任务 | 文件 | 描述 |
|------|------|------|
| P8.1 | 新增 `api/evolution/visualize.go` | 适应度/多样性/基因值序列导出 |

---

## 四、优先级排序

```
高优先级（修复阻塞）：
  P1.1 → P1.2 → P1.3  （修复公共 API TODO）

中优先级（核心增强）：
  P2.1 → P2.2 → P2.3  （更多交叉类型）
  P3.1 → P3.2 → P3.3  （更多变异类型）
  P5.1 → P5.2          （完整 NSGA-II）

低优先级（锦上添花）：
  P4.1 → P4.2          （稳态模式）
  P6.1 → P6.2 → P6.3   （回调系统）
  P7.1 → P7.2          （历史追踪）
  P8.1                  （可视化）
```