// agent — opt-in pprof/expvar exposure (Phase 3 observability,
// plan/stability_performance_plan.md). Config-gated (server.pprof_addr in
// ares.yaml), loopback-enforced, off by default.
package main

import (
	"context"
	"errors"
	_ "expvar" // registers /debug/vars on http.DefaultServeMux
	"fmt"
	"net"
	"net/http"
	_ "net/http/pprof" // registers /debug/pprof/* on http.DefaultServeMux
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Timwood0x10/ares/internal/ares_config"
)

// validatePprofAddr enforces the loopback-only contract for the pprof
// listener: the pprof endpoints expose heap profiles and goroutine dumps —
// an information-disclosure surface that must never bind a wildcard or a
// routable address by accident (the same fail-closed posture as serve's
// wildcard-bind refusal).
func validatePprofAddr(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("ARES_PPROF_ADDR must be host:port, got %q: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("ARES_PPROF_ADDR must be a loopback address (got %q): pprof exposes memory and goroutine dumps", addr)
}

// startPprofServer serves net/http/pprof and expvar on the address named by
// server.pprof_addr in ares.yaml (e.g. "127.0.0.1:6060"). Unset = off (the
// default); a non-loopback address is a startup error. The listener is
// managed by the serve errgroup and shuts down with the process.
func startPprofServer(ctx context.Context, g *errgroup.Group, cfg *ares_config.Config) error {
	addr := cfg.Server.PprofAddr
	if addr == "" {
		return nil
	}
	if err := validatePprofAddr(addr); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("pprof listen %s: %w", addr, err)
	}
	srv := &http.Server{Handler: http.DefaultServeMux}
	g.Go(func() error {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("pprof shutdown: %w", err)
		}
		return nil
	})
	g.Go(func() error {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("pprof server: %w", err)
		}
		return nil
	})
	fmt.Printf("pprof/expvar listening on %s (loopback)\n", ln.Addr())
	return nil
}
