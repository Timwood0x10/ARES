// Package mysql implements a GraphProvider for external MySQL databases.
// It reads table rows via SQL queries and converts them to KnowledgeObjects.
//
// Usage:
//
//	db, _ := sql.Open("mysql", "user:pass@tcp(host:3306)/dbname")
//	provider, _ := mysql.NewMySQLProvider(db, cfg, mapping)
package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/provider"
)

// MySQLProvider connects to an external MySQL database and streams table rows
// as KnowledgeObjects. The caller is responsible for opening the *sql.DB with
// the appropriate MySQL driver.
type MySQLProvider struct {
	config  provider.ProviderConfig
	db      *sql.DB
	mapping provider.ColumnMapping
}

// NewMySQLProvider creates a MySQL provider. The db parameter must be a
// *sql.DB connected to a MySQL database (the caller is responsible for
// registering and opening the driver).
func NewMySQLProvider(db *sql.DB, cfg provider.ProviderConfig, mapping provider.ColumnMapping) (*MySQLProvider, error) {
	if cfg.Name == "" {
		return nil, fmt.Errorf("provider name is required")
	}
	if cfg.Table == "" {
		return nil, fmt.Errorf("table name is required")
	}
	if mapping.IDColumn == "" {
		return nil, fmt.Errorf("id_column mapping is required")
	}
	if mapping.SummaryColumn == "" {
		return nil, fmt.Errorf("summary_column mapping is required")
	}

	return &MySQLProvider{
		config:  cfg,
		db:      db,
		mapping: mapping,
	}, nil
}

// Name returns the provider identifier.
func (p *MySQLProvider) Name() string { return p.config.Name }

// IntentMatch returns a relevance score based on type/goal overlap.
func (p *MySQLProvider) IntentMatch(intent knowledge.Intent) float64 {
	if len(p.config.IntentTags) == 0 || len(intent.Scope.Types) == 0 {
		return 0.5
	}
	typeMap := make(map[string]bool, len(intent.Scope.Types))
	for _, t := range intent.Scope.Types {
		typeMap[strings.ToLower(string(t))] = true
	}
	matches := 0
	for _, tag := range p.config.IntentTags {
		if typeMap[strings.ToLower(tag)] {
			matches++
		}
	}
	if matches == 0 {
		return 0.1
	}
	return float64(matches) / float64(len(p.config.IntentTags))
}

// Stream delivers rows from the configured table as KnowledgeObjects.
func (p *MySQLProvider) Stream(ctx context.Context, _ knowledge.Intent) (<-chan *knowledge.KnowledgeObject, <-chan error) {
	objCh := make(chan *knowledge.KnowledgeObject, 64)
	errCh := make(chan error, 1)

	go func() {
		defer close(objCh)
		defer close(errCh)

		query := p.buildQuery()
		rows, err := p.db.QueryContext(ctx, query)
		if err != nil {
			errCh <- fmt.Errorf("mysql query %q: %w", query, err)
			return
		}
		defer func() {
			if err := rows.Close(); err != nil {
				fmt.Printf("mysql rows close error: %v\n", err)
			}
		}()

		for rows.Next() {
			if ctx.Err() != nil {
				return
			}

			obj, err := p.scanRow(rows)
			if err != nil {
				errCh <- fmt.Errorf("mysql scan row: %w", err)
				continue
			}
			if obj != nil {
				select {
				case objCh <- obj:
				case <-ctx.Done():
					return
				}
			}
		}

		if err := rows.Err(); err != nil {
			errCh <- fmt.Errorf("mysql rows iteration: %w", err)
		}
	}()

	return objCh, errCh
}

// Close closes the underlying database connection.
func (p *MySQLProvider) Close() error {
	return p.db.Close()
}

// buildQuery constructs the SELECT query from the column mapping and config.
func (p *MySQLProvider) buildQuery() string {
	cols := []string{
		p.mapping.IDColumn,
		p.mapping.SummaryColumn,
	}
	if p.mapping.ContentColumn != "" {
		cols = append(cols, p.mapping.ContentColumn)
	}
	if p.mapping.TagColumn != "" {
		cols = append(cols, p.mapping.TagColumn)
	}
	if p.mapping.TimeColumn != "" {
		cols = append(cols, p.mapping.TimeColumn)
	}

	return fmt.Sprintf("SELECT %s FROM %s", strings.Join(cols, ", "), p.config.Table)
}

// scanRow scans a database row into a KnowledgeObject.
func (p *MySQLProvider) scanRow(scanner interface {
	Scan(dest ...interface{}) error
}) (*knowledge.KnowledgeObject, error) {
	var id, summary string
	var content sql.NullString
	var tag sql.NullString
	var t sql.NullTime

	args := []interface{}{&id, &summary}
	if p.mapping.ContentColumn != "" {
		args = append(args, &content)
	}
	if p.mapping.TagColumn != "" {
		args = append(args, &tag)
	}
	if p.mapping.TimeColumn != "" {
		args = append(args, &t)
	}

	if err := scanner.Scan(args...); err != nil {
		return nil, err
	}

	obj := &knowledge.KnowledgeObject{
		ID:         id,
		Summary:    summary,
		Namespace:  p.config.Namespace,
		Confidence: 1.0,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	if content.Valid {
		obj.Raw = []byte(content.String)
	}
	if tag.Valid {
		obj.Tags = strings.Split(tag.String, ",")
		for i := range obj.Tags {
			obj.Tags[i] = strings.TrimSpace(obj.Tags[i])
		}
	}
	if t.Valid {
		obj.CreatedAt = t.Time
		obj.UpdatedAt = t.Time
	}

	return obj, nil
}
