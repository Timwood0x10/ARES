package postgres

import (
	"fmt"
	"net/url"
	"time"

	"github.com/Timwood0x10/ares/internal/errors"
)

// DefaultSSLMode is the safe default SSL mode for new connections.
// Production deployments should always use SSL.
const DefaultSSLMode = "require"

// Config represents the database configuration.
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
	// SSLMode controls the PostgreSQL sslmode connection parameter.
	// Defaults to "require" via Validate(); use "disable" only for local dev.
	SSLMode         string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
	QueryTimeout    time.Duration
	Embedding       *EmbeddingConfig
}

// EmbeddingConfig represents embedding-related configuration.
type EmbeddingConfig struct {
	DefaultModel         string
	DefaultVersion       int
	MaxRetries           int
	MaxBatchSize         int
	MaxVectorSearchLimit int
	ReconcileBatchSize   int
	EmbeddingTimeout     time.Duration

	// IndexMethod selects the pgvector index type: VectorIndexIVFFlat (default)
	// or VectorIndexHNSW. Switching an EXISTING deployment's method needs the
	// index rebuilt (see vector_index.go); this setting decides what the
	// builders create.
	IndexMethod string
	// SearchProbes is the ivfflat probe count applied per search (0 = leave the
	// server default of 1). It is the recall lever that needs no rebuild.
	SearchProbes int
	// SearchEFSearch is the HNSW ef_search applied per search (0 = leave the server
	// default of 40). It is the HNSW half of the same lever, applied through the same
	// transaction as SearchProbes.
	//
	// Both knobs are carried together because each method IGNORES the other's: setting
	// probes on an HNSW index (or ef_search on an ivfflat one) does nothing, so a
	// deployment that switches method must set the matching one to keep the recall it
	// measured.
	SearchEFSearch int
}

// DefaultEmbeddingConfig returns the default embedding configuration.
func DefaultEmbeddingConfig() *EmbeddingConfig {
	return &EmbeddingConfig{
		DefaultModel:         "intfloat/e5-large",
		DefaultVersion:       1,
		MaxRetries:           3,
		MaxBatchSize:         32,
		MaxVectorSearchLimit: 1000,
		ReconcileBatchSize:   1000,
		EmbeddingTimeout:     30 * time.Second,
	}
}

// Validate validates the embedding configuration.
func (e *EmbeddingConfig) Validate() error {
	if e.DefaultModel == "" {
		e.DefaultModel = "intfloat/e5-large"
	}
	if e.DefaultVersion <= 0 {
		e.DefaultVersion = 1
	}
	if e.MaxRetries <= 0 {
		e.MaxRetries = 3
	}
	if e.MaxRetries > 10 {
		return fmt.Errorf("max retries too large: %d (max 10)", e.MaxRetries)
	}
	if e.MaxBatchSize <= 0 {
		e.MaxBatchSize = 32
	}
	if e.MaxBatchSize > 1000 {
		return fmt.Errorf("max batch size too large: %d (max 1000)", e.MaxBatchSize)
	}
	if e.MaxVectorSearchLimit <= 0 {
		e.MaxVectorSearchLimit = 1000
	}
	if e.MaxVectorSearchLimit > 10000 {
		return fmt.Errorf("max vector search limit too large: %d (max 10000)", e.MaxVectorSearchLimit)
	}
	if e.ReconcileBatchSize <= 0 {
		e.ReconcileBatchSize = 1000
	}
	if e.ReconcileBatchSize > 10000 {
		return fmt.Errorf("reconcile batch size too large: %d (max 10000)", e.ReconcileBatchSize)
	}
	if e.IndexMethod == "" {
		e.IndexMethod = VectorIndexIVFFlat
	}
	if err := validateVectorIndexMethod(e.IndexMethod); err != nil {
		return err
	}
	if e.SearchProbes < 0 {
		// A negative probe count is meaningless rather than dangerous (the
		// statement builder ignores non-positive values), but silently ignoring a
		// configured value is how a recall knob ends up doing nothing.
		return fmt.Errorf("search probes must not be negative: %d", e.SearchProbes)
	}
	if e.SearchEFSearch < 0 {
		// Same reasoning as the probe count: this is the HNSW half of one decision.
		return fmt.Errorf("search ef_search must not be negative: %d", e.SearchEFSearch)
	}
	if e.EmbeddingTimeout <= 0 {
		e.EmbeddingTimeout = 30 * time.Second
	}
	return nil
}

// DefaultConfig returns the default database configuration.
// Password is intentionally empty: callers must provide credentials explicitly
// (e.g. via environment variables or config files) rather than relying on a
// hardcoded default that could leak into production.
func DefaultConfig() *Config {
	return &Config{
		Host:            "localhost",
		Port:            5432,
		User:            "postgres",
		Password:        "",
		Database:        "ARES",
		SSLMode:         DefaultSSLMode,
		MaxOpenConns:    25,
		MaxIdleConns:    10,
		ConnMaxLifetime: 5 * time.Minute,
		ConnMaxIdleTime: 1 * time.Minute,
		QueryTimeout:    30 * time.Second,
		Embedding:       DefaultEmbeddingConfig(),
	}
}

// DSN returns the connection string in PostgreSQL URI format.
// URI format with URL encoding handles all special characters safely.
func (c *Config) DSN() string {
	sslMode := c.SSLMode
	if sslMode == "" {
		sslMode = DefaultSSLMode
	}
	return fmt.Sprintf("postgresql://%s:%s@%s:%d/%s?sslmode=%s&client_encoding=UTF8",
		url.QueryEscape(c.User),
		url.QueryEscape(c.Password),
		url.QueryEscape(c.Host),
		c.Port,
		c.Database,
		sslMode,
	)
}

// Validate validates the configuration.
func (c *Config) Validate() error {
	if c.Host == "" {
		c.Host = "localhost"
	}
	if c.Port <= 0 || c.Port > 65535 {
		c.Port = 5432
	}
	if c.SSLMode == "" {
		c.SSLMode = DefaultSSLMode
	}
	if c.MaxOpenConns <= 0 {
		c.MaxOpenConns = 25
	}
	if c.MaxIdleConns <= 0 {
		c.MaxIdleConns = 10
	}
	if c.ConnMaxLifetime <= 0 {
		c.ConnMaxLifetime = 5 * time.Minute
	}
	if c.ConnMaxIdleTime <= 0 {
		c.ConnMaxIdleTime = 1 * time.Minute
	}
	if c.QueryTimeout <= 0 {
		c.QueryTimeout = 30 * time.Second
	}
	if c.Embedding == nil {
		c.Embedding = DefaultEmbeddingConfig()
	}
	if err := c.Embedding.Validate(); err != nil {
		return errors.Wrap(err, "invalid embedding config")
	}
	return nil
}
