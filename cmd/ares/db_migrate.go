package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/storage/postgres"
	"github.com/spf13/cobra"
	_ "github.com/jackc/pgx/v5/stdlib"
)

var dbMigrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Run full database migration",
	Long: `Creates the database if it doesn't exist and runs all migrations.
Reads DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME env vars.
Default: postgres://postgres:postgres@localhost:5432/goagent?sslmode=disable`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDbMigrate()
	},
}

func init() {
	dbCmd.AddCommand(dbMigrateCmd)
}

func runDbMigrate() error {
	host := getEnv("DB_HOST", "localhost")
	port := getEnv("DB_PORT", "5433")
	user := getEnv("DB_USER", "postgres")
	password := getEnv("DB_PASSWORD", "postgres")
	dbname := getEnv("DB_NAME", "goagent")

	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
		url.QueryEscape(user), url.QueryEscape(password),
		host, port, dbname)

	parsed, _ := url.Parse(dsn)
	dbname = strings.TrimPrefix(parsed.Path, "/")
	portStr := parsed.Port()

	adminDB := connectAdmin(changeDB(dsn, "postgres"))
	defer func() {
		_ = adminDB.Close()
	}()

	ensureDatabase(adminDB, dbname)
	_ = adminDB.Close()

	cfg := &postgres.Config{
		Host:            parsed.Hostname(),
		Port:            parsePort(portStr, 5432),
		User:            parsed.User.Username(),
		Password:        passwordFromURL(parsed),
		Database:        dbname,
		MaxOpenConns:    25,
		MaxIdleConns:    10,
		ConnMaxLifetime: 0,
		ConnMaxIdleTime: 0,
		QueryTimeout:    30 * time.Second,
	}

	pool, err := postgres.NewPool(cfg)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer func() {
		_ = pool.Close()
	}()

	ctx := context.Background()

	if _, err := pool.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS vector"); err != nil {
		return fmt.Errorf("enable pgvector: %w", err)
	}
	fmt.Println("pgvector extension enabled")

	if err := postgres.MigrateStorage(ctx, pool); err != nil {
		return fmt.Errorf("migration: %w", err)
	}
	fmt.Println("Production database migrations completed successfully")
	fmt.Println()
	fmt.Println("Tables created:")
	fmt.Println("  - knowledge_chunks_1024")
	fmt.Println("  - experiences_1024")
	fmt.Println("  - tools")
	fmt.Println("  - conversations")
	fmt.Println("  - task_results_1024")
	fmt.Println("  - secrets")
	fmt.Println("  - embedding_queue")
	fmt.Println("  - embedding_dead_letter")
	fmt.Println("  - distilled_memories")

	return nil
}

func connectAdmin(dsn string) *sql.DB {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to connect to postgres: %v\n", err)
		os.Exit(1)
	}
	if err := db.Ping(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to ping postgres: %v\n", err)
		os.Exit(1)
	}
	return db
}

func ensureDatabase(db *sql.DB, name string) {
	var exists bool
	if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists); err != nil {
		fmt.Fprintf(os.Stderr, "failed to check database existence: %v\n", err)
		os.Exit(1)
	}
	if !exists {
		if _, err := db.Exec(fmt.Sprintf("CREATE DATABASE %s", pqQuoteIdent(name))); err != nil {
			fmt.Fprintf(os.Stderr, "failed to create database: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Created database: %s\n", name)
	}
}

func getEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

func changeDB(dsn, dbname string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	u.Path = "/" + dbname
	return u.String()
}

func parsePort(port string, defaultPort int) int {
	if port == "" {
		return defaultPort
	}
	var p int
	if _, err := fmt.Sscanf(port, "%d", &p); err != nil || p <= 0 {
		return defaultPort
	}
	return p
}

func passwordFromURL(u *url.URL) string {
	if pw, ok := u.User.Password(); ok {
		return pw
	}
	return ""
}

func pqQuoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
