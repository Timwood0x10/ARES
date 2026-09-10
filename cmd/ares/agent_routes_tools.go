// agent_routes_tools — the /api/tools and /api/mcp surfaces, the tool/MCP
// registry assembly helpers they serve, and the mcp-null demo CLI, split out
// of agent.go (M-C2). Handler bodies are moved verbatim; only the file
// boundary is new.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/Timwood0x10/ares/internal/agents/sub"
	api_tools "github.com/Timwood0x10/ares/internal/apitools"
	"github.com/Timwood0x10/ares/internal/ares_security"
	"github.com/Timwood0x10/ares/internal/knowledge/skills"
	ares_mcp "github.com/Timwood0x10/ares/internal/runtime/protocol/mcp"
	"github.com/Timwood0x10/ares/internal/tools/discovery"
	"github.com/Timwood0x10/ares/internal/tools/envcap"
	"github.com/Timwood0x10/ares/internal/tools/planner"
	builtintools "github.com/Timwood0x10/ares/internal/tools/resources/builtin"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// routeCallTool invokes a tool from the ARES registry by name.
func (h *actionHandler) routeCallTool(w http.ResponseWriter, r *http.Request, princ *ares_security.Principal) {
	h.handleCallTool(w, r, princ)
}

// routeListTools lists the ARES tool inventory.
func (h *actionHandler) routeListTools(w http.ResponseWriter, _ *http.Request, _ *ares_security.Principal) {
	h.handleListTools(w)
}

// routeListMCPTools lists the MCP tool inventory.
func (h *actionHandler) routeListMCPTools(w http.ResponseWriter, _ *http.Request, _ *ares_security.Principal) {
	h.handleListMCPTools(w)
}

// routeCallMCPTool invokes an MCP tool; the tool name is the {name}
// segment, re-derived from the path exactly as the pre-registry switch did.
func (h *actionHandler) routeCallMCPTool(w http.ResponseWriter, r *http.Request, princ *ares_security.Principal) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/mcp/tools/"), "/")
	h.handleCallMCPTool(w, r, princ, parts[0])
}

// ── Tool API ─────────────────────────────────────────────

type callToolRequest struct {
	Name   string         `json:"name"`
	Params map[string]any `json:"params"`
}

func (h *actionHandler) handleCallTool(w http.ResponseWriter, r *http.Request, princ *ares_security.Principal) {
	w.Header().Set("Content-Type", "application/json")
	var req callToolRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}
	if req.Name == "" {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]any{"error": "name is required"})
		return
	}

	if h.tools != nil {
		result, err := h.tools.Execute(r.Context(), req.Name, req.Params)
		if err != nil {
			h.auditAction("call_tool", req.Name, princ, false)
			// Distinguish "tool not found" from a real execution failure so
			// callers get an accurate error instead of a blanket 404.
			if _, ok := h.tools.Get(req.Name); ok {
				w.WriteHeader(http.StatusInternalServerError)
				writeJSON(w, map[string]any{
					"error": "tool execution failed: " + err.Error(),
				})
			} else {
				w.WriteHeader(http.StatusNotFound)
				writeJSON(w, map[string]any{
					"error": "tool not found: " + req.Name,
					"tools": h.tools.List(),
				})
			}
			return
		}
		h.auditAction("call_tool", req.Name, princ, true)
		writeJSON(w, map[string]any{
			"tool": req.Name, "success": result.Success, "data": result.Data,
		})
		return
	}

	w.WriteHeader(http.StatusServiceUnavailable)
	writeJSON(w, map[string]any{"error": "no tool registry"})
}

func (h *actionHandler) handleListTools(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	if h.tools == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		writeJSON(w, map[string]any{"error": "no tool registry"})
		return
	}
	names := h.tools.List()
	writeJSON(w, map[string]any{
		"tools": names,
		"count": len(names),
	})
}

// ── MCP Tool API (migrated from internal/monitoring) ──

// handleListMCPTools returns the available tools with descriptions, matching
// the shape the old monitoring gin /api/mcp/tools endpoint produced.
func (h *actionHandler) handleListMCPTools(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	if h.tools == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		writeJSON(w, map[string]any{"error": "no tool registry"})
		return
	}
	infos := h.tools.ListTools()
	if infos == nil {
		infos = []api_tools.ToolInfo{}
	}
	writeJSON(w, infos)
}

// handleCallMCPTool invokes an MCP tool by name. The outcome is audited after
// the call runs so failures are recorded as such (same contract as the old
// monitoring handler).
func (h *actionHandler) handleCallMCPTool(w http.ResponseWriter, r *http.Request, princ *ares_security.Principal, name string) {
	w.Header().Set("Content-Type", "application/json")
	var args map[string]any
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil && !errors.Is(err, io.EOF) {
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(w, map[string]any{"error": "invalid request body"})
			return
		}
	}
	result, err := h.tools.Execute(r.Context(), name, args)
	h.auditAction("call_mcp_tool", name, princ, err == nil)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{
		"tool_name": name,
		"is_error":  !result.Success,
		"output":    map[string]any{"success": result.Success, "data": result.Data},
	})
}

