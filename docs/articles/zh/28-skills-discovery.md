# ares 架构深度解析（二十八）：Skills 发现 — 不扫盘的能力目录（0.3.x）

> 0.3.x 更新：Skills 发现已升级为 **Capability Fabric**——框架原生的技能发现、索引和加载系统。SkillCatalog 配合 SourceManager 聚合多种技能来源（MCP 服务器、Git 仓库、本地可执行文件、HTTP 清单）。五件套 catalog 工具（skill_search/load/activate/list/experience）实现 Level-0/1/2 渐进披露。Experience 模块从历史使用中学习相关性先验。Capability Fabric 直接成为 Kernel Scheduler 的 capability-aware 评分来源。

> 说明：本文基于实际代码（`internal/ares_skills` 全部实现：source.go / indexer.go / discovery.go / loader.go / resolver.go / experience.go / fts5.go / git_source.go / http_source.go / catalog.go），是 docs 系列中 Capability Fabric 发现链路的专门篇。

## 一、Skills 发现：从"找"到"声明"

传统工具发现是**扫盘**：`find /`、扫 PATH、探测 executable——寻找"可能存在的东西"。ARES 的 Capability Fabric 反其道而行（设计原则①）：

> **不扫盘，只扫"声明过的 Source"。** 只发现"被声明的东西"，不寻找"可能存在的东西"。

发现链路是五段管线：

```
SourceManager（声明源）→ Indexer（metadata 索引）→ Discovery（检索）→ Loader（按需加载）→ Resolver（信任门控）
```

## 二、SourceManager：只认识 4 类声明源

`internal/ares_skills/source.go`：

| 源 | 位置 | 说明 |
|----|------|------|
| **Project** | `<project>/.ares/skills/` | 项目自声明，只读 metadata **不执行 Skill** |
| **User** | `~/.ares/skills/` | 用户安装的全局能力 |
| **Registered** | `~/.ares/config.toml` `[[skill_sources]]` | 显式声明的额外目录 |
| **Experience**（Learned） | `~/.ares/experience.json` | 任务经验记录（relevance prior，**永不自动执行**） |

0.3.0 扩展（`git_source.go` / `http_source.go`）：Registered 源支持 `type = "git"`（浅克隆到本地缓存后索引，`SyncGitSource` clone/pull）与 `type = "http"/"oci"`（拉取 JSON manifest 清单，`FetchHTTPManifest`）。

关键实现：`SkillDirs` 只读 source 根目录**一层**，且要求目录含 `SKILL.md` 或 `skill.yaml` 标记才算 skill——**声明验证，绝非深递归扫描**。

## 三、Indexer：只存 metadata，不读 body

`internal/ares_skills/indexer.go` 构建 Level-0 索引，设计要点：

```go
// SkillIndexEntry is the metadata-only index record (Level 0 of progressive
// disclosure). The SKILL.md body is deliberately NOT loaded here.
type SkillIndexEntry struct {
    ID           string   // 稳定标识（如 "rust-review"）
    Name         string   // 人类可读名
    Description  string   // 常驻一句话摘要
    Keywords     []string // 检索关键词
    Source       SourceKind // project | user | registered | experience
    Path         string   // 技能目录
    Version      string   // manifest 版本
    Capabilities []string // 能力标签
    ToolTypes    []string // 声明的工具类型（来自 manifest）
    Hash         string   // 内容 hash，变更检测
}
```

- **front matter + manifest 合并**：`SKILL.md` 的 front matter（name/description/keywords/version）与 `skill.yaml`（工具声明）合并，manifest 字段优先
- **content hash**：确定性哈希（SKILL.md + skill.yaml + tools 目录）→ `DetectIndexChanges` 按 ID+Source+Hash 分类 Added/Modified/Removed
- **渐进披露边界**：索引永远不含 body——100 个技能 = 100 × ~100 token

## 四、Discovery：关键词匹配 + FTS5 回退

`internal/ares_skills/discovery.go` 负责检索（只碰 Level-0 metadata）：

- **关键词匹配**（默认）：`splitTerms` 分词 → `matchScore` 对 ID/name/keywords/capabilities/description 计数命中 → 按命中数降序 + ID 升序排序（确定性）
- **FTS5 全文检索**（`fts5.go`）：SQLite 内存 FTS5 虚拟表（`modernc.org/sqlite`，无 CGO），覆盖 id/name/description/keywords，`ORDER BY rank` 排序
- **优雅回退**：`Search` 优先 FTS5，查询不安全/失败时**自动回退关键词匹配**——两种检索共享同一入口，调用方无感知

## 五、Loader / Resolver：Level-1/2 按需披露

