// Package sqlitestore implements KnowledgeStore for SQLite.
package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/knowledge"
	_ "modernc.org/sqlite"
)

var (
	// ErrObjectNotFound is returned when a Get call finds no matching object.
	ErrObjectNotFound = fmt.Errorf("object not found")
)

// Store is a SQLite-backed KnowledgeStore.
type Store struct {
	db *sql.DB
}

// New creates a new SQLite KnowledgeStore with the given database path.
func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", dbPath, err)
	}
	db.SetMaxOpenConns(1) // SQLite only supports single-writer
	db.SetMaxIdleConns(1)

	s := &Store{db: db}
	if err := s.initTables(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init tables: %w", err)
	}
	return s, nil
}

// NewWithDB creates a new SQLite KnowledgeStore with an existing db connection.
func NewWithDB(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("db is nil")
	}
	s := &Store{db: db}
	if err := s.initTables(context.Background()); err != nil {
		return nil, fmt.Errorf("init tables: %w", err)
	}
	return s, nil
}

func (s *Store) initTables(ctx context.Context) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS akf_objects (
			id TEXT PRIMARY KEY,
			type TEXT NOT NULL DEFAULT '',
			namespace TEXT NOT NULL DEFAULT '',
			raw BLOB,
			normalized TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			metadata TEXT DEFAULT '{}',
			tags TEXT DEFAULT '',
			confidence REAL NOT NULL DEFAULT 1.0,
			version INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS akf_representations (
			id TEXT PRIMARY KEY,
			object_id TEXT NOT NULL REFERENCES akf_objects(id) ON DELETE CASCADE,
			model TEXT NOT NULL,
			dimension INTEGER NOT NULL DEFAULT 0,
			vector BLOB,
			metadata TEXT DEFAULT '{}',
			created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_akf_objects_type ON akf_objects(type)`,
		`CREATE INDEX IF NOT EXISTS idx_akf_objects_namespace ON akf_objects(namespace)`,
		`CREATE INDEX IF NOT EXISTS idx_akf_repr_obj_model ON akf_representations(object_id, model)`,
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
		tags := strings.Join(obj.Tags, ",")
		now := time.Now().UTC().Format(time.RFC3339)

		_, err := s.db.ExecContext(ctx, `
			INSERT INTO akf_objects (id, type, namespace, raw, normalized, summary, metadata, tags, confidence, version, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET
				type = excluded.type,
				namespace = excluded.namespace,
				raw = excluded.raw,
				normalized = excluded.normalized,
				summary = excluded.summary,
				metadata = excluded.metadata,
				tags = excluded.tags,
				confidence = excluded.confidence,
				version = akf_objects.version + 1,
				updated_at = excluded.updated_at
		`, obj.ID, string(obj.Type), obj.Namespace, obj.Raw, obj.Normalized, obj.Summary,
			string(metaJSON), tags, obj.Confidence, obj.Version,
			obj.CreatedAt.UTC().Format(time.RFC3339), now)
		if err != nil {
			return fmt.Errorf("save %q: %w", obj.ID, err)
		}
	}
	return nil
}

func (s *Store) Get(ctx context.Context, id string) (*knowledge.KnowledgeObject, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, type, namespace, raw, normalized, summary, metadata, tags, confidence, version, created_at, updated_at
		FROM akf_objects WHERE id = ?`, id)

	var obj knowledge.KnowledgeObject
	var typeStr, ns, norm, summary string
	var raw []byte
	var metaJSON, tagsStr, createdAtStr, updatedAtStr string

	err := row.Scan(&obj.ID, &typeStr, &ns, &raw, &norm, &summary, &metaJSON, &tagsStr,
		&obj.Confidence, &obj.Version, &createdAtStr, &updatedAtStr)
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
	if tagsStr != "" {
		obj.Tags = strings.Split(tagsStr, ",")
	}
	obj.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
	obj.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAtStr)
	_ = json.Unmarshal([]byte(metaJSON), &obj.Metadata)

	return &obj, nil
}

