// Package postgres implements a GraphProvider for external PostgreSQL databases.
// It reads table rows via SQL queries and converts them to KnowledgeObjects.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/provider"
)

// PGProvider connects to an external PostgreSQL database and streams table rows
// as KnowledgeObjects. Configuration is provided via ProviderConfig.
type PGProvider struct {
	config  provider.ProviderConfig
	db      *sql.DB
	mapping provider.ColumnMapping
}

// validateIdentifier returns an error if name is empty, longer than the
// PostgreSQL identifier limit (63 bytes), or contains characters outside the
// safe set [a-zA-Z0-9_]. Schema-qualified names (containing '.') are rejected
// to prevent bypassing the table allowlist via "evil.public_table" style names.
func validateIdentifier(name string) error {
	if name == "" {
		return fmt.Errorf("identifier cannot be empty")
	}
	if len(name) > 63 {
		return fmt.Errorf("identifier too long: %d bytes (max 63)", len(name))
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !((c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '_') {
			return fmt.Errorf("identifier %q contains illegal character %q", name, c)
		}
	}
	return nil
}

// NewPGProvider creates a PostgreSQL provider with the given DSN and config.
func NewPGProvider(dsn string, cfg provider.ProviderConfig, mapping provider.ColumnMapping) (*PGProvider, error) {
	if cfg.Name == "" {
		return nil, fmt.Errorf("provider name is required")
	}
	if dsn == "" {
		return nil, fmt.Errorf("DSN is required")
	}
	if cfg.Table == "" {
		return nil, fmt.Errorf("provider config Table is required")
	}
	if err := validateIdentifier(cfg.Table); err != nil {
		return nil, fmt.Errorf("invalid table name %q: %w", cfg.Table, err)
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)

	return &PGProvider{
		config:  cfg,
		db:      db,
		mapping: mapping,
	}, nil
}

// Name returns the provider identifier.
func (p *PGProvider) Name() string { return p.config.Name }

// IntentMatch scores based on configured intent tags.
func (p *PGProvider) IntentMatch(intent knowledge.Intent) float64 {
	if len(p.config.IntentTags) == 0 || intent.Goal == "" {
		return 0.3
	}
	goal := strings.ToLower(intent.Goal)
	matches := 0
	for _, tag := range p.config.IntentTags {
		if strings.Contains(goal, strings.ToLower(tag)) {
			matches++
		}
	}
	if matches == 0 {
		return 0.1
	}
	return 0.3 + (float64(matches)/float64(len(p.config.IntentTags)))*0.7
}

// Stream queries the configured table and streams KnowledgeObjects.
func (p *PGProvider) Stream(ctx context.Context, intent knowledge.Intent) (<-chan *knowledge.KnowledgeObject, <-chan error) {
	objCh := make(chan *knowledge.KnowledgeObject, 32)
	errCh := make(chan error, 1)

	go func() {
		defer close(objCh)
		defer close(errCh)

		query, args, err := p.buildQuery(intent)
		if err != nil {
			errCh <- fmt.Errorf("build postgres query: %w", err)
			return
		}
		rows, err := p.db.QueryContext(ctx, query, args...)
		if err != nil {
			errCh <- fmt.Errorf("postgres query: %w", err)
			return
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			obj, err := p.scanRow(rows)
			if err != nil {
				errCh <- fmt.Errorf("scan row: %w", err)
				continue
			}
			if obj == nil {
				continue
			}

			select {
			case objCh <- obj:
			case <-ctx.Done():
				return
			}
		}

		if err := rows.Err(); err != nil {
			errCh <- fmt.Errorf("rows iteration: %w", err)
		}
	}()

	return objCh, errCh
}

// Close closes the database connection.
func (p *PGProvider) Close() error {
	if p.db != nil {
		return p.db.Close()
	}
	return nil
}

func (p *PGProvider) buildQuery(intent knowledge.Intent) (string, []any, error) {
	maxResults := intent.Scope.MaxObjects
	if maxResults <= 0 {
		maxResults = 100
	}

	if p.config.Table == "" {
		return "", nil, fmt.Errorf("postgres provider %s: config.Table is required", p.config.Name)
	}
	if p.mapping.IDColumn == "" || p.mapping.SummaryColumn == "" {
		return "", nil, fmt.Errorf("postgres provider %s: id_column and summary_column are required", p.config.Name)
	}

	// Quote every identifier to prevent SQL injection via configured column
	// or table names. Identifier quoting follows the PostgreSQL rule: wrap
	// in double quotes and double any embedded double quotes.
	columns := []string{quoteIdentifier(p.mapping.IDColumn), quoteIdentifier(p.mapping.SummaryColumn)}
	if p.mapping.ContentColumn != "" {
		columns = append(columns, quoteIdentifier(p.mapping.ContentColumn))
	}
	if p.mapping.TagColumn != "" {
		columns = append(columns, quoteIdentifier(p.mapping.TagColumn))
	}
	if p.mapping.TimeColumn != "" {
		columns = append(columns, quoteIdentifier(p.mapping.TimeColumn))
	}

	table := quoteIdentifier(p.config.Table)
	orderCol := quoteIdentifier(p.mapping.TimeColumn)

	// LIMIT is parameterized as a placeholder to keep the query safe and
	// to let the driver handle type coercion.
	query := fmt.Sprintf(
		"SELECT %s FROM %s ORDER BY %s DESC NULLS LAST LIMIT $1",
		strings.Join(columns, ", "),
		table,
		orderCol,
	)
	return query, []any{maxResults}, nil
}

// quoteIdentifier wraps a PostgreSQL identifier in double quotes and escapes
// any embedded double quotes by doubling them, per the SQL standard. This
// prevents identifier injection when configured column or table names are
// substituted into a query.
func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (p *PGProvider) scanRow(rows *sql.Rows) (*knowledge.KnowledgeObject, error) {
	var id, summary string
	var content, tagCol sql.NullString
	var timeCol sql.NullTime

	args := []any{&id, &summary}
	if p.mapping.ContentColumn != "" {
		args = append(args, &content)
	}
	if p.mapping.TagColumn != "" {
		args = append(args, &tagCol)
	}
	if p.mapping.TimeColumn != "" {
		args = append(args, &timeCol)
	}

	if err := rows.Scan(args...); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}

	obj := &knowledge.KnowledgeObject{
		ID:         fmt.Sprintf("%s:%s", p.config.Namespace, id),
		Type:       knowledge.ObjectDocument,
		Namespace:  p.config.Namespace,
		Summary:    summary,
		Confidence: 0.5,
	}

	if content.Valid {
		obj.Raw = []byte(content.String)
	}
	if timeCol.Valid {
		obj.CreatedAt = timeCol.Time
	}

	return obj, nil
}
