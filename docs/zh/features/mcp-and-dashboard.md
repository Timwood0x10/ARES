# GoAgentX v3: MCP Client + Web Dashboard 开发文档

## 概述

两个核心功能扩展:

1. **MCP Client** -- 连接外部 MCP Server, 自动发现工具, 注册到现有 Tool Registry, Agent 像使用内置工具一样使用 MCP 工具
2. **Web Dashboard** -- DAG 执行可视化、Agent 状态监控、Memory/蒸馏查询、事件流实时推送

---

## 第一部分: MCP Client 集成

### 1.1 设计原则

- MCP 工具是 **Tool interface 的实现者** -- 走和内置工具完全一样的 Registry/CapabilityEngine/AgentTools 管线
- 利用现有 **PluginRegistry + ToolFactory** 模式做生命周期管理
- 双传输层: **stdio**(子进程) 和 **SSE**(HTTP), 对齐 MCP 规范
- 动态工具发现: MCP server 增删工具时, 自动同步到 Registry

### 1.2 架构图

```
┌──────────────────────────────────────────────────────┐
│                    Agent System                       │
│  AgentTools ──► CapabilityEngine ──► Registry         │
│       │                                  ▲            │
│       │                                  │            │
│       │              ┌───────────────────┤            │
│       │              │ Register          │ Register   │
│       ▼              │                   │            │
│  Execute(name,params)│                   │            │
│       │         ┌────┴─────┐     ┌───────┴──────┐    │
│       ├────────►│BuiltinTool│     │   MCPTool    │    │
│       │         └──────────┘     └───────┬──────┘    │
│       │                                  │            │
└───────┼──────────────────────────────────┼────────────┘
        │                                  │
        │                          ┌───────▼──────┐
        │                          │  MCPClient   │
        │                          │  (每个server) │
        │                          └───────┬──────┘
        │                                  │
        │                          ┌───────▼──────┐
        │                          │  Transport   │
        │                          │ stdio | SSE  │
        │                          └───────┬──────┘
        │                                  │
        │                          ┌───────▼──────┐
        │                          │  MCP Server  │
        │                          │  (外部进程)   │
        │                          └──────────────┘
```

### 1.3 数据结构设计

#### 1.3.1 JSON-RPC 层

```go
// internal/mcp/jsonrpc.go

type JSONRPCMessage struct {
    JSONRPC string          `json:"jsonrpc"`           // 固定 "2.0"
    ID      *int64          `json:"id,omitempty"`      // nil 表示通知
    Method  string          `json:"method,omitempty"`
    Params  json.RawMessage `json:"params,omitempty"`
    Result  json.RawMessage `json:"result,omitempty"`
    Error   *JSONRPCError   `json:"error,omitempty"`
}

type JSONRPCError struct {
    Code    int             `json:"code"`
    Message string          `json:"message"`
    Data    json.RawMessage `json:"data,omitempty"`
}
```

#### 1.3.2 传输层接口

```go
// internal/mcp/transport.go

type Transport interface {
    Start(ctx context.Context) error
    Send(ctx context.Context, msg *JSONRPCMessage) error
    Receive(ctx context.Context) (*JSONRPCMessage, error)
    Close() error
}
```

**Stdio 传输** -- 启动子进程, 通过 stdin/stdout 读写 JSON-RPC:

```go
// internal/mcp/transport_stdio.go

type StdioTransport struct {
    cmd     *exec.Cmd
    stdin   io.WriteCloser
    stdout  *bufio.Scanner
    stderr  io.ReadCloser
    mu      sync.Mutex
    started bool
}

type StdioConfig struct {
    Command string            // 如 "codegraph"
    Args    []string          // 如 ["serve", "--mcp"]
    Env     map[string]string // 额外环境变量
    WorkDir string            // 工作目录
}
```

**SSE 传输** -- HTTP SSE 连接, POST 发送 JSON-RPC:

