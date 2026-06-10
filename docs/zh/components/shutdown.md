# Shutdown 模块 API 文档

## 1. 概述

Shutdown 模块提供了系统优雅关闭的完整解决方案，支持多阶段关闭、回调管理、信号处理和并发控制。

## 2. 核心组件

### 2.1 Manager

`Manager` 是关闭流程的协调器，管理多个关闭阶段和回调函数。

#### 方法列表

| 方法 | 参数 | 返回值 | 描述 |
|------|------|--------|------|
| `NewManager(timeout)` | `timeout time.Duration` | `*Manager` | 创建新的关闭管理器 |
| `RegisterPhase(phase, timeout)` | `phase Phase, timeout time.Duration` | - | 注册关闭阶段 |
| `AddCallback(phase, callback)` | `phase Phase, callback Callback` | `error` | 向指定阶段添加回调函数 |
| `StartShutdown(ctx)` | `ctx context.Context` | `error` | 启动关闭流程 |
| `SetOnTimeout(phase, fn)` | `phase Phase, fn func()` | - | 设置阶段超时回调 |
| `SetOnPanic(phase, fn)` | `phase Phase, fn func(interface{})` | - | 设置 panic 恢复回调 |
| `CurrentPhase()` | - | `Phase` | 获取当前阶段 |
| `Wait()` | - | - | 等待所有操作完成 |
| `IsShutdown()` | - | `bool` | 检查是否已开始关闭 |

#### 使用示例

```go
// 创建管理器
manager := NewManager(30 * time.Second)

// 注册阶段
manager.RegisterPhase(PhasePreShutdown, 5*time.Second)
manager.RegisterPhase(PhaseGraceful, 10*time.Second)
manager.RegisterPhase(PhaseForce, 5*time.Second)

// 添加回调
manager.AddCallback(PhaseGraceful, func(ctx context.Context) error {
    // 保存状态
    return nil
})

// 启动关闭
ctx := context.Background()
err := manager.StartShutdown(ctx)
```

### 2.2 Phase

`Phase` 表示关闭阶段。

#### 阶段常量

| 常量 | 值 | 描述 |
|------|------|------|
| `PhasePreShutdown` | 0 | 预关闭阶段 |
| `PhaseGraceful` | 1 | 优雅关闭阶段 |
| `PhaseForce` | 2 | 强制关闭阶段 |
| `PhaseDone` | 3 | 完成阶段 |

#### 方法列表

| 方法 | 返回值 | 描述 |
|------|--------|------|
| `String()` | `string` | 获取阶段名称 |
| `IsValid()` | `bool` | 检查阶段是否有效 |

### 2.3 PhaseExecutor

`PhaseExecutor` 执行特定阶段，支持重试和回滚。

#### 方法列表

| 方法 | 参数 | 返回值 | 描述 |
|------|------|--------|------|
| `NewPhaseExecutor(phase, maxRetries)` | `phase Phase, maxRetries int` | `*PhaseExecutor` | 创建阶段执行器 |
| `Execute(ctx, fn)` | `ctx context.Context, fn func(ctx context.Context) error` | `error` | 执行阶段 |
| `Rollback()` | - | `error` | 执行回滚 |
| `State()` | - | `PhaseState` | 获取当前状态 |
| `Phase()` | - | `Phase` | 获取阶段 |
| `Duration()` | - | `time.Duration` | 获取执行时长 |
| `Error()` | - | `error` | 获取错误 |
| `Retries()` | - | `int` | 获取重试次数 |
| `SetRollbackFn(fn)` | `fn func() error` | - | 设置回滚函数 |
| `SetOnComplete(fn)` | `fn func() error` | - | 设置完成回调 |
| `SetOnFailure(fn)` | `fn func(error) error` | - | 设置失败回调 |

#### 使用示例

```go
executor := NewPhaseExecutor(PhaseGraceful, 3)

// 设置回滚函数
executor.SetRollbackFn(func() error {
    // 清理资源
    return nil
})

// 执行阶段
err := executor.Execute(context.Background(), func(ctx context.Context) error {
    // 执行业务逻辑
    return nil
})

if err != nil {
    executor.Rollback()
}
```

### 2.4 PhaseState

`PhaseState` 表示阶段执行状态。

#### 状态常量

| 常量 | 值 | 描述 |
|------|------|------|
| `PhaseStatePending` | 0 | 等待执行 |
| `PhaseStateRunning` | 1 | 正在执行 |
| `PhaseStateCompleted` | 2 | 已完成 |
| `PhaseStateFailed` | 3 | 执行失败 |
| `PhaseStateSkipped` | 4 | 已跳过 |

#### 方法列表

| 方法 | 返回值 | 描述 |
|------|--------|------|
| `String()` | `string` | 获取状态名称 |

### 2.5 CallbackRegistry

`CallbackRegistry` 管理关闭回调，支持优先级排序。

#### 方法列表

