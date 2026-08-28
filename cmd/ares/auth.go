// Auth commands — ares auth token
package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_security"
)

// defaultTokenTTL is the fallback lifetime for `ares auth token` when neither
// --ttl nor security.jwt_expiry is configured.
const defaultTokenTTL = "24h"

func init() {
	authCmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage ARES authentication (JWT tokens)",
	}

	var (
		tokenRole       string
		tokenSubject    string
		tokenTTL        string
		tokenConfigPath string
	)
	tokenCmd := &cobra.Command{
		Use:   "token",
		Short: "Issue a signed JWT for protected HTTP endpoints",
		Long: `Issue a signed JWT (HS256) for protected HTTP endpoints (agent kill/resume/
retry, MCP tool calls, chaos actions). The token is signed with the configured
JWT secret — the same one the serve process validates. Configure the secret in
security.jwt_secret in ares.yaml or via the ARES_JWT_SECRET environment
variable, and enable enforcement with security.auth_enabled: true (or
ARES_AUTH_ENABLED=1).

Roles: admin (full control), operator (write, no destructive chaos), agent
(read-only).

The token lifetime resolves in this order: --ttl flag, then the
security.jwt_expiry config, then the built-in default of ` + defaultTokenTTL + `.

Example:
  ARES_JWT_SECRET=changeme ares auth token --role operator --sub "deploy-user"`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := ares_config.Load(tokenConfigPath)
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			secret := tokenEnvSecret(cfg)
			if secret == "" {
				return errors.New("no JWT secret configured: set security.jwt_secret or ARES_JWT_SECRET")
			}
			// Lifetime precedence: explicit --ttl flag > security.jwt_expiry
			// config > defaultTokenTTL. An empty flag leaves the config (and
			// the default) in charge.
			ttlStr := tokenTTL
			if ttlStr == "" {
				ttlStr = cfg.Security.JWTExpiry
			}
			if ttlStr == "" {
				ttlStr = defaultTokenTTL
			}
			ttl, err := time.ParseDuration(ttlStr)
			if err != nil {
				return fmt.Errorf("parse ttl %q: %w", ttlStr, err)
			}
			if _, err := ares_security.ParseRole(tokenRole); err != nil {
				return err
			}
			tok, err := ares_security.SignJWT([]byte(secret), tokenSubject, tokenRole, ttl, time.Now())
			if err != nil {
				return err
			}
			fmt.Println(tok)
			return nil
		},
	}
	tokenCmd.Flags().StringVar(&tokenConfigPath, "config", "", "Path to ares.yaml (uses ARES_JWT_SECRET otherwise)")
	tokenCmd.Flags().StringVar(&tokenRole, "role", "operator", "Role: admin, operator, or agent")
	tokenCmd.Flags().StringVar(&tokenSubject, "sub", "cli-user", "Token subject")
	tokenCmd.Flags().StringVar(&tokenTTL, "ttl", "", "Token lifetime (e.g. 24h, 1h30m); defaults to security.jwt_expiry or "+defaultTokenTTL)
	authCmd.AddCommand(tokenCmd)

	rootCmd.AddCommand(authCmd)
}

// tokenEnvSecret exposes the JWT secret resolution used by both the CLI and
// serve wiring: the environment variable wins, then the config file. It is a
// small helper kept beside the command so tests can exercise the precedence
// without spawning a subprocess.
func tokenEnvSecret(cfg *ares_config.Config) string {
	if v := os.Getenv("ARES_JWT_SECRET"); v != "" {
		return v
	}
	return cfg.Security.JWTSecret
}
