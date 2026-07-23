# Conversation Compiler 代码审查报告

> 审查范围：`internal/ares_memory/compiler/`（26 文件）+ config/bootstrap 接线 + 设计文档
> 审查模式：只读，不修改任何源码
> 日期：2026-07-23

---

## 总体结论

代码质量高、架构清晰、测试覆盖 87.2%、全绿。管线分层（Extract → Normalize → Compile → KM → Selector → Consumer → Distiller）设计合理，全程 zero-LLM，复用现有 distillation 模块。

**但有一个方向性致命问题：这个压缩模块完全没有接入 live agent 的任何消费路径，是又一个"接上却空转"的孤立模块。**

---

## P0 — 方向性问题：完全孤立，零实际效果

### 现象

`wireKnowledgeCompiler` 把 Pipeline + Lifecycle 组装到 `comp.KnowledgeCompiler` 字段（`bootstrap.go:279`），但全仓库搜索 `comp.KnowledgeCompiler` 的读取方，**结果全部在 bootstrap 和它的测试文件里**。没有任何生产代码消费这个字段：

| 组件 | 被谁调用 | 实际效果 |
|:-----|:---------|:--------:|
| `ShouldCompile()` | 无（仅 lifecycle_test） | ❌ |
| `Lifecycle.Compile()` | 无（仅 lifecycle_test） | ❌ |
| `RenderPrompt()` | 无（仅 lifecycle_test） | ❌ |
| `Pipeline.Run()` | 无（仅 pipeline_test） | ❌ |
| `comp.KnowledgeCompiler` 字段 | 无生产读取方 | ❌ |

### 对比设计目标

CONVERSATION_COMPILER.md Phase 4 明确写了目标是 **"替换 `BuildPromptMessages`"**。而 `BuildPromptMessages` 仍然存在于 `internal/ares_memory/manager.go`、`production_manager.go`、`manager_impl.go`，**完全没有被编译器替换或桥接**。

### 结论

即使把 `knowledge_compiler.enabled` 设为 `true`，系统行为**零变化**。这与你之前点名过的 `internal/evolution` 是完全相同的模式——bootstrap 接了线，但没有消费者。

---

## P1 — 逻辑缺陷

### 1. `splitSentences` 误切小数点和缩写

**文件**：`akg_extractor.go:522-536`

```go
func splitSentences(content string) []string {
    for _, r := range content {
        current.WriteRune(r)
        if r == '.' || r == '!' || r == '?' {
            sentences = append(sentences, current.String())
            current.Reset()
        }
    }
}
```

"3.14" → "3." + "14"；"e.g." → "e." + "g."；"v0.2.7" → "v0." + "2." + "7"。

这导致 fact/constraint/tradeoff 提取在含数字版本号、IP 地址、URL 的技术对话上严重碎片化，而这些恰恰是 ARES 的主要输入。

### 2. `extractTriple` 只取 object 第一个词

**文件**：`akg_extractor.go:486-491`

"ARES uses Patch for runtime updates" → fact = (ARES, uses, **Patch**)，丢失了 "for runtime updates"。

object 永远是单 token，导致 fact 语义严重缺失。Triple 提取应该是 "subject + predicate + 整个剩余子句（到下一个标点）"，而不是截到第一个空格。

### 3. `Prune` 用 O(n²) 冒泡排序

**文件**：types.go:249-255

```go
for i := 0; i < len(candidates); i++ {
    for j := i + 1; j < len(candidates); j++ {
        if candidates[j].score < candidates[i].score {
            candidates[i], candidates[j] = candidates[j], candidates[i]
        }
    }
}
```

500 节点时 125K 次比较。虽然不致命，但包内其他所有选择器都用了 `sort.Slice`，这里应保持一致。

### 4. `memoryID` 用 FNV-32a，碰撞风险

**文件**：`km_distiller.go:453-457`

32 位 hash 在 memory 节点增多后碰撞概率非零。两个不同 summary 映射到同一 memory ID 会导致**错误的跨 cluster 合并**——这是语义正确性问题，不是性能问题。建议换 FNV-64a 或 SHA-256 截断。

---

## P2 — 设计风险

### 5. `CurrentModel()` 返回 live model 裸指针

**文件**：`context_lifecycle.go:148-152`