- **Loader**（`loader.go`）：`Load(id)` 返回完整 SKILL.md 指令体（Level-1，按需）；`ListReferences`/`LoadReference` 管理 references 目录（**路径穿越防护**：拒绝含 `/`、`\`、`..` 的引用名）
- **Resolver**（`resolver.go`）：把 manifest 的 tools 声明绑定为可调用能力，过**信任门**：

| 工具类型 | 信任条件 |
|----------|----------|
| `builtin` | 必须在内置工具名单（trusted） |
| `mcp` | 只需声明 server 名——连接留给 `skill_activate` 懒加载 |
| `executable` | 仅信任源（project/user）且 `allow_local_executables` 开启；**声明验证存在性，不扫盘** |

`Discovery ≠ Permission`（设计原则⑤）：learned/external 源可被索引到，但**不可自动执行**。

## 六、Experience：学习源不"生成"Skill

`internal/ares_skills/experience.go` 记录 `{skill, task_pattern, success_rate}` 相关度先验：

```go
type ExperienceRecord struct {
    Skill       string  // "pdf-generation"
    TaskPattern string  // "document-to-pdf"
    SuccessRate float64 // 0.94
}
```

- **不是 LLM 生成 Skill**——只记录"哪个 skill 对哪个任务模式成功率高"
- **JSON 持久化**（`experience_store.go`）：原子写（tmp→rename），`~/.ares/experience.json`，跨重启保留
- **闭环**：`SkillOutcomeRecorder` 订阅 `EventSubTaskResult` 事件流（只读观察者）→ 提取 `task.UsedExperienceID` + success → `Experience.Record(skill, pattern, rate)` → 下次 `BestMatch` 命中更高 success_rate

## 七、Catalog 门面：一条链的封装

`internal/ares_skills/catalog.go` 的 `Catalog` 组合全部组件：

```go
func (c *Catalog) Build() error          // 索引全部声明源（git 先 sync、http 拉清单）
func (c *Catalog) Search(q string, n int) []SkillIndexEntry  // Level-0 检索
func (c *Catalog) Load(id string) (string, error)            // Level-1 按需 body
func (c *Catalog) ResolveTools(id string) ([]ResolvedTool, error) // Level-2 信任门控
func (c *Catalog) Activate(ctx, id string) ([]ResolvedTool, error) // 懒连接 MCP（Level-2 执行）
func (c *Catalog) Refresh() (IndexChange, error)             // hash 增量重索引
func (c *Catalog) SeedRegistry(reg *skills.Registry) error   // 灌入 memoryManager 常驻块
```

- **并发安全**：`sync.RWMutex` 保护索引 swap（Build/Refresh 写锁，Search/Load 读锁）
- **Refresh 与 Build 语义对齐**：重拉 http 源 + 重建 FTS5（关旧索引）+ 重 seed registry

## 八、生产接线

`cmd/ares/serve.go` 的 `wireSkillCatalog`：

1. 读 `~/.ares/config.toml` `[[skill_sources]]`（directory + git + http/oci）
2. `NewCatalog`（project `.ares/skills` + user `~/.ares/skills` + 注册源 + ExperiencePath）
3. git 源 2 分钟超时 sync → `Build()` 建索引
4. `SeedRegistry` → memoryManager 常驻技能块
5. `SetMCPConnector(comp.MCP)` → skill_activate 懒连接
6. `SetToolChangeHandler` → MCP listChanged 触发 Refresh
7. `CatalogTools(catalog)` 注册 skill_search/skill_load/skill_activate/skill_list/skill_experience 五件套

### 8.1 envcap 统一检索桥接（tools/skills/commands 聚合）

除了 catalog 自己的检索，发现链路还通过 `internal/tools/envcap` 的 `Searcher` 提供**统一环境检索**——把"注册工具（builtin/MCP）+ 技能（skills）+ 本机命令（allowlist）"三类能力聚合成一个查询入口：

```go
// internal/tools/envcap/envcap.go
type Searcher struct {
    tools  ToolLister          // 已注册工具（builtin + MCP）
    skills *skills.Registry    // catalog 索引经 SeedRegistry 灌入
    cmds   *discovery.Discoverer // 本机命令 allowlist（可为 nil）
}
```

- **桥接**：`catalog.SeedRegistry(reg)` 把 skills 索引灌进 `*skills.Registry`，再 `envcap.NewSearcher(tools, reg, cmds)`——catalog 成为 envcap 的技能源（`TestCatalogSeedsEnvcapAggregation` 守护）
- **聚合排序**：`Search` 返回 `Capability{Kind: tool|skill|command}`，按 `kindRank`（tool < skill < command）+ name 升序稳定排序
- **定位**：envcap 是 ToolResolver 的**查询前端**——统一检索命中后再经 catalog `ResolveTools`/`Activate` 落到底层 provider

## 九、总结

| 组件 | 职责 | 设计原则 |
|------|------|----------|
| SourceManager | 声明源枚举（含 git/http 扩展） | ① 不扫盘 |
| Indexer | metadata-only 索引 + hash | ④ 渐进披露 Level-0 |
| Discovery | 关键词 + FTS5（回退） | ④ 只给 LLM metadata |
| Loader | 按需 body / references | ④ Level-1/2 |
| Resolver | 信任门控绑定工具 | ⑤ 发现 ≠ 执行 |
| Experience | 学习源先验（可持久化） | ⑤ learned 永不自动执行 |
| Catalog | 门面 + Refresh + Activate | ② Skill ≠ Tool |

**发现链路的主线：把"能力管理"从运行时扫盘变成启动期索引。** 五段管线各守一个原则，配合 ContextCleaner（token 预算）与 peer/actionlog/lease（协作原语），构成 0.3.0 Agent 出生自带的能力底座。

### 9.1 100 skills 性能基准（0.3.0 实测）

`internal/ares_skills/benchmark_test.go`（`BenchmarkCatalogBuild100Skills` / `Search100Skills` / `ExperienceBestMatch100` + `TestResidentMetadataTokenBudget`）：

| 场景 | 实测（darwin/arm64, 200 次） |
|------|------------------------------|
| 100 skills 索引（Build） | **6.7 ms/op**（metadata-only，零扫盘） |
| 100 skills 检索（Search，FTS5 命中） | **16.4 µs/op**（26 allocs） |
| 检索回退（keyword fallback） | **16.4 µs/op**（23 allocs） |
| 100 条 Experience BestMatch | **4.3 µs/op**（2 allocs） |
| 常驻 metadata token | **~747 tokens / 100 skills ≈ 7 tokens/skill**（承诺 ~100/skill，实测一个数量级更优） |

渐进披露承诺"100 skills ≠ 100 × 全文"得到实测验证：常驻块只含 name+description（绝无 SKILL.md body），100 skills 仅 ~747 tokens。
