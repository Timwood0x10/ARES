// Package ares_bootstrap — periodic storage maintenance.
//
// Closes the "write-only retention" open loop (REVIEW_PROGRESS #7): tables
// with expires_at / decay_at columns are purged on a schedule instead of
// growing unboundedly while read paths silently filter dead rows.
package ares_bootstrap

import (
	"context"
	"time"

	"github.com/Timwood0x10/ares/internal/logger"
)

var logMaintenance = logger.Module("ares_bootstrap.maintenance")

// ExpiryCleaner is implemented by repositories that own rows with a
// retention window (expires_at / decay_at). Consumer-side interface: it is
// defined here so repositories do not need to know about the bootstrap
// maintenance worker.
type ExpiryCleaner interface {
	// CleanupExpired deletes expired/decayed rows and reports how many were
	// removed. Implementations must be idempotent and safe to call repeatedly.
	CleanupExpired(ctx context.Context) (int64, error)
}

// NamedExpiryCleaner pairs an ExpiryCleaner with the table family it owns, so
// maintenance logs identify what was purged without parsing SQL.
type NamedExpiryCleaner struct {
	// Name identifies the table family (e.g. "experiences_1024").
	Name string
	// Cleaner performs the purge.
	Cleaner ExpiryCleaner
}

// expiryCleanupInterval is how often the maintenance worker purges expired
// rows. One hour keeps dead-row volume negligible relative to write rates
// without adding meaningful database load; the first run happens one full
// interval after startup so a booting system never pays cleanup latency.
const expiryCleanupInterval = time.Hour

// startExpiryCleanupWorker launches a background ticker on comp.bgGroup that
// periodically invokes every registered cleaner. Best-effort by design: one
// cleaner failing or panicking never cancels the loop nor blocks the others,
// and no cleaners being wired is a no-op. The goroutine exits when ctx is
// cancelled during graceful shutdown.
func startExpiryCleanupWorker(ctx context.Context, comp *Components) {
	if comp == nil || len(comp.ExpiryCleaners) == 0 {
		return
	}
	cleaners := make([]NamedExpiryCleaner, len(comp.ExpiryCleaners))
	copy(cleaners, comp.ExpiryCleaners)
	comp.bgGroup.Go(func() error {
		ticker := time.NewTicker(expiryCleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				runExpiredCleanup(ctx, cleaners)
			}
		}
	})
	logMaintenance.InfoContext(ctx, "bootstrap: expiry cleanup worker started",
		"cleaners", len(cleaners),
		"interval", expiryCleanupInterval.String())
}

// runExpiredCleanup executes one purge pass over all cleaners. Panics are
// recovered per cleaner: maintenance must not take the process down.
func runExpiredCleanup(ctx context.Context, cleaners []NamedExpiryCleaner) {
	for _, nc := range cleaners {
		nc := nc
		func() {
			defer func() {
				if r := recover(); r != nil {
					logMaintenance.ErrorContext(ctx, "bootstrap: expiry cleanup panicked",
						"table", nc.Name, "panic", r)
				}
			}()
			deleted, err := nc.Cleaner.CleanupExpired(ctx)
			if err != nil {
				logMaintenance.WarnContext(ctx, "bootstrap: expiry cleanup failed",
					"table", nc.Name, "error", err)
				return
			}
			if deleted > 0 {
				logMaintenance.InfoContext(ctx, "bootstrap: expiry cleanup pass",
					"table", nc.Name, "deleted", deleted)
			}
		}()
	}
}