// ── Tool registry assembly (serve wiring helpers) ────────

// nativeToolsEnvVar names the comma-separated allowlist of host commands to
// discover and register as tools (primitive 7: native command discovery).
// Empty disables discovery so hosts without the commands degrade gracefully.
const nativeToolsEnvVar = "ARES_NATIVE_TOOLS"

// nativeToolsAllowlist parses the ARES_NATIVE_TOOLS env var into a cleaned
// allowlist of host command names. Returns an empty slice when unset/blank so
// callers disable native discovery gracefully. This is the single security
// boundary: only listed commands are ever probed or executed.
func nativeToolsAllowlist() []string {
	raw := strings.TrimSpace(os.Getenv(nativeToolsEnvVar))
	if raw == "" {
		return nil
	}
	allowlist := make([]string, 0)
	for _, name := range strings.Split(raw, ",") {
		if name = strings.TrimSpace(name); name != "" {
			allowlist = append(allowlist, name)
		}
	}
	return allowlist
}

// registerNativeTools probes the allowlisted host commands via `command -v` +
// `--help` and registers the ones present into the internal registry. Only
// commands explicitly listed in ARES_NATIVE_TOOLS are ever probed or executed
// (allowlist security boundary); non-existent commands are skipped.
func registerNativeTools(ctx context.Context, internalReg *core.Registry) error {
	allowlist := nativeToolsAllowlist()
	if len(allowlist) == 0 {
		return nil
	}

	d := discovery.NewDiscoverer(allowlist)
	tools, err := d.Discover(ctx)
	if err != nil {
		return fmt.Errorf("native tools: discover: %w", err)
	}
	registered := 0
	for _, t := range tools {
		if err := internalReg.Register(t); err != nil {
			fmt.Printf("native tool: failed to register %q: %v\n", t.Name(), err)
			continue
		}
		registered++
	}
	fmt.Printf("native tools registered: %d (allowlist: %v)\n", registered, allowlist)
	return nil
}

// newToolRegistry creates the public tool registry with built-in + custom tools.
// The file tool is sandboxed to ARES_WORKSPACE_DIR (or the current working
// directory if the env var is unset) to prevent path-traversal attacks.
func newToolRegistry() (*api_tools.Registry, error) {
	r := api_tools.NewRegistry()
	workspaceDir := os.Getenv("ARES_WORKSPACE_DIR")
	if workspaceDir == "" {
		workspaceDir, _ = os.Getwd()
	}
	if err := api_tools.RegisterBuiltinTools(r, api_tools.WithFileSandboxDir(workspaceDir)); err != nil {
		return nil, err
	}
	return r, nil
}

// newToolBinder creates a sub.ToolBinder bridged from the internal core.Registry.
func newToolBinder(internalReg *core.Registry) sub.ToolBinder {
	binder := sub.NewToolBinder()
	binder.BridgeFromRegistry(internalReg)
	return binder
}

// registerCapabilitySearch wires the environment-capability searcher (envcap)
// as the `search_capabilities` tool and registers it into the internal
// registry — envcap.NewSearcher was previously constructed nowhere in serve.
// This completes the SKILLS progressive-disclosure story: the memory manager
// surfaces a resident skill block, and this tool lets the agent actively search
// across the environment's capabilities, returning name + one-line description
// with details loaded on demand.
//
// Two sources are wired: registered tools (the registry itself) and skills (the
// bootstrap-seeded registry). Native commands are deliberately NOT wired as a
// separate discovery source here because registerNativeTools has already
// registered each allowlisted command as a CommandTool in the same registry —
// so they surface through the registry (KindTool). Wiring a second Discoverer
// would double-list every command and re-probe the host (command -v + --help)
// on every search call.
//
// skillReg may be nil (skills disabled) — the searcher simply skips that source.
func registerCapabilitySearch(internalReg *core.Registry, skillReg *skills.Registry) error {
	searcher := envcap.NewSearcher(envcap.NewRegistryLister(internalReg), skillReg, nil)
	tool := envcap.NewSearchTool(searcher)
	if err := internalReg.Register(tool); err != nil {
		return fmt.Errorf("register capability search tool: %w", err)
	}
	return nil
}

