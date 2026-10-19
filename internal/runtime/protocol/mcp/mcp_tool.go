package ares_mcp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/tools/capability"
	"github.com/Timwood0x10/ares/internal/tools/resources/base"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

const (
	contentTypeText = "text"
)

// MCPTool wraps an MCP tool definition as a ares Tool.
type MCPTool struct {
	*base.BaseTool
	client     *MCPClient
	serverName string
	toolDef    *MCPToolDef
}

// NewMCPTool creates an MCPTool from an MCP tool definition.
func NewMCPTool(client *MCPClient, def *MCPToolDef) (*MCPTool, error) {
	if client == nil {
		return nil, errors.New("client is required")
	}
	if def == nil {
		return nil, errors.New("tool definition is required")
	}

	schema, err := ConvertJSONSchema(def.InputSchema)
	if err != nil {
		return nil, fmt.Errorf("convert schema for %s: %w", def.Name, err)
	}

	name := fmt.Sprintf("mcp.%s.%s", client.ServerName(), def.Name)

	bt := base.NewBaseToolWithCapabilities(
		name,
		def.Description,
		core.CategoryExternal,
		[]core.Capability{core.CapabilityExternal},
		schema,
	)

	return &MCPTool{
		BaseTool:   bt,
		client:     client,
		serverName: client.ServerName(),
		toolDef:    def,
	}, nil
}

// Description returns the tool description, with the server's availability note
// appended when the server is known to be unusable. It shadows BaseTool's method
// deliberately: the description is what the model reads in the tool list, so it
// is the one channel that can tell the model "the tool is still here, but its
// server is down" without new prompt plumbing (S-mcp-S2 — a tool that simply
// vanished left the model convinced it had misremembered the name).
func (t *MCPTool) Description() string {
	desc := t.BaseTool.Description()
	if note := t.client.Health().UnavailableNote(); note != "" {
		return desc + " [" + note + "]"
	}
	return desc
}

// Execute delegates the tool call to the MCP server.
//
// It checks the server's shared availability FIRST. Once the transport is known
// to be dead, calling through would fail with a raw transport error ("connection
// closed") that says nothing about why or since when — and the tool would still
// advertise itself as usable, which is the zombie S-mcp-S5 describes.
// Tags declares the capability domains this tool serves, derived from its advertised
// description at registration time.
//
// MCP carries no capability metadata, only prose, so this is a keyword read of that prose using
// the SAME table the task side uses (internal/tools/capability) — one vocabulary in both
// directions, so a task tagged `network` can actually select an MCP tool that described itself
// as fetching HTTP. Without this the tool declared nothing, and an untagged tool is invisible to
// capability narrowing: it could never be selected by it, and (before the fix next to
// declaresAnyCapability) could be dropped by it.
func (t *MCPTool) Tags() map[string]string { return capabilityTagsFor(t.Name(), t.Description()) }

// capabilityTagsFor maps a tool's name and description onto cap_* tags. Split out from Tags so
// it can be exercised without constructing a live MCP client.
func capabilityTagsFor(name, description string) map[string]string {
	domains := capability.FromText(name + " " + description)
	if len(domains) == 0 {
		return nil
	}
	tags := make(map[string]string, len(domains))
	for _, d := range domains {
		tags["cap_"+d] = "true"
	}
	return tags
}

func (t *MCPTool) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	if health := t.client.Health(); !health.IsAvailable() {
		return core.NewErrorResult(unavailableToolMessage(t.Name(), t.serverName, health)), nil
	}
	result, err := t.client.CallTool(ctx, t.toolDef.Name, params)
	if err != nil {
		return core.NewErrorResult(err.Error()), nil
	}

	if result.IsError {
		text := extractText(result.Content)
		return core.NewErrorResult(text), nil
	}

	text := extractText(result.Content)
	return core.NewResult(true, map[string]interface{}{
		"content": text,
		"blocks":  result.Content,
	}), nil
}

// ServerName returns the MCP server name this tool belongs to.
func (t *MCPTool) ServerName() string {
	return t.serverName
}

// Close is a no-op. The underlying client connection lifecycle is owned by
// MCPManager — closing one MCPTool must not cut the shared connection for
// other tools on the same server. Safe to call multiple times.
func (t *MCPTool) Close() error {
	return nil
}

// MCPTName returns the original tool name on the MCP server (without namespace).
func (t *MCPTool) MCPTName() string {
	return t.toolDef.Name
}

// unavailableToolMessage is the refusal a tool returns when its server is known
// to be unusable. It names the tool by the name a caller actually uses (the
// registered one, taken from the tool rather than rebuilt here, so the two cannot
// drift), plus the server, the reason and the since stamp: a caller deciding what
// to do next cannot act on "tool not found", and "connection closed" reads as a
// transient blip rather than a dead server.
func unavailableToolMessage(tool, server string, health *ServerHealth) string {
	_, reason, since := health.State()
	msg := fmt.Sprintf("mcp tool %s is unavailable: server %q is not usable", tool, server)
	if reason != "" {
		msg += ": " + reason
	}
	if !since.IsZero() {
		msg += fmt.Sprintf(" (since %s)", since.UTC().Format(time.RFC3339))
	}
	return msg + "; use another tool, or retry once the server recovers"
}

// extractText concatenates text from all text-type content blocks.
func extractText(blocks []ContentBlock) string {
	if len(blocks) == 0 {
		return ""
	}

	totalLen := 0
	for _, b := range blocks {
		if b.Type == contentTypeText {
			totalLen += len(b.Text)
		}
	}

	buf := make([]byte, 0, totalLen)
	for _, b := range blocks {
		if b.Type == contentTypeText {
			buf = append(buf, b.Text...)
		}
	}

	return string(buf)
}

// Ensure MCPTool implements core.Tool at compile time.
var _ core.Tool = (*MCPTool)(nil)
var _ core.TaggableTool = (*MCPTool)(nil)
