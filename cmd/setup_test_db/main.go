// Package main provides database setup for testing.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/Timwood0x10/goagent/internal/storage/postgres"
)

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func main() {
	ctx := context.Background()

	// Create database config
	cfg := &postgres.Config{
		Host:            getEnv("DB_HOST", "localhost"),
		Port:            getEnvInt("DB_PORT", 5433),
		User:            getEnv("DB_USER", "postgres"),
		Password:        getEnv("DB_PASSWORD", ""),
		Database:        getEnv("DB_NAME", "goagent"),
		MaxOpenConns:    25,
		MaxIdleConns:    10,
		ConnMaxLifetime: 5 * time.Minute,
		ConnMaxIdleTime: 1 * time.Minute,
		QueryTimeout:    2 * time.Second,
	}

	// Create pool
	pool, err := postgres.NewPool(cfg)
	if err != nil {
		slog.Error("Failed to create pool", "error", err)
	}
	defer func() {
		if err := pool.Close(); err != nil {
			slog.Error("Failed to close pool", "error", err)
		}
	}()

	fmt.Println("Connected to database successfully")

	// Enable pgvector extension
	_, err = pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector")
	if err != nil {
		slog.Error("Failed to create vector extension", "error", err)
	}
	fmt.Println("Enabled pgvector extension")

	// Run migrations
	if err := postgres.MigrateStorage(ctx, pool); err != nil {
		slog.Error("Failed to run migrations", "error", err)
	}
	fmt.Println("Migrations completed successfully")
}
