# GoAgentX v3: MCP Client + Web Dashboard

## Overview

Two features to extend GoAgentX:

1. **MCP Client** -- Connect to external MCP servers, auto-discover tools, and register them into the existing Tool Registry as first-class tools. Agents use MCP tools identically to built-in tools.
2. **Web Dashboard** -- Real-time visualization of DAG execution, agent status, memory/distillation state, and event streams via REST API + WebSocket.

---

## Part 1: MCP Client Integration

### 1.1 Design Principles

- MCP tools are **Tool interface implementors** -- they go through the same Registry/CapabilityEngine/AgentTools pipeline as built-in tools
- MCP Client uses the existing **PluginRegistry + ToolFactory** pattern for lifecycle management
- Two transports: **stdio** (subprocess) and **SSE** (HTTP), matching the MCP spec
- Dynamic tool discovery: MCP servers can add/remove tools at runtime, reflected via Registry updates

### 1.2 Architecture

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
        │                          │  (per server)│
        │                          └───────┬──────┘
        │                                  │
        │                          ┌───────▼──────┐
        │                          │  Transport   │
        │                          │ stdio | SSE  │
        │                          └───────┬──────┘
        │                                  │
        │                          ┌───────▼──────┐
        │                          │  MCP Server  │
        │                          │  (external)  │
        │                          └──────────────┘
```

### 1.3 Data Structures

#### 1.3.1 JSON-RPC Layer

```go
// internal/mcp/jsonrpc.go

type JSONRPCMessage struct {
    JSONRPC string          `json:"jsonrpc"`           // always "2.0"
    ID      *int64          `json:"id,omitempty"`      // nil for notifications
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

#### 1.3.2 Transport Interface

```go
// internal/mcp/transport.go

type Transport interface {
    Start(ctx context.Context) error
    Send(ctx context.Context, msg *JSONRPCMessage) error
    Receive(ctx context.Context) (*JSONRPCMessage, error)
    Close() error
}
```

**Stdio Transport** -- launches a subprocess, reads/writes JSON-RPC over stdin/stdout:

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
    Command string            // e.g. "codegraph"
    Args    []string          // e.g. ["serve", "--mcp"]
    Env     map[string]string // extra env vars
    WorkDir string            // working directory
}
```

**SSE Transport** -- connects to an HTTP SSE endpoint, sends JSON-RPC via POST:

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
    Headers map[string]string // auth headers etc.
    Timeout time.Duration
}
```

#### 1.3.3 MCP Protocol Types

```go
// internal/mcp/types.go

// --- Initialize ---

type InitializeParams struct {
    ProtocolVersion string         `json:"protocolVersion"`
    ClientInfo      Implementation `json:"clientInfo"`
    Capabilities    ClientCaps     `json:"capabilities"`
}

type Implementation struct {
    Name    string `json:"name"`    // "GoAgentX"
    Version string `json:"version"` // from build info
}

type ClientCaps struct {
    Tools *ToolClientCaps `json:"tools,omitempty"`
}

type ToolClientCaps struct {
    // client supports tools/list_changed notifications
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

// --- Tools ---

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
    Data     string `json:"data,omitempty"`      // base64 for images
    URI      string `json:"uri,omitempty"`       // for resources
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
    pending    map[int64]chan *JSONRPCMessage  // inflight request tracking
    pendingMu  sync.Mutex
    onChange   func()                  // called when tools/list changes
    ctx        context.Context
    cancel     context.CancelFunc
    wg         sync.WaitGroup
}

type MCPClientConfig struct {
    ServerName string
    Transport  TransportConfig
    Timeout    time.Duration      // per-call timeout, default 30s
    OnChange   func()             // tool list change callback
}

type TransportConfig struct {
    Type  string       `yaml:"type" json:"type"` // "stdio" or "sse"
    Stdio *StdioConfig `yaml:"stdio,omitempty" json:"stdio,omitempty"`
    SSE   *SSEConfig   `yaml:"sse,omitempty" json:"sse,omitempty"`
}
```

**MCPClient key methods:**

```go
func (c *MCPClient) Connect(ctx context.Context) error           // Start transport + initialize handshake
func (c *MCPClient) Close() error                                 // Shutdown
func (c *MCPClient) ListTools(ctx context.Context) ([]MCPToolDef, error)
func (c *MCPClient) CallTool(ctx context.Context, name string, args map[string]any) (*ToolCallResult, error)
func (c *MCPClient) ServerName() string
func (c *MCPClient) ServerCapabilities() *ServerCaps
```

#### 1.3.5 MCPTool -- Bridge to Tool Interface

Each MCP tool is wrapped as a GoAgentX `Tool`:

```go
// internal/mcp/mcp_tool.go

type MCPTool struct {
    *base.BaseTool
    client     *MCPClient
    serverName string
    toolDef    *MCPToolDef
    schema     *core.ParameterSchema  // parsed from MCPToolDef.InputSchema
}
```

**Construction:**

```go
func NewMCPTool(client *MCPClient, def *MCPToolDef) (*MCPTool, error)
```

- `Name()` returns `"mcp.<serverName>.<toolName>"` (namespaced to avoid collisions)
- `Category()` returns `core.CategoryExternal`
- `Capabilities()` returns `[]core.Capability{core.CapabilityExternal}`
- `Execute(ctx, params)` delegates to `client.CallTool()`, converts `ToolCallResult` to `core.Result`
- `Parameters()` parsed from `MCPToolDef.InputSchema` (JSON Schema -> `core.ParameterSchema`)

#### 1.3.6 MCPManager -- Multi-Server Orchestrator

```go
// internal/mcp/manager.go

type MCPManager struct {
    clients    map[string]*MCPClient   // serverName -> client
    registry   *core.Registry          // the GoAgentX tool registry to register into
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

**MCPManager key methods:**

```go
func NewMCPManager(config *MCPManagerConfig, registry *core.Registry) *MCPManager
func (m *MCPManager) Start(ctx context.Context) error       // Connect all enabled servers
func (m *MCPManager) Stop(ctx context.Context) error        // Disconnect all
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

#### 1.3.7 MCPToolFactory -- PluginRegistry Integration

```go
// internal/mcp/factory.go

type MCPToolFactory struct {
    manager *MCPManager
}
```

Implements `core.ToolFactory`:
- `Name()` returns `"mcp"`
- `Create(config)` parses `MCPServerConfig` from config map, connects to server, returns tools
- `ValidateConfig(config)` validates server config

#### 1.3.8 JSON Schema -> ParameterSchema Converter

```go
// internal/mcp/schema.go

func ConvertJSONSchema(raw json.RawMessage) (*core.ParameterSchema, error)
```

Handles JSON Schema `type`, `properties`, `required`, `enum`, `minimum`, `maximum`, `description` mapping to `core.ParameterSchema` and `core.Parameter`.

#### 1.3.9 Configuration (YAML)

Extension to existing `config.yaml`:

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

### 1.4 Integration Points

| Existing Component | Change |
|---|---|
| `core.Registry` | No change -- MCPTool implements `Tool` interface, registers normally |
| `core.CapabilityEngine` | No change -- MCPTool declares `CapabilityExternal`, rebuilds capMap |
| `AgentTools` | No change -- filtered view works with MCP tools |
| `PluginRegistry` | Register `MCPToolFactory` as factory named `"mcp"` |
| `ToolLifecycle` | `MCPTool` implements `Init`/`Stop` for connection lifecycle |
| `api/client/Config` | Add `MCP *MCPManagerConfig` field |
| `internal/config` | Add `MCP MCPManagerConfig` to config struct |
| Runtime `Manager.Start()` | Call `MCPManager.Start()` during bootstrap |

### 1.5 File Layout

```
internal/mcp/
├── jsonrpc.go          # JSON-RPC 2.0 message types
├── transport.go        # Transport interface
├── transport_stdio.go  # Stdio transport implementation
├── transport_sse.go    # SSE transport implementation
├── types.go            # MCP protocol types (Initialize, Tools, Content)
├── client.go           # MCPClient (per-server connection)
├── mcp_tool.go         # MCPTool (Tool interface bridge)
├── schema.go           # JSON Schema -> ParameterSchema converter
├── manager.go          # MCPManager (multi-server orchestrator)
├── factory.go          # MCPToolFactory (PluginRegistry integration)
└── *_test.go           # Tests for each file
```

---

## Part 2: Web Dashboard

### 2.1 Design Principles

- **Backend**: Go `net/http` (stdlib) -- no new dependencies, consistent with existing codebase
- **Real-time**: WebSocket for live updates (gorilla/websocket is the only new dependency)
- **Frontend**: Embedded SPA (single HTML + JS, no build step), served from Go binary via `embed`
- **Data source**: Read from existing EventStore, Runtime, MemoryManager, MutableDAG -- no new storage

### 2.2 Architecture

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

