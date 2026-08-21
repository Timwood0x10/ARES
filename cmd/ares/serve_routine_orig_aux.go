//go:build ignore

// Part 2 of the serve_routine.go backup split (auxiliary wiring: tool
// adapter, DAG patch executor, skill catalog). Excluded from builds via the
// ignore tag; reference only.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_mcp"
	memory "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/Timwood0x10/ares/internal/ares_skills"
	"github.com/Timwood0x10/ares/internal/evolution/patch"
	"github.com/Timwood0x10/ares/internal/knowledge/skills"
	core_tools "github.com/Timwood0x10/ares/internal/tools/resources/core"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
)

// akfToolAdapter adapts an AKF MCP tool (func(ctx, input string) -> string)
// to the core_tools.Tool interface so it can be registered in the internal
// tool registry and used by sub-agents through the ToolBinder. This is the
// wiring that makes knowledge genome patches affect the agent's knowledge
// tools — because both share the same comp.KnowledgeRuntime instance.
type akfToolAdapter struct {
	name string
	desc string
	fn   func(ctx context.Context, input string) (string, error)
}

func (a *akfToolAdapter) Name() string                      { return a.name }
func (a *akfToolAdapter) Description() string               { return a.desc }
func (a *akfToolAdapter) Category() core_tools.ToolCategory { return core_tools.CategoryKnowledge }
func (a *akfToolAdapter) Capabilities() []core_tools.Capability {
	return []core_tools.Capability{core_tools.CapabilityKnowledge}
}
func (a *akfToolAdapter) Parameters() *core_tools.ParameterSchema { return nil }
func (a *akfToolAdapter) Execute(ctx context.Context, params map[string]interface{}) (core_tools.Result, error) {
	input, _ := params["input"].(string)
	if input == "" {
		// Serialize the whole params map as JSON input.
		b, _ := json.Marshal(params)
		input = string(b)
	}
	out, err := a.fn(ctx, input)
	if err != nil {
		return core_tools.NewErrorResult(err.Error()), nil
	}
	return core_tools.NewResult(true, map[string]interface{}{"output": out}), nil
}

// liveDAGPatchExecutor applies workflow structure patches directly to the
// agent's live engine.MutableDAG held by the runtime manager. Unlike the
// synthetic GraphPatchExecutor (which operates on a private noop *wfgraph.Graph),
// this executor reads the live DAG from the manager's dagStore, applies the
// mutation, and writes it back — so genome evolution patches to workflow
// structure (insert/remove nodes/edges) actually change the DAG the agent
// reads at runtime.
type liveDAGPatchExecutor struct {
	mgr     *ares_runtime.Manager
	agentID string
}

// errNoSnapshot is returned by Snapshot to signal that this executor does
// not produce a serializable snapshot. Callers should treat it as "no diff
// available" rather than a real failure.
var errNoSnapshot = errors.New("live DAG executor: snapshot not supported")

// errNoRollback is returned by Apply when the patch succeeds but produces no
// rollback patch (the operation is its own inverse or is irreversible).
var errNoRollback = errors.New("live DAG executor: no rollback patch")

func newLiveDAGPatchExecutor(mgr *ares_runtime.Manager, agentID string) *liveDAGPatchExecutor {
	return &liveDAGPatchExecutor{mgr: mgr, agentID: agentID}
}

func (e *liveDAGPatchExecutor) Name() string { return "live_dag" }

func (e *liveDAGPatchExecutor) Snapshot(_ context.Context) (any, error) {
	return nil, errNoSnapshot
}

func (e *liveDAGPatchExecutor) CanApply(_ context.Context, p patch.RuntimePatch) error {
	// All patch types that GraphPatchExecutor supports are supported here.
	switch p.Type {
	case patch.PatchInsertNode, patch.PatchRemoveNode,
		patch.PatchReplaceNode, patch.PatchAddEdge,
		patch.PatchRemoveEdge, patch.PatchChangeScheduler:
		return nil
	default:
		return fmt.Errorf("live DAG executor: unsupported patch type %s", p.Type)
	}
}

