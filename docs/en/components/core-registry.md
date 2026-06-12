# Agent Registry Design Document

## 1. Overview

Agent Registry is the Agent registration and management module of the Style Agent framework, responsible for Sub Agent registration, discovery, status management, and dynamic scaling.

## 2. Core Functions

| Function | Description |
|----------|-------------|
| **Agent Registration** | Sub Agent registers to Registry on startup |
| **Agent Discovery** | Leader can query available Agents |
| **Status Management** | Track Agent online/offline status |
| **Load Balancing** | Distribute tasks based on Agent load |
| **Health Check** | Regular health check of Agents |

## 3. Data Structures

```go
type AgentInfo struct {
    AgentID    string            `json:"agent_id"`     // Unique Agent identifier
    AgentType  AgentType         `json:"agent_type"`   // Agent type
    Status     AgentStatus      `json:"status"`       // Status
    Address    string            `json:"address"`      // Address (for distributed)
    Capacity   int               `json:"capacity"`     // Concurrent capacity
    Load       int               `json:"load"`          // Current load
    Tags       map[string]string `json:"tags"`          // Tags
    Version    string            `json:"version"`      // Version
    StartedAt  time.Time         `json:"started_at"`   // Start time
    HeartbeatAt time.Time        `json:"heartbeat_at"` // Last heartbeat time
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

## 4. Core Interfaces

```go
type Registry interface {
    // Register registers an Agent
    Register(ctx context.Context, info *AgentInfo) error
    
    // Unregister unregisters an Agent
    Unregister(ctx context.Context, agentID string) error
    
    // Get gets Agent info
    Get(ctx context.Context, agentID string) (*AgentInfo, error)
    
    // List gets Agent list by type
    List(ctx context.Context, agentType AgentType) ([]*AgentInfo, error)
    
    // UpdateStatus updates Agent status
    UpdateStatus(ctx context.Context, agentID string, status AgentStatus) error
    
    // UpdateLoad updates Agent load
    UpdateLoad(ctx context.Context, agentID string, load int) error
    
    // Heartbeat receives heartbeat
    Heartbeat(ctx context.Context, agentID string) error
    
    // GetHealthy gets healthy Agents
    GetHealthy(ctx context.Context, agentType AgentType) ([]*AgentInfo, error)
}
```

## 5. Architecture Design

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

## 6. Health Check Mechanism

```go
// Health check configuration
type HealthCheckConfig struct {
    Interval     time.Duration // Check interval
    Timeout      time.Duration // Check timeout
    MaxMissed    int           // Max missed heartbeats
    FallbackThreshold float64 // Load threshold
}

// Regular check
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
        // Check heartbeat timeout
        if time.Since(info.HeartbeatAt) > r.config.Timeout {
            r.setOffline(agentID)
        }
        
        // Check load
        if float64(info.Load)/float64(info.Capacity) > r.config.FallbackThreshold {
            r.setBusy(agentID)
        }
    }
}
```

## 7. Load Balancing

```go
// Load balance strategy
type LoadBalanceStrategy interface {
    Select(agents []*AgentInfo) *AgentInfo
}

// Least connections strategy
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

// Round robin strategy
type RoundRobin struct {
    counter map[AgentType]int
}

func (s *RoundRobin) Select(agents []*AgentInfo) *AgentInfo {
    // Round robin selection
}
```

## 8. Configuration Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| health_check_interval | 10s | Health check interval |
| health_check_timeout | 30s | Health check timeout |
| max_missed_heartbeats | 3 | Max missed heartbeats |
| load_threshold | 0.8 | Load threshold |
| registry_storage | memory | Storage type (memory/etcd) |
