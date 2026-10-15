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

// WithVectorSearchProbes runs fn with ivfflat.probes applied for its duration.
//
// It exists because SET LOCAL only applies INSIDE a transaction block: on a plain
// pooled handle each statement runs in its own implicit transaction, so setting the
// probe count there is a silent no-op that leaves recall exactly where it was — the
// "wired but inert" shape this defect keeps producing. So this helper opens the
// transaction the setting needs and hands fn a handle bound to it.
//
// Handles it cannot cover are REPORTED, not swallowed: a configured recall knob that
// does nothing is worse than an absent one, because the deployment believes it is
// tuned. Callers pass the handle they were given; *sql.Tx, *Pool and *sql.DB are all
// supported, which is every shape the two search paths use.
//
// probes <= 0 means "leave the server default" and runs fn unchanged, with no
// transaction opened: an unconfigured deployment must not pay for a feature it did
// not ask for.
func WithVectorSearchProbes(ctx context.Context, db DBTX, probes int, fn func(ctx context.Context, db DBTX) error) error {
	if probes <= 0 {
		return fn(ctx, db)
	}
	stmt := searchProbesStatement(probes)

	switch handle := db.(type) {
	case *sql.Tx:
		// Already transactional: the setting is scoped to this transaction and
		// disappears with it.
		if _, err := handle.ExecContext(ctx, stmt); err != nil {
			return errors.Wrap(err, "set ivfflat probes")
		}
		return fn(ctx, handle)
	case txBeginner:
		return runInProbedTransaction(ctx, stmt, handle.Begin, fn)
	case txBeginnerStd:
		begin := func(ctx context.Context) (*sql.Tx, error) { return handle.BeginTx(ctx, nil) }
		return runInProbedTransaction(ctx, stmt, begin, fn)
	default:
		log.WarnContext(ctx, "vector search probes configured but the handle cannot carry a transaction-local setting; recall is NOT tuned",
			"probes", probes)
		return fn(ctx, db)
	}
}

// runInProbedTransaction opens a transaction, applies stmt in it, runs fn against
// the transaction, and commits — rolling back on any failure so a failed search
// cannot leave the connection inside an open transaction.
func runInProbedTransaction(ctx context.Context, stmt string, begin func(context.Context) (*sql.Tx, error), fn func(ctx context.Context, db DBTX) error) error {
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
	if _, err := tx.ExecContext(ctx, stmt); err != nil {
		return errors.Wrap(err, "set ivfflat probes")
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
