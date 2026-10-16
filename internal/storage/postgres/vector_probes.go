package postgres

import (
	"context"
	"database/sql"

	"github.com/Timwood0x10/ares/internal/errors"
)

// txBeginner is the transaction-opening shape a *Pool has.
type txBeginner interface {
	Begin(ctx context.Context) (*sql.Tx, error)
}

// txBeginnerStd is the transaction-opening shape a *sql.DB has.
type txBeginnerStd interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// VectorSearchTuning is the per-query index tuning a search applies.
//
// Both knobs live here because they are one decision seen through two index methods:
// ivfflat reads probes, HNSW reads ef_search, and each IGNORES the other's value. A
// deployment that switches method must set the matching one or silently keep the
// other's tuning.
type VectorSearchTuning struct {
	// Probes is the ivfflat probe count (<= 0 leaves the server default).
	Probes int
	// EFSearch is the hnsw ef_search value (<= 0 leaves the server default).
	EFSearch int
}

// statements renders the transaction-local settings this tuning needs, in a stable
// order; empty when nothing is configured.
func (t VectorSearchTuning) statements() []string {
	out := make([]string, 0, 2)
	if stmt := searchProbesStatement(t.Probes); stmt != "" {
		out = append(out, stmt)
	}
	if stmt := searchEFSearchStatement(t.EFSearch); stmt != "" {
		out = append(out, stmt)
	}
	return out
}

// WithVectorSearchTuning runs fn with the tuning applied for its duration.
//
// It exists because SET LOCAL only applies INSIDE a transaction block: on a plain
// pooled handle each statement runs in its own implicit transaction, so setting a knob
// there is a silent no-op that leaves recall exactly where it was — the "wired but
// inert" shape this defect keeps producing. So this helper opens the transaction the
// settings need and hands fn a handle bound to it.
//
// Handles it cannot cover are REPORTED, not swallowed: a configured recall knob that
// does nothing is worse than an absent one, because the deployment believes it is
// tuned. *sql.Tx, *Pool and *sql.DB are all supported, which is every shape the search
// paths use.
//
// An empty tuning runs fn unchanged, with no transaction opened: an unconfigured
// deployment must not pay for a feature it did not ask for.
func WithVectorSearchTuning(ctx context.Context, db DBTX, tuning VectorSearchTuning, fn func(ctx context.Context, db DBTX) error) error {
	stmts := tuning.statements()
	if len(stmts) == 0 {
		return fn(ctx, db)
	}

	switch handle := db.(type) {
	case *sql.Tx:
		// Already transactional: the settings are scoped to this transaction and
		// disappear with it.
		if err := applyTuningStatements(ctx, handle, stmts); err != nil {
			return err
		}
		return fn(ctx, handle)
	case txBeginner:
		return runInTunedTransaction(ctx, stmts, handle.Begin, fn)
	case txBeginnerStd:
		begin := func(ctx context.Context) (*sql.Tx, error) { return handle.BeginTx(ctx, nil) }
		return runInTunedTransaction(ctx, stmts, begin, fn)
	default:
		log.WarnContext(ctx, "vector search tuning configured but the handle cannot carry transaction-local settings; recall is NOT tuned",
			"settings", stmts)
		return fn(ctx, db)
	}
}

// applyTuningStatements sets every tuning knob inside the given transaction.
func applyTuningStatements(ctx context.Context, tx *sql.Tx, stmts []string) error {
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return errors.Wrapf(err, "apply vector search tuning %q", stmt)
		}
	}
	return nil
}

// runInTunedTransaction opens a transaction, applies the tuning in it, runs fn against
// the transaction, and commits — rolling back on any failure so a failed search cannot
// leave the connection inside an open transaction.
func runInTunedTransaction(ctx context.Context, stmts []string, begin func(context.Context) (*sql.Tx, error), fn func(ctx context.Context, db DBTX) error) error {
	tx, err := begin(ctx)
	if err != nil {
		return errors.Wrap(err, "begin vector search transaction")
	}
	committed := false
	defer func() {
		if !committed {
			// Best-effort: the search error is the one worth reporting, and a
			// rollback failure on a dying connection adds nothing.
			_ = tx.Rollback()
		}
	}()
	if err := applyTuningStatements(ctx, tx, stmts); err != nil {
		return err
	}
	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.Wrap(err, "commit vector search transaction")
	}
	committed = true
	return nil
}
