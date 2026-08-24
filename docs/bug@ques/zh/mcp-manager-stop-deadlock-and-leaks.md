# MCP Manager：停机死锁、连接覆盖泄漏、刷新清零工具

日期：2026-08-24
范围：internal/ares_mcp

## 1. Stop/DisconnectServer 死锁

`MCPManager.Stop` 持有 `m.mu` 的同时调用 `mc.client.Close()`，而 Close 会等待
`eg.Wait()`。该 errgroup 包含 receive loop 派生的通知协程；
`handleNotification` → `onChange()` → `RefreshTools` 会阻塞在获取 `m.mu` 上。
停机期间恰有 `tools/listChanged` 通知在途时，整个 manager 永久死锁
（后续所有 ListServers/GetClient 一并阻塞）。

修复：在锁内快照并摘除 clients，然后在锁外 `Close()`。

## 2. ConnectServer 无条件覆盖存活连接

`ConnectServer` 以无条件 `m.clients[name] = mc` 收尾。并发的
Start/ApplyConfig 重连会孤儿化旧的 managedClient：其 stdio 子进程永远不会被
kill，receive loop 协程泄漏。

修复：锁内完成替换，锁外关闭旧 client。

## 3. RefreshTools 先注销、失败即中止

刷新期间一次瞬时 `ListTools` 失败（每次 listChanged 通知都会触发）会让该
server 的已注册工具清零，直到下一次成功的刷新。

修复：重新发现失败时，用 client 内仍有效的缓存工具定义尽力恢复上一次的
注册集合，然后再返回错误。

## 回归测试

internal/ares_mcp/manager_lifecycle_test.go：
- `TestStopDoesNotDeadlockWithInFlightNotification`：通知处理器在途时 Stop 必须
  在超时内完成（旧锁纪律下本测试永久挂起）。
- `TestConnectTwiceReplacesAndClosesStaleClient`：二次连接必须关闭旧 client。
- `TestRefreshToolsFailureRestoresPreviousTools`：刷新失败后旧工具必须恢复。
