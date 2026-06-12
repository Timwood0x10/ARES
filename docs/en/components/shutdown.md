# Shutdown Module API Documentation

## 1. Overview

The Shutdown module provides a comprehensive solution for graceful system shutdown, supporting multi-phase shutdown, callback management, signal handling, and concurrency control.

## 2. Core Components

### 2.1 Manager

`Manager` coordinates the shutdown process, managing multiple shutdown phases and callbacks.

#### Methods

| Method | Parameters | Returns | Description |
|--------|------------|---------|-------------|
| `NewManager(timeout)` | `timeout time.Duration` | `*Manager` | Creates a new shutdown manager |
| `RegisterPhase(phase, timeout)` | `phase Phase, timeout time.Duration` | - | Registers a shutdown phase |
| `AddCallback(phase, callback)` | `phase Phase, callback Callback` | `error` | Adds a callback to a phase |
| `StartShutdown(ctx)` | `ctx context.Context` | `error` | Starts the shutdown process |
| `SetOnTimeout(phase, fn)` | `phase Phase, fn func()` | - | Sets phase timeout callback |
| `SetOnPanic(phase, fn)` | `phase Phase, fn func(interface{})` | - | Sets panic recovery callback |
| `CurrentPhase()` | - | `Phase` | Gets current phase |
| `Wait()` | - | - | Waits for all operations to complete |
| `IsShutdown()` | - | `bool` | Checks if shutdown has started |

#### Usage Example

```go
// Create manager
manager := NewManager(30 * time.Second)

// Register phases
manager.RegisterPhase(PhasePreShutdown, 5*time.Second)
manager.RegisterPhase(PhaseGraceful, 10*time.Second)
manager.RegisterPhase(PhaseForce, 5*time.Second)

// Add callbacks
manager.AddCallback(PhaseGraceful, func(ctx context.Context) error {
    // Save state
    return nil
})

// Start shutdown
ctx := context.Background()
err := manager.StartShutdown(ctx)
```

### 2.2 Phase

`Phase` represents a shutdown phase.

#### Phase Constants

| Constant | Value | Description |
|----------|-------|-------------|
| `PhasePreShutdown` | 0 | Pre-shutdown phase |
| `PhaseGraceful` | 1 | Graceful shutdown phase |
| `PhaseForce` | 2 | Force shutdown phase |
| `PhaseDone` | 3 | Completion phase |

#### Methods

| Method | Returns | Description |
|--------|---------|-------------|
| `String()` | `string` | Gets phase name |
| `IsValid()` | `bool` | Checks if phase is valid |

### 2.3 PhaseExecutor

`PhaseExecutor` executes a specific phase with retry and rollback support.

#### Methods

| Method | Parameters | Returns | Description |
|--------|------------|---------|-------------|
| `NewPhaseExecutor(phase, maxRetries)` | `phase Phase, maxRetries int` | `*PhaseExecutor` | Creates a phase executor |
| `Execute(ctx, fn)` | `ctx context.Context, fn func(ctx context.Context) error` | `error` | Executes the phase |
| `Rollback()` | - | `error` | Performs rollback |
| `State()` | - | `PhaseState` | Gets current state |
| `Phase()` | - | `Phase` | Gets phase |
| `Duration()` | - | `time.Duration` | Gets execution duration |
| `Error()` | - | `error` | Gets error |
| `Retries()` | - | `int` | Gets retry count |
| `SetRollbackFn(fn)` | `fn func() error` | - | Sets rollback function |
| `SetOnComplete(fn)` | `fn func() error` | - | Sets completion callback |
| `SetOnFailure(fn)` | `fn func(error) error` | - | Sets failure callback |

#### Usage Example

```go
executor := NewPhaseExecutor(PhaseGraceful, 3)

// Set rollback function
executor.SetRollbackFn(func() error {
    // Clean up resources
    return nil
})

// Execute phase
err := executor.Execute(context.Background(), func(ctx context.Context) error {
    // Execute business logic
    return nil
})

if err != nil {
    executor.Rollback()
}
```

### 2.4 PhaseState

`PhaseState` represents phase execution state.

#### State Constants

| Constant | Value | Description |
|----------|-------|-------------|
| `PhaseStatePending` | 0 | Pending execution |
| `PhaseStateRunning` | 1 | Currently executing |
| `PhaseStateCompleted` | 2 | Completed |
| `PhaseStateFailed` | 3 | Execution failed |
| `PhaseStateSkipped` | 4 | Skipped |

#### Methods

| Method | Returns | Description |
|--------|---------|-------------|
| `String()` | `string` | Gets state name |

### 2.5 CallbackRegistry

`CallbackRegistry` manages shutdown callbacks with priority sorting.

#### Methods