注释自己承认风险："callers must NOT read it concurrently with a Compile call"。但返回的是 `*KnowledgeModel` 裸指针——调用方拿到后做任何遍历（`range km.Nodes`）都会与 Compile 的 map write 竞争，Go 的 concurrent map read+write 是 **fatal panic**，不是数据竞争。

要么返回深拷贝快照，要么移除此方法只保留 `RenderPrompt`（后者已经持锁安全）。

### 6. 增量编译直接 mutate 调用者的 model 指针

**文件**：`graph_compiler.go:48-49`

```go
if cfg.Incremental && cfg.PreviousModel != nil {
    model = cfg.PreviousModel  // 直接复用指针
}
```

在 `ContextLifecycle` 内部是故意的（共享指针 + mutex），但 `CompileConfig.PreviousModel` 是公开字段，如果有其他调用方传入自己的 model 后继续使用，会遭遇意外的 in-place mutation。接口契约应在文档中明确标注 "model will be mutated in place"。

### 7. `defaultNormalizerAdapter` 名不副实

**文件**：`akg_extractor.go:61-76`

注释说 "uses AKG's EntityMatcher for entity recognition"，但实际只做了 `strings.Fields` + `Join` 去多余空白。没有调用 EntityMatcher，没有 NER。AKGExtractionPipeline 里只挂了一个 normalize 接口，EntityMatcher/Summarizer 完全没接线。

### 8. `ShouldCompile` 的 token 估算对中文严重不准

**文件**：`compiler.go:226`

```go
estimatedTokens := totalChars / 4
```

英文约 4 chars/token 合理；中文一个字约 1-2 token，实际 token 数远高于 `chars/4` 估算。**对中文环境会低估 4-8 倍**，导致压缩触发严重滞后——等真正触发时上下文早已溢出。考虑到你的项目是中文环境，这是实际影响。

---

## P3 — 代码异味

### 9. `extractCodeBlockEntities` 与 `extractCodeBlockLanguages` 完全重复

**文件**：`akg_extractor.go:167-187` vs `504-519`

两个函数逻辑完全相同，应合并为一个。

### 10. `tradeoffIndicators` 有重复项

**文件**：`akg_extractor.go:378-379`

`" but sacrifices "` 出现两次。

### 11. `isCommonWord` 硬编码 75 个词

**文件**：`akg_extractor.go:548-572`

每次调用都重建 map，且仅覆盖英文。中文对话里大写字母启发式完全失效（`isCapitalized` 对中文返回 false），导致中文实体提取率接近零。

---

## 优点

- **架构分层清晰**：Compiler(Extract→Normalize→Compile) → KM(Graph) → Selector → Consumer → Distiller，每层职责明确，接口小而聚焦。
- **全程 zero-LLM**：提取靠规则、归一化靠别名表、蒸馏复用 distillation 的 MemoryClassifier + ImportanceScorer，无任何 LLM token 成本。
- **确定性保证**：所有选择器 sort by score desc then ID asc，输出稳定可复现。
- **opt-in 设计**：默认 `enabled: false`，不破坏现有行为。
- **测试质量高**：87.2% 覆盖，含并发竞争测试（`TestContextLifecycleConcurrentRender`）、边界测试、表驱动验证测试。
- **文档注释充分**：每个导出函数都有 Args/Returns 文档，设计意图可追溯。
- **配置校验完整**：`validateKnowledgeCompiler` 覆盖所有参数边界。
- **接线错误处理**：`db.Close()` 返回值不再被忽略（`bootstrap_steps.go` 修复）。

---

## 优先级建议

| 优先级 | 问题 | 影响 |
|:------:|:-----|:-----|
| **P0** | 模块完全孤立，无 live agent 消费者 | 压缩功能零效果，需接入消息处理路径 |
| **P1** | splitSentences 误切数字/缩写 | 技术对话提取碎片化 |
| **P1** | extractTriple 丢 object 多词 | fact 语义缺失 |
| **P2** | CurrentModel 返回裸指针 | 并发 map panic 风险 |
| **P2** | ShouldCompile 中文 token 估算 | 中文环境触发滞后 4-8 倍 |
| **P2** | memoryID FNV-32a 碰撞 | 错误合并风险 |
| **P3** | 代码重复/异味 | 可维护性 |