func (e *liveDAGPatchExecutor) Apply(ctx context.Context, p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	dagAny, ok := e.mgr.GetAgentDAG(e.agentID)
	if !ok || dagAny == nil {
		return nil, fmt.Errorf("live DAG executor: no DAG for agent %s", e.agentID)
	}
	dag, dagOk := dagAny.(*engine.MutableDAG)
	if !dagOk || dag == nil {
		return nil, fmt.Errorf("live DAG executor: DAG for agent %s is not a MutableDAG", e.agentID)
	}

	switch p.Type {
	case patch.PatchInsertNode:
		step := &engine.Step{ID: p.Target, Name: p.Target, AgentType: "processor"}
		if err := dag.AddNode(ctx, step); err != nil {
			return nil, fmt.Errorf("live DAG: insert node %s: %w", p.Target, err)
		}
		return &patch.RuntimePatch{
			Type:   patch.PatchRemoveNode,
			Target: p.Target,
			Reason: "rollback: remove inserted node",
		}, nil

	case patch.PatchRemoveNode:
		if err := dag.RemoveNode(ctx, p.Target); err != nil {
			return nil, fmt.Errorf("live DAG: remove node %s: %w", p.Target, err)
		}
		return nil, errNoRollback

	case patch.PatchReplaceNode:
		step := &engine.Step{ID: p.Target, Name: p.Target, AgentType: "processor"}
		if err := dag.RemoveNode(ctx, p.Target); err != nil {
			return nil, fmt.Errorf("live DAG: replace (remove) node %s: %w", p.Target, err)
		}
		if err := dag.AddNode(ctx, step); err != nil {
			return nil, fmt.Errorf("live DAG: replace (add) node %s: %w", p.Target, err)
		}
		return nil, errNoRollback

	case patch.PatchAddEdge:
		val, ok := p.Value.(map[string]string)
		if !ok {
			return nil, fmt.Errorf("live DAG: AddEdge value must be map[string]string")
		}
		from, to := val["from"], val["to"]
		if err := dag.AddEdge(ctx, from, to); err != nil {
			return nil, fmt.Errorf("live DAG: add edge %s→%s: %w", from, to, err)
		}
		return &patch.RuntimePatch{
			Type:   patch.PatchRemoveEdge,
			Value:  map[string]string{"from": from, "to": to},
			Reason: "rollback: remove added edge",
		}, nil

	case patch.PatchRemoveEdge:
		val, ok := p.Value.(map[string]string)
		if !ok {
			return nil, fmt.Errorf("live DAG: RemoveEdge value must be map[string]string")
		}
		from, to := val["from"], val["to"]
		if err := dag.RemoveEdge(ctx, from, to); err != nil {
			return nil, fmt.Errorf("live DAG: remove edge %s→%s: %w", from, to, err)
		}
		return nil, errNoRollback

	case patch.PatchChangeScheduler:
		// Store the scheduler type on the live DAG so the agent's runtime
		// scheduler selection reads the evolved config instead of the default.
		schedType := fmt.Sprintf("%T", p.Value)
		dag.SchedulerType = schedType
		log.Printf("live DAG: scheduler change for agent %s: %s", e.agentID, schedType)
		return nil, errNoRollback

	default:
		return nil, fmt.Errorf("live DAG executor: unsupported patch type %s", p.Type)
	}
}

// Ensure liveDAGPatchExecutor implements patch.RuntimeComponent.
var _ patch.RuntimeComponent = (*liveDAGPatchExecutor)(nil)

// toolChangeDebounceWindow collapses bursts of MCP tools/listChanged
// notifications into a single refresh.
const toolChangeDebounceWindow = 2 * time.Second

// debounceToolChange returns a notification handler that runs catalog.Refresh
// at most once per debounce window. Notifications arriving inside the window
// (a) reset the timer (leading-edge coalescing), so a burst of listChanged
// events results in exactly one refresh. The trailing edge is preferred: the
// refresh runs debounceWindow after the last notification, giving the MCP
// servers time to finish their tool registration before the catalog indexes.
func debounceToolChange(catalog *ares_skills.Catalog) func() {
	var (
		mu         sync.Mutex
		timer      *time.Timer
		refreshing bool
		pending    bool
	)
	// runRefresh executes one catalog refresh under the single-flight guard.
	// A notification that arrives while a refresh is in flight is marked
	// pending (never dropped) and re-runs once the in-flight refresh returns;
	// a panic inside Refresh is recovered so refreshing can never strand true.
	// Declared with var so the closure can reference itself.
	var runRefresh func()
	runRefresh = func() {
		mu.Lock()
		if refreshing {
			pending = true // a change arrived mid-refresh: re-run afterwards
			mu.Unlock()
			return
		}
		refreshing = true
		mu.Unlock()

		func() {
			defer func() { _ = recover() }() // never strand refreshing=true on panic
			if _, refreshErr := catalog.Refresh(); refreshErr != nil {
				log.Printf("skill catalog: listChanged refresh failed: %v", refreshErr)
			}
		}()

		mu.Lock()
		refreshing = false
		reArm := pending
		pending = false
		mu.Unlock()

		if reArm {
			time.AfterFunc(toolChangeDebounceWindow, runRefresh)
		}
	}
	return func() {
		mu.Lock()
		defer mu.Unlock()
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(toolChangeDebounceWindow, runRefresh)
	}
}

