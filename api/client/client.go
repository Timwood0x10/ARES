// Package client provides client interface for GoAgent API.
package client

import (
	"context"
	"time"

	"github.com/Timwood0x10/goagent/api/core"
	agentSvc "github.com/Timwood0x10/goagent/api/service/agent"
	llmSvc "github.com/Timwood0x10/goagent/api/service/llm"
	memorySvc "github.com/Timwood0x10/goagent/api/service/memory"
	retrievalSvc "github.com/Timwood0x10/goagent/api/service/retrieval"
	"github.com/Timwood0x10/goagent/internal/errors"
)

// Client provides a unified client interface for all GoAgent modules.
type Client struct {
	agentService     *agentSvc.Service
	memoryService    *memorySvc.Service
	retrievalService *retrievalSvc.Service
	llmService       *llmSvc.Service
	config           *Config
	configFile       *ConfigFile
}

// Config holds configuration for the GoAgent client.
type Config struct {
	BaseConfig *core.BaseConfig     // Base configuration
	Agent      *agentSvc.Config     // Agent service configuration
	Memory     *memorySvc.Config    // Memory service configuration
	Retrieval  *retrievalSvc.Config // Retrieval service configuration
	LLM        *llmSvc.Config       // LLM service configuration
}

// NewClient creates a new GoAgent client instance.
// Args:
// config - client configuration.
// Returns new client instance or error.
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

	return client, nil
}

// Agent returns the agent service.
// Returns the agent service or an error if not configured.
func (c *Client) Agent() (*agentSvc.Service, error) {
	if c.agentService == nil {
		return nil, ErrAgentNotConfigured
	}
	return c.agentService, nil
}

// Memory returns the memory service.
// Returns the memory service or an error if not configured.
func (c *Client) Memory() (*memorySvc.Service, error) {
	if c.memoryService == nil {
		return nil, ErrMemoryNotConfigured
	}
	return c.memoryService, nil
}

// Retrieval returns the retrieval service.
// Returns the retrieval service or an error if not configured.
func (c *Client) Retrieval() (*retrievalSvc.Service, error) {
	if c.retrievalService == nil {
		return nil, ErrRetrievalNotConfigured
	}
	return c.retrievalService, nil
}

// LLM returns the LLM service.
// Returns the LLM service or an error if not configured.
func (c *Client) LLM() (*llmSvc.Service, error) {
	if c.llmService == nil {
		return nil, ErrLLMNotConfigured
	}
	return c.llmService, nil
}

// Close closes the client and cleans up resources.
func (c *Client) Close(ctx context.Context) error {
	return nil
}

// GetConfig returns the loaded configuration file.
// Returns the configuration file structure or nil if not available.
func (c *Client) GetConfig() *ConfigFile {
	return c.configFile
}

// NewClientWithConfigFile creates a new GoAgent client with both config and config file.
func NewClientWithConfigFile(config *Config, configFile *ConfigFile) (*Client, error) {
	client, err := NewClient(config)
	if err != nil {
		return nil, err
	}
	client.configFile = configFile
	return client, nil
}

// Ping checks if all configured services are available.
// Returns true if all services are available, false otherwise.
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