```go
// internal/mcp/transport_sse.go

type SSETransport struct {
    baseURL    string
    httpClient *http.Client
    msgCh      chan *JSONRPCMessage
    cancel     context.CancelFunc
    wg         sync.WaitGroup
    mu         sync.Mutex
    started    bool
}

type SSEConfig struct {
    URL     string
    Headers map[string]string // 认证头等
    Timeout time.Duration
}
```

#### 1.3.3 MCP 协议类型

```go
// internal/mcp/types.go

// --- 初始化 ---

type InitializeParams struct {
    ProtocolVersion string         `json:"protocolVersion"`
    ClientInfo      Implementation `json:"clientInfo"`
    Capabilities    ClientCaps     `json:"capabilities"`
}

type Implementation struct {
    Name    string `json:"name"`    // "GoAgentX"
    Version string `json:"version"`
}

type ClientCaps struct {
    Tools *ToolClientCaps `json:"tools,omitempty"`
}

type ToolClientCaps struct {
    ListChanged bool `json:"listChanged,omitempty"`
}

type InitializeResult struct {
    ProtocolVersion string         `json:"protocolVersion"`
    ServerInfo      Implementation `json:"serverInfo"`
    Capabilities    ServerCaps     `json:"capabilities"`
}

type ServerCaps struct {
    Tools     *ToolServerCaps     `json:"tools,omitempty"`
    Resources *ResourceServerCaps `json:"resources,omitempty"`
    Prompts   *PromptServerCaps   `json:"prompts,omitempty"`
}

type ToolServerCaps struct {
    ListChanged bool `json:"listChanged,omitempty"`
}

type ResourceServerCaps struct {
    Subscribe   bool `json:"subscribe,omitempty"`
    ListChanged bool `json:"listChanged,omitempty"`
}

type PromptServerCaps struct {
    ListChanged bool `json:"listChanged,omitempty"`
}

// --- 工具 ---

type MCPToolDef struct {
    Name        string          `json:"name"`
    Description string          `json:"description,omitempty"`
    InputSchema json.RawMessage `json:"inputSchema"` // JSON Schema
}

type ToolsListResult struct {
    Tools []MCPToolDef `json:"tools"`
}

type ToolCallParams struct {
    Name      string         `json:"name"`
    Arguments map[string]any `json:"arguments,omitempty"`
}

type ToolCallResult struct {
    Content []ContentBlock `json:"content"`
    IsError bool           `json:"isError,omitempty"`
}

type ContentBlock struct {
    Type     string `json:"type"`               // "text", "image", "resource"
    Text     string `json:"text,omitempty"`
    MimeType string `json:"mimeType,omitempty"`
    Data     string `json:"data,omitempty"`      // base64 (图片)
    URI      string `json:"uri,omitempty"`       // resource 引用
}
```

#### 1.3.4 MCP Client

```go
// internal/mcp/client.go

type MCPClient struct {
    transport  Transport
    serverName string
    serverCaps *ServerCaps
    tools      map[string]*MCPToolDef  // name -> definition
    mu         sync.RWMutex
    nextID     atomic.Int64
    pending    map[int64]chan *JSONRPCMessage  // 在途请求追踪
    pendingMu  sync.Mutex
    onChange   func()                  // 工具列表变更回调
    ctx        context.Context
    cancel     context.CancelFunc
    wg         sync.WaitGroup
}

type MCPClientConfig struct {
    ServerName string
    Transport  TransportConfig
    Timeout    time.Duration      // 单次调用超时, 默认 30s
    OnChange   func()             // 工具列表变更回调
}

type TransportConfig struct {
    Type  string       `yaml:"type" json:"type"` // "stdio" 或 "sse"
    Stdio *StdioConfig `yaml:"stdio,omitempty" json:"stdio,omitempty"`
    SSE   *SSEConfig   `yaml:"sse,omitempty" json:"sse,omitempty"`
}
```

**MCPClient 核心方法:**

