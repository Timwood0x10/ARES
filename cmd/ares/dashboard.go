// ares dashboard — open the runtime introspection panel.
//
// The panel (internal/introspect) is served by a running `ares serve` at
// /introspect. This command resolves that address (--addr, else the config's
// server address, else http://localhost:8080), verifies the panel is
// reachable, and opens it in the default browser. With --url it only prints
// the address (useful for headless / remote hosts), and with --wait it polls
// until the panel comes up (handy right after launching serve).
package main

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	dashboardAddr       string // --addr: panel address (overrides config)
	dashboardConfigPath string // --config: config file to resolve the address from
	dashboardURLOnly    bool   // --url: print the URL, do not open a browser
	dashboardWait       bool   // --wait: poll until the panel is reachable
)

func init() {
	dashboardCmd := &cobra.Command{
		Use:     "dashboard",
		Aliases: []string{"panel", "introspect", "ui"},
		Short:   "Open the runtime introspection panel in a browser",
		Long: "Open the ARES runtime introspection panel served by `ares serve`.\n\n" +
			"The panel shows the live kernel scheduler, task-fabric leases/quanta,\n" +
			"agent lifecycle, and an activity feed (who died, who took work,\n" +
			"recoveries). It is read-only.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDashboard(cmd.Context())
		},
	}
	dashboardCmd.Flags().StringVar(&dashboardAddr, "addr", "", "Panel base address (default: config server or http://localhost:8080)")
	dashboardCmd.Flags().StringVarP(&dashboardConfigPath, "config", "c", "", "Config file to resolve the address from (default: auto-detect)")
	dashboardCmd.Flags().BoolVar(&dashboardURLOnly, "url", false, "Print the panel URL instead of opening a browser")
	dashboardCmd.Flags().BoolVar(&dashboardWait, "wait", false, "Poll until the panel is reachable before opening")
	rootCmd.AddCommand(dashboardCmd)
}

func runDashboard(ctx context.Context) error {
	// Resolve the base address: --addr wins, else the config's server address,
	// else the default fallback (reuses the status command's resolver so the
	// two agree on where serve listens).
	base := dashboardAddr
	if base == "" {
		cfg, _ := inspectStatusConfig(dashboardConfigPath)
		base = statusConfigServerAddr(cfg)
	}
	base = strings.TrimRight(base, "/")
	panelURL := base + "/introspect"

	if dashboardURLOnly {
		fmt.Println(panelURL)
		return nil
	}

	// Verify (optionally wait for) the panel before opening a browser, so the
	// user gets a clear "not running" message instead of a blank tab.
	if err := waitForPanel(ctx, base, dashboardWait); err != nil {
		return fmt.Errorf("dashboard: %w (start it with 'ares serve')", err)
	}

	if err := openBrowser(panelURL); err != nil {
		// Best-effort: on headless hosts there is no browser. Fall back to
		// printing the URL rather than failing the command.
		fmt.Printf("open the panel manually: %s\n(%v)\n", panelURL, err)
		return nil
	}
	fmt.Printf("opening runtime introspection panel: %s\n", panelURL)
	return nil
}

// waitForPanel probes base/api/v1/introspect/snapshot. When wait is false it
// probes once; when true it polls (2s interval) up to ~30s. A 200 or 503
// (collector warming up) both count as "panel is up".
func waitForPanel(ctx context.Context, base string, wait bool) error {
	probe := func() error {
		reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, base+"/api/v1/introspect/snapshot", nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusServiceUnavailable {
			return nil
		}
		return fmt.Errorf("panel returned HTTP %d", resp.StatusCode)
	}

	if !wait {
		if err := probe(); err != nil {
			return fmt.Errorf("panel not reachable at %s: %w", base, err)
		}
		return nil
	}

	deadline := time.Now().Add(30 * time.Second)
	for {
		if err := probe(); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("panel not reachable at %s after 30s", base)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// openBrowser opens url in the platform default browser.
func openBrowser(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default: // linux, *bsd
		cmd = "xdg-open"
		args = []string{url}
	}
	return exec.Command(cmd, args...).Start() // #nosec G204 — args are the fixed opener + our own URL
}
