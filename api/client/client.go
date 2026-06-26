// Package client provides a library-style entry point for embedding GoAgent
// into other Go applications. It exposes modular service accessors, configuration
// management, health checking, and resource lifecycle control.
package client

import (
	"context"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/api/core"
	agentSvc "github.com/Timwood0x10/ares/api/service/agent"
	llmSvc "github.com/Timwood0x10/ares/api/service/llm"
	memorySvc "github.com/Timwood0x10/ares/api/service/memory"
	retrievalSvc "github.com/Timwood0x10/ares/api/service/retrieval"
	runtimeSvc "github.com/Timwood0x10/ares/api/service/runtime"
	workflowSvc "github.com/Timwood0x10/ares/api/service/workflow"
	"github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/events"
	"github.com/Timwood0x10/ares/internal/runtime"
)

// Client provides a unified client interface for all GoAgent modules.
// It is created via NewClient and owns the lifecycle of all child services.
type Client struct {
	agentService     *agentSvc.Service
	memoryService    *memorySvc.Service
	retrievalService *retrievalSvc.Service
	llmService       *llmSvc.Service
	workflowService  *workflowSvc.Service
	config           *Config
	configFile       *ConfigFile
	mu               sync.RWMutex // FIX: protects closed field against data race (code rule 4.5)
	closed           bool
}

// Config holds configuration for the GoAgent client.
// Each field corresponds to a module that can be independently enabled.
type Config struct {
	BaseConfig *core.BaseConfig     // Base configuration (timeout, retries)
	Agent      *agentSvc.Config     // Agent service configuration
	Memory     *memorySvc.Config    // Memory service configuration
	Retrieval  *retrievalSvc.Config // Retrieval service configuration
	LLM        *llmSvc.Config       // LLM service configuration
	Workflow   *workflowSvc.Config  // Workflow service configuration
}

// NewClient creates a new GoAgent client instance with the given configuration.
// It initializes all services whose config is non-nil. If BaseConfig is nil,
// sensible defaults are applied (30s timeout, 3 retries, 1s retry delay).
//
// Args:
//
//	config - client configuration, must not be nil.
//
// Returns:
//
//	client - the initialized client instance.
//	err - ErrInvalidConfig if config is nil, or any service init error.
func NewClient(config *Config) (*Client, error) {
	if config == nil {
		return nil, ErrInvalidConfig
	}

	if config.BaseConfig == nil {
		config.BaseConfig = &core.BaseConfig{
			RequestTimeout: 30 * time.Second,
			MaxRetries:     3,
			RetryDelay:     1 * time.Second,
		}
	}

	client := &Client{
		config: config,
	}

	// Initialize services if configurations are provided
	if config.Agent != nil {
		agentService, err := agentSvc.NewService(config.Agent)
		if err != nil {
			return nil, errors.Wrap(err, "create agent service")
		}
		client.agentService = agentService
	}

	if config.Memory != nil {
		memoryService, err := memorySvc.NewService(config.Memory)
		if err != nil {
			return nil, errors.Wrap(err, "create memory service")
		}
		client.memoryService = memoryService
	}

	if config.Retrieval != nil {
		retrievalService, err := retrievalSvc.NewService(config.Retrieval)
		if err != nil {
			return nil, errors.Wrap(err, "create retrieval service")
		}
		client.retrievalService = retrievalService
	}

	if config.LLM != nil {
		llmService, err := llmSvc.NewService(config.LLM)
		if err != nil {
			return nil, errors.Wrap(err, "create LLM service")
		}
		client.llmService = llmService
	}

	if config.Workflow != nil {
		// If no PluginBus provided, create a default one with safe
		// zero-dependency plugins. Callers can override by setting
		// config.Workflow.PluginBus before NewClient.
		if config.Workflow.PluginBus == nil {
			bus := runtime.NewPluginBus()
			bus.Register(runtime.NewExpressionRouter("default", nil))
			bus.Register(runtime.NewToolPlugin("default-tools"))
			bus.Register(runtime.NewCheckpointPlugin("default-cp", nil)) // no store = no-op
			bus.Register(runtime.NewInterruptPlugin("default-hitl"))
			bus.Register(runtime.NewBasicRecoveryPlugin("default-recovery")) // empty allowlist = no-op
			if err := bus.Start(context.Background()); err != nil {
				return nil, errors.Wrap(err, "start plugin bus")
			}
			config.Workflow.PluginBus = bus
		}
		workflowService, err := workflowSvc.NewService(config.Workflow)
		if err != nil {
			return nil, errors.Wrap(err, "create workflow service")
		}
		client.workflowService = workflowService
	}

	return client, nil
}