```go
func (c *MCPClient) Connect(ctx context.Context) error           // 启动传输 + 初始化握手
func (c *MCPClient) Close() error                                 // 关闭
func (c *MCPClient) ListTools(ctx context.Context) ([]MCPToolDef, error)
func (c *MCPClient) CallTool(ctx context.Context, name string, args map[string]any) (*ToolCallResult, error)
func (c *MCPClient) ServerName() string
func (c *MCPClient) ServerCapabilities() *ServerCaps
```

#### 1.3.5 MCPTool -- 桥接到 Tool 接口

每个 MCP 工具包装为一个 GoAgentX `Tool`:

```go
// internal/mcp/mcp_tool.go

type MCPTool struct {
    *base.BaseTool
    client     *MCPClient
    serverName string
    toolDef    *MCPToolDef
    schema     *core.ParameterSchema  // 从 MCPToolDef.InputSchema 解析
}
```

- `Name()` 返回 `"mcp.<serverName>.<toolName>"` (命名空间隔离, 避免冲突)
- `Category()` 返回 `core.CategoryExternal`
- `Capabilities()` 返回 `[]core.Capability{core.CapabilityExternal}`
- `Execute(ctx, params)` 委托给 `client.CallTool()`, 将 `ToolCallResult` 转为 `core.Result`
- `Parameters()` 从 `MCPToolDef.InputSchema` 解析 (JSON Schema -> `core.ParameterSchema`)

#### 1.3.6 MCPManager -- 多服务器管理器

```go
// internal/mcp/manager.go

type MCPManager struct {
    clients    map[string]*MCPClient   // serverName -> client
    registry   *core.Registry          // 注册到的 GoAgentX 工具注册表
    mu         sync.RWMutex
    config     *MCPManagerConfig
}

type MCPManagerConfig struct {
    Servers []MCPServerConfig `yaml:"servers" json:"servers"`
}

type MCPServerConfig struct {
    Name      string          `yaml:"name" json:"name"`
    Transport TransportConfig `yaml:"transport" json:"transport"`
    Timeout   time.Duration   `yaml:"timeout" json:"timeout"`
    Enabled   bool            `yaml:"enabled" json:"enabled"`
    AutoStart bool            `yaml:"auto_start" json:"auto_start"`
}
```

**核心方法:**

```go
func NewMCPManager(config *MCPManagerConfig, registry *core.Registry) *MCPManager
func (m *MCPManager) Start(ctx context.Context) error       // 连接所有 auto_start 服务器
func (m *MCPManager) Stop(ctx context.Context) error        // 断开所有
func (m *MCPManager) ConnectServer(ctx context.Context, name string) error
func (m *MCPManager) DisconnectServer(ctx context.Context, name string) error
func (m *MCPManager) RefreshTools(ctx context.Context, serverName string) error
func (m *MCPManager) ListServers() []MCPServerStatus
func (m *MCPManager) GetClient(serverName string) (*MCPClient, bool)
```

```go
type MCPServerStatus struct {
    Name      string    `json:"name"`
    Connected bool      `json:"connected"`
    ToolCount int       `json:"tool_count"`
    Version   string    `json:"version"`
    Error     string    `json:"error,omitempty"`
    ConnAt    time.Time `json:"connected_at,omitempty"`
}
```

#### 1.3.7 JSON Schema -> ParameterSchema 转换器

```go
// internal/mcp/schema.go

func ConvertJSONSchema(raw json.RawMessage) (*core.ParameterSchema, error)
```

将 JSON Schema 的 `type`, `properties`, `required`, `enum`, `minimum`, `maximum`, `description` 映射到 `core.ParameterSchema` 和 `core.Parameter`.

#### 1.3.8 配置 (YAML)

在现有 `config.yaml` 中扩展:

```yaml
mcp:
  servers:
    - name: codegraph
      enabled: true
      auto_start: true
      timeout: 30s
      transport:
        type: stdio
        stdio:
          command: codegraph
          args: ["serve", "--mcp"]

    - name: remote-search
      enabled: true
      auto_start: true
      timeout: 60s
      transport:
        type: sse
        sse:
          url: "http://localhost:8080/mcp"
          headers:
            Authorization: "Bearer ${MCP_TOKEN}"
```