| Method | Parameters | Returns | Description |
|--------|------------|---------|-------------|
| `NewCallbackRegistry()` | - | `*CallbackRegistry` | Creates a callback registry |
| `Register(phase, id, priority, fn, timeout)` | `phase Phase, id string, priority int, fn Callback, timeout time.Duration` | `error` | Registers a callback |
| `Unregister(phase, id)` | `phase Phase, id string` | `error` | Unregisters a callback |
| `GetCallbacks(phase)` | `phase Phase` | `[]Callback` | Gets callbacks for a phase |
| `Clear(phase)` | `phase Phase` | - | Clears callbacks for a phase |
| `Count(phase)` | `phase Phase` | `int` | Gets callback count |
| `SetOnError(phase, id, onError)` | `phase Phase, id string, onError func(error)` | `error` | Sets error handler |

#### Usage Example

```go
registry := NewCallbackRegistry()

// Register callback
registry.Register(PhaseGraceful, "save-state", 10, func(ctx context.Context) error {
    // Save state
    return nil
}, 5*time.Second)

// Get callbacks
callbacks := registry.GetCallbacks(PhaseGraceful)
```

### 2.6 CallbackChain

`CallbackChain` supports sequential or parallel execution of multiple callbacks.

#### Methods

| Method | Parameters | Returns | Description |
|--------|------------|---------|-------------|
| `NewCallbackChain()` | - | `*CallbackChain` | Creates a callback chain |
| `Add(fn)` | `fn Callback` | `*CallbackChain` | Adds a callback |
| `Execute(ctx)` | `ctx context.Context` | `error` | Executes all callbacks sequentially |
| `ExecuteParallel(ctx)` | `ctx context.Context` | `error` | Executes all callbacks in parallel |

#### Usage Example

```go
chain := NewCallbackChain()

// Add callbacks
chain.Add(func(ctx context.Context) error {
    // First callback
    return nil
})

chain.Add(func(ctx context.Context) error {
    // Second callback
    return nil
})

// Sequential execution
err := chain.Execute(context.Background())

// Parallel execution
err = chain.ExecuteParallel(context.Background())
```

### 2.7 SignalHandler

`SignalHandler` handles system signals to trigger graceful shutdown.

#### Methods

| Method | Parameters | Returns | Description |
|--------|------------|---------|-------------|
| `NewSignalHandler(manager)` | `manager *Manager` | `*SignalHandler` | Creates a signal handler |
| `Start(ctx)` | `ctx context.Context` | `error` | Starts listening for signals |
| `Stop()` | - | `error` | Stops listening for signals |
| `AddSignal(sig)` | `sig os.Signal` | - | Adds a signal to listen for |
| `SetContext(ctx)` | `ctx context.Context` | - | Sets context |

#### Usage Example

```go
manager := NewManager(30 * time.Second)
handler := NewSignalHandler(manager)

// Start signal handling
ctx := context.Background()
handler.Start(ctx)

// Add custom signal
handler.AddSignal(syscall.SIGUSR1)
```

## 3. Utility Functions

### 3.1 WaitForSignal

```go
func WaitForSignal(signals ...os.Signal) os.Signal
```

Blocks waiting for a signal. Defaults to waiting for `os.Interrupt`.

### 3.2 WaitForContextOrSignal

```go
func WaitForContextOrSignal(ctx context.Context, signals ...os.Signal) (os.Signal, error)
```

Blocks waiting for context cancellation or signal.

## 4. Error Types

### 4.1 PhaseError

Phase execution error.

```go
var ErrPhaseAlreadyRunning = &PhaseError{"phase already running"}
```

### 4.2 CallbackError

Callback operation error.

```go
var ErrCallbackNotFound = &CallbackError{"callback not found"}
```

### 4.3 SignalError

Signal handling error.

```go
var ErrSignalHandlerAlreadyStarted = &SignalError{"signal handler already started"}
```

## 5. Type Definitions

### 5.1 Callback

```go
type Callback func(ctx context.Context) error
```

Shutdown callback function type.

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

Registered callback structure.

## 6. Best Practices

### 6.1 Error Handling

- All callbacks must return error
- Use `SetOnPanic` to recover from panics and prevent affecting other callbacks
- Use `SetOnTimeout` to handle timeout scenarios

### 6.2 Concurrency Safety

- `Manager` uses `sync.RWMutex` to protect shared state
- `CallbackRegistry` supports concurrent registration and unregistration
- `CallbackChain.ExecuteParallel` method for concurrent execution

### 6.3 Timeout Control

- Set reasonable timeout for each phase
- Use context to control callback execution time
- Timeout will trigger the callback set by `SetOnTimeout`

## 7. Performance Considerations

- Callback execution uses goroutines for concurrency
- Callback registration uses read-write locks for high concurrency
- Supports callback priority sorting to ensure critical callbacks execute first

## 8. Test Coverage

Current test coverage: **95.4%**

Includes following test scenarios:
- Complete shutdown process testing
- Callback timeout and panic recovery
- Concurrent execution and context cancellation
- Phase retry and rollback mechanisms
- Signal handling and error recovery