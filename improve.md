我觉得这个方向是对的，而且比单纯做一个 MCP Client 更有价值。

你现在其实已经不是在做：

Connect to MCP

而是在做：

MCP Discovery Framework

这是两个完全不同的定位。

⸻

我建议把 Discovery 分成几层，而不是现在只有”来源(Source)”

例如：

                Discovery Engine
                       │
 ┌─────────────────────┼──────────────────────┐
 │                     │                      │
Local              Network               Registry
 │                     │                      │
Claude          HTTP Discovery        ARES Registry
Cursor          mDNS                 Remote Registry
VSCode          DNS-SD               Enterprise Registry
PATH            Broadcast            Git Registry

也就是说：

Discovery Provider

而不是 Discovery Source。

以后别人只需要：

type DiscoveryProvider interface {
    Discover(ctx context.Context) ([]Server, error)
}

ARES 自动加载。

以后：

providers/
filesystem/
http/
mdns/
registry/
docker/
k8s/

全部插件化。

⸻

第二个建议

增加

Confidence

例如：

Server
codegraph
Source:
PATH
Confidence:
95%
Verified:
YES
Latency:
2ms
Health:
Healthy

为什么？

PATH 扫描

其实不能保证：

codegraph

就是 MCP。

可能只是一个同名程序。

Claude Config

可信度就高很多。

所以：

ARES Registry
100%
Claude
95%
Cursor
95%
VSCode
95%
HTTP Discovery
80%
PATH
60%

ARES 就知道：

优先连接谁。

⸻

第三个建议

增加

Health Check

例如：

ares mcp discover
Found 12 servers
✔ healthy
✔ timeout
✔ version mismatch
✔ auth required
✔ duplicate

Discovery

和

Available

不是一回事。

发现之后：

自动

initialize
↓
list_tools
↓
ping
↓
version
↓
capabilities

整个生命周期。

⸻

第四个建议（我觉得很重要）

Duplicate Merge

例如：

Claude

filesystem

发现

codegraph

PATH

又发现

codegraph

HTTP

又发现

codegraph

ARES 不应该显示三个。

应该：

codegraph
Sources
✔ PATH
✔ Claude
✔ HTTP
Priority:
Claude
Status:
Healthy

也就是说：

Server Identity

和

Discovery Record

应该分离。

⸻

第五个建议

Metadata

例如：

Name
Description
Version
Author
Homepage
Capabilities
Tools
Resources
Prompts
Transport
Environment
Working Directory

ARES 就可以：

ares mcp ls
codegraph
Version
0.4.2
Transport
stdio
Capabilities
✔ tools
✔ prompts
✔ resources
Tools
13
Resources
4

以后 Agent 可以自动挑。

⸻

第六个建议

HTTP Discovery

我觉得别只做：

GET
/mcp

最好抽象一下。

例如：

Discovery Strategy
Static URL
/.well-known/mcp.json
HTTP API
Registry API
DNS
mDNS
Broadcast
Enterprise Registry

以后：

公司内部：

http://registry.internal
↓
1000 个 MCP
↓
ARES
↓
全部发现

这个比写死 URL 强很多。

⸻

第七个建议（这是我最喜欢的）

Event

Discovery 不应该只是：

ares discover

应该：

MCP Added
↓
MCP Removed
↓
MCP Updated
↓
Reconnect
↓
Health Changed

ARES Runtime：

EventBus
↓
Discovery Event
↓
Plugin Manager
↓
Reconnect
↓
Capability Refresh
↓
Memory Update

Agent

就完全不知道。

全部 Runtime 自动完成。

⸻

第八个建议

Cache

Discovery

不要每次：

扫描
↓
解析
↓
初始化

应该：

~/.ares/discovery.db
codegraph
hash
version
mtime
last_seen
health
latency

启动：

cache
↓
增量更新
↓
很快

⸻

如果是我，我甚至会改个名字

不要叫：

MCP Discovery

叫：

Service Discovery Engine

以后：

不仅发现：

MCP
A2A
OpenAPI
REST
gRPC
CLI
Plugin
Local Tool
Docker
Kubernetes Service

ARES Runtime：

统一：

Service
↓
Capability
↓
Scheduler
↓
Agent

这样你的 Runtime 就又提升了一个抽象层。

⸻

我真正觉得有价值的一点

你这个功能的意义，不在于”扫描几个配置文件”。很多人都能做到这一层。

真正有价值的是把它设计成一个可扩展的服务发现子系统：

Discovery Providers
        │
        ▼
Identity Merge
        │
        ▼
Health Verification
        │
        ▼
Capability Discovery
        │
        ▼
Event Bus
        │
        ▼
Runtime Scheduler

这样，MCP 只是其中一种 Service。以后无论是本地工具、远程 Agent、HTTP 服务还是其他协议，都能通过同一套 Discovery Framework 接入 ARES。

从架构上看，这和你一直坚持的方向是一致的：**不是为某一种技术写适配，而是为一类能力建立统一抽象。**这也会让 ARES 的 Runtime 更像一个真正的自治运行时，而不是一个只支持 MCP 的 Agent 框架。