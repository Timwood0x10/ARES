# ares 架构深度解析（二十六）：Agent 通信 — 原语层如何让 Agent 互相说话

> 说明：本文基于实际代码（`internal/agents/peer`、`internal/agents/leader`、`internal/ares_protocol/ahp`、`internal/agents/actionlog`、`internal/agents/lease`），是 docs 系列中 Agent OS 通信原语的专门篇。

## 一、为什么需要 Agent 通信

多 Agent 系统里，leader 与 sub、sub 与 sub 之间需要交换消息。ARES 的默认执行路径是**事件驱动单路径**（`EventSubTaskScheduled` → 执行 → `EventSubTaskResult`），那为什么还要一套直连通信原语？

因为事件流是"调度视角"，通信原语是"协作视角"：

| 维度 | 事件流（EventStore） | 通信原语（peer） |
|------|----------------------|------------------|
| 视角 | 任务调度、审计、重放 | Agent 间临时协作、通知 |
| 持久化 | 全量落库（事件溯源） | 内存注册表 + 即时投递 |
| 时序 | 异步、可重放 | 同步调用、尽力而为 |
| 职责 | 谁该执行什么 | 谁需要知道什么 |

**核心原则：peer 直发是补充通知通道，不是任务执行通道。** 任务执行永远走事件流（保证可审计、可恢复）；peer 直发只做轻量通知（进度、协作提示），失败仅记日志，绝不阻塞主流程。

## 二、Peer Registry：谁认识谁

`internal/agents/peer/registry.go` 是通信的地址簿：

```go
// SendFunc 把一条消息投递给一个 Agent。实现方负责入队（阻塞或异步）。
type SendFunc func(ctx context.Context, msg *ahp.AHPMessage) error

type Registry struct {
    peers map[string]SendFunc
    mu    sync.RWMutex
}
```

核心方法：

```go
func (r *Registry) Register(agentID string, send SendFunc) error   // 注册投递函数
func (r *Registry) Unregister(agentID string)                      // 注销
func (r *Registry) Lookup(agentID string) (SendFunc, bool)         // 查找投递函数
func (r *Registry) IDs() []string                                  // 全部在线 Agent
func (r *Registry) Send(ctx context.Context, targetID string, msg *ahp.AHPMessage) error // 按 ID 投递
```

`Register` 接受**投递函数**而非 Agent 对象——这让注册表与具体 Agent 实现解耦：任何能 `SendMessage` 的对象都能成为 peer，接口断言即可（见第四节生产接线）。

## 三、消息格式：AHP 协议

消息统一走 `internal/ares_protocol/ahp` 的 `AHPMessage`：

```go
type AHPMessage struct {
    ID        string            // 消息唯一 ID
    From      string            // 发送方 Agent ID
    To        string            // 接收方 Agent ID
    Method    AHPMethod         // task / ack / heartbeat / progress ...
    Payload   map[string]any    // 业务负载
}
```

方法集（`AHPMethod`）：
- `AHPMethodTask`：任务请求（sub 的 `messageHandler` 收到后**不做任务执行**——任务由事件流驱动，见第九节）
- `AHPMethodACK`：确认回执（同样是协议级占位，任务结果走事件流）
- `AHPMethodHeartbeat`：心跳
- `AHPMethodProgress`：进度通知（NotifyPeer 使用）

## 四、NotifyPeer：leader 的补充通知

`internal/agents/leader/agent_types.go` 的 `NotifyPeer` 是 leader 侧的直发入口：

```go
func (a *leaderAgent) NotifyPeer(ctx context.Context, targetID, message string) {
    // 无注册表或空 target 直接返回（幂等空操作）
    msg := ahp.NewMessage(ahp.AHPMethodProgress, a.id, targetID, "", "")
    msg.Payload = map[string]any{"note": message}
    if err := reg.Send(ctx, targetID, msg); err != nil {
        log.Debug("leader peer notify skipped", "target", targetID, "error", err)
    }
}
```

注意两处刻意设计：
1. **失败仅 Debug 日志**——直发是"补充通知"，丢了不影响任务执行，绝不上抛
2. **进度消息语义**——NotifyPeer 发的是 `AHPMethodProgress`，接收方知道这是"协作提示"而非"任务指令"，不会与事件流任务混淆

## 五、消息队列：缓冲与背压

`internal/ares_protocol/ahp/queue.go` 提供有界消息队列：

```go
type Queue struct {
    MaxSize    int              // 默认 1000（sub 任务场景配置 500）
    messages   chan *AHPMessage
}
```