### 1.4 集成点 (无需修改的现有组件)

| 现有组件 | 变动 |
|---|---|
| `core.Registry` | 无变动 -- MCPTool 实现 Tool 接口, 正常注册 |
| `core.CapabilityEngine` | 无变动 -- MCPTool 声明 CapabilityExternal, 自动重建 capMap |
| `AgentTools` | 无变动 -- 过滤视图对 MCP 工具同样生效 |
| `PluginRegistry` | 注册 `MCPToolFactory` 为 `"mcp"` 工厂 |
| `ToolLifecycle` | `MCPTool` 实现 `Init`/`Stop` 管理连接生命周期 |
| `api/client/Config` | 增加 `MCP *MCPManagerConfig` 字段 |
| `internal/config` | 增加 `MCP MCPManagerConfig` 到配置结构体 |
| Runtime `Manager.Start()` | 启动时调用 `MCPManager.Start()` |

### 1.5 文件布局

```
internal/mcp/
├── jsonrpc.go          # JSON-RPC 2.0 消息类型
├── transport.go        # Transport 接口
├── transport_stdio.go  # Stdio 传输实现
├── transport_sse.go    # SSE 传输实现
├── types.go            # MCP 协议类型 (Initialize, Tools, Content)
├── client.go           # MCPClient (单服务器连接)
├── mcp_tool.go         # MCPTool (Tool 接口桥接)
├── schema.go           # JSON Schema -> ParameterSchema 转换器
├── manager.go          # MCPManager (多服务器管理器)
├── factory.go          # MCPToolFactory (PluginRegistry 集成)
└── *_test.go
```

---

## 第二部分: Web Dashboard

### 2.1 设计原则

- **后端**: Go `net/http` 标准库 -- 不引入新的 HTTP 框架, 保持一致性
- **实时推送**: WebSocket (唯一新依赖: `gorilla/websocket`)
- **前端**: 嵌入式 SPA (单 HTML + JS, 无构建步骤), 通过 `embed` 打包到 Go 二进制
- **数据源**: 读取现有 EventStore, Runtime, MemoryManager, MutableDAG -- 不新增存储

### 2.2 架构图

```
┌─────────────────────────────────────────────────────┐
│                    Browser (SPA)                     │
│  ┌─────────┐ ┌──────────┐ ┌──────────┐ ┌────────┐  │
│  │Agent    │ │DAG       │ │Memory    │ │Event   │  │
│  │Monitor  │ │Visualizer│ │Explorer  │ │Stream  │  │
│  └────┬────┘ └─────┬────┘ └─────┬────┘ └───┬────┘  │
│       │            │            │           │        │
│       └──────┬─────┴────────────┴───────────┘        │
│              │ REST + WebSocket                      │
└──────────────┼───────────────────────────────────────┘
               │
┌──────────────┼───────────────────────────────────────┐
│   Dashboard  │  API Server                           │
│   ┌──────────▼─────────┐                             │
│   │  DashboardRouter   │                             │
│   │  /api/dashboard/*  │                             │
│   └──────────┬─────────┘                             │
│              │                                       │
│   ┌──────────▼─────────┐    ┌──────────────────┐    │
│   │   REST Handlers    │    │  WebSocket Hub   │    │
│   │  agents, dag,      │    │  broadcast,      │    │
│   │  memory, events,   │    │  subscribe,      │    │
│   │  mcp               │    │  rooms           │    │
│   └──────────┬─────────┘    └────────┬─────────┘    │
│              │                       │               │
│   ┌──────────▼───────────────────────▼──────────┐   │
│   │            DashboardService                  │   │
│   │  runtime, eventStore, memoryMgr, mcpMgr     │   │
│   └─────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────┘
```

### 2.3 数据结构设计

#### 2.3.1 Dashboard Service

