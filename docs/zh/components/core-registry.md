# Agent Registry 设计文档

## 1. 概述

Agent Registry 是 Style Agent 框架的 Agent 注册与管理模块，负责 Sub Agent 的注册、发现、状态管理和动态扩缩容。

## 2. 核心功能

| 功能 | 说明 |
|------|------|
| **Agent 注册** | Sub Agent 启动时注册到 Registry |
| **Agent 发现** | Leader 可以查询可用的 Agent |
| **状态管理** | 跟踪 Agent 的在线/离线状态 |
| **负载均衡** | 根据 Agent 负载分发任务 |
| **健康检查** | 定期检查 Agent 健康状态 |

## 3. 数据结构

```go
type AgentInfo struct {
    AgentID    string            `json:"agent_id"`     // Agent 唯一标识
    AgentType  AgentType         `json:"agent_type"`   // Agent 类型
    Status     AgentStatus      `json:"status"`       // 状态
    Address    string            `json:"address"`      // 地址 (用于分布式)
    Capacity   int               `json:"capacity"`      // 并发容量
    Load       int               `json:"load"`          // 当前负载
    Tags       map[string]string `json:"tags"`          // 标签
    Version    string            `json:"version"`      // 版本
    StartedAt  time.Time         `json:"started_at"`   // 启动时间
    HeartbeatAt time.Time        `json:"heartbeat_at"` // 最后心跳时间
}

type AgentStatus string

const (
    AgentStatusStarting AgentStatus = "starting"
    AgentStatusReady    AgentStatus = "ready"
    AgentStatusBusy     AgentStatus = "busy"
    AgentStatusStopping AgentStatus = "stopping"
    AgentStatusOffline  AgentStatus = "offline"
)

type AgentType string

const (
    AgentTypeLeader AgentType = "leader"
    AgentTypeTop    AgentType = "agent_top"
    AgentTypeBottom AgentType = "agent_bottom"
    AgentTypeShoes  AgentType = "agent_shoes"
    AgentTypeHead   AgentType = "agent_head"
    AgentTypeAccessory AgentType = "agent_accessory"
)
```

## 4. 核心接口

```go
type Registry interface {
    // Register 注册 Agent
    Register(ctx context.Context, info *AgentInfo) error
    
    // Unregister 注销 Agent
    Unregister(ctx context.Context, agentID string) error
    
    // Get 获取 Agent 信息
    Get(ctx context.Context, agentID string) (*AgentInfo, error)
    
    // List 根据类型获取 Agent 列表
    List(ctx context.Context, agentType AgentType) ([]*AgentInfo, error)
    
    // UpdateStatus 更新 Agent 状态
    UpdateStatus(ctx context.Context, agentID string, status AgentStatus) error
    
    // UpdateLoad 更新 Agent 负载
    UpdateLoad(ctx context.Context, agentID string, load int) error
    
    // Heartbeat 接收心跳
    Heartbeat(ctx context.Context, agentID string) error
    
    // GetHealthy 获取健康 Agent
    GetHealthy(ctx context.Context, agentType AgentType) ([]*AgentInfo, error)
}
```

## 5. 架构设计

```
┌─────────────────────────────────────────────────────────────────┐
│                      Agent Registry                            │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                    Agent Registry                         │   │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐                │   │
│  │  │ agents   │  │ heartbeat│  │ health   │                │   │
│  │  │ map      │  │ checker  │  │ checker  │                │   │
│  │  └──────────┘  └──────────┘  └──────────┘                │   │
│  └──────────────────────────────────────────────────────────┘   │
│                              │                                   │
│         ┌────────────────────┼────────────────────┐              │
│         ▼                    ▼                    ▼              │
│  ┌──────────┐         ┌──────────┐         ┌──────────┐         │
│  │ Leader   │         │ Sub      │         │ Monitor  │         │
│  │ Agent    │         │ Agent    │         │ System   │         │
│  └──────────┘         └──────────┘         └──────────┘         │
└─────────────────────────────────────────────────────────────────┘
```

## 6. 健康检查机制

```go
// 健康检查配置
type HealthCheckConfig struct {
    Interval     time.Duration // 检查间隔
    Timeout      time.Duration // 检查超时
    MaxMissed    int           // 最大丢失心跳次数
    FallbackThreshold float64 // 负载阈值
}

// 定期检查
func (r *Registry) StartHealthCheck(ctx context.Context) {
    ticker := time.NewTicker(r.config.Interval)
    defer ticker.Stop()
    
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            r.checkAgents()
        }
    }
}

func (r *Registry) checkAgents() {
    for agentID, info := range r.agents {
        // 检查心跳超时
        if time.Since(info.HeartbeatAt) > r.config.Timeout {
            r.setOffline(agentID)
        }
        
        // 检查负载
        if float64(info.Load)/float64(info.Capacity) > r.config.FallbackThreshold {
            r.setBusy(agentID)
        }
    }
}
```

## 7. 负载均衡

```go
// 负载均衡策略
type LoadBalanceStrategy interface {
    Select(agents []*AgentInfo) *AgentInfo
}

// 最少连接策略
type LeastConnections struct{}

func (s *LeastConnections) Select(agents []*AgentInfo) *AgentInfo {
    var minLoad *AgentInfo
    for _, agent := range agents {
        if agent.Status != AgentStatusReady {
            continue
        }
        if minLoad == nil || agent.Load < minLoad.Load {
            minLoad = agent
        }
    }
    return minLoad
}

// 轮询策略
type RoundRobin struct {
    counter map[AgentType]int
}

func (s *RoundRobin) Select(agents []*AgentInfo) *AgentInfo {
    // 轮询选择
}
```

## 8. 配置参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| health_check_interval | 10s | 健康检查间隔 |
| health_check_timeout | 30s | 健康检查超时 |
| max_missed_heartbeats | 3 | 最大丢失心跳次数 |
| load_threshold | 0.8 | 负载阈值 |
| registry_storage | memory | 存储方式 (memory/etcd) |
