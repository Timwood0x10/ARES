package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_mcp"
	memory "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/Timwood0x10/ares/internal/ares_skills"
	"github.com/Timwood0x10/ares/internal/knowledge/skills"
	core_tools "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

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
