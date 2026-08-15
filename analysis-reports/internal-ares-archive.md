# 模块分析报告：`internal/ares_archive`（归档）

> 分析范围：`internal/ares_archive/`（17 个 Go 文件）

---

## LOGIC（逻辑问题）

---

## DEAD_CODE

---

## 结论

| 优先级 | 位置 | 问题 |
|--------|------|------|
| 中 | `identifiers.go` 86 | ip 角色强制要求端口，纯 IP 被拒绝 |
| 中 | `extract.go` 117 | 任意退出码记入 GoVet，可能误归因 |
