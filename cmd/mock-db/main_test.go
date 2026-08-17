package main

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestRunCreatesTableAndSampleRow verifies run() builds the mock table and
// inserts the idempotent sample row into a fresh database file.
func TestRunCreatesTableAndSampleRow(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "mock.db")

	if err := run(dbPath, false); err != nil {
		t.Fatalf("run: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer func() { _ = db.Close() }()

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM mock_memories").Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("want 1 sample row, got %d", count)
	}

	var content string
	if err := db.QueryRow("SELECT content FROM mock_memories WHERE id = 'mock-1'").Scan(&content); err != nil {
		t.Fatalf("select sample: %v", err)
	}
	if content != "Hello from the mock database" {
		t.Fatalf("unexpected content %q", content)
	}
}

// TestRunIdempotent verifies run() on an existing database does not duplicate
// the sample row (INSERT OR IGNORE).
func TestRunIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "mock.db")

	if err := run(dbPath, false); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := run(dbPath, false); err != nil {
		t.Fatalf("second run: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer func() { _ = db.Close() }()

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM mock_memories").Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("want still 1 row after rerun, got %d", count)
	}
}

// TestRunResetRebuilds verifies --reset removes an existing database file so
// the table is recreated from scratch.
func TestRunResetRebuilds(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "mock.db")

	if err := run(dbPath, false); err != nil {
		t.Fatalf("run: %v", err)
	}
	if err := run(dbPath, true); err != nil {
		t.Fatalf("run with reset: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer func() { _ = db.Close() }()

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM mock_memories").Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("want 1 row after reset, got %d", count)
	}
}