```go
// internal/dashboard/service.go

type DashboardService struct {
    runtime    runtime.Runtime
    eventStore events.EventStore
    memoryMgr  memory.MemoryManager
    mcpMgr     *mcp.MCPManager       // MCP 未配置时为 nil
    startTime  time.Time
}

type DashboardConfig struct {
    Addr           string        `yaml:"addr" json:"addr"`             // ":8090"
    EnableAuth     bool          `yaml:"enable_auth" json:"enable_auth"`
    StaticDir      string        `yaml:"static_dir" json:"static_dir"` // "" = 使用嵌入资源
    WSPingInterval time.Duration `yaml:"ws_ping_interval" json:"ws_ping_interval"`
}
```

#### 2.3.2 REST API 响应类型

**系统概览:**

```go
// internal/dashboard/types.go

type SystemOverview struct {
    Uptime       string            `json:"uptime"`
    AgentCount   int               `json:"agent_count"`
    ActiveTasks  int               `json:"active_tasks"`
    TotalEvents  int64             `json:"total_events"`
    MemoryStats  MemoryStats       `json:"memory_stats"`
    MCPStatus    *MCPOverview      `json:"mcp_status,omitempty"`
    RuntimeStats runtime.RuntimeStats `json:"runtime_stats"`
}

type MemoryStats struct {
    ActiveSessions int `json:"active_sessions"`
    DistilledCount int `json:"distilled_count"`
    TotalMessages  int `json:"total_messages"`
}

type MCPOverview struct {
    ServerCount    int `json:"server_count"`
    ConnectedCount int `json:"connected_count"`
    TotalTools     int `json:"total_tools"`
}
```

**Agent 视图:**

```go
type AgentView struct {
    ID         string              `json:"id"`
    Type       string              `json:"type"`
    Status     string              `json:"status"`
    Restarts   int                 `json:"restarts"`
    Uptime     string              `json:"uptime"`
    LastEvent  *EventView          `json:"last_event,omitempty"`
    Heartbeat  *HeartbeatView      `json:"heartbeat,omitempty"`
}

type HeartbeatView struct {
    LastSeen    time.Time `json:"last_seen"`
    MissedCount int       `json:"missed_count"`
    IsAlive     bool      `json:"is_alive"`
}
```

**DAG 视图:**

```go
type DAGView struct {
    Nodes   []DAGNodeView   `json:"nodes"`
    Edges   []DAGEdgeView   `json:"edges"`
    Version uint64          `json:"version"`
}

type DAGNodeView struct {
    ID        string            `json:"id"`
    Name      string            `json:"name"`
    AgentType string            `json:"agent_type"`
    Status    string            `json:"status"`    // pending/running/completed/failed
    InDegree  int               `json:"in_degree"`
    OutDegree int               `json:"out_degree"`
    Duration  string            `json:"duration,omitempty"`
    Error     string            `json:"error,omitempty"`
    Metadata  map[string]string `json:"metadata,omitempty"`
}

type DAGEdgeView struct {
    From string `json:"from"`
    To   string `json:"to"`
}

type WorkflowExecutionView struct {
    ID         string            `json:"id"`
    WorkflowID string            `json:"workflow_id"`
    Status     string            `json:"status"`
    Steps      []StepStateView   `json:"steps"`
    StartedAt  time.Time         `json:"started_at"`
    Duration   string            `json:"duration,omitempty"`
    Error      string            `json:"error,omitempty"`
}

type StepStateView struct {
    StepID   string `json:"step_id"`
    Name     string `json:"name"`
    Status   string `json:"status"`
    Duration string `json:"duration,omitempty"`
    Output   string `json:"output,omitempty"`
    Error    string `json:"error,omitempty"`
    Attempts int    `json:"attempts"`
}
```

**Memory 视图:**

```go
type SessionView struct {
    SessionID    string        `json:"session_id"`
    UserID       string        `json:"user_id"`
    MessageCount int           `json:"message_count"`
    CreatedAt    time.Time     `json:"created_at"`
    Messages     []MessageView `json:"messages,omitempty"` // 仅详情请求时返回
}

type MessageView struct {
    Role    string    `json:"role"`
    Content string    `json:"content"`
    Time    time.Time `json:"time"`
}

type DistilledMemoryView struct {
    ID         string    `json:"id"`
    Type       string    `json:"type"`
    Content    string    `json:"content"`
    Importance float64   `json:"importance"`
    Source     string    `json:"source"`
    CreatedAt  time.Time `json:"created_at"`
}
```

