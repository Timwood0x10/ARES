// Package api provides legacy unified client interface for GoAgent framework.
//
// DEPRECATED: This package is deprecated. Please use goagentx/api/client package for new code.
// This package is maintained for backward compatibility only.
package api

import (
	"context"
	"time"

	"goagentx/api/agent"
	"goagentx/api/memory"
	"goagentx/api/retrieval"
	"goagentx/internal/errors"
	internalmemory "goagentx/internal/memory"
	"goagentx/internal/storage/postgres"
	"goagentx/internal/storage/postgres/embedding"
	"goagentx/internal/storage/postgres/repositories"
)

// Client provides unified client interface for all GoAgent modules.
//
// DEPRECATED: Use goagentx/api/client.NewClient instead.
type Client struct {
	agentService     *agent.Service
	memoryService    *memory.Service
	retrievalService *retrieval.Service
	pool             *postgres.Pool
	config           *Config
}

// Config configuration for GoAgent client.
type Config struct {
	Database  *DatabaseConfig
	LLM       *LLMConfig
	Embedding *EmbeddingConfig
	Retrieval *RetrievalConfig
	Memory    *MemoryConfig
}

// DatabaseConfig database configuration.
type DatabaseConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
}

// LLMConfig LLM configuration.
type LLMConfig struct {
	Provider string
	APIKey   string
	BaseURL  string
	Model    string
	Timeout  int
}

// EmbeddingConfig embedding configuration.
type EmbeddingConfig struct {
	ServiceURL string
	Model      string
}

// RetrievalConfig retrieval configuration.
type RetrievalConfig struct {
	UseSimpleRetrieval bool
	TopK               int
	MinScore           float64
}

// MemoryConfig memory configuration.
type MemoryConfig struct {
	Enabled        bool
	MaxHistory     int
	EnablePostgres bool
}

// NewClient creates a new GoAgent client instance with simplified initialization.
//
// DEPRECATED: This method is deprecated. Use goagentx/api/client.NewClient instead.
// Args:
// config - client configuration.
// Returns new client instance or error if initialization fails.
func NewClient(config *Config) (*Client, error) {
	if config == nil {
		return nil, ErrInvalidConfig
	}

	// Initialize database connection
	dbConfig := &postgres.Config{
		Host:            config.Database.Host,
		Port:            config.Database.Port,
		User:            config.Database.User,
		Password:        config.Database.Password,
		Database:        config.Database.Database,
		MaxOpenConns:    25,
		MaxIdleConns:    10,
		ConnMaxLifetime: 5 * time.Minute,
		QueryTimeout:    30 * time.Minute,
		Embedding:       postgres.DefaultEmbeddingConfig(),
	}

	pool, err := postgres.NewPool(dbConfig)
	if err != nil {
		return nil, errors.Wrap(err, "create database pool")
	}

	// Initialize memory manager
	memoryMgr, err := internalmemory.NewMemoryManager(&internalmemory.MemoryConfig{
		Enabled:        config.Memory.Enabled,
		MaxHistory:     config.Memory.MaxHistory,
		EnablePostgres: config.Memory.EnablePostgres,
	})
	if err != nil {
		return nil, errors.Wrap(err, "create memory manager")
	}

	// Initialize embedding client
	embeddingClient := embedding.NewEmbeddingClient(
		config.Embedding.ServiceURL,
		config.Embedding.Model,
		nil,
		30, // 30 seconds timeout
	)

	// Initialize knowledge repository
	kbRepo := repositories.NewKnowledgeRepository(pool.GetDB(), pool.GetDB())

	// Initialize retrieval service
	retrievalConfig := &retrieval.Config{
		UseSimpleRetrieval: config.Retrieval.UseSimpleRetrieval,
		TopK:               config.Retrieval.TopK,
		MinScore:           config.Retrieval.MinScore,
	}

	retrievalService, err := retrieval.NewService(pool, embeddingClient, kbRepo, retrievalConfig)
	if err != nil {
		return nil, errors.Wrap(err, "create retrieval service")
	}

	// Initialize services
	agentService := agent.NewService(memoryMgr)
	memoryService := memory.NewService(memoryMgr)

	return &Client{
		agentService:     agentService,
		memoryService:    memoryService,
		retrievalService: retrievalService,
		pool:             pool,
		config:           config,
	}, nil
}

// Agent returns the agent API.
//
// DEPRECATED: Use goagentx/api/client.Client.Agent instead.
func (c *Client) Agent() *agent.Service {
	return c.agentService
}

// Memory returns the memory API.
//
// DEPRECATED: Use goagentx/api/client.Client.Memory instead.
func (c *Client) Memory() *memory.Service {
	return c.memoryService
}

// Retrieval returns the retrieval API.
//
// DEPRECATED: Use goagentx/api/client.Client.Retrieval instead.
func (c *Client) Retrieval() *retrieval.Service {
	return c.retrievalService
}

// Close closes the client and cleans up resources.
// Args:
// ctx - operation context.
// Returns error if cleanup fails.
func (c *Client) Close(ctx context.Context) error {
	if c.pool != nil {
		return c.pool.Close()
	}
	return nil
}
