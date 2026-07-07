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

// NewPGProvider creates a PostgreSQL provider with the given DSN and config.
func NewPGProvider(dsn string, cfg provider.ProviderConfig, mapping provider.ColumnMapping) (*PGProvider, error) {
	if cfg.Name == "" {
		return nil, fmt.Errorf("provider name is required")
	}
	if dsn == "" {
		return nil, fmt.Errorf("DSN is required")
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

		query := p.buildQuery(intent)
		rows, err := p.db.QueryContext(ctx, query)
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

func (p *PGProvider) buildQuery(intent knowledge.Intent) string {
	maxResults := intent.Scope.MaxObjects
	if maxResults <= 0 {
		maxResults = 100
	}

	columns := []string{p.mapping.IDColumn, p.mapping.SummaryColumn}
	if p.mapping.ContentColumn != "" {
		columns = append(columns, p.mapping.ContentColumn)
	}
	if p.mapping.TagColumn != "" {
		columns = append(columns, p.mapping.TagColumn)
	}
	if p.mapping.TimeColumn != "" {
		columns = append(columns, p.mapping.TimeColumn)
	}

	return fmt.Sprintf(
		"SELECT %s FROM %s ORDER BY %s DESC NULLS LAST LIMIT %d",
		strings.Join(columns, ", "),
		p.config.Mapping.IDColumn,
		p.mapping.TimeColumn,
		maxResults,
	)
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
