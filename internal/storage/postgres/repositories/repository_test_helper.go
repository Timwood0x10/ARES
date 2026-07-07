//go:build integration
// +build integration

// Package repositories provides test helper functions for repository tests.
package repositories

import (
	"database/sql"
	"fmt"
	golog "log"
	"testing"

	"github.com/Timwood0x10/ares/internal/errors"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// getTestDB returns a test database connection.
// This function connects to local Docker PostgreSQL container and creates required tables.
// Returns nil if database is not available (tests will be skipped).
func getTestDB(t *testing.T) *sql.DB {
	host := "localhost"
	port := "5433"
	user := "postgres"
	password := "postgres"
	dbname := "styleagent"

	// #nosec G101 - This is a test file with well-known test credentials
	connStr := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)

	db, err := sql.Open("pgx", connStr)
	if err != nil {
		t.Skipf("Skipping test - failed to open test database: %v", err)
		return nil
	}

	// Test connection
	if err := db.Ping(); err != nil {
		if err := db.Close(); err != nil {
			t.Errorf("Error closing test database: %v", err)
		}
		t.Skipf("Skipping test - failed to connect to test database: %v", err)
		return nil
	}

	golog.Printf("Connected to test database: %s", dbname)

	// Create required tables if they don't exist
	if err := createTestTables(t, db); err != nil {
		if err := db.Close(); err != nil {
			t.Errorf("Error closing test database: %v", err)
		}
		t.Skipf("Skipping test - failed to create test tables: %v", err)
		return nil
	}

	return db
}

