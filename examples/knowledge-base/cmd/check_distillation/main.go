package main

import (
	"database/sql"
	"fmt"
	"log"
	"log/slog"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	// connect to PostgreSQL database
	connStr := "host=localhost port=5433 user=postgres password=postgres dbname=goagent sslmode=disable"
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		slog.Error("Failed to connect to database:", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("Failed to close database connection: %v", err)
		}
	}()

	// test database connection
	if err := db.Ping(); err != nil {
		slog.Error("Failed to ping database:", "error", err)
		os.Exit(1)
	}

	slog.Info("✅ Connected to database successfully\n")

	// query distilled memory
	fmt.Println("=== Distilled Memory ===")
	rows, err := db.Query(`
		SELECT id, content, source_type, source, created_at
		FROM knowledge_chunks_1024
		WHERE source_type = 'distilled' 
		ORDER BY created_at DESC
		LIMIT 10
	`)
	if err != nil {
		slog.Error("Failed to query distilled memory:", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			slog.Error("Failed to close rows: ", "error", err)
		}
	}()

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
	statsRows, err := db.Query(`
		SELECT source_type, COUNT(*) as count
		FROM knowledge_chunks_1024
		GROUP BY source_type
		ORDER BY count DESC
	`)
	if err != nil {
		slog.Error("Failed to query statistics:", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := statsRows.Close(); err != nil {
			slog.Error("Failed to close stats rows", "error", err)
		}
	}()

	for statsRows.Next() {
		var sourceType string
		var count int
		if err := statsRows.Scan(&sourceType, &count); err != nil {
			continue
		}
		fmt.Printf("  %s: %d records\n", sourceType, count)
	}

	fmt.Println("\n✅ Check completed")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
