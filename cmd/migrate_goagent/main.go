// Package main provides database setup for goagent knowledge base.
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
		QueryTimeout:    30 * time.Second,
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

	fmt.Println("Connected to goagent database successfully")

	// Run storage migrations
	if err := postgres.MigrateStorage(ctx, pool); err != nil {
		slog.Error("Failed to run storage migrations", "error", err)
	}
	fmt.Println("Storage migrations completed successfully!")
	fmt.Println("")
	fmt.Println("Tables created:")
	fmt.Println("  - knowledge_chunks_1024")
	fmt.Println("  - experiences_1024")
	fmt.Println("  - tools")
	fmt.Println("  - conversations")
	fmt.Println("  - task_results_1024")
	fmt.Println("  - secrets")
	fmt.Println("  - embedding_queue")
	fmt.Println("  - embedding_dead_letter")
}