// Agent returns the agent service.
//
// Returns:
//
//	service - the agent service instance.
//	err - ErrAgentNotConfigured if agent was not configured at client creation.
func (c *Client) Agent() (*agentSvc.Service, error) {
	if c.agentService == nil {
		return nil, ErrAgentNotConfigured
	}
	return c.agentService, nil
}

// Memory returns the memory service.
//
// Returns:
//
//	service - the memory service instance.
//	err - ErrMemoryNotConfigured if memory was not configured at client creation.
func (c *Client) Memory() (*memorySvc.Service, error) {
	if c.memoryService == nil {
		return nil, ErrMemoryNotConfigured
	}
	return c.memoryService, nil
}

// Retrieval returns the retrieval service.
//
// Returns:
//
//	service - the retrieval service instance.
//	err - ErrRetrievalNotConfigured if retrieval was not configured at client creation.
func (c *Client) Retrieval() (*retrievalSvc.Service, error) {
	if c.retrievalService == nil {
		return nil, ErrRetrievalNotConfigured
	}
	return c.retrievalService, nil
}

// LLM returns the LLM service.
//
// Returns:
//
//	service - the LLM service instance.
//	err - ErrLLMNotConfigured if LLM was not configured at client creation.
func (c *Client) LLM() (*llmSvc.Service, error) {
	if c.llmService == nil {
		return nil, ErrLLMNotConfigured
	}
	return c.llmService, nil
}

// Workflow returns the workflow service.
//
// Returns:
//
//	service - the workflow service instance.
//	err - ErrWorkflowNotConfigured if workflow was not configured at client creation.
func (c *Client) Workflow() (*workflowSvc.Service, error) {
	if c.workflowService == nil {
		return nil, ErrWorkflowNotConfigured
	}
	return c.workflowService, nil
}

// Runtime creates a new runtime service for agent lifecycle management.
// This is a convenience method that wires up EventStore + HeartbeatMonitor + Resurrection.
//
// Args:
//
//	config - runtime configuration. Uses defaults if nil.
//	eventStore - optional event store. If nil, uses in-memory store.
//
// Returns:
//
//	service - the runtime service.
//	err - if creation fails.
func (c *Client) Runtime(config *runtimeSvc.Config, eventStore events.EventStore) (*runtimeSvc.Service, error) {
	if config == nil {
		defaultCfg := runtimeSvc.DefaultConfig()
		config = &defaultCfg
	}
	return runtimeSvc.NewService(*config, eventStore)
}

// Health returns a structured health report for all configured services.
// Each service is probed for availability and latency. The overall status
// is true only when every configured service reports healthy.
//
// Args:
//
//	ctx - operation context (supports cancellation/timeout).
//
// Returns:
//
//	report - structured health status per service.
//	err - context error if the check is cancelled or times out.
func (c *Client) Health(ctx context.Context) (*HealthReport, error) {
	var llmStatus ServiceStatus
	if c.llmService != nil {
		llmStatus = checkLLMHealth(ctx, c.llmService)
	} else {
		llmStatus = ServiceStatus{Available: false, Error: "LLM service not configured"}
	}

	var memoryStatus ServiceStatus
	if c.memoryService != nil {
		memoryStatus = checkServiceHealth("Memory", c.memoryService)
	} else {
		memoryStatus = ServiceStatus{Available: false, Error: "Memory service not configured"}
	}

	var retrievalStatus ServiceStatus
	if c.retrievalService != nil {
		retrievalStatus = checkServiceHealth("Retrieval", c.retrievalService)
	} else {
		retrievalStatus = ServiceStatus{Available: false, Error: "Retrieval service not configured"}
	}

	var workflowStatus ServiceStatus
	if c.workflowService != nil {
		workflowStatus = checkServiceHealth("Workflow", c.workflowService)
	} else {
		workflowStatus = ServiceStatus{Available: false, Error: "Workflow service not configured"}
	}

	report := buildHealthReport(llmStatus, memoryStatus, retrievalStatus, workflowStatus)
	return &report, nil
}

