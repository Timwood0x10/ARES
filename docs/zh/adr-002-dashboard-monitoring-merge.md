# ADR-002: Dashboard 与 Monitoring Console 合并

## 状态

✅ 已实施（2026-07）

## 背景

项目初期有两个独立的监控系统：
- `internal/dashboard/` — 基于 `net/http.ServeMux`，提供 REST API + WebSocket
- `internal/monitoring/` — 基于 Gin，提供控制台 UI + SSE

两者功能严重重叠（均有 `/health`、`/anomalies`、`/insights` 端点），且各自维护独立的 intelligence 数据流。`dashboard.Engine`（300+ 行生产级智能引擎）的 health scoring / anomaly detection / insight generation 结果无法被 `monitoring.ConsoleAPI` 使用。

## 决策

1. **路由统一到 Gin**：`dashboard.APIv2.MountGinRoutes(rg)` 将 arena/flight/ws 注册到 Gin engine，`api_impl/service.go` 使用 `monitoring.NewHTTPServer(WithDashboardAPI(dashAPI))`
2. **统一 intelligence 数据流**：通过 `IntelAdapter` 桥接 `dashboard.Engine` → `monitoring.IntelProvider`，4 条 intelligence 路由由同一引擎驱动
3. **统一实时推送**：WebSocket (`/ws`) 作为唯一推送通道，`AgentWatcher` 每 5s 主动推 health/anomalies/insights

## 架构

```
事件 → EventBridge → dashboard.Engine (健康评分/异常检测)
                   → IntelAdapter → monitoring.MonitorPlugin.SetIntel()
                                  → /api/health → real system level
                                  → /api/anomalies → real anomaly count
                                  → /api/insights → real insight count
                   → AgentWatcher.pushLoop() → WebSocket broadcast every 5s
```

## 影响

- 正面：同一个 intelligence engine 驱动所有监控端点，数据一致
- 正面：消除重复端口监听、重复路由定义
- 正面：`AgentWatcher` 实现主动推送（proactive），而非被动 polling（passive）
- 负面：Gin engine 依赖添加到 `internal/dashboard/`（之前只有 net/http）

## 实现文件

- `internal/dashboard/intelligence.go` — Engine 健康评分/异常检测
- `internal/dashboard/watcher.go` — AgentWatcher 主动推送
- `internal/dashboard/arena_bridge.go` — 真实 arena 桥接
- `internal/monitoring/adapter/intel_adapter.go` — Engine → IntelProvider
- `internal/dashboard/api.go` — MountGinRoutes()
