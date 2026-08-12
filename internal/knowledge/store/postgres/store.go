package postgresstore

//nolint: errcheck // best-effort operations: ResponseWriter writes, cleanup Close/Wait, deferred shutdown
import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
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
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			status TEXT NOT NULL DEFAULT '',
			quality JSONB DEFAULT '{}',
			relations JSONB DEFAULT '[]',
			embedding_model TEXT NOT NULL DEFAULT ''
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
		`CREATE INDEX IF NOT EXISTS idx_akf_objects_status ON akf_objects(status)`,
		`CREATE INDEX IF NOT EXISTS idx_akf_representations_object_model ON akf_representations(object_id, model)`,
		// Migrate pre-0.2.9 databases by adding the new columns. PostgreSQL
		// supports IF NOT EXISTS on ADD COLUMN, so this is idempotent.
		`ALTER TABLE akf_objects ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE akf_objects ADD COLUMN IF NOT EXISTS quality JSONB DEFAULT '{}'`,
		`ALTER TABLE akf_objects ADD COLUMN IF NOT EXISTS relations JSONB DEFAULT '[]'`,
		`ALTER TABLE akf_objects ADD COLUMN IF NOT EXISTS embedding_model TEXT NOT NULL DEFAULT ''`,
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
		qualityJSON := marshalQuality(obj.Quality)
		relationsJSON := marshalRelations(obj.Relations)

		_, err := s.db.ExecContext(ctx, `
			INSERT INTO akf_objects (id, type, namespace, raw, normalized, summary, metadata, tags, confidence, version, created_at, updated_at, status, quality, relations, embedding_model)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
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
				updated_at = NOW(),
				status = EXCLUDED.status,
				quality = EXCLUDED.quality,
				relations = EXCLUDED.relations,
				embedding_model = EXCLUDED.embedding_model
		`, obj.ID, string(obj.Type), obj.Namespace, obj.Raw, obj.Normalized, obj.Summary,
			string(metaJSON), pqStringArray(tags), obj.Confidence, obj.Version,
			obj.CreatedAt, obj.UpdatedAt,
			string(obj.Status), qualityJSON, relationsJSON, obj.EmbeddingModel)
		if err != nil {
			return fmt.Errorf("save %q: %w", obj.ID, err)
		}
	}
	return nil
}

// marshalQuality returns the JSON encoding of q, or "{}" when q is nil (the
// JSONB column default, keeping the value a valid JSON object).
// Marshal errors are logged as warnings and return "{}" so the caller never sees
// a partial write.
func marshalQuality(q *knowledge.Quality) string {
	if q == nil {
		return "{}"
	}
	b, err := json.Marshal(q)
	if err != nil {
		slog.Warn("marshal quality failed", "error", err)
		return "{}"
	}
	return string(b)
}

// marshalRelations returns the JSON encoding of rels, or "[]" when empty (the
// JSONB column default, keeping the value a valid JSON array).
// Marshal errors are logged as warnings and return "[]" so the caller never sees
// a partial write.
func marshalRelations(rels []knowledge.Relation) string {
	if len(rels) == 0 {
		return "[]"
	}
	b, err := json.Marshal(rels)
	if err != nil {
		slog.Warn("marshal relations failed", "error", err)
		return "[]"
	}
	return string(b)
}