- 有界容量 → 天然背压：队列满时发送方感知阻塞/丢弃，防止内存无界增长
- 生产接线：`ahp.NewMessageQueue(leaderID, &ahp.QueueOptions{MaxSize: 500})`（leader 与 sub 各一个）

## 六、Action Log：可审计的任务记录

`internal/agents/actionlog/actionlog.go` 是"做了什么事"的追加式审计日志（`Store`）：

```go
func (s *Store) Append(ctx context.Context, e Entry) error        // 追加（幂等）
func (s *Store) List(sessionID string) []Entry                    // 会话内全部记录
func (s *Store) Replay(sessionID, startID string) ([]Entry, error) // 从某条之后重放
func (s *Store) Count() int                                       // 记录数
```

- sub agent 在任务完成的三个结果出口（成功/失败/异常）都会 `recordAction` 追加一条 `actionlog.Entry`（任务结果 + 元数据）
- `Append` 幂等：同一条 Entry 重复追加不产生重复记录（重试/事件重放安全）
- `Replay` 支持从指定 `startID` 之后重放——审计与故障恢复的抓手

## 七、Session Lease：并发会话控制

`internal/agents/lease` 提供**会话级租约**（TTL 租约）：并发 worker 在修改同一 session 前必须先获取租约，防止两个 worker 同时写一个会话导致竞态。

- 挂载点：`internal/ares_memory` 的 memoryManager（`leaseMgr` 字段）
- 语义：租约有时效（TTL），超时自动失效；持有者必须在租约内完成操作或主动释放
- 与 peer 通信的关系：lease 是"谁能动这个会话"的互斥控制，peer 是"谁需要知道这个消息"的协作通知——两者正交

## 八、生产接线：buildPeerRegistry

`cmd/ares/serve.go` 在创建 leader/sub 后组装通信原语：

```go
// buildPeerRegistry 把 leader 与 sub 的消息投递能力注册进 peer.Registry。
// 不暴露 SendMessage 的 Agent（接口断言失败）被跳过，不算错误。
func buildPeerRegistry(leaderAgent leader.Agent, subAgents []sub.Agent) *peer.Registry {
    reg := peer.NewRegistry()
    if sender, ok := leaderAgent.(interface{ SendMessage(...) error }); ok {
        _ = reg.Register(leaderAgent.ID(), sender.SendMessage)
    }
    for _, sa := range subAgents {
        if sender, ok := sa.(interface{ SendMessage(...) error }); ok {
            _ = reg.Register(sa.ID(), sender.SendMessage)
        }
    }
    return reg
}
```

然后：

```go
leaderWithPeer := leaderAgent // 构造后
leaderWithPeer.SetPeerRegistry(peerRegistry) // leader 获得 peer 注册表
```

**接口断言而非类型断言**——这是关键设计：peer 注册表只要求"能发消息"，不要求具体类型，新增 Agent 类型无需改注册逻辑。

## 九、通信 vs 任务执行：边界在哪

sub 的 `messageHandler`（`internal/agents/sub/handler.go`）收到 AHP 直发消息时的处理方式最能说明边界：

```go
case ahp.AHPMethodTask:
    return h.handleTaskMessage(ctx, msg)  // 空实现：任务执行由 executor 负责
case ahp.AHPMethodACK:
    return h.handleAckMessage(ctx, msg)   // 空实现：协议级占位
case ahp.AHPMethodHeartbeat:
    return nil // 心跳确认
```

`handleTaskMessage` / `handleAckMessage` 的空实现**是有意的**（代码注释明确）：任务执行永远由事件流（`EventSubTaskScheduled`）驱动，AHP 直发 task/ack 仅是协议层的消息通道占位——避免两套任务分发路径并存导致重复执行。

## 十、总结

| 原语 | 包 | 职责 | 失败语义 |
|------|-----|------|----------|
| Peer Registry | `internal/agents/peer` | Agent 地址簿 + 直发投递 | 尽力而为，失败仅日志 |
| NotifyPeer | `internal/agents/leader` | leader 的补充通知（Progress 语义） | Debug 日志，不上抛 |
| Message Queue | `internal/ares_protocol/ahp` | 有界缓冲 + 背压 | 队列满 → 阻塞/丢弃 |
| Action Log | `internal/agents/actionlog` | 任务审计 + 重放（Append 幂等） | 记录失败不影响任务 |
| Session Lease | `internal/agents/lease` | 并发会话互斥（TTL 租约） | 超时自动失效 |

**设计主线：通信原语是协作层，事件流是执行层。** 两者职责严格分离——执行可审计、可恢复、可重放；协作轻量、即时、可丢。这正是 Agent OS 原语层"积木式"设计：每个原语可独立拆装，也可随机组合，都不影响主执行闭环。
