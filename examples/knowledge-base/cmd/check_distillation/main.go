package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	// connect to PostgreSQL database
	connStr := "host=localhost port=5433 user=postgres password=postgres dbname=goagent sslmode=disable"
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		lg.Error("Failed to connect to database:", "error", err)
		os.Exit(1)
	}

	// test database connection
	if err := db.PingContext(context.Background()); err != nil {
		_ = db.Close()
		lg.Error("Failed to ping database:", "error", err)
		os.Exit(1)
	}

	lg.Info("✅ Connected to database successfully\n")

	// query distilled memory
	fmt.Println("=== Distilled Memory ===")
	rows, err := db.QueryContext(context.Background(), `
		SELECT id, content, source_type, source, created_at
		FROM knowledge_chunks_1024
		WHERE source_type = 'distilled'
		ORDER BY created_at DESC
		LIMIT 10
	`)
	if err != nil {
		_ = db.Close()
		_ = rows.Close()
		lg.Error("Failed to query distilled memory:", "error", err)
		os.Exit(1)
	}

	count := 0
	for rows.Next() {
		var id, content, sourceType, source, createdAt string
		if err := rows.Scan(&id, &content, &sourceType, &source, &createdAt); err != nil {
			log.Printf("Failed to scan row: %v", err)
			continue
		}
		count++
		fmt.Printf("\n[%d] ID: %s\n", count, id[:20]+"...")
		fmt.Printf("    Time: %s\n", createdAt)
		fmt.Printf("    Type: %s\n", sourceType)
		fmt.Printf("    Source: %s\n", source)
		fmt.Printf("    Content Preview: %s\n", truncate(content, 100))
	}

	if count == 0 {
		fmt.Println("⚠️  No distilled memory found")
		fmt.Println("   Tip: Need at least 3 rounds of conversation to trigger distillation")
	} else {
		fmt.Printf("\n✅ Found %d distilled memory records\n", count)
	}

	// Content statistics
	fmt.Println("\n=== Content Statistics ===")
	statsRows, err := db.QueryContext(context.Background(), `
		SELECT source_type, COUNT(*) as count
		FROM knowledge_chunks_1024
		GROUP BY source_type
		ORDER BY count DESC
	`)
	if err != nil {
		_ = rows.Close()
		_ = db.Close()
		lg.Error("Failed to query statistics:", "error", err)
		os.Exit(1)
	}

	for statsRows.Next() {
		var sourceType string
		var count int
		if err := statsRows.Scan(&sourceType, &count); err != nil {
			continue
		}
		fmt.Printf("  %s: %d records\n", sourceType, count)
	}
	_ = statsRows.Close()
	_ = rows.Close()
	_ = db.Close()

	fmt.Println("\n✅ Check completed")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
