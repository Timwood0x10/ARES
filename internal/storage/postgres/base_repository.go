package postgres

import (
	"context"
	"database/sql"
	"fmt"

	coreerrors "goagentx/internal/core/errors"
	"goagentx/internal/errors"
)

// Scannable is satisfied by *sql.Row and *sql.Rows.
type Scannable interface {
	Scan(dest ...interface{}) error
}

// GetByID retrieves a single entity by its primary key.
func GetByID[T any](ctx context.Context, db DBTX, table, id string, scan func(Scannable) (*T, error)) (*T, error) {
	query := fmt.Sprintf("SELECT * FROM %s WHERE id = $1", table)
	row := db.QueryRowContext(ctx, query, id)
	entity, err := scan(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, errors.Wrap(err, "get by id")
	}
	return entity, nil
}

// DeleteByID deletes a single entity by its primary key.
// Returns ErrRecordNotFound if no row was deleted.
func DeleteByID(ctx context.Context, db DBTX, table, id string) error {
	query := fmt.Sprintf("DELETE FROM %s WHERE id = $1", table)
	result, err := db.ExecContext(ctx, query, id)
	if err != nil {
		return errors.Wrap(err, "delete by id")
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "get rows affected")
	}
	if rows == 0 {
		return coreerrors.ErrRecordNotFound
	}
	return nil
}

// CountByTenant counts entities for a given tenant.
func CountByTenant(ctx context.Context, db DBTX, table, tenantID string) (int64, error) {
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE tenant_id = $1", table)
	var count int64
	err := db.QueryRowContext(ctx, query, tenantID).Scan(&count)
	if err != nil {
		return 0, errors.Wrap(err, "count by tenant")
	}
	return count, nil
}
