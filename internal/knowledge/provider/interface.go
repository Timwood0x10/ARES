// Package provider defines the GraphProvider interface for pluggable
// external data sources. Every data source (PostgreSQL, MySQL, Git, Memory,
// Code, etc.) can be adapted to AKF by implementing GraphProvider.
package provider

import (
	"context"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// GraphProvider converts external data sources into KnowledgeObject streams.
//
// Stream mode: Instead of loading all objects into memory (which would OOM
// for 10M-order tables), providers emit objects one at a time through a
// channel. The caller (KnowledgeRuntime) can process objects incrementally
// and cancel via context at any point.
type GraphProvider interface {
	// Name returns a unique identifier for this provider instance.
	Name() string

	// IntentMatch returns a match score [0, 1] indicating how relevant this
	// provider is for the given intent. Used by SourceDiscovery to select
	// the best providers for a task. Providers that don't match at all
	// should return 0 to avoid being selected.
	IntentMatch(intent knowledge.Intent) float64

	// Stream delivers KnowledgeObjects one at a time through the channel.
	// The provider must close the channel when done. If ctx is cancelled,
	// the provider should stop producing and return immediately.
	// Errors during streaming are sent through the error channel.
	Stream(ctx context.Context, intent knowledge.Intent) (<-chan *knowledge.KnowledgeObject, <-chan error)
}

// ProviderConfig is a generic configuration for database-backed providers.
// Specific providers (PG, MySQL, etc.) extend this with their own connection params.
type ProviderConfig struct {
	Name       string        `yaml:"name"`
	Namespace  string        `yaml:"namespace"`
	IntentTags []string      `yaml:"intent_tags"`
	Mapping    ColumnMapping `yaml:"mapping"`
	// Table is the source table (or view) name used by SQL-based providers
	// when building SELECT queries. Required for providers that issue SQL
	// against an external database.
	Table string `yaml:"table"`
}

// ColumnMapping defines how a database table maps to KnowledgeObject fields.
// For SQL-based providers, this drives the SELECT query construction.
type ColumnMapping struct {
	IDColumn      string `yaml:"id_column"`
	SummaryColumn string `yaml:"summary_column"`
	ContentColumn string `yaml:"content_column,omitempty"`
	TagColumn     string `yaml:"tag_column,omitempty"`
	TimeColumn    string `yaml:"time_column,omitempty"`
}
