# ares Architecture Deep Dive (XXVIII): Skills Discovery — A Capability Catalog That Never Scans the Disk (0.3.x)

> 0.3.x update: Skills discovery has been upgraded to **Capability Fabric** — the framework-native skill discovery, indexing, and loading system. SkillCatalog with SourceManager aggregates multiple skill sources (MCP servers, git repos, local executables, HTTP manifests). The five-piece catalog toolset (skill_search/load/activate/list/experience) implements Level-0/1/2 progressive disclosure. The Experience module learns relevance priors from historical usage. Capability Fabric directly serves as the capability-aware scoring source for the Kernel Scheduler.

> Note: This article is grounded in the actual code (all of `internal/ares_skills`: source.go / indexer.go / discovery.go / loader.go / resolver.go / experience.go / fts5.go / git_source.go / http_source.go / catalog.go) — the dedicated Capability-Fabric discovery-chain article in the docs series.

## 1. Skills Discovery: From "Searching" to "Declaring"

Traditional tool discovery is disk scanning: `find /`, sweep PATH, probe executables — hunting for "what might exist". ARES's Capability Fabric does the opposite (design principle 1):

> **Never scan the disk; only scan "declared Sources".** Discover only "what is declared", never hunt for "what might exist".

The discovery chain is a five-stage pipeline:

```
SourceManager (declared sources) → Indexer (metadata index) → Discovery (retrieval) → Loader (on-demand loading) → Resolver (trust gate)
```

## 2. SourceManager: Only Four Kinds of Declared Sources

`internal/ares_skills/source.go`:

| Source | Location | Description |
|--------|----------|-------------|
| **Project** | `<project>/.ares/skills/` | Project self-declared; metadata only, **skills are never executed** |
| **User** | `~/.ares/skills/` | User-installed global capabilities |
| **Registered** | `~/.ares/config.toml` `[[skill_sources]]` | Explicitly declared extra directories |
| **Experience** (Learned) | `~/.ares/experience.json` | Task-experience records (relevance priors, **never auto-executed**) |

0.3.0 extensions (`git_source.go` / `http_source.go`): Registered sources support `type = "git"` (shallow-cloned into a local cache then indexed via `SyncGitSource` clone/pull) and `type = "http"/"oci"` (fetches a JSON manifest listing via `FetchHTTPManifest`).

Key implementation: `SkillDirs` reads exactly **one level** below the source root and only counts directories containing a `SKILL.md` or `skill.yaml` marker as skills — **declaration validation, never deep recursive scanning**.

## 3. Indexer: Metadata Only, Never the Body

`internal/ares_skills/indexer.go` builds the Level-0 index:

```go
// SkillIndexEntry is the metadata-only index record (Level 0 of progressive
// disclosure). The SKILL.md body is deliberately NOT loaded here.
type SkillIndexEntry struct {
    ID           string   // stable identifier (e.g. "rust-review")
    Name         string   // human-readable name
    Description  string   // resident one-liner
    Keywords     []string // search keywords
    Source       SourceKind // project | user | registered | experience
    Path         string   // skill directory
    Version      string   // manifest version
    Capabilities []string // capability labels
    ToolTypes    []string // declared tool kinds (from the manifest)
    Hash         string   // content hash for change detection
}
```

- **Front matter + manifest merge**: `SKILL.md` front matter (name/description/keywords/version) merges with `skill.yaml` (tool declarations), manifest fields win
- **Content hash**: deterministic hash (SKILL.md + skill.yaml + tools dir) → `DetectIndexChanges` classifies Added/Modified/Removed by ID+Source+Hash
- **Progressive-disclosure boundary**: the index never contains bodies — 100 skills = 100 × ~100 tokens

## 4. Discovery: Keyword Matching + FTS5 Fallback

`internal/ares_skills/discovery.go` handles retrieval (touching Level-0 metadata only):

- **Keyword matching** (default): `splitTerms` tokenizes → `matchScore` counts hits across ID/name/keywords/capabilities/description → ranked by hit count desc + ID asc (deterministic)
- **FTS5 full-text search** (`fts5.go`): in-memory SQLite FTS5 virtual table (`modernc.org/sqlite`, CGO-free) covering id/name/description/keywords, ordered by `ORDER BY rank`
- **Graceful fallback**: `Search` prefers FTS5 and **automatically falls back to keyword matching** on unsafe/failed queries — both share one entry point, invisible to callers

## 5. Loader / Resolver: Level-1/2 On-Demand Disclosure