**Event 视图:**

```go
type EventView struct {
    ID        string         `json:"id"`
    StreamID  string         `json:"stream_id"`
    Type      string         `json:"type"`
    Payload   map[string]any `json:"payload"`
    Version   int64          `json:"version"`
    Timestamp time.Time      `json:"timestamp"`
}

type EventQueryParams struct {
    StreamID  string     // 按 stream 过滤
    Types     []string   // 按事件类型过滤
    Since     time.Time  // 起始时间
    Limit     int        // 最大条数, 默认 50
    Direction string     // "asc" 或 "desc"
}
```

**MCP 视图:**

```go
type MCPServerView struct {
    Name      string        `json:"name"`
    Connected bool          `json:"connected"`
    Version   string        `json:"version"`
    Tools     []MCPToolView `json:"tools"`
    Error     string        `json:"error,omitempty"`
    ConnAt    time.Time     `json:"connected_at,omitempty"`
}

type MCPToolView struct {
    Name        string `json:"name"`
    Description string `json:"description"`
    ServerName  string `json:"server_name"`
}
```

#### 2.3.3 REST API 端点

```
GET    /api/dashboard/overview                  -> SystemOverview
GET    /api/dashboard/agents                    -> []AgentView
GET    /api/dashboard/agents/:id                -> AgentView
GET    /api/dashboard/agents/:id/events         -> []EventView

GET    /api/dashboard/workflows                 -> []WorkflowExecutionView
GET    /api/dashboard/workflows/:id             -> WorkflowExecutionView
GET    /api/dashboard/workflows/:id/dag         -> DAGView

GET    /api/dashboard/memory/sessions           -> []SessionView
GET    /api/dashboard/memory/sessions/:id       -> SessionView (含消息)
GET    /api/dashboard/memory/distilled          -> []DistilledMemoryView
POST   /api/dashboard/memory/search             -> []DistilledMemoryView
       body: {"query": "...", "limit": 10}

GET    /api/dashboard/events                    -> []EventView
       query: ?stream_id=&type=&since=&limit=&direction=
GET    /api/dashboard/events/stream             -> WebSocket 升级

GET    /api/dashboard/mcp/servers               -> []MCPServerView
POST   /api/dashboard/mcp/servers/:name/refresh -> MCPServerView
```

#### 2.3.4 WebSocket 协议

```go
// internal/dashboard/ws.go

type WSMessage struct {
    Type    string         `json:"type"`
    Channel string         `json:"channel,omitempty"`
    Data    any            `json:"data"`
    TS      time.Time      `json:"ts"`
}
```

**客户端 -> 服务端 (订阅/取消订阅):**

```json
{"type": "subscribe", "channel": "events"}
{"type": "subscribe", "channel": "agents"}
{"type": "subscribe", "channel": "workflow:<execution_id>"}
{"type": "subscribe", "channel": "dag:<workflow_id>"}
{"type": "unsubscribe", "channel": "events"}
```

**服务端 -> 客户端 (推送):**

```json
{"type": "event",          "channel": "events",              "data": <EventView>,     "ts": "..."}
{"type": "agent_update",   "channel": "agents",              "data": <AgentView>,     "ts": "..."}
{"type": "step_update",    "channel": "workflow:<exec_id>",  "data": <StepStateView>, "ts": "..."}
{"type": "dag_change",     "channel": "dag:<wf_id>",         "data": <DAGView>,       "ts": "..."}
{"type": "heartbeat",      "channel": "agents",              "data": {"agent_id":"...","alive":true}, "ts": "..."}
{"type": "mcp_tool_change","channel": "mcp",                 "data": <MCPServerView>, "ts": "..."}
```

#### 2.3.5 WebSocket Hub

