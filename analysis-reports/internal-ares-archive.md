# 模块分析报告：`internal/ares_archive`（归档）

> 分析范围：`internal/ares_archive/`（17 个 Go 文件）

---

## LOGIC（逻辑问题）

### 1. `identifiers.go` `patternForRole` 的 `ip` 角色强制要求带端口
- **位置**：`identifiers.go` 86 行
- **说明**：`roleIP` 映射到 `reIPPort`（要求 `ip:port` 形式，正则 `\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}:\d+\b`）。以纯 IP（如 `10.0.0.1`）作为 `"ip"` 角色值时，`ProtectIdentifiers` 校验返回 `ErrInvalidIdentifier`，尽管该值是完全合法的 IP。角色名 `"ip"` 强烈暗示应接受裸 IP，但模式强制带端口。**角色到模式的映射错误或常量命名错误。**

### 2. `extract.go` `extractVerdict` 把任意 code_runner 退出码记入 GoVet
- **位置**：`extract.go` 117-124 行
- **说明**：任何 `code_runner` 工具事件的退出码都设置 `GoVet` 字段，即便工具并未运行 `go vet`。非零退出码（如构建/测试失败）会被记录为 `vet=fail`，可能错误归因判定。**（标注：按文档是有意的，但存在误归因风险。）**

---

## DEAD_CODE

---

## 结论

| 优先级 | 位置 | 问题 |
|--------|------|------|
| 中 | `identifiers.go` 86 | ip 角色强制要求端口，纯 IP 被拒绝 |
| 中 | `extract.go` 117 | 任意退出码记入 GoVet，可能误归因 |