// newPlannerBridge wires the capability planner into a ToolExecutionBridge.
// The bridge provides intent-based tool fallback when agents call unknown tools.
// If planner dependencies are missing, it returns nil (no bridge) gracefully.
func newPlannerBridge(internalReg *core.Registry) *planner.ToolExecutionBridge {
	// Create a tool provider from the registry and build the planner.
	provider := planner.NewRegistryProvider(internalReg)
	resolver, err := planner.NewToolResolver(provider)
	if err != nil {
		fmt.Printf("planner: resolver: %v\n", err)
		return nil
	}

	evStore := planner.NewMemoryEvidenceStore()
	p, err := planner.NewPlanner(
		planner.NewRuleBasedAnalyzer(),
		planner.NewCapabilityPlanner(),
		resolver,
		planner.NewEvidenceScorer(evStore),
		planner.NewExecutionPlanner(),
		evStore,
	)
	if err != nil {
		fmt.Printf("planner: new: %v\n", err)
		return nil
	}

	bridge, err := planner.NewToolExecutionBridge(internalReg, p, evStore)
	if err != nil {
		fmt.Printf("planner: bridge: %v\n", err)
		return nil
	}
	return bridge
}

// setupMCP registers builtin and MCP tools into the internal registry and
// bridges them into the public registry. It reuses the MCP manager created
// by Bootstrap (comp.MCP) instead of creating a second manager, so server
// connections are not duplicated and the single manager's Stop hook (already
// registered at shutdown) covers every connection.
func setupMCP(_ context.Context, mcpMgr *ares_mcp.MCPManager, registry *api_tools.Registry, deps builtintools.GeneralToolsDeps) (*core.Registry, error) {
	internalReg := core.NewRegistry()

	// Register builtin general tools into the internal registry so sub-agents
	// receive them through the ToolBinder (closure of the tools module).
	// Real backends (knowledge store adapter, memory manager, LLM client) are
	// injected via deps so the knowledge/memory/planning tools are usable,
	// not just nil-guarded.
	if err := builtintools.RegisterGeneralTools(internalReg, deps); err != nil {
		return internalReg, fmt.Errorf("register general tools: %w", err)
	}

	// Copy tools from the bootstrap-created MCP manager into the internal
	// registry so sub-agents and the dashboard see MCP tools. The manager was
	// already started by Bootstrap; no second manager is created here.
	if mcpMgr != nil {
		for _, tool := range mcpMgr.RegisteredTools() {
			t := tool
			if err := internalReg.Register(t); err != nil {
				fmt.Printf("MCP bridge: failed to register tool %s: %v\n", t.Name(), err)
			}
		}
	}

	// Bridge: register all internal tools (builtin + MCP) into the public
	// api/tools registry so the dashboard sees them regardless of whether MCP
	// servers are configured.
	for _, name := range internalReg.List() {
		tool, ok := internalReg.Get(name)
		if !ok || tool == nil {
			continue
		}
		t := tool
		if err := registry.Register(api_tools.ToolFunc{
			ToolName: t.Name(),
			ToolDesc: t.Description(),
			Fn: func(ctx context.Context, params map[string]any) (any, error) {
				res, err := t.Execute(ctx, params)
				if err != nil {
					return nil, err
				}
				return res.Data, nil
			},
		}); err != nil {
			fmt.Printf("MCP bridge: failed to register tool %s: %v\n", t.Name(), err)
		}
	}

	return internalReg, nil
}

// ── mcp-null demo CLI ────────────────────────────────────

var mcpNullCmd = &cobra.Command{
	Use:   "mcp-null",
	Short: "Start minimal MCP null server (stdio)",
	Long: `Starts a minimal MCP server with an echo tool over stdio transport.
Useful for demos and testing the MCP protocol without external tools.`,
}

var mcpNullServeCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the MCP null server",
	RunE: func(cmd *cobra.Command, args []string) error {
		server := ares_mcp.NewMCPServer(
			ares_mcp.Implementation{Name: "ares_mcp-null", Version: "1.0.0"},
			ares_mcp.NewStdioServerTransport(),
		)

		echoSchema := json.RawMessage(`{
			"type": "object",
			"properties": {
				"message": {"type": "string"}
			},
			"required": ["message"]
		}`)

		err := server.RegisterTool("echo", "Echoes back the input (no-op for demos)", echoSchema,
			func(ctx context.Context, args map[string]any) (*ares_mcp.ToolCallResult, error) {
				msg, _ := args["message"].(string)
				return &ares_mcp.ToolCallResult{
					Content: []ares_mcp.ContentBlock{
						{Type: "text", Text: fmt.Sprintf("ares_mcp-null: %s", msg)},
					},
				}, nil
			})
		if err != nil {
			return fmt.Errorf("register echo tool: %w", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

		sigEg, sigCtx := errgroup.WithContext(ctx)
		sigEg.Go(func() error {
			select {
			case <-sigCh:
				cancel()
				return nil
			case <-sigCtx.Done():
				return sigCtx.Err()
			}
		})

		if err := server.Serve(ctx); err != nil {
			return fmt.Errorf("serve: %w", err)
		}
		// server.Serve returned nil (clean shutdown); cancel the signal
		// context so sigEg.Wait() does not block forever waiting for a
		// signal that will never arrive.
		cancel()
		if err := sigEg.Wait(); err != nil {
			return fmt.Errorf("signal handler: %w", err)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(mcpNullCmd)
	mcpNullCmd.AddCommand(mcpNullServeCmd)
}