// createTestTables creates required tables for testing.
func createTestTables(t *testing.T, db *sql.DB) error {
	// Enable pgvector extension if not already enabled
	if _, err := db.Exec("CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		return errors.Wrap(err, "failed to create pgvector extension")
	}

	// Create knowledge_chunks_1024 table
	knowledgeTableSQL := `
		CREATE TABLE IF NOT EXISTS knowledge_chunks_1024 (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id TEXT NOT NULL,
			content TEXT NOT NULL,
			embedding VECTOR(1024),
			embedding_model TEXT NOT NULL DEFAULT 'intfloat/e5-large',
			embedding_version INT NOT NULL DEFAULT 1,
			embedding_status TEXT DEFAULT 'completed',
			embedding_queued_at TIMESTAMP,
			embedding_processed_at TIMESTAMP,
			embedding_error TEXT,
			tsv TSVECTOR,
			source_type VARCHAR(50),
			source TEXT,
			metadata JSONB DEFAULT '{}'::jsonb,
			document_id UUID,
			chunk_index INTEGER,
			content_hash TEXT UNIQUE,
			access_count INTEGER DEFAULT 0,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW()
		)`

	if _, err := db.Exec(knowledgeTableSQL); err != nil {
		return errors.Wrap(err, "failed to create knowledge_chunks_1024 table")
	}

	// Create experiences_1024 table
	experiencesTableSQL := `
		CREATE TABLE IF NOT EXISTS experiences_1024 (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id TEXT NOT NULL,
			type VARCHAR(50) NOT NULL CHECK (type IN ('query', 'solution', 'failure', 'pattern', 'distilled')),
			input TEXT,
			output TEXT,
			embedding VECTOR(1024) NOT NULL,
			embedding_model TEXT NOT NULL DEFAULT 'intfloat/e5-large',
			embedding_version INT NOT NULL DEFAULT 1,
			score FLOAT DEFAULT 0.5 CHECK (score >= 0 AND score <= 1),
			success BOOLEAN DEFAULT true,
			agent_id VARCHAR(255),
			metadata JSONB DEFAULT '{}'::jsonb,
			decay_at TIMESTAMP DEFAULT NOW() + INTERVAL '30 days',
			created_at TIMESTAMP DEFAULT NOW()
		)`

	if _, err := db.Exec(experiencesTableSQL); err != nil {
		return errors.Wrap(err, "failed to create experiences_1024 table")
	}

	// Create tools table
	toolsTableSQL := `
		DROP TABLE IF EXISTS tools;
		CREATE TABLE tools (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id TEXT NOT NULL,
			name VARCHAR(255) NOT NULL,
			description TEXT,
			embedding VECTOR(1024) NOT NULL,
			embedding_model TEXT NOT NULL DEFAULT 'intfloat/e5-large',
			embedding_version INT NOT NULL DEFAULT 1,
			agent_type VARCHAR(50),
			tags TEXT[] DEFAULT ARRAY[]::TEXT[],
			usage_count INTEGER DEFAULT 0,
			success_rate FLOAT DEFAULT 0.0,
			last_used_at TIMESTAMP,
			metadata JSONB DEFAULT '{}'::jsonb,
			created_at TIMESTAMP DEFAULT NOW(),
			UNIQUE (tenant_id, name)
		)`

	if _, err := db.Exec(toolsTableSQL); err != nil {
		return errors.Wrap(err, "failed to create tools table")
	}

	// Create conversations table
	conversationsTableSQL := `
		CREATE TABLE IF NOT EXISTS conversations (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			session_id VARCHAR(64) NOT NULL,
			tenant_id TEXT NOT NULL,
			user_id VARCHAR(64),
			agent_id VARCHAR(64),
			role VARCHAR(32) NOT NULL,
			content TEXT NOT NULL,
			expires_at TIMESTAMP,
			created_at TIMESTAMP DEFAULT NOW()
		)`

	if _, err := db.Exec(conversationsTableSQL); err != nil {
		return errors.Wrap(err, "failed to create conversations table")
	}

	// Create task_results_1024 table
	taskResultsTableSQL := `
		CREATE TABLE IF NOT EXISTS task_results_1024 (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id TEXT NOT NULL,
			session_id VARCHAR(64) NOT NULL,
			task_type VARCHAR(64),
			agent_id VARCHAR(64),
			input JSONB NOT NULL,
			output JSONB,
			embedding VECTOR(1024),
			embedding_model TEXT NOT NULL DEFAULT 'intfloat/e5-large',
			embedding_version INT NOT NULL DEFAULT 1,
			status VARCHAR(32) NOT NULL DEFAULT 'pending',
			error TEXT,
			latency_ms INTEGER,
			metadata JSONB DEFAULT '{}'::jsonb,
			created_at TIMESTAMP DEFAULT NOW()
		)`

	if _, err := db.Exec(taskResultsTableSQL); err != nil {
		return errors.Wrap(err, "failed to create task_results_1024 table")
	}

	// Create secrets table
	// #nosec G101 - Test file with SQL table definition
	secretsTableSQL := `
		DROP TABLE IF EXISTS secrets;
		CREATE TABLE secrets (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id TEXT NOT NULL,
			key VARCHAR(255) NOT NULL,
			value BYTEA NOT NULL,
			key_version INTEGER NOT NULL DEFAULT 1,
			algorithm VARCHAR(32) NOT NULL DEFAULT 'aes-gcm',
			expires_at TIMESTAMP,
			metadata JSONB DEFAULT '{}'::jsonb,
			created_at TIMESTAMP DEFAULT NOW(),
			updated_at TIMESTAMP DEFAULT NOW(),
			UNIQUE (tenant_id, key)
		)`

	if _, err := db.Exec(secretsTableSQL); err != nil {
		return errors.Wrap(err, "failed to create secrets table")
	}

	return nil
}

// cleanupTestDB cleans up test data from database.
func cleanupTestDB(t *testing.T, db *sql.DB) {
	tables := []string{
		"knowledge_chunks_1024",
		"experiences_1024",
		"tools",
		"conversations",
		"task_results_1024",
		"secrets",
	}

	for _, table := range tables {
		_, err := db.Exec(fmt.Sprintf("DELETE FROM %s", table))
		if err != nil {
			t.Logf("Warning: Failed to clean up table %s: %v", table, err)
		}
	}
}

// closeTestDB closes the test database connection.
func closeTestDB(t *testing.T, db *sql.DB) {
	if err := db.Close(); err != nil {
		t.Logf("Warning: Failed to close test database: %v", err)
	}
}