| 方法 | 参数 | 返回值 | 描述 |
|------|------|--------|------|
| `NewCallbackRegistry()` | - | `*CallbackRegistry` | 创建回调注册表 |
| `Register(phase, id, priority, fn, timeout)` | `phase Phase, id string, priority int, fn Callback, timeout time.Duration` | `error` | 注册回调 |
| `Unregister(phase, id)` | `phase Phase, id string` | `error` | 注销回调 |
| `GetCallbacks(phase)` | `phase Phase` | `[]Callback` | 获取指定阶段的回调 |
| `Clear(phase)` | `phase Phase` | - | 清空指定阶段的回调 |
| `Count(phase)` | `phase Phase` | `int` | 获取回调数量 |
| `SetOnError(phase, id, onError)` | `phase Phase, id string, onError func(error)` | `error` | 设置错误处理器 |

#### 使用示例

```go
registry := NewCallbackRegistry()

// 注册回调
registry.Register(PhaseGraceful, "save-state", 10, func(ctx context.Context) error {
    // 保存状态
    return nil
}, 5*time.Second)

// 获取回调
callbacks := registry.GetCallbacks(PhaseGraceful)
```

### 2.6 CallbackChain

`CallbackChain` 支持顺序或并行执行多个回调。

#### 方法列表

| 方法 | 参数 | 返回值 | 描述 |
|------|------|--------|------|
| `NewCallbackChain()` | - | `*CallbackChain` | 创建回调链 |
| `Add(fn)` | `fn Callback` | `*CallbackChain` | 添加回调 |
| `Execute(ctx)` | `ctx context.Context` | `error` | 顺序执行所有回调 |
| `ExecuteParallel(ctx)` | `ctx context.Context` | `error` | 并行执行所有回调 |

#### 使用示例

```go
chain := NewCallbackChain()

// 添加回调
chain.Add(func(ctx context.Context) error {
    // 第一个回调
    return nil
})

chain.Add(func(ctx context.Context) error {
    // 第二个回调
    return nil
})

// 顺序执行
err := chain.Execute(context.Background())

// 并行执行
err = chain.ExecuteParallel(context.Background())
```

### 2.7 SignalHandler

`SignalHandler` 处理系统信号，触发优雅关闭。

#### 方法列表

| 方法 | 参数 | 返回值 | 描述 |
|------|------|--------|------|
| `NewSignalHandler(manager)` | `manager *Manager` | `*SignalHandler` | 创建信号处理器 |
| `Start(ctx)` | `ctx context.Context` | `error` | 开始监听信号 |
| `Stop()` | - | `error` | 停止监听信号 |
| `AddSignal(sig)` | `sig os.Signal` | - | 添加要监听的信号 |
| `SetContext(ctx)` | `ctx context.Context` | - | 设置上下文 |

#### 使用示例

```go
manager := NewManager(30 * time.Second)
handler := NewSignalHandler(manager)

// 启动信号处理
ctx := context.Background()
handler.Start(ctx)

// 添加自定义信号
handler.AddSignal(syscall.SIGUSR1)
```

## 3. 辅助函数

### 3.1 WaitForSignal

```go
func WaitForSignal(signals ...os.Signal) os.Signal
```

阻塞等待信号。默认等待 `os.Interrupt`。

### 3.2 WaitForContextOrSignal

```go
func WaitForContextOrSignal(ctx context.Context, signals ...os.Signal) (os.Signal, error)
```

阻塞等待上下文取消或信号。

## 4. 错误类型

### 4.1 PhaseError

阶段执行错误。

```go
var ErrPhaseAlreadyRunning = &PhaseError{"phase already running"}
```

### 4.2 CallbackError

回调操作错误。

```go
var ErrCallbackNotFound = &CallbackError{"callback not found"}
```

### 4.3 SignalError

信号处理错误。

```go
var ErrSignalHandlerAlreadyStarted = &SignalError{"signal handler already started"}
```

## 5. 类型定义

### 5.1 Callback

```go
type Callback func(ctx context.Context) error
```

关闭回调函数类型。

### 5.2 RegisteredCallback

```go
type RegisteredCallback struct {
    ID       string
    Priority int
    Fn       Callback
    Timeout  time.Duration
    OnError  func(error)
}
```

已注册的回调结构。

## 6. 最佳实践

### 6.1 错误处理

- 所有回调必须返回 error
- 使用 `SetOnPanic` 恢复 panic，防止影响其他回调
- 使用 `SetOnTimeout` 处理超时情况

### 6.2 并发安全

- `Manager` 使用 `sync.RWMutex` 保护共享状态
- `CallbackRegistry` 支持并发注册和注销
- `CallbackChain` 的 `ExecuteParallel` 方法用于并发执行

### 6.3 超时控制

- 为每个阶段设置合理的超时时间
- 使用 context 控制回调执行时间
- 超时后会调用 `SetOnTimeout` 设置的回调

## 7. 性能考虑

- 回调执行使用 goroutine 并发
- 回调注册使用读写锁，支持高并发
- 支持回调优先级排序，确保关键回调优先执行

## 8. 测试覆盖率

当前测试覆盖率：**95.4%**

包含以下测试场景：
- 完整关闭流程测试
- 回调超时和 panic 恢复
- 并发执行和上下文取消
- 阶段重试和回滚机制
- 信号处理和错误恢复