# 模块分析报告：`internal/ares_flight`（飞行记录 / 追踪）

> 分析范围：`internal/ares_flight/`（13 个 Go 文件）

---

## LOGIC（逻辑问题）

---

## LOGIC（低置信度）

### 5. `genealogy.go` `RecordResurrection` 父节点 children 未清理
- **位置**：`genealogy.go` 85-130 行
- **说明**：当 `oldNode.ParentID != ""` 时，`newNode` 作为父节点子节点加入（108-111 行），但 `oldNode` **没有**从 `parent.Children` 移除。父节点同时持有已死的 `oldNode` 和复活的 `newNode`，且 `newNode` 还继承 `oldNode.Children`。取决于意图可能是双表示或有意簿记。**（标注：需确认意图。）**

---

## 结论

| 优先级 | 位置 | 问题 |
|--------|------|------|
| 低 | `genealogy.go` 108 | 父 children 未清理复活节点 |
