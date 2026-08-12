# 模块分析报告：`internal/tools`（工具系统）

> 分析范围：`internal/tools/`（107 个 Go 文件），含 resources/、planner/ 等子包

---

## BUG（高置信度）

### 1. `resources/builtin/network/web_search.go` 默认 SearXNG 端点被 SSRF 自阻
- **位置**：`web_search.go` 104-109 行
- **说明**：`NewWebSearch()` 配置 `Transport: SSRFTransport()`（阻断 loopback/私有 IP），同时默认 SearXNG base URL 是 `http://localhost:5605`。`localhost` 解析到 `127.0.0.1`（loopback），SSRF dialer 拒绝所有到默认实例的连接。**web_search 工具无法访问自己的默认 SearXNG 服务器**。除非操作者显式 `SetAllowedBaseURLs` 一个非 loopback 主机（且该主机不解析到 loopback）。

### 2. `planner/extractor.go` 数学求和忽略下界
- **位置**：`extractor.go` 76-82 行
- **说明**：对"从 a 到 b"求和，当 `a>1 && b>a` 时返回 `b*(b+1)/2`，这是 1..b 的和。正确公式是 `(b-a+1)*(a+b)/2`。例："sum from 5 to 10" 返回 `10*11/2=55` 而非 `5+6+7+8+9+10=45`。**`a` 被解析但从未用于公式。**

### 3. `planner/bridge.go` 多步计划从不保存执行证据
- **位置**：`bridge.go` 226-305、348-388 行
- **说明**：`executeMultiStep` 通过 `executeStepWithFallback` 执行各步，该方法直接调 `tool.Execute`，从未调用 `executeStep`（唯一调用 `b.evidence.Save` 的方法）。因此多步（`IsMultiStep`）计划**不记录任何证据**。文档（doc.go 130-133）声称"fallback 不保存证据，仅最终尝试保存（由 executeStepWithFallback 调用的 executeStep 控制）"，但 `executeStepWithFallback` 根本不调 `executeStep`。文档或代码必有其一错误，按现有代码多步路径证据收集静默缺失。

---

## LOGIC（逻辑问题）

### 4. `planner/analyzer.go` 把正则当作字面量子串
- **位置**：`analyzer.go` 77、219-226 行
- **说明**：规则含关键字 `"到.*和"`，但 `matchAnyKeyword` 用 `strings.Contains`（不解释 `.*`），只匹配字面文本 `到.*和`，永远匹配不到作者想要的（如"1到100的和"）。正则风格关键字实际无效。

### 5. `planner/provider.go` 文档/实现不符
- **位置**：`provider.go` 74-87 行
- **说明**：`GetToolCapabilities` 文档说"工具未找到时返回 nil（resolver 视为'无动态能力'）"，但代码返回 `fmt.Errorf("tool %q not found", toolName)`。调用方当前丢弃错误（resolver.go 127 行 `continue`），碰巧能工作，但文档契约未被遵守。

### 6. `resources/builtin/file/file_tools.go` 冗余相同的分支
- **位置**：`file_tools.go` 467-478 行
- **说明**：递归 walk 模式检查中，`if info.IsDir() { return nil }` 与随后的 `return nil` 完全相同。`IsDir()` 区分无意义，意图可能是递归进入匹配目录。

### 7. `resources/builtin/text/data_validation.go` 每次调用重新编译正则
- **位置**：`data_validation.go` 172 行
- **说明**：`urlRegex` 在 `validate_url` 的每次调用内（`validateURL`）重新编译，与其它预编译的包级正则（`reEmail`）不一致。性能问题。

### 8. `resources/builtin/math/calculator.go` `round` 精度无界
- **位置**：`calculator.go` 240-247 行
- **说明**：`round(x, precision)` 对极大/负 `precision` 调 `math.Pow10(int(...))`，大值溢出、负值产生小数导致 NaN/inf 且无错误。precision 参数无范围保护。

---

## DEAD_CODE

### 9. 多个 `constants.go` 文件未被使用
- **位置**：`resources/builtin/hash/constants.go`、`execution/constants.go`、`knowledge/constants.go`
- **说明**：各文件中的 `FieldOperation`、`FieldInput`、`OpBase64Encode` 等常量仅测试使用，生产代码直接用字符串字面量。

### 10. `resources/core/capability.go` 整个 CapabilityEngine 未用
- **位置**：`capability.go` 30-228 行
- **说明**：`CapabilityEngine`、`capabilityKeywords` map 及全部方法在仓库中仅测试引用，生产未用。

### 11. `resources/core/factory.go` / `registry.go` / `result.go` / `base_tool.go`
- **位置**：多处
- **说明**：`ToolFactory`、`PluginRegistry`、`GlobalRegistry`（废弃）、`ToolGroup`、`ResultWithTiming`、`ErrorResult`、`NewBaseTool`、`WithMetadata` 等仅在测试中引用，生产未用。

### 12. `planner/extractor.go` 哑引用 `var _ = math.Round`
- **位置**：`extractor.go` 253-254 行
- **说明**：仅为保留 `math` import 的哑引用，`math` 在本文件其它地方未用。

### 13. `planner/evidence.go` `evidenceAggregator.Record` / `AggregateMetrics`
- **位置**：`evidence.go` 39-66 行
- **说明**：生产通过 `EvidenceStore.Save` 直接记录，`evidenceScorer.Score` 用自己的 `aggregateEvidence`，aggregator 的 `Record`/`AggregateMetrics` 仅测试使用，字段实为 write-only。

---

## 结论

| 优先级 | 位置 | 问题 |
|--------|------|------|
| **高** | `web_search.go` 104 | SSRF 自阻默认 SearXNG 端点 |
| **高** | `extractor.go` 76-82 | 求和忽略下界 a |
| **高** | `bridge.go` 226-305 | 多步计划从不保存证据 |
| 中 | `analyzer.go` 77 | 正则关键字当字面量匹配 |
| 中 | `file_tools.go` 467 | 冗余相同分支 |
| 低 | 多处 | 大量死代码（constants、capability、factory、registry 等） |
