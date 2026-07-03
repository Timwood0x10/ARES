package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Timwood0x10/ares/internal/storage/postgres"
	"github.com/spf13/cobra"
)

// errStdout is used by check-rls for warning messages.
var errStdout = os.Stderr

var dbCheckRLSCmd = &cobra.Command{
	Use:   "check-rls",
	Short: "Check RLS policies on distilled_memories table",
	Long: `Inspects Row-Level Security policies and column structure
of the distilled_memories table.
Env vars: DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME.
Default: postgres://postgres:postgres@localhost:5432/goagent?sslmode=disable`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDbCheckRLS()
	},
}

func init() {
	dbCmd.AddCommand(dbCheckRLSCmd)
}

func runDbCheckRLS() error {
	dbConfig := &postgres.Config{
		Host:            "127.0.0.1",
		Port:            5433,
		User:            "postgres",
		Password:        "postgres",
		Database:        "goagent",
		MaxOpenConns:    25,
		MaxIdleConns:    10,
		ConnMaxLifetime: 5 * time.Minute,
		QueryTimeout:    30 * time.Second,
		Embedding:       postgres.DefaultEmbeddingConfig(),
	}

	pool, err := postgres.NewPool(dbConfig)
	if err != nil {
		return fmt.Errorf("create database pool: %w", err)
	}
	defer func() {
		if err := pool.Close(); err != nil {
			_, _ = fmt.Fprintf(errStdout, "warning: close pool: %v\n", err)
		}
	}()

	ctx := context.Background()

	fmt.Println("=== RLS Policies for distilled_memories ===")
	rows, err := pool.Query(ctx, `
		SELECT schemaname, tablename, policyname, permissive, roles, cmd, qual, with_check
		FROM pg_policies
		WHERE tablename = 'distilled_memories'
	`)
	if err != nil {
		return fmt.Errorf("query RLS policies: %w", err)
	}
	defer func() { _ = rows.Close() }()

	hasPolicies := false
	for rows.Next() {
		hasPolicies = true
		var schema, table, policyName, roles, cmd, qual, withCheck string
		var permissive bool
		if err := rows.Scan(&schema, &table, &policyName, &permissive, &roles, &cmd, &qual, &withCheck); err != nil {
			_, _ = fmt.Fprintf(errStdout, "scan policy: %v\n", err)
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

	fmt.Println("\n=== Table Structure ===")
	rows2, err := pool.Query(ctx, `
		SELECT column_name, data_type, is_nullable, column_default
		FROM information_schema.columns
		WHERE table_name = 'distilled_memories'
		ORDER BY ordinal_position
	`)
	if err != nil {
		return fmt.Errorf("query table structure: %w", err)
	}
	defer func() { _ = rows2.Close() }()

	for rows2.Next() {
		var colName, dataType, nullable, defaultVal string
		if err := rows2.Scan(&colName, &dataType, &nullable, &defaultVal); err != nil {
			_, _ = fmt.Fprintf(errStdout, "scan column: %v\n", err)
			continue
		}
		fmt.Printf("  %-20s %-20s %-8s %s\n", colName, dataType, nullable, defaultVal)
	}

	return nil
}