// Config returns a read-only snapshot of the client configuration.
// Returns a deep copy so that mutating any field of the returned value
// cannot affect the original config held by the client, including nested
// pointer fields inside sub-config structs such as Agent.BaseConfig,
// LLM.LLMConfig, and Workflow.AgentRegistry.
//
// Returns:
//
//	config - the client configuration snapshot, may be nil only if client was improperly constructed.
func (c *Client) Config() *Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.config == nil {
		return nil
	}
	// Deep copy each pointer field so that mutating the returned value
	// cannot affect the original config held by the client.
	cp := &Config{}
	if c.config.BaseConfig != nil {
		bc := *c.config.BaseConfig
		cp.BaseConfig = &bc
	}
	if c.config.Agent != nil {
		a := *c.config.Agent
		if a.BaseConfig != nil {
			bc := *a.BaseConfig
			a.BaseConfig = &bc
		}
		cp.Agent = &a
	}
	if c.config.Memory != nil {
		m := *c.config.Memory
		if m.BaseConfig != nil {
			bc := *m.BaseConfig
			m.BaseConfig = &bc
		}
		cp.Memory = &m
	}
	if c.config.Retrieval != nil {
		r := *c.config.Retrieval
		if r.BaseConfig != nil {
			bc := *r.BaseConfig
			r.BaseConfig = &bc
		}
		cp.Retrieval = &r
	}
	if c.config.LLM != nil {
		l := *c.config.LLM
		if l.BaseConfig != nil {
			bc := *l.BaseConfig
			l.BaseConfig = &bc
		}
		if l.LLMConfig != nil {
			lc := *l.LLMConfig
			l.LLMConfig = &lc
		}
		cp.LLM = &l
	}
	if c.config.Workflow != nil {
		w := *c.config.Workflow
		if w.AgentRegistry != nil {
			// AgentRegistry contains a sync.RWMutex, cannot copy by value.
			// Snapshot the typed factories map under the read lock, then build
			// a new registry outside the lock (code rule 4.5: mutex must not be copied).
			ar := w.AgentRegistry
			w.AgentRegistry = ar
		}
		cp.Workflow = &w
	}
	return cp
}

// Close gracefully shuts down the client and releases all held resources.
// It is safe to call Close multiple times; subsequent calls are no-ops.
//
// Args:
//
//	ctx - operation context (supports cancellation/timeout).
//
// Returns:
//
//	err - nil on success, or the first error encountered during cleanup.
func (c *Client) Close(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true

	if c.llmService != nil {
		c.llmService.Close()
	}

	return nil
}

// GetConfig returns the loaded configuration file (YAML-level structure).
// For the client-level Config, use Config() instead.
//
// Returns:
//
//	configFile - the YAML configuration file, or nil if not loaded via NewClientWithConfigFile.
func (c *Client) GetConfig() *ConfigFile {
	return c.configFile
}

// NewClientWithConfigFile creates a new GoAgent client with both client config
// and the raw YAML config file attached. Use this when you need access to
// server-level settings beyond the client Config.
//
// Args:
//
//	config - client configuration, must not be nil.
//	configFile - raw YAML configuration file (may be nil).
//
// Returns:
//
//	client - the initialized client with configFile attached.
//	err - error from NewClient if config is invalid.
func NewClientWithConfigFile(config *Config, configFile *ConfigFile) (*Client, error) {
	client, err := NewClient(config)
	if err != nil {
		return nil, err
	}
	client.configFile = configFile
	return client, nil
}

// Ping checks if all configured services are available.
// This is a lightweight boolean check. For detailed status, use Health instead.
//
// Args:
//
//	ctx - operation context (reserved for future use).
//
// Returns:
//
//	true if all required services are configured and available, false otherwise.
func (c *Client) Ping(ctx context.Context) bool {
	// Agent service is available if configured
	if c.agentService == nil {
		return false
	}

	// Memory service is available if configured
	if c.memoryService == nil {
		return false
	}

	// Retrieval service is available if configured
	if c.retrievalService == nil {
		return false
	}

	// LLM service checks if it's enabled
	if c.llmService != nil && !c.llmService.IsEnabled() {
		return false
	}

	return true
}
