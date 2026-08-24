# MCP Manager: Shutdown Deadlock, Connection-Overwrite Leak, Refresh Zeroing Tools

Date: 2026-08-24
Scope: internal/ares_mcp

## 1. Stop/DisconnectServer deadlock

`MCPManager.Stop` held `m.mu` while calling `mc.client.Close()`, which waits on
`eg.Wait()`. The errgroup contains notification goroutines spawned by the
receive loop; `handleNotification` → `onChange()` → `RefreshTools` blocks
acquiring `m.mu`. A `tools/listChanged` notification in flight during shutdown
deadlocked the whole manager permanently (every later ListServers/GetClient
blocked too).

Fix: snapshot + evict clients under the lock, then `Close()` OUTSIDE it.

## 2. ConnectServer overwrote a live connection

`ConnectServer` ended with an unconditional `m.clients[name] = mc`. Overlapping
Start/ApplyConfig re-connects orphaned the previous managedClient: its stdio
subprocess was never killed and its receive loop leaked.

Fix: swap under the lock, then close the STALE client outside the lock.

## 3. RefreshTools unregistered first, aborted on failure

A transient `ListTools` failure (triggered by every listChanged notification)
left the server with ZERO registered tools until some later successful refresh.

Fix: on re-discovery failure, restore the previous registration from the
client's still-valid cached tool definitions (best effort) before returning
the error.

## Regression Tests

Existing manager tests never exercised connect→register cycles or these paths;
the fixes are pinned by:
- `TestDeploymentStaging_DoesNotMutateLiveRegistry` pattern applied to MCP:
  see `internal/ares_mcp/manager_test.go` additions covering
  DisconnectServer-after-Stop idempotence and double-connect cleanup.
