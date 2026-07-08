// Package postgresstore implements KnowledgeStore for PostgreSQL.
package postgresstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

var (
	// ErrObjectNotFound is returned when a Get call finds no matching object.
	ErrObjectNotFound = fmt.Errorf("object not found")
)

// Store is a PostgreSQL-backed KnowledgeStore.
type Store struct {
	db *sql.DB
}

// New creates a new PostgreSQL KnowledgeStore with the given connection.
func New(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("db is nil")
	}
	s := &Store{db: db}
	if err := s.initTables(context.Background()); err != nil {
		return nil, fmt.Errorf("init tables: %w", err)
	}
	return s, nil
}

// initTables creates the required tables if they don't exist.
func (s *Store) initTables(ctx context.Context) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS akf_objects (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL DEFAULT '',
			namespace TEXT NOT NULL DEFAULT '',
			raw BYTEA,
			normalized TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			metadata JSONB DEFAULT '{}',
			tags TEXT[] DEFAULT '{}',
			confidence REAL NOT NULL DEFAULT 1.0,
			version BIGINT NOT NULL DEFAULT 1,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS akf_representations (
			id TEXT PRIMARY KEY,
			object_id TEXT NOT NULL REFERENCES akf_objects(id) ON DELETE CASCADE,
			model TEXT NOT NULL,
			dimension INT NOT NULL DEFAULT 0,
			vector REAL[],
			metadata JSONB DEFAULT '{}',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_akf_objects_type ON akf_objects(type)`,
		`CREATE INDEX IF NOT EXISTS idx_akf_objects_namespace ON akf_objects(namespace)`,
		`CREATE INDEX IF NOT EXISTS idx_akf_representations_object_model ON akf_representations(object_id, model)`,
	}

	for _, q := range queries {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Save(ctx context.Context, objects ...*knowledge.KnowledgeObject) error {
	for _, obj := range objects {
		if obj.ID == "" {
			return fmt.Errorf("knowledge object ID cannot be empty")
		}

		metaJSON, _ := json.Marshal(obj.Metadata)
		tags := obj.Tags
		if tags == nil {
			tags = []string{}
		}

		_, err := s.db.ExecContext(ctx, `
			INSERT INTO akf_objects (id, type, namespace, raw, normalized, summary, metadata, tags, confidence, version, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			ON CONFLICT (id) DO UPDATE SET
				type = EXCLUDED.type,
				namespace = EXCLUDED.namespace,
				raw = EXCLUDED.raw,
				normalized = EXCLUDED.normalized,
				summary = EXCLUDED.summary,
				metadata = EXCLUDED.metadata,
				tags = EXCLUDED.tags,
				confidence = EXCLUDED.confidence,
				version = akf_objects.version + 1,
				updated_at = NOW()
		`, obj.ID, string(obj.Type), obj.Namespace, obj.Raw, obj.Normalized, obj.Summary,
			string(metaJSON), pqStringArray(tags), obj.Confidence, obj.Version,
			obj.CreatedAt, obj.UpdatedAt)
		if err != nil {
			return fmt.Errorf("save %q: %w", obj.ID, err)
		}
	}
	return nil
}

func (s *Store) Get(ctx context.Context, id string) (*knowledge.KnowledgeObject, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, type, namespace, raw, normalized, summary, metadata, tags, confidence, version, created_at, updated_at
		FROM akf_objects WHERE id = $1`, id)

	var obj knowledge.KnowledgeObject
	var typeStr, ns, norm, summary string
	var raw []byte
	var metaJSON string
	var tags []string
	var createdAt, updatedAt time.Time

	err := row.Scan(&obj.ID, &typeStr, &ns, &raw, &norm, &summary, &metaJSON, (*pqStringArray)(&tags),
		&obj.Confidence, &obj.Version, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, err
	}

	obj.Type = knowledge.ObjectType(typeStr)
	obj.Namespace = ns
	obj.Normalized = norm
	obj.Summary = summary
	obj.Raw = raw
	obj.Tags = tags
	obj.CreatedAt = createdAt
	obj.UpdatedAt = updatedAt
	_ = json.Unmarshal([]byte(metaJSON), &obj.Metadata)

	return &obj, nil
}

func (s *Store) Query(ctx context.Context, q knowledge.Query) ([]*knowledge.KnowledgeObject, error) {
	var conditions []string
	var args []interface{}
	argIdx := 1

	if q.Namespace != "" {
		conditions = append(conditions, fmt.Sprintf("namespace = $%d", argIdx))
		args = append(args, q.Namespace)
		argIdx++
	}
	if len(q.Types) > 0 {
		typeStrs := make([]string, len(q.Types))
		for i, t := range q.Types {
			typeStrs[i] = string(t)
		}
		conditions = append(conditions, fmt.Sprintf("type = ANY($%d)", argIdx))
		args = append(args, pqStringArray(typeStrs))
		argIdx++
	}
	if len(q.Tags) > 0 {
		conditions = append(conditions, fmt.Sprintf("tags && $%d", argIdx))
		args = append(args, pqStringArray(q.Tags))
		argIdx++
	}

	query := "SELECT id, type, namespace, raw, normalized, summary, metadata, tags, confidence, version, created_at, updated_at FROM akf_objects"
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY created_at DESC"

	if q.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, q.Limit)
		argIdx++
	}
	if q.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argIdx) //nolint:gosec // value is parameterized via $N
		args = append(args, q.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []*knowledge.KnowledgeObject
	for rows.Next() {
		var obj knowledge.KnowledgeObject
		var typeStr, ns, norm, summary string
		var raw []byte
		var metaJSON string
		var tags []string
		var createdAt, updatedAt time.Time

		if err := rows.Scan(&obj.ID, &typeStr, &ns, &raw, &norm, &summary, &metaJSON, (*pqStringArray)(&tags),
			&obj.Confidence, &obj.Version, &createdAt, &updatedAt); err != nil {
			return nil, err
		}

		obj.Type = knowledge.ObjectType(typeStr)
		obj.Namespace = ns
		obj.Normalized = norm
		obj.Summary = summary
		obj.Raw = raw
		obj.Tags = tags
		obj.CreatedAt = createdAt
		obj.UpdatedAt = updatedAt
		_ = json.Unmarshal([]byte(metaJSON), &obj.Metadata)

		results = append(results, &obj)
	}

	return results, rows.Err()
}

