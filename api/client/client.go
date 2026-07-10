// Package client provides a library-style entry point for embedding GoAgent
// into other Go applications. It exposes modular service accessors via api/core
// interfaces, with implementations injected at construction time.
//
// Internal implementations are constructed by api/bootstrap; this package
// has zero direct imports from internal/.
package client

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/api/core"
	runtimeSvc "github.com/Timwood0x10/ares/api/service/runtime"
	workflowSvc "github.com/Timwood0x10/ares/api/service/workflow"
)

// Client provides a unified client interface for all GoAgent modules.
// It is created via NewClient and owns the lifecycle of all child services.
type Client struct {
	agentService     core.AgentService
	memoryService    core.MemoryService
	retrievalService core.RetrievalService
	llmService       core.LLMService
	workflowService  core.WorkflowService
	config           *Config
	configFile       *ConfigFile
	mu               sync.RWMutex
	closed           bool
}

// Config holds configuration for the GoAgent client.
// Service fields accept pre-built implementations (via api/bootstrap)
// or nil to skip the module.
type Config struct {
	BaseConfig *core.BaseConfig
	Agent      core.AgentService
	Memory     core.MemoryService
	Retrieval  core.RetrievalService
	LLM        core.LLMService
	Workflow   *workflowSvc.Config
}

// NewClient creates a new GoAgent client instance with the given configuration.
// Each service in Config is already fully constructed — this function does not
// import internal/ packages. Use api/bootstrap to build services.
//
// Args:
//
//	config - client configuration, must not be nil.
//
// Returns:
//
//	client - the initialized client instance.
//	err - ErrInvalidConfig if config is nil.
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

	if config.Agent != nil {
		client.agentService = config.Agent
	}

	if config.Memory != nil {
		client.memoryService = config.Memory
	}

	if config.Retrieval != nil {
		client.retrievalService = config.Retrieval
	}

	if config.LLM != nil {
		client.llmService = config.LLM
	}

	if config.Workflow != nil {
		svc, err := workflowSvc.NewService(config.Workflow)
		if err != nil {
			return nil, err
		}
		client.workflowService = svc
	}

	return client, nil
}

// Agent returns the agent service.
func (c *Client) Agent() (core.AgentService, error) {
	if c.agentService == nil {
		return nil, ErrAgentNotConfigured
	}
	return c.agentService, nil
}

// Memory returns the memory service.
func (c *Client) Memory() (core.MemoryService, error) {
	if c.memoryService == nil {
		return nil, ErrMemoryNotConfigured
	}
	return c.memoryService, nil
}

// Retrieval returns the retrieval service.
func (c *Client) Retrieval() (core.RetrievalService, error) {
	if c.retrievalService == nil {
		return nil, ErrRetrievalNotConfigured
	}
	return c.retrievalService, nil
}

// LLM returns the LLM service.
func (c *Client) LLM() (core.LLMService, error) {
	if c.llmService == nil {
		return nil, ErrLLMNotConfigured
	}
	return c.llmService, nil
}

// Workflow returns the workflow service.
func (c *Client) Workflow() (core.WorkflowService, error) {
	if c.workflowService == nil {
		return nil, ErrWorkflowNotConfigured
	}
	return c.workflowService, nil
}

// Runtime creates a new runtime service for agent lifecycle management.
func (c *Client) Runtime(config *runtimeSvc.Config, _ interface{}) (*runtimeSvc.Service, error) {
	if config == nil {
		defaultCfg := runtimeSvc.DefaultConfig()
		config = &defaultCfg
	}
	return runtimeSvc.NewService(*config, nil)
}

// Close closes the client and releases resources held by child services.
// Services that implement optional Close() or Stop(context.Context) methods
// are gracefully shut down. Idempotent — calling Close multiple times is safe.
func (c *Client) Close(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true

	var errs []error

	// Shut down services that expose lifecycle methods. The core interfaces
	// don't include Close/Stop, so use type assertions to detect optional
	// implementations.
	if c.memoryService != nil {
		if svc, ok := c.memoryService.(interface{ Stop(context.Context) error }); ok {
			if err := svc.Stop(ctx); err != nil {
				errs = append(errs, fmt.Errorf("memory stop: %w", err))
			}
		}
	}
	if c.llmService != nil {
		if svc, ok := c.llmService.(interface{ Close() }); ok {
			svc.Close()
		}
	}

	return errors.Join(errs...)
}

// Config returns the client configuration.
func (c *Client) Config() *Config {
	return c.config
}

// GetConfig returns the loaded configuration file.
func (c *Client) GetConfig() *ConfigFile {
	return c.configFile
}

// Health returns a structured health report.
func (c *Client) Health(ctx context.Context) (*HealthReport, error) {
	return &HealthReport{
		Healthy:   !c.closed,
		Timestamp:     time.Now(),
	}, nil
}

// Ping checks if the client is operational.
func (c *Client) Ping(ctx context.Context) bool {
	return !c.closed
}