### 2.3 Data Structures

#### 2.3.1 Dashboard Service

```go
// internal/dashboard/service.go

type DashboardService struct {
    runtime    runtime.Runtime
    eventStore events.EventStore
    memoryMgr  memory.MemoryManager
    mcpMgr     *mcp.MCPManager       // nil if MCP not configured
    startTime  time.Time
}

type DashboardConfig struct {
    Addr           string        `yaml:"addr" json:"addr"`             // ":8090"
    EnableAuth     bool          `yaml:"enable_auth" json:"enable_auth"`
    StaticDir      string        `yaml:"static_dir" json:"static_dir"` // "" = use embedded
    WSPingInterval time.Duration `yaml:"ws_ping_interval" json:"ws_ping_interval"`
}
```

#### 2.3.2 REST API Response Types

**System Overview:**

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

**Agent Views:**

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

**DAG Views:**

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
    Status    string            `json:"status"`
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

**Memory Views:**

```go
type SessionView struct {
    SessionID    string        `json:"session_id"`
    UserID       string        `json:"user_id"`
    MessageCount int           `json:"message_count"`
    CreatedAt    time.Time     `json:"created_at"`
    Messages     []MessageView `json:"messages,omitempty"` // only when detail requested
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

**Event Views:**

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
    StreamID  string     // filter by stream
    Types     []string   // filter by event type
    Since     time.Time  // from timestamp
    Limit     int        // max results, default 50
    Direction string     // "asc" or "desc"
}
```

**MCP Views:**

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

#### 2.3.3 REST API Endpoints

```
GET    /api/dashboard/overview                  -> SystemOverview
GET    /api/dashboard/agents                    -> []AgentView
GET    /api/dashboard/agents/:id                -> AgentView
GET    /api/dashboard/agents/:id/events         -> []EventView

GET    /api/dashboard/workflows                 -> []WorkflowExecutionView
GET    /api/dashboard/workflows/:id             -> WorkflowExecutionView
GET    /api/dashboard/workflows/:id/dag         -> DAGView

GET    /api/dashboard/memory/sessions           -> []SessionView
GET    /api/dashboard/memory/sessions/:id       -> SessionView (with messages)
GET    /api/dashboard/memory/distilled          -> []DistilledMemoryView
POST   /api/dashboard/memory/search             -> []DistilledMemoryView
       body: {"query": "...", "limit": 10}

GET    /api/dashboard/events                    -> []EventView
       query: ?stream_id=&type=&since=&limit=&direction=
GET    /api/dashboard/events/stream             -> WebSocket upgrade

GET    /api/dashboard/mcp/servers               -> []MCPServerView
POST   /api/dashboard/mcp/servers/:name/refresh -> MCPServerView
```

#### 2.3.4 WebSocket Protocol

```go
// internal/dashboard/ws.go

type WSMessage struct {
    Type    string         `json:"type"`
    Channel string         `json:"channel,omitempty"`
    Data    any            `json:"data"`
    TS      time.Time      `json:"ts"`
}
```

**Client -> Server (subscribe/unsubscribe):**

```json
{"type": "subscribe", "channel": "events"}
{"type": "subscribe", "channel": "agents"}
{"type": "subscribe", "channel": "workflow:<execution_id>"}
{"type": "subscribe", "channel": "dag:<workflow_id>"}
{"type": "unsubscribe", "channel": "events"}
```

**Server -> Client (push):**

```json
{"type": "event",          "channel": "events",              "data": <EventView>,   "ts": "..."}
{"type": "agent_update",   "channel": "agents",              "data": <AgentView>,   "ts": "..."}
{"type": "step_update",    "channel": "workflow:<exec_id>",  "data": <StepStateView>, "ts": "..."}
{"type": "dag_change",     "channel": "dag:<wf_id>",         "data": <DAGView>,     "ts": "..."}
{"type": "heartbeat",      "channel": "agents",              "data": {"agent_id":"...","alive":true}, "ts": "..."}
{"type": "mcp_tool_change","channel": "mcp",                 "data": <MCPServerView>, "ts": "..."}
```

#### 2.3.5 WebSocket Hub