func (s *Store) Get(ctx context.Context, id string) (*knowledge.KnowledgeObject, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, type, namespace, raw, normalized, summary, metadata, tags, confidence, version, created_at, updated_at, status, quality, relations, embedding_model
		FROM akf_objects WHERE id = $1`, id)

	obj, err := scanObject(row)
	if err == sql.ErrNoRows {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, err
	}
	return obj, nil
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

	query := "SELECT id, type, namespace, raw, normalized, summary, metadata, tags, confidence, version, created_at, updated_at, status, quality, relations, embedding_model FROM akf_objects"
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
		obj, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, obj)
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
		SELECT id, type, namespace, raw, normalized, summary, metadata, tags, confidence, version, created_at, updated_at, status, quality, relations, embedding_model
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
		obj, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, obj)
	}

	return results, rows.Err()
}

// scanner is the common interface implemented by *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...interface{}) error
}

// scanObject scans one row (16 columns) into a KnowledgeObject. It unmarshals
// metadata/quality/relations JSON best-effort. Quality/relations JSON that is
// empty or the column default is ignored.
func scanObject(row scanner) (*knowledge.KnowledgeObject, error) {
	var obj knowledge.KnowledgeObject
	var typeStr, ns, norm, summary string
	var raw []byte
	var metaJSON, qualityJSON, relationsJSON string
	var tags []string
	var createdAt, updatedAt time.Time
	var statusStr, embeddingModel string

	if err := row.Scan(&obj.ID, &typeStr, &ns, &raw, &norm, &summary, &metaJSON, (*pqStringArray)(&tags),
		&obj.Confidence, &obj.Version, &createdAt, &updatedAt,
		&statusStr, &qualityJSON, &relationsJSON, &embeddingModel); err != nil {
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
	obj.Status = knowledge.ObjectStatus(statusStr)
	obj.EmbeddingModel = embeddingModel
	if qualityJSON != "" && qualityJSON != "{}" {
		var q knowledge.Quality
		if err := json.Unmarshal([]byte(qualityJSON), &q); err == nil {
			obj.Quality = &q
		}
	}
	if relationsJSON != "" && relationsJSON != "[]" && relationsJSON != "null" {
		_ = json.Unmarshal([]byte(relationsJSON), &obj.Relations)
	}
	_ = json.Unmarshal([]byte(metaJSON), &obj.Metadata)

	return &obj, nil
}

// scanRepresentation scans a representation row.
func scanRepresentation(row scanner) (*knowledge.Representation, error) {
	var rep knowledge.Representation
	var metaJSON string
	var vec []float32

	if err := row.Scan(&rep.ID, &rep.ObjectID, &rep.Model, &rep.Dimension, (*pqFloat32Array)(&vec), &metaJSON, &rep.CreatedAt); err != nil {
		return nil, err
	}
	rep.Vector = vec
	_ = json.Unmarshal([]byte(metaJSON), &rep.Metadata)
	return &rep, nil
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

	rep, err := scanRepresentation(row)
	if err == sql.ErrNoRows {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, err
	}
	return rep, nil
}

// HybridSearch performs vector + lexical scoring over PostgreSQL-stored objects.
func (s *Store) HybridSearch(ctx context.Context, req knowledge.HybridSearchRequest) ([]knowledge.ScoredObject, error) {
	conditions, args := hybridConditions(req)
	//nolint:gosec // conditions are static WHERE fragments; values use $N placeholders.
	query := `SELECT id, type, namespace, raw, normalized, summary, metadata, tags, confidence, version, created_at, updated_at, status, quality, relations, embedding_model
		FROM akf_objects` + conditions
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("hybrid search query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var candidates []*knowledge.KnowledgeObject
	var ids []string
	for rows.Next() {
		obj, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, obj)
		ids = append(ids, obj.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Load representations for the requested model into a map keyed by object ID.
	reps := make(map[string]*knowledge.Representation, len(ids))
	if len(ids) > 0 && req.Model != "" {
		// The query declares exactly two placeholders: $1 (the object ID array)
		// and $2 (the model). The previous code built a slice of N ids + model
		// and overwrote only index 0 with the array, so $2 bound to the second
		// object ID instead of the model, making the model filter match nothing.
		repQuery := `SELECT id, object_id, model, dimension, vector, metadata, created_at
			FROM akf_representations WHERE object_id = ANY($1) AND model = $2`
		repArgs := []interface{}{pqStringArray(ids), req.Model} //nolint:gosec // ids are local object IDs
		repRows, err := s.db.QueryContext(ctx, repQuery, repArgs...)
		if err != nil {
			return nil, fmt.Errorf("hybrid search reps: %w", err)
		}
		for repRows.Next() {
			rep, err := scanRepresentation(repRows)
			if err != nil {
				_ = repRows.Close()
				return nil, err
			}
			reps[rep.ObjectID] = rep
		}
		_ = repRows.Close()
		if err := repRows.Err(); err != nil {
			return nil, err
		}
	}

	scored := knowledge.ScoreHybrid(candidates, reps, req.QueryVector, req.Query)

	// Filter by MinScore.
	if req.MinScore > 0 {
		filtered := scored[:0]
		for _, r := range scored {
			if r.FinalScore >= req.MinScore {
				filtered = append(filtered, r)
			}
		}
		scored = filtered
	}

	// Sort by FinalScore descending.
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].FinalScore > scored[j].FinalScore
	})

	topK := req.TopK
	if topK <= 0 {
		topK = 20
	}
	if len(scored) > topK {
		scored = scored[:topK]
	}
	finalK := req.FinalK
	if finalK <= 0 {
		finalK = 5
	}
	if len(scored) > finalK {
		scored = scored[:finalK]
	}
	return scored, nil
}

// hybridConditions builds the WHERE clause (with parameterized $N
// placeholders) and args for HybridSearch candidates based on namespace, types,
// and status filter. Empty status on a row matches the active filter for
// back-compat.
func hybridConditions(req knowledge.HybridSearchRequest) (string, []interface{}) {
	var conditions []string
	var args []interface{}
	argIdx := 1
	if req.Namespace != "" {
		conditions = append(conditions, fmt.Sprintf("namespace = $%d", argIdx))
		args = append(args, req.Namespace)
		argIdx++
	}
	if len(req.Types) > 0 {
		typeStrs := make([]string, len(req.Types))
		for i, t := range req.Types {
			typeStrs[i] = string(t)
		}
		conditions = append(conditions, fmt.Sprintf("type = ANY($%d)", argIdx))
		args = append(args, pqStringArray(typeStrs))
		argIdx++
	}
	statuses := req.StatusFilter
	if len(statuses) == 0 {
		statuses = []knowledge.ObjectStatus{knowledge.StatusActive}
	}
	var statusConds []string
	for _, st := range statuses {
		if st == knowledge.StatusActive {
			// Backward compat: empty status is treated as active.
			statusConds = append(statusConds, "status = ''")
			statusConds = append(statusConds, fmt.Sprintf("status = $%d", argIdx))
			args = append(args, string(st))
			argIdx++
		} else {
			statusConds = append(statusConds, fmt.Sprintf("status = $%d", argIdx))
			args = append(args, string(st))
			argIdx++
		}
	}
	conditions = append(conditions, "("+strings.Join(statusConds, " OR ")+")")
	return " WHERE " + strings.Join(conditions, " AND "), args
}

// ListByStatus returns objects in ns matching the given status.
// Empty status matches objects with no status (backward compatibility).
func (s *Store) ListByStatus(ctx context.Context, ns string, status knowledge.ObjectStatus, limit int) ([]*knowledge.KnowledgeObject, error) {
	var conditions []string
	var args []interface{}
	argIdx := 1
	if ns != "" {
		conditions = append(conditions, fmt.Sprintf("namespace = $%d", argIdx))
		args = append(args, ns)
		argIdx++
	}
	if status == knowledge.StatusActive {
		// Backward compat: empty status is treated as active.
		conditions = append(conditions, fmt.Sprintf("(status = '' OR status = $%d)", argIdx))
		args = append(args, string(status))
		argIdx++
	} else {
		conditions = append(conditions, fmt.Sprintf("status = $%d", argIdx))
		args = append(args, string(status))
		argIdx++
	}
	//nolint:gosec // conditions are static WHERE fragments; values use $N placeholders.
	query := `SELECT id, type, namespace, raw, normalized, summary, metadata, tags, confidence, version, created_at, updated_at, status, quality, relations, embedding_model
		FROM akf_objects WHERE ` + strings.Join(conditions, " AND ") + " ORDER BY created_at DESC"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list by status: %w", err)
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

// UpdateStatus transitions an object's lifecycle status.
func (s *Store) UpdateStatus(ctx context.Context, id string, status knowledge.ObjectStatus) error {
	res, err := s.db.ExecContext(ctx, "UPDATE akf_objects SET status = $1, updated_at = NOW() WHERE id = $2",
		string(status), id)
	if err != nil {
		return fmt.Errorf("update status %q: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrObjectNotFound
	}
	return nil
}

// Promote moves a candidate to active and records its computed Quality.
func (s *Store) Promote(ctx context.Context, id string, q *knowledge.Quality) error {
	qualityJSON := marshalQuality(q)
	res, err := s.db.ExecContext(ctx, "UPDATE akf_objects SET status = $1, quality = $2, updated_at = NOW() WHERE id = $3",
		string(knowledge.StatusActive), qualityJSON, id)
	if err != nil {
		return fmt.Errorf("promote %q: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrObjectNotFound
	}
	return nil
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