- **Loader** (`loader.go`): `Load(id)` returns the full SKILL.md instruction body (Level-1, on demand); `ListReferences`/`LoadReference` manage the references directory (**path-traversal guard**: rejects names containing `/`, `\`, or `..`)
- **Resolver** (`resolver.go`): binds the manifest's tool declarations into callable capabilities through the **trust gate**:

| Tool Kind | Trust Condition |
|-----------|-----------------|
| `builtin` | Must be in the builtin tool registry (trusted) |
| `mcp` | Only needs a declared server name — connecting is left to `skill_activate` lazy loading |
| `executable` | Trusted sources only (project/user) and `allow_local_executables` enabled; **declaration-only existence check, no scanning** |

`Discovery ≠ Permission` (design principle 5): learned/external sources may be indexed but **never auto-executed**.

## 6. Experience: The Learned Source Doesn't "Generate" Skills

`internal/ares_skills/experience.go` records `{skill, task_pattern, success_rate}` relevance priors:

```go
type ExperienceRecord struct {
    Skill       string  // "pdf-generation"
    TaskPattern string  // "document-to-pdf"
    SuccessRate float64 // 0.94
}
```

- **Not LLM-generated skills** — it only records "which skill has high success on which task pattern"
- **JSON persistence** (`experience_store.go`): atomic write (tmp→rename), `~/.ares/experience.json`, survives restarts
- **Closed loop**: `SkillOutcomeRecorder` subscribes to the `EventSubTaskResult` event stream (read-only observer) → extracts `task.UsedExperienceID` + success → `Experience.Record(skill, pattern, rate)` → next `BestMatch` hits a higher success_rate

## 7. Catalog Facade: One Wrapper for the Whole Chain

`Catalog` in `internal/ares_skills/catalog.go` composes all components:

```go
func (c *Catalog) Build() error          // index all declared sources (git synced first, http manifests fetched)
func (c *Catalog) Search(q string, n int) []SkillIndexEntry  // Level-0 retrieval
func (c *Catalog) Load(id string) (string, error)            // Level-1 on-demand body
func (c *Catalog) ResolveTools(id string) ([]ResolvedTool, error) // Level-2 trust gate
func (c *Catalog) Activate(ctx, id string) ([]ResolvedTool, error) // lazy MCP connect (Level-2 execution)
func (c *Catalog) Refresh() (IndexChange, error)             // hash-based incremental re-index
func (c *Catalog) SeedRegistry(reg *skills.Registry) error   // pour into the memoryManager resident block
```

- **Concurrency-safe**: `sync.RWMutex` guards index swaps (Build/Refresh take the write lock; Search/Load take the read lock)
- **Refresh aligned with Build**: re-fetches http sources + rebuilds FTS5 (closing the old index) + re-seeds the registry

## 8. Production Wiring

`wireSkillCatalog` in `cmd/ares/serve.go`:

1. Reads `~/.ares/config.toml` `[[skill_sources]]` (directory + git + http/oci)
2. `NewCatalog` (project `.ares/skills` + user `~/.ares/skills` + registered sources + ExperiencePath)
3. Git sources sync with a 2-minute timeout → `Build()` builds the index
4. `SeedRegistry` → memoryManager resident skill block
5. `SetMCPConnector(comp.MCP)` → skill_activate lazy connection
6. `SetToolChangeHandler` → MCP listChanged triggers Refresh
7. `CatalogTools(catalog)` registers the skill_search/skill_load/skill_activate/skill_list/skill_experience quintet

### 8.1 envcap Unified Retrieval Bridge (tools/skills/commands aggregation)

Beyond the catalog's own retrieval, the discovery chain also exposes a **unified environment search** through the `Searcher` in `internal/tools/envcap` — aggregating "registered tools (builtin/MCP) + skills + native commands (allowlist)" into a single query entry point:

```go
// internal/tools/envcap/envcap.go
type Searcher struct {
    tools  ToolLister          // registered tools (builtin + MCP)
    skills *skills.Registry    // catalog index poured in via SeedRegistry
    cmds   *discovery.Discoverer // native-command allowlist (may be nil)
}
```

- **Bridge**: `catalog.SeedRegistry(reg)` pours the skills index into `*skills.Registry`, then `envcap.NewSearcher(tools, reg, cmds)` — the catalog becomes envcap's skill source (guarded by `TestCatalogSeedsEnvcapAggregation`)
- **Aggregated ranking**: `Search` returns `Capability{Kind: tool|skill|command}`, stably sorted by `kindRank` (tool < skill < command) then name ascending
- **Role**: envcap is the ToolResolver's **query front-end** — after a unified hit, resolution lands on the underlying provider via catalog `ResolveTools`/`Activate`

## 9. Summary

| Component | Responsibility | Design Principle |
|-----------|----------------|------------------|
| SourceManager | Declared-source enumeration (git/http extensions) | 1. No disk scanning |
| Indexer | Metadata-only index + hash | 4. Level-0 progressive disclosure |
| Discovery | Keyword + FTS5 (fallback) | 4. Metadata only to the LLM |
| Loader | On-demand body / references | 4. Level-1/2 |
| Resolver | Trust-gated tool binding | 5. Discovery ≠ execution |
| Experience | Learned-source priors (persistable) | 5. learned never auto-executes |
| Catalog | Facade + Refresh + Activate | 2. Skill ≠ Tool |

**The discovery chain's main line: turn capability management from runtime scanning into startup-time indexing.** Each of the five stages guards one principle; combined with ContextCleaner (token budget) and the peer/actionlog/lease collaboration primitives, they form the capability foundation the 0.3.0 agent is born with.

### 9.1 100-Skill Performance Benchmarks (measured in 0.3.0)

`internal/ares_skills/benchmark_test.go` (`BenchmarkCatalogBuild100Skills` / `Search100Skills` / `ExperienceBestMatch100` + `TestResidentMetadataTokenBudget`):

| Scenario | Measured (darwin/arm64, 200 runs) |
|----------|-----------------------------------|
| 100-skill index (Build) | **6.7 ms/op** (metadata-only, zero scanning) |
| 100-skill retrieval (Search, FTS5 hit) | **16.4 µs/op** (26 allocs) |
| Retrieval fallback (keyword) | **16.4 µs/op** (23 allocs) |
| 100-record Experience BestMatch | **4.3 µs/op** (2 allocs) |
| Resident metadata tokens | **~747 tokens / 100 skills ≈ 7 tokens/skill** (promised ~100/skill; measured an order of magnitude better) |

The progressive-disclosure promise "100 skills ≠ 100 full bodies" is measured: the resident block holds only name+description (never SKILL.md bodies), ~747 tokens for 100 skills.