func (s *Store) Query(ctx context.Context, q knowledge.Query) ([]*knowledge.KnowledgeObject, error) {
	var conditions []string
	var args []interface{}

	if q.Namespace != "" {
		conditions = append(conditions, "namespace = ?")
		args = append(args, q.Namespace)
	}
	if len(q.Types) > 0 {
		placeholders := make([]string, len(q.Types))
		for i, t := range q.Types {
			placeholders[i] = "?"
			args = append(args, string(t))
		}
		conditions = append(conditions, fmt.Sprintf("type IN (%s)", strings.Join(placeholders, ",")))
	}
	if len(q.Tags) > 0 {
		tagConditions := make([]string, len(q.Tags))
		for i, tag := range q.Tags {
			tagConditions[i] = "tags LIKE ?"
			args = append(args, "%"+tag+"%")
		}
		conditions = append(conditions, "("+strings.Join(tagConditions, " OR ")+")")
	}

	query := "SELECT id, type, namespace, raw, normalized, summary, metadata, tags, confidence, version, created_at, updated_at FROM akf_objects"
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ") //nolint:gosec // conditions use ? placeholders, values are parameterized
	}
	query += " ORDER BY created_at DESC"

	if q.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, q.Limit)
	}
	if q.Offset > 0 {
		query += " OFFSET ?"
		args = append(args, q.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []*knowledge.KnowledgeObject
	for rows.Next() {
		obj, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, obj)
	}

	return results, rows.Err()
}

func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM akf_objects WHERE id = ?", id)
	return err
}

func (s *Store) Search(ctx context.Context, text string, _ string, limit int) ([]*knowledge.KnowledgeObject, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, type, namespace, raw, normalized, summary, metadata, tags, confidence, version, created_at, updated_at
		FROM akf_objects
		WHERE normalized LIKE ? OR summary LIKE ?
		ORDER BY created_at DESC
		LIMIT ?`, "%"+text+"%", "%"+text+"%", limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []*knowledge.KnowledgeObject
	for rows.Next() {
		obj, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, obj)
	}

	return results, rows.Err()
}

func (s *Store) SaveRepresentation(ctx context.Context, rep *knowledge.Representation) error {
	if rep.ID == "" {
		return fmt.Errorf("representation ID cannot be empty")
	}
	metaJSON, _ := json.Marshal(rep.Metadata)
	now := time.Now().UTC().Format(time.RFC3339)

	// Serialize vector as JSON array.
	vecJSON, _ := json.Marshal(rep.Vector)

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO akf_representations (id, object_id, model, dimension, vector, metadata, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			model = excluded.model,
			dimension = excluded.dimension,
			vector = excluded.vector,
			metadata = excluded.metadata
	`, rep.ID, rep.ObjectID, rep.Model, rep.Dimension, string(vecJSON), string(metaJSON), now)
	return err
}

func (s *Store) GetRepresentation(ctx context.Context, objectID string, model string) (*knowledge.Representation, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, object_id, model, dimension, vector, metadata, created_at
		FROM akf_representations WHERE object_id = ? AND model = ?`, objectID, model)

	var rep knowledge.Representation
	var metaJSON, vecJSON, createdAtStr string

	err := row.Scan(&rep.ID, &rep.ObjectID, &rep.Model, &rep.Dimension, &vecJSON, &metaJSON, &createdAtStr)
	if err == sql.ErrNoRows {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, err
	}

	_ = json.Unmarshal([]byte(vecJSON), &rep.Vector)
	_ = json.Unmarshal([]byte(metaJSON), &rep.Metadata)
	rep.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
	return &rep, nil
}

// scanObject scans a row into a KnowledgeObject.
type scanner interface {
	Scan(dest ...interface{}) error
}

func scanObject(row scanner) (*knowledge.KnowledgeObject, error) {
	var obj knowledge.KnowledgeObject
	var typeStr, ns, norm, summary string
	var raw []byte
	var metaJSON, tagsStr, createdAtStr, updatedAtStr string

	if err := row.Scan(&obj.ID, &typeStr, &ns, &raw, &norm, &summary, &metaJSON, &tagsStr,
		&obj.Confidence, &obj.Version, &createdAtStr, &updatedAtStr); err != nil {
		return nil, err
	}

	obj.Type = knowledge.ObjectType(typeStr)
	obj.Namespace = ns
	obj.Normalized = norm
	obj.Summary = summary
	obj.Raw = raw
	if tagsStr != "" {
		obj.Tags = strings.Split(tagsStr, ",")
	}
	obj.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
	obj.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAtStr)
	_ = json.Unmarshal([]byte(metaJSON), &obj.Metadata)

	return &obj, nil
}