```go
// internal/dashboard/ws_hub.go

type WSHub struct {
    clients    map[*WSClient]struct{}
    channels   map[string]map[*WSClient]struct{}  // channel -> 订阅者
    register   chan *WSClient
    unregister chan *WSClient
    broadcast  chan *WSMessage
    mu         sync.RWMutex
}

type WSClient struct {
    hub      *WSHub
    conn     *websocket.Conn
    send     chan []byte
    channels map[string]struct{}
    mu       sync.Mutex
}
```

**Hub 生命周期:**
- `Run()` -- 主 select 循环, 处理 register/unregister/broadcast
- `BroadcastToChannel(channel string, msg *WSMessage)` -- 向指定 channel 的订阅者广播
- `BroadcastAll(msg *WSMessage)` -- 向所有连接客户端广播

#### 2.3.6 Event Bridge (EventStore -> WebSocket)

```go
// internal/dashboard/event_bridge.go

type EventBridge struct {
    eventStore events.EventStore
    hub        *WSHub
    cancel     context.CancelFunc
    wg         sync.WaitGroup
}
```

订阅 `EventStore` 全部事件, 将 `*events.Event` 转换为 `WSMessage`, 广播到 `"events"` channel. 同时根据事件类型映射到对应 channel:
- `agent.started`, `agent.stopped` -> `"agents"` channel
- `task.created`, `task.completed`, `task.failed` -> `"workflow:<exec_id>"` channel
- `failover.*` -> `"agents"` channel

#### 2.3.7 需要在现有类型上新增的方法

```go
// 添加到 runtime.Runtime 接口:
ListAgents() []AgentInfo
GetAgent(id string) (*AgentInfo, bool)

type AgentInfo struct {
    ID       string
    Type     string
    Status   string
    Restarts int
}

// 添加到 runtime.Manager 实现:
func (m *Manager) ListAgents() []AgentInfo
func (m *Manager) GetAgent(id string) (*AgentInfo, bool)
```

### 2.4 文件布局

```
internal/dashboard/
├── service.go        # DashboardService (读取 runtime/events/memory/mcp)
├── types.go          # 所有视图类型
├── router.go         # DashboardRouter (REST 端点 + 静态文件服务)
├── handlers.go       # HTTP handler 函数
├── ws.go             # WebSocket 消息类型
├── ws_hub.go         # WebSocket Hub + Client
├── event_bridge.go   # EventStore -> WebSocket 桥接
├── static.go         # embed.FS 嵌入 SPA 资源
├── static/
│   ├── index.html    # 单页应用
│   ├── app.js        # 前端逻辑 (vanilla JS 或 Alpine.js)
│   └── style.css     # 样式
└── *_test.go
```

### 2.5 新依赖

```
go get github.com/gorilla/websocket
```

这是唯一的新依赖, 其他全部使用标准库.

---

## 第三部分: 启动序列

```go
// 应用启动顺序:

1. 加载配置
2. 初始化 EventStore (已有)
3. 初始化 MemoryManager (已有)
4. 初始化 Runtime (已有)

// --- 新增 ---
5. 初始化 MCPManager(config.MCP.Servers)
6. MCPManager.Start(ctx)  // 连接所有 auto_start 服务器
7. 将 MCP 工具注册到 GlobalRegistry

8. 启动 agents (已有)
9. Runtime.Start(ctx) (已有)

// --- 新增 ---
10. 初始化 DashboardService(runtime, eventStore, memoryMgr, mcpMgr)
11. 初始化 WSHub, EventBridge
12. 在 config.Dashboard.Addr 上启动 DashboardRouter
```

---

## 第四部分: Task List

### Phase 1: MCP Client (优先级: 高)

