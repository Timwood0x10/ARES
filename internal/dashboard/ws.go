package dashboard

import "time"

// WSMessage represents a WebSocket message sent to clients.
type WSMessage struct {
	Type    string    `json:"type"`
	Channel string    `json:"channel,omitempty"`
	Data    any       `json:"data"`
	TS      time.Time `json:"ts"`
}

// WSClientMessage represents a message received from a WebSocket client.
type WSClientMessage struct {
	Type    string `json:"type"`
	Channel string `json:"channel,omitempty"`
}

// WebSocket message types.
const (
	WSTypeSubscribe   = "subscribe"
	WSTypeUnsubscribe = "unsubscribe"
	WSTypeEvent       = "event"
	WSTypeAgentUpdate = "agent_update"
	WSTypeStepUpdate  = "step_update"
	WSTypeDAGChange   = "dag_change"
	WSTypeHeartbeat   = "heartbeat"
	WSTypeMCPChange   = "mcp_tool_change"
	WSTypePing        = "ping"
	WSTypePong        = "pong"
	WSTypeAgentStream = "agent_stream"
)

// Well-known WebSocket channels.
const (
	WSChannelEvents         = "events"
	WSChannelAgents         = "agents"
	WSChannelMCP            = "mcp"
	WSChannelPrefixWorkflow = "workflow:"
	WSChannelPrefixDAG      = "dag:"
)
