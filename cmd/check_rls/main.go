package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Timwood0x10/ares/internal/storage/postgres"
)

func main() {
	// Create database configuration
	dbConfig := &postgres.Config{
		Host:            "127.0.0.1",
		Port:            5433,
		User:            "postgres",
		Password:        "postgres",
		Database:        "ARES",
		MaxOpenConns:    25,
		MaxIdleConns:    10,
		ConnMaxLifetime: 5 * time.Minute,
		QueryTimeout:    30 * time.Second,
		Embedding:       postgres.DefaultEmbeddingConfig(),
	}

	// Create database connection pool
	pool, err := postgres.NewPool(dbConfig)
	if err != nil {
		log.Fatalf("Failed to create database pool: %v", err)
	}
	defer func() {
		if err := pool.Close(); err != nil {
			lg.Error("Failed to close database pool", "error", err)
		}
	}()

	ctx := context.Background()

	// Check RLS policies
	fmt.Println("=== RLS Policies for distilled_memories ===")
	rows, err := pool.Query(ctx, `
		SELECT schemaname, tablename, policyname, permissive, roles, cmd, qual, with_check
		FROM pg_policies
		WHERE tablename = 'distilled_memories'
	`)
	if err != nil {
		log.Printf("Failed to query RLS policies: %v", err)
		return
	}
	defer func() {
		if err := rows.Close(); err != nil {
			lg.Error("Failed to close rows", "error", err)
		}
	}()

	hasPolicies := false
	for rows.Next() {
		hasPolicies = true
		var schema, table, policyName, roles, cmd, qual, withCheck string
		var permissive bool
		if err := rows.Scan(&schema, &table, &policyName, &permissive, &roles, &cmd, &qual, &withCheck); err != nil {
			log.Printf("Failed to scan policy: %v", err)
			continue
		}
		fmt.Printf("\nPolicy: %s\n", policyName)
		fmt.Printf("  Schema: %s\n", schema)
		fmt.Printf("  Table: %s\n", table)
		fmt.Printf("  Permissive: %v\n", permissive)
		fmt.Printf("  Roles: %s\n", roles)
		fmt.Printf("  Command: %s\n", cmd)
		fmt.Printf("  Qual: %s\n", qual)
		fmt.Printf("  With Check: %s\n", withCheck)
	}

	if !hasPolicies {
		fmt.Println("No RLS policies found")
	}

	// Check table structure
	fmt.Println("\n=== Table Structure ===")
	rows2, err := pool.Query(ctx, `
		SELECT column_name, data_type, is_nullable, column_default
		FROM information_schema.columns
		WHERE table_name = 'distilled_memories'
		ORDER BY ordinal_position
	`)
	if err != nil {
		log.Printf("Failed to query table structure: %v", err)
		return
	}
	defer func() {
		if err := rows2.Close(); err != nil {
			lg.Error("Failed to close rows", "error", err)
		}
	}()

	for rows2.Next() {
		var colName, dataType, nullable, defaultVal string
		if err := rows2.Scan(&colName, &dataType, &nullable, &defaultVal); err != nil {
			log.Printf("Failed to scan column: %v", err)
			continue
		}
		fmt.Printf("  %-20s %-20s %-8s %s\n", colName, dataType, nullable, defaultVal)
	}
}
