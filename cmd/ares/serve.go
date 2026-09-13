// serve — the `ares serve` command: runServe assembly skeleton, event store,
// config/LLM/tool wiring, agent + peer registry setup, control plane, and
// HTTP start. The chaos wiring lives in serve_chaos_domain.go, the L1/Live
// DAG builders in serve_live_dag.go, and the arena CLI in serve_arena.go —
// all split from the former merged serve.go (M-C2), which in turn came from
// serve.go, serve_routine.go, serve_agents.go, serve_chaos.go,
// serve_live_dag.go, arena.go.
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_shutdown"
	"github.com/Timwood0x10/ares/internal/llm/output"
	"github.com/Timwood0x10/ares/internal/logger"
)

// log is the package-level structured logger for the ares serve command.
var log = logger.Module("ares")

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start full agent monitoring with LLM + MCP + dashboard",
	Long: `Starts the full ARES peer-agent runtime with LLM integration,
MCP tools, and the monitoring dashboard.

Flags:
  --config  Path to config YAML (default: ares.yaml)
  --port    HTTP port for dashboard (overrides config)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runServe()
	},
}

var (
	serveConfigPath string
	serveHost       string
	servePort       int
	serveLLMURL     string
	serveLLMKey     string
	serveLLMModel   string
)

func init() {
	rootCmd.AddCommand(serveCmd)
	serveCmd.Flags().StringVarP(&serveConfigPath, "config", "c", "", "Path to config YAML (optional; use --llm-url instead for minimal setup)")
	serveCmd.Flags().StringVar(&serveHost, "host", "", "HTTP bind address (overrides config; default 127.0.0.1 — use 0.0.0.0 to expose, requires auth)")
	serveCmd.Flags().IntVarP(&servePort, "port", "p", 0, "HTTP port for dashboard (overrides config)")
	serveCmd.Flags().StringVar(&serveLLMURL, "llm-url", "", "LLM endpoint URL — minimal setup, no config file needed")
	serveCmd.Flags().StringVar(&serveLLMKey, "llm-api-key", "", "LLM API key (minimal setup)")
	serveCmd.Flags().StringVar(&serveLLMModel, "llm-model", "", "LLM model name (optional, provider default when empty)")
}

//nolint:gocyclo // runServe is the serve assembly hub; each step is extracted.
func runServe() error {
	// --- Config ---
	cfg, err := loadServeConfig()
	if err != nil {
		return err
	}
	if err := validateServeConfig(cfg); err != nil {
		return err
	}

	// --- Context with signal handling ---
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Graceful shutdown coordinator (internal/ares_shutdown). Real teardown
	// hooks (HTTP server, MCP, runtime) are registered below once those
	// components are initialized.
	shutdownMgr := ares_shutdown.NewManager(30 * time.Second)
	shutdownMgr.RegisterPhase(ares_shutdown.PhasePreShutdown, 5*time.Second)
	shutdownMgr.RegisterPhase(ares_shutdown.PhaseGraceful, 20*time.Second)
	shutdownMgr.RegisterPhase(ares_shutdown.PhaseForce, 5*time.Second)
	shutdownMgr.RegisterPhase(ares_shutdown.PhaseDone, 1*time.Second)

	g, ctx := errgroup.WithContext(ctx)
	// comp is assigned by Bootstrap below; the signal goroutine references it
	// for shutdown (WaitBackground + snapshot). The pointer is exchanged via
	// atomic.Store/Load so the goroutine never races with the Bootstrap
	// assignment on the main goroutine.
	var compPtr atomic.Pointer[ares_bootstrap.Components]
	wiringServeSignalWatch(g, sigCh, ctx, cancel, shutdownMgr, &compPtr)

	// --- EventStore + Bootstrap ---
	comp, store, mgr, err := wiringServeEventStore(ctx, cfg)
	if err != nil {
		return err
	}
	compPtr.Store(comp)
	// Assembly-phase exit check — if a shutdown signal arrived during the
	// (potentially long) Bootstrap, abort the startup instead of proceeding
	// to wire components and start the runtime on a canceled context.
	if err := ctx.Err(); err != nil {
		log.Info("serve: shutdown was requested during assembly; aborting startup", "err", err)
		return normalizeShutdownErr(err)
	}

	// --- Runtime config store + hot-reload watcher + startup snapshot ---
	cfgStore := wiringServeCfgStoreWatch(ctx, g, cfg, comp)

	// --- LLM adapter with fallback ---
	llmAdapter, err := createLLMAdapterWithFallback(cfg)
	if err != nil {
		return fmt.Errorf("create llm adapter: %w", err)
	}

	// --- ChatClient for native tool calling ---
	chatClient, err := createChatClient(cfg)
	if err != nil {
		return fmt.Errorf("create chat client: %w", err)
	}
	log.Info("chat client created", "provider", cfg.LLM.Provider, "model", cfg.LLM.Model)

	// --- Tools + MCP + binder ---
	toolBinder, registry, internalReg, err := wiringServeToolchain(ctx, cfg, comp)
	if err != nil {
		return err
	}

	// --- Create + register agents with the runtime manager ---
	subAgents, peerKernel, err := createAndServeAgents(ctx, cfg, internalReg, llmAdapter, chatClient, toolBinder, comp, mgr)
	if err != nil {
		return err
	}

	// --- Peer registry: enable direct agent-to-agent messaging ---
	// setupPeerRegistry builds the registry; the kernel handle powers
	// collaboration-topic execution through the fabric DAG. The
	// registry is retained on the kernel handle so it stays reachable for
	// direct peer messaging / capability discovery instead of being discarded.
	reg, err := setupPeerRegistry(ctx, g, subAgents, comp, peerKernel)
	if err != nil {
		return err
	}
	if peerKernel != nil {
		peerKernel.peerRegistry = reg
		log.Info("serve: peer registry retained on kernel (agents)", "count", len(reg.IDs()))
	}

	// --- Runtime introspection control plane:
	// intelligence engine + read-only control server (extracted to
	// setupServeControlPlane to keep runServe's cyclomatic complexity within
	// gocyclo's 30 limit). The old MonitorPlugin/tabs/PluginBus bridge is gone.
	intelEngine, controlServer, err := setupServeControlPlane(ctx, g, cfg, cfgStore, store, peerKernel, comp.Dashboard, comp.FlightRecorder, evolutionLifecycleForServe(comp))
	if err != nil {
		return err
	}

	// --- Start runtime ---
	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("start runtime: %w", err)
	}

	// Sub-agents are execution units only (ares-runtime: agents are not
	// orchestrated, they are scheduled). The Kernel owns dispatch: the
	// kernelScheduler drives each task through RunQuantum →
	// sub.Agent.ExecuteStep; agents never subscribe to the event stream and
	// self-dispatch (self-dispatch was removed).

	// --- Dashboard APIv2 server (observability read side) ---
	// The old standalone dashboard :8090 server was removed; the
	// observability providers now feed introspect.ControlServer below.

	// --- HTTP server + graceful-shutdown hooks (extracted to keep runServe
	// cyclomatic complexity within lint limits) ---
	if _, err := startServeHTTPAndHooks(ctx, g, cfg, cfgStore, controlServer, intelEngine, mgr, registry, toolBinder, shutdownMgr, comp, peerKernel); err != nil {
		return err
	}

	// Wait for all goroutines to complete (signal handler, bridge, tasks, HTTP).
	// A context cancellation (SIGINT/SIGTERM → graceful shutdown) surfaces as
	// context.Canceled from the errgroup; that is a NORMAL exit, not an error —
	// normalized to nil so `ares serve` exits 0 on Ctrl-C.
	return normalizeShutdownErr(g.Wait())
}

// normalizeShutdownErr treats context cancellation (graceful shutdown) as a
// clean exit: Ctrl-C is not a failure. Extracted so runServe stays within the
// cyclomatic-complexity limit.
func normalizeShutdownErr(err error) error {
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// allowConfigDirFor confines ares_config.Load to the directory holding the
// given config path. The path-traversal guard inside Load is opt-in via
// SetAllowedConfigDir and had NO production caller — SECURITY.md documented a
// control that was a no-op at runtime (review C-3). Every Load entry point in
// this binary calls this first, so each load in the process (initial,
// hot-reload re-reads, status fallback candidates) is confined to the
// operator's config directory.
func allowConfigDirFor(path string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		// A path that cannot be absolutized cannot be confined; Load
		// surfaces its own error when it fails to read the file.
		return
	}
	ares_config.SetAllowedConfigDir(filepath.Dir(abs))
}

func loadServeConfig() (*ares_config.Config, error) {
	// Minimal setup: the user provides only the LLM endpoint (--llm-url) and
	// optionally the API key / model. Everything else — agents, memory, tools,
	// storage, kernel policy — is assembled by the runtime from defaults, so no
	// config file is required.
	if serveLLMURL != "" {
		cfg := ares_config.NewMinimalConfig(serveLLMURL, serveLLMKey, serveLLMModel)
		if serveHost != "" {
			cfg.Server.Host = serveHost
		}
		if servePort > 0 {
			cfg.Server.Port = servePort
		}
		log.Info("serve: minimal config (llm-url only); runtime defaults for all subsystems")
		return cfg, nil
	}

	configPath := serveConfigPath
	if configPath == "" {
		for _, p := range []string{
			"ares.yaml",
			"./ares.yaml",
		} {
			if _, err := os.Stat(p); err == nil {
				configPath = p
				break
			}
		}
		if configPath == "" {
			configPath = "ares.yaml"
		}
		// Write the resolved path back so runServe's watcher starts for the
		// auto-detected config too (previously Watch only ran with an explicit
		// --config; hot-reload silently no-op'd on the default ares.yaml).
		serveConfigPath = configPath
	}

	allowConfigDirFor(configPath)
	cfg, err := ares_config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if err := ares_config.LoadFromEnv(cfg); err != nil {
		return nil, fmt.Errorf("load env: %w", err)
	}
	// CLI flags win over env (SERVER_HOST/SERVER_PORT) and YAML: the explicit
	// argument is the most specific intent.
	if serveHost != "" {
		cfg.Server.Host = serveHost
	}
	if servePort > 0 {
		cfg.Server.Port = servePort
	}
	return cfg, nil
}

// validateServeConfig enforces the configuration contract of the full agent
// serving entry point before Bootstrap starts any component.
//
// It runs the shared Config validator, which the config-file path already
// gets from Config.Load. The no-config path (`ares serve --llm-url …`)
// builds a config with NewMinimalConfig and returns it directly, so without
// this call nothing validated that config at all — the only thing standing
// between an operator and a late, deep wiring failure was a nil check.
func validateServeConfig(cfg *ares_config.Config) error {
	if cfg == nil {
		return errors.New("serve: config is required")
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("serve: invalid configuration: %w", err)
	}
	return nil
}

// createLLMAdapterWithFallback creates an LLM adapter with fallback chain.
func createLLMAdapterWithFallback(cfg *ares_config.Config) (output.LLMAdapter, error) {
	factory := output.NewFactory()

	// Try primary
	primaryCfg := &output.Config{
		Provider:  cfg.LLM.Provider,
		APIKey:    cfg.LLM.APIKey,
		BaseURL:   cfg.LLM.BaseURL,
		Model:     cfg.LLM.Model,
		Timeout:   cfg.LLM.Timeout,
		MaxTokens: cfg.LLM.MaxTokens,
	}

	adapter, err := factory.Create(cfg.LLM.Provider, primaryCfg)
	if err == nil {
		log.Info("LLM adapter created", "provider", cfg.LLM.Provider, "model", cfg.LLM.Model)
		return adapter, nil
	}
	log.Warn("primary LLM failed, trying fallbacks", "err", err)

	// Try fallbacks from config
	for _, fb := range cfg.LLM.Fallbacks {
		fbCfg := &output.Config{
			Provider:  fb.Provider,
			APIKey:    fb.APIKey,
			BaseURL:   fb.BaseURL,
			Model:     fb.Model,
			Timeout:   fb.Timeout,
			MaxTokens: fb.MaxTokens,
		}
		if fbCfg.Provider == "" {
			fbCfg.Provider = "openai"
		}
		adapter, err = factory.Create(fbCfg.Provider, fbCfg)
		if err == nil {
			log.Info("LLM fallback adapter created", "provider", fbCfg.Provider, "model", fbCfg.Model)
			return adapter, nil
		}
		log.Warn("fallback LLM failed", "provider", fbCfg.Provider, "err", err)
	}
	// Last resort: local ollama — but ONLY when the config did not
	// explicitly name any LLM. Silently switching an explicitly configured
	// hosted provider (bad key, wrong base URL) to a local llama hides the
	// misconfiguration and changes model behavior without notice; that is
	// a hard error now. An empty/unset LLM config is the legitimate
	// "local dev" case where the ollama default is a real convenience.
	llmConfigured := cfg.LLM.Provider != "" || len(cfg.LLM.Fallbacks) > 0
	if !llmConfigured {
		log.Info("no LLM configured, defaulting to local ollama (llama3.2)")
		ollamaCfg := &output.Config{
			Provider:  "ollama",
			BaseURL:   "http://localhost:11434",
			Model:     "llama3.2",
			Timeout:   120,
			MaxTokens: 2048,
		}
		adapter, err = factory.Create("ollama", ollamaCfg)
		if err == nil {
			return adapter, nil
		}
		// Wrap the sentinel so callers can errors.Is(err, ErrNoLLMAdapter)
		// while still retaining the underlying adapter-creation error.
		return nil, fmt.Errorf("no LLM adapter available: %w (last attempt: %v)", ErrNoLLMAdapter, err)
	}
	// The user configured LLM provider(s) and every one of them failed:
	// surface that instead of papering over it with an unrequested local
	// model.
	return nil, fmt.Errorf("all configured LLM providers failed (primary %q + %d fallback(s)): %w",
		cfg.LLM.Provider, len(cfg.LLM.Fallbacks), ErrNoLLMAdapter)
}

// ErrNoLLMAdapter is the sentinel returned by createLLMAdapterWithFallback when
// every configured provider (primary, fallbacks, and the local ollama last
// resort) fails to produce an adapter. Callers that need to distinguish "no
// LLM available" from other serve failures should use errors.Is(err,
// ErrNoLLMAdapter) — e.g. to surface a degraded-mode warning instead of a hard
// crash. (Prefer typed errors over string matching.)
var ErrNoLLMAdapter = errors.New("serve: no LLM adapter available")

// defaultServeHost is the fallback bind host when the config leaves
// server.host empty (a hand-built Config may skip setDefaults). The explicit
// loopback IP, not the "localhost" name — the bind must not depend on
// hosts-file resolution (M-S1).
const defaultServeHost = "127.0.0.1"

// serverBindAddr resolves the HTTP listen address from the server config.
// The host is the real bind address (default "localhost"); empty falls back
// rather than silently widening to a wildcard bind.
func serverBindAddr(host string, port int) string {
	if host == "" {
		host = defaultServeHost
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// isWildcardHost reports whether host selects all network interfaces
// (the "0.0.0.0" wildcard; IPv6's "::" is also a wildcard form).
func isWildcardHost(host string) bool {
	switch host {
	case "0.0.0.0", "::":
		return true
	default:
		return false
	}
}

// displayServeHost picks the host to print on the startup console: a wildcard
// bind prints the loopback probe address, because connecting to 0.0.0.0
// directly does not work on every platform and the panel URL must be usable.
func displayServeHost(host, addr string) string {
	if isWildcardHost(host) {
		return "localhost:" + strconv.Itoa(portOf(addr))
	}
	return addr
}

// portOf extracts the numeric port from a host:port address.
func portOf(addr string) int {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0
	}
	return port
}