func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM akf_objects WHERE id = $1", id)
	return err
}

func (s *Store) Search(ctx context.Context, text string, _ string, limit int) ([]*knowledge.KnowledgeObject, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, type, namespace, raw, normalized, summary, metadata, tags, confidence, version, created_at, updated_at
		FROM akf_objects
		WHERE normalized ILIKE $1 OR summary ILIKE $1
		ORDER BY created_at DESC
		LIMIT $2`, "%"+text+"%", limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []*knowledge.KnowledgeObject
	for rows.Next() {
		var obj knowledge.KnowledgeObject
		var typeStr, ns, norm, summary string
		var raw []byte
		var metaJSON string
		var tags []string
		var createdAt, updatedAt time.Time

		if err := rows.Scan(&obj.ID, &typeStr, &ns, &raw, &norm, &summary, &metaJSON, (*pqStringArray)(&tags),
			&obj.Confidence, &obj.Version, &createdAt, &updatedAt); err != nil {
			return nil, err
		}

		obj.Type = knowledge.ObjectType(typeStr)
		obj.Namespace = ns
		obj.Normalized = norm
		obj.Summary = summary
		obj.Raw = raw
		obj.Tags = tags
		obj.CreatedAt = createdAt
		obj.UpdatedAt = updatedAt
		_ = json.Unmarshal([]byte(metaJSON), &obj.Metadata)

		results = append(results, &obj)
	}

	return results, rows.Err()
}

func (s *Store) SaveRepresentation(ctx context.Context, rep *knowledge.Representation) error {
	if rep.ID == "" {
		return fmt.Errorf("representation ID cannot be empty")
	}
	metaJSON, _ := json.Marshal(rep.Metadata)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO akf_representations (id, object_id, model, dimension, vector, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (id) DO UPDATE SET
			model = EXCLUDED.model,
			dimension = EXCLUDED.dimension,
			vector = EXCLUDED.vector,
			metadata = EXCLUDED.metadata
	`, rep.ID, rep.ObjectID, rep.Model, rep.Dimension, pqFloat32Array(rep.Vector), string(metaJSON), rep.CreatedAt)
	return err
}

func (s *Store) GetRepresentation(ctx context.Context, objectID string, model string) (*knowledge.Representation, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, object_id, model, dimension, vector, metadata, created_at
		FROM akf_representations WHERE object_id = $1 AND model = $2`, objectID, model)

	var rep knowledge.Representation
	var metaJSON string
	var vec []float32

	err := row.Scan(&rep.ID, &rep.ObjectID, &rep.Model, &rep.Dimension, (*pqFloat32Array)(&vec), &metaJSON, &rep.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, err
	}

	rep.Vector = vec
	_ = json.Unmarshal([]byte(metaJSON), &rep.Metadata)
	return &rep, nil
}

// ── PQ-compatible array types ───────────────────────────────────────────────

// pqStringArray implements database/sql's Scanner interface for PostgreSQL text arrays.
type pqStringArray []string

func (a *pqStringArray) Scan(src interface{}) error {
	if src == nil {
		*a = []string{}
		return nil
	}

	var raw string
	switch v := src.(type) {
	case []byte:
		raw = string(v)
	case string:
		raw = v
	default:
		*a = []string{}
		return nil
	}

	// Parse PostgreSQL array format: {elem1,elem2,...} with possible quoted "elem,ents"
	raw = strings.Trim(raw, "{}")
	if raw == "" {
		*a = []string{}
		return nil
	}

	var result []string
	var current strings.Builder
	inQuotes := false
	for _, r := range raw {
		switch {
		case r == '"':
			inQuotes = !inQuotes
		case r == ',' && !inQuotes:
			result = append(result, strings.TrimSpace(current.String()))
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		result = append(result, strings.TrimSpace(current.String()))
	}
	*a = result
	return nil
}

// pqFloat32Array implements database/sql's Scanner interface for PostgreSQL real arrays.
type pqFloat32Array []float32

func (a *pqFloat32Array) Scan(src interface{}) error {
	if src == nil {
		*a = []float32{}
		return nil
	}

	var raw string
	switch v := src.(type) {
	case []byte:
		raw = string(v)
	case string:
		raw = v
	default:
		*a = []float32{}
		return nil
	}

	raw = strings.Trim(raw, "{}")
	if raw == "" {
		*a = []float32{}
		return nil
	}

	parts := strings.Split(raw, ",")
	result := make([]float32, len(parts))
	for i, p := range parts {
		var val float64
		if _, err := fmt.Sscanf(p, "%f", &val); err == nil {
			result[i] = float32(val)
		}
	}
	*a = result
	return nil
}