| # | 任务 | 包 | 依赖 | 预估 |
|---|------|---------|------------|----------|
| 1.1 | JSON-RPC 消息类型 + 编解码 | `internal/mcp` | - | 0.5d |
| 1.2 | Transport 接口 + StdioTransport | `internal/mcp` | 1.1 | 1d |
| 1.3 | SSETransport | `internal/mcp` | 1.1 | 1d |
| 1.4 | MCP 协议类型 (Initialize, Tools, Content) | `internal/mcp` | - | 0.5d |
| 1.5 | MCPClient (connect, list tools, call tool) | `internal/mcp` | 1.1, 1.2, 1.4 | 1.5d |
| 1.6 | JSON Schema -> ParameterSchema 转换器 | `internal/mcp` | - | 0.5d |
| 1.7 | MCPTool (Tool 接口桥接) | `internal/mcp` | 1.5, 1.6 | 1d |
| 1.8 | MCPManager (多服务器管理器) | `internal/mcp` | 1.5, 1.7 | 1d |
| 1.9 | MCPToolFactory (PluginRegistry 集成) | `internal/mcp` | 1.8 | 0.5d |
| 1.10 | 配置集成 (YAML 解析, 校验) | `internal/config` | 1.8 | 0.5d |
| 1.11 | 启动引导集成 (MCPManager 加入启动流程) | `cmd/` 或 `api/client` | 1.8, 1.10 | 0.5d |
| 1.12 | 集成测试: mock MCP server | `internal/mcp` | 1.5 | 1d |
| 1.13 | 集成测试: 真实 codegraph MCP | `internal/mcp` | 1.8 | 0.5d |

**Phase 1 合计: ~10 天**

### Phase 2: Web Dashboard (优先级: 中)

| # | 任务 | 包 | 依赖 | 预估 |
|---|------|---------|------------|----------|
| 2.1 | DashboardService + 视图类型 | `internal/dashboard` | - | 1d |
| 2.2 | Runtime.ListAgents() / GetAgent() 方法 | `internal/runtime` | - | 0.5d |
| 2.3 | REST handlers (overview, agents, events) | `internal/dashboard` | 2.1, 2.2 | 1.5d |
| 2.4 | REST handlers (workflows, DAG, memory) | `internal/dashboard` | 2.1 | 1.5d |
| 2.5 | REST handlers (MCP 状态) | `internal/dashboard` | 2.1, Phase 1 | 0.5d |
| 2.6 | WebSocket Hub + Client | `internal/dashboard` | - | 1d |
| 2.7 | EventBridge (EventStore -> WS) | `internal/dashboard` | 2.6 | 1d |
| 2.8 | DashboardRouter (挂载所有端点) | `internal/dashboard` | 2.3-2.7 | 0.5d |
| 2.9 | 前端: Dashboard 首页 + Agent Monitor | `internal/dashboard/static` | 2.3 | 2d |
| 2.10 | 前端: DAG 可视化器 | `internal/dashboard/static` | 2.4 | 2d |
| 2.11 | 前端: Memory Explorer | `internal/dashboard/static` | 2.4 | 1d |
| 2.12 | 前端: Event Stream (实时) | `internal/dashboard/static` | 2.7 | 1d |
| 2.13 | 前端: MCP 状态页 | `internal/dashboard/static` | 2.5 | 0.5d |
| 2.14 | 配置集成 + 启动引导 | `internal/config`, `cmd/` | 2.8 | 0.5d |
| 2.15 | 集成测试 | `internal/dashboard` | 2.8 | 1d |

**Phase 2 合计: ~14.5 天**

### 实施顺序

```
第 1-2 周:  MCP 任务 1.1-1.7  (核心 client, transport, tool 桥接)
第 2-3 周:  MCP 任务 1.8-1.13 (manager, 配置, 集成测试)
第 3-4 周:  Dashboard 任务 2.1-2.8 (后端 API + WebSocket)
第 4-5 周:  Dashboard 任务 2.9-2.15 (前端 SPA + 集成)
```

### 关键路径

```
1.1 ──► 1.2 ──► 1.5 ──► 1.7 ──► 1.8 ──► 1.11
                 │                │
                 ▼                ▼
                1.12             2.5
                                  │
2.2 ──► 2.3 ──► 2.8 ──► 2.14    │
         │                        │
2.6 ──► 2.7 ──────────────────────┘
```