```go
// internal/dashboard/ws_hub.go

type WSHub struct {
    clients    map[*WSClient]struct{}
    channels   map[string]map[*WSClient]struct{}  // channel -> subscribers
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

**Hub lifecycle:**
- `Run()` -- main select loop processing register/unregister/broadcast
- `BroadcastToChannel(channel string, msg *WSMessage)` -- fan-out to channel subscribers
- `BroadcastAll(msg *WSMessage)` -- fan-out to all connected clients

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

Subscribes to `EventStore` with `EventFilter{}` (all events), converts each `*events.Event` to `WSMessage`, and broadcasts to the `"events"` channel. Also maps specific event types to agent/workflow channels:
- `agent.started`, `agent.stopped` -> `"agents"` channel
- `task.created`, `task.completed`, `task.failed` -> `"workflow:<exec_id>"` channel
- `failover.*` -> `"agents"` channel

#### 2.3.7 Dashboard Router

```go
// internal/dashboard/router.go

type DashboardRouter struct {
    mux     *http.ServeMux
    service *DashboardService
    hub     *WSHub
    bridge  *EventBridge
    config  *DashboardConfig
}
```

Mounts all REST endpoints + WebSocket upgrade endpoint + static file serving.

#### 2.3.8 Static Assets (Embedded SPA)

```go
// internal/dashboard/static.go

//go:embed static/*
var staticFS embed.FS
```

Frontend is a single-page app with:
- **Dashboard home**: system overview cards (agent count, task count, events, uptime)
- **Agent monitor**: table of agents with status badges, click to see event log
- **DAG visualizer**: interactive DAG rendering (nodes + edges, color-coded by status)
- **Memory explorer**: session list, message viewer, distilled memory search
- **Event stream**: live scrolling event log with type/stream filters
- **MCP status**: server connection status, tool inventory

### 2.4 Integration Points

| Existing Component | How Dashboard Reads It |
|---|---|
| `runtime.Runtime.Stats()` | Agent count, restart count, uptime |
| `runtime.Manager.agents` | Need to expose `ListAgents() []AgentView` method |
| `events.EventStore.Subscribe()` | Real-time event streaming via EventBridge |
| `events.EventStore.Read()` | Historical event queries |
| `memory.MemoryManager.GetMessages()` | Session/message viewing |
| `memory.MemoryManager.SearchSimilarTasks()` | Distilled memory search |
| `workflow.MutableDAG` | DAG structure for visualization |
| `mcp.MCPManager.ListServers()` | MCP server status |

### 2.5 New Methods Required on Existing Types

```go
// Add to runtime.Runtime interface:
ListAgents() []AgentInfo
GetAgent(id string) (*AgentInfo, bool)

type AgentInfo struct {
    ID       string
    Type     string
    Status   string
    Restarts int
}

// Add to runtime.Manager:
func (m *Manager) ListAgents() []AgentInfo
func (m *Manager) GetAgent(id string) (*AgentInfo, bool)
```

### 2.6 File Layout

```
internal/dashboard/
├── service.go        # DashboardService (reads from runtime/events/memory/mcp)
├── types.go          # All view types (AgentView, DAGView, etc.)
├── router.go         # DashboardRouter (REST endpoints + static serving)
├── handlers.go       # HTTP handler functions
├── ws.go             # WebSocket message types
├── ws_hub.go         # WebSocket Hub + Client
├── event_bridge.go   # EventStore -> WebSocket bridge
├── static.go         # embed.FS for SPA assets
├── static/
│   ├── index.html    # Single-page application
│   ├── app.js        # Frontend logic (vanilla JS or Alpine.js)
│   └── style.css     # Styles
└── *_test.go
```

### 2.7 New Dependency

```
go get github.com/gorilla/websocket
```

This is the only new dependency. Everything else uses stdlib.

---

## Part 3: Configuration Integration

### 3.1 Extended config.yaml

```yaml
# Existing config sections remain unchanged...

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

dashboard:
  addr: ":8090"
  enable_auth: false
  ws_ping_interval: 30s
```

### 3.2 Extended Config Struct

```go
// Add to internal/config/config.go

type Config struct {
    // ... existing fields ...
    MCP       MCPConfig       `yaml:"mcp"`
    Dashboard DashboardConfig `yaml:"dashboard"`
}

type MCPConfig struct {
    Servers []mcp.MCPServerConfig `yaml:"servers"`
}

type DashboardConfig struct {
    Addr           string        `yaml:"addr"`
    EnableAuth     bool          `yaml:"enable_auth"`
    WSPingInterval time.Duration `yaml:"ws_ping_interval"`
}
```

---

## Part 4: Bootstrap Sequence

```go
// Application startup order:

1. Load config
2. Initialize EventStore (existing)
3. Initialize MemoryManager (existing)
4. Initialize Runtime (existing)

