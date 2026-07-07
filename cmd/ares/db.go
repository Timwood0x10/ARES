package main

import "github.com/spf13/cobra"

var dbCmd = &cobra.Command{
	Use:   "db",
	Short: "Database management commands",
	Long: `Manage ARES databases: migrate, setup test databases,
create specific tables, and inspect RLS policies.`,
}

func init() {
	rootCmd.AddCommand(dbCmd)
}