// wireSkillCatalog builds the Capability Fabric catalog over the declared
// skill sources (project ".ares/skills" + user "~/.ares/skills") and seeds
// the memory manager's resident skill block (Level-0 metadata only). It then
// registers the catalog's agent-facing tools (skill_search / skill_load /
// skill_activate / skill_list) into the shared internal registry and re-bridges
// the tool binder, so the LLM can actually discover, load and activate skills
// at runtime (design §10 main loop). The catalog is wired via duck typing:
// SetSkillsRegistry is a concrete method on the memory manager, not part of
// the MemoryManager interface. Any failure is logged and serve continues
// without skills.
//
// Returns:
//   - *ares_skills.Catalog: the built catalog, or nil when building/seeding
//     failed (callers treat nil as "skills unavailable").
func wireSkillCatalog(cfg *ares_config.Config, internalReg *core_tools.Registry, toolBinder sub.ToolBinder, memMgr memory.MemoryManager, mcpMgr *ares_mcp.MCPManager) *ares_skills.Catalog {
	projectSkills := filepath.Join(".", ".ares", "skills")
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	// Registered extra sources come from ~/.ares/config.toml [[skill_sources]];
	// a missing file or parse error degrades to project+user sources only.
	// LoadSkillSources parses the file once and returns directory, git and
	// http sources together (LoadRegisteredSkillDirs is just its directory
	// subset — calling both would re-read the same config file).
	extraDirs, gitSources, httpSources, err := ares_skills.LoadSkillSources("")
	if err != nil {
		log.Printf("skill catalog: load registered sources failed: %v", err)
	}
	catalog := ares_skills.NewCatalog(ares_skills.CatalogConfig{
		ProjectSkillsDir:      projectSkills,
		UserSkillsDir:         filepath.Join(home, ".ares", "skills"),
		RegisteredDirs:        extraDirs,
		AllowLocalExecutables: true,
		Builtins:              toolBinder.ListTools(),
		ExperiencePath:        filepath.Join(home, ".ares", "experience.json"),
	})
	catalog.SetGitSources(gitSources)
	catalog.SetHTTPSources(httpSources)
	if len(gitSources) > 0 {
		// Bound the git sync so an unreachable host degrades to
		// local-checkout-only indexing instead of blocking serve startup
		// for the OS connect timeout.
		syncCtx, syncCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer syncCancel()
		if syncErr := catalog.SyncGitSources(syncCtx); syncErr != nil {
			log.Printf("skill catalog: git sync failed (indexing local checkouts only): %v", syncErr)
		}
	}
	if mcpMgr != nil {
		// MCP servers are lazy: connected only when a skill declaring them is
		// activated (design principle 3 / acceptance #3).
		catalog.SetMCPConnector(mcpMgr)
		// tools/listChanged notifications trigger an incremental re-index so
		// the catalog reflects newly surfaced MCP tools on demand. The
		// notifications can arrive in bursts (e.g. several servers starting at
		// once); debounce them so each burst collapses into a single Refresh
		// instead of hammering git/http sources and rebuilding FTS5 repeatedly.
		mcpMgr.SetToolChangeHandler(debounceToolChange(catalog))
	}
	if err := catalog.Build(); err != nil {
		log.Printf("skill catalog: build failed: %v", err)
		return nil
	}
	reg := skills.NewRegistry()
	if err := catalog.SeedRegistry(reg); err != nil {
		log.Printf("skill catalog: seed registry failed: %v", err)
		return nil
	}
	if mm, ok := memMgr.(interface{ SetSkillsRegistry(*skills.Registry) }); ok {
		mm.SetSkillsRegistry(reg)
	}
	// Agent-facing tools close the design §10 loop (Discover -> Load ->
	// Execute). Registering into the shared registry surfaces their schemas to
	// the LLM; re-bridging makes CallTool reach them (BridgeFromRegistry never
	// overwrites existing bindings, so repeating it is safe).
	registered := 0
	for _, tool := range ares_skills.CatalogTools(catalog) {
		if regErr := internalReg.Register(tool); regErr != nil {
			log.Printf("skill catalog: register tool %q failed: %v", tool.Name(), regErr)
			continue
		}
		registered++
	}
	if registered > 0 {
		toolBinder.BridgeFromRegistry(internalReg)
	}
	log.Printf("skill catalog: indexed %d skills, %d agent tools registered", len(catalog.All()), registered)
	return catalog
}