// --- NEW ---
5. Initialize MCPManager with config.MCP.Servers
6. MCPManager.Start(ctx)  // connects to all auto_start servers
7. Register MCP tools into GlobalRegistry

8. Start agents (existing)
9. Runtime.Start(ctx) (existing)

// --- NEW ---
10. Initialize DashboardService(runtime, eventStore, memoryMgr, mcpMgr)
11. Initialize WSHub, EventBridge
12. Start DashboardRouter on config.Dashboard.Addr
```

---

## Part 5: Task List

### Phase 1: MCP Client (Priority: High)

| # | Task | Package | Depends On | Estimate |
|---|------|---------|------------|----------|
| 1.1 | JSON-RPC message types + encode/decode | `internal/mcp` | - | 0.5d |
| 1.2 | Transport interface + StdioTransport | `internal/mcp` | 1.1 | 1d |
| 1.3 | SSETransport | `internal/mcp` | 1.1 | 1d |
| 1.4 | MCP protocol types (Initialize, Tools, Content) | `internal/mcp` | - | 0.5d |
| 1.5 | MCPClient (connect, list tools, call tool) | `internal/mcp` | 1.1, 1.2, 1.4 | 1.5d |
| 1.6 | JSON Schema -> ParameterSchema converter | `internal/mcp` | - | 0.5d |
| 1.7 | MCPTool (Tool interface bridge) | `internal/mcp` | 1.5, 1.6 | 1d |
| 1.8 | MCPManager (multi-server orchestrator) | `internal/mcp` | 1.5, 1.7 | 1d |
| 1.9 | MCPToolFactory (PluginRegistry integration) | `internal/mcp` | 1.8 | 0.5d |
| 1.10 | Config integration (YAML parsing, validation) | `internal/config` | 1.8 | 0.5d |
| 1.11 | Bootstrap wiring (MCPManager in app startup) | `cmd/` or `api/client` | 1.8, 1.10 | 0.5d |
| 1.12 | Integration tests with mock MCP server | `internal/mcp` | 1.5 | 1d |
| 1.13 | Integration test with real codegraph MCP | `internal/mcp` | 1.8 | 0.5d |

**Phase 1 total: ~10 days**

### Phase 2: Web Dashboard (Priority: Medium)

| # | Task | Package | Depends On | Estimate |
|---|------|---------|------------|----------|
| 2.1 | DashboardService + view types | `internal/dashboard` | - | 1d |
| 2.2 | Runtime.ListAgents() / GetAgent() methods | `internal/runtime` | - | 0.5d |
| 2.3 | REST handlers (overview, agents, events) | `internal/dashboard` | 2.1, 2.2 | 1.5d |
| 2.4 | REST handlers (workflows, DAG, memory) | `internal/dashboard` | 2.1 | 1.5d |
| 2.5 | REST handlers (MCP status) | `internal/dashboard` | 2.1, Phase 1 | 0.5d |
| 2.6 | WebSocket Hub + Client | `internal/dashboard` | - | 1d |
| 2.7 | EventBridge (EventStore -> WS) | `internal/dashboard` | 2.6 | 1d |
| 2.8 | DashboardRouter (mount all endpoints) | `internal/dashboard` | 2.3-2.7 | 0.5d |
| 2.9 | Frontend: dashboard home + agent monitor | `internal/dashboard/static` | 2.3 | 2d |
| 2.10 | Frontend: DAG visualizer | `internal/dashboard/static` | 2.4 | 2d |
| 2.11 | Frontend: memory explorer | `internal/dashboard/static` | 2.4 | 1d |
| 2.12 | Frontend: event stream (live) | `internal/dashboard/static` | 2.7 | 1d |
| 2.13 | Frontend: MCP status page | `internal/dashboard/static` | 2.5 | 0.5d |
| 2.14 | Config integration + bootstrap wiring | `internal/config`, `cmd/` | 2.8 | 0.5d |
| 2.15 | Integration tests | `internal/dashboard` | 2.8 | 1d |

**Phase 2 total: ~14.5 days**

### Implementation Order

```
Week 1-2:  MCP Tasks 1.1-1.7  (core client, transport, tool bridge)
Week 2-3:  MCP Tasks 1.8-1.13 (manager, config, integration tests)
Week 3-4:  Dashboard Tasks 2.1-2.8 (backend API + WebSocket)
Week 4-5:  Dashboard Tasks 2.9-2.15 (frontend SPA + integration)
```

### Critical Path

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
