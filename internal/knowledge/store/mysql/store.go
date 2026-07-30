package mysqlstore

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

// Store is a MySQL-backed KnowledgeStore.
type Store struct {
	db *sql.DB
}

// New opens a MySQL connection using the named driver and DSN, then prepares
// the schema. driverName is typically "mysql"; the caller must blank-import
// the matching driver in their binary. NewWithDB is preferred when the caller
// already owns the *sql.DB (e.g. for connection pooling or test harnesses).
func New(driverName, dsn string) (*Store, error) {
	if driverName == "" {
		return nil, fmt.Errorf("mysql store: driverName cannot be empty")
	}
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("open mysql %q: %w", driverName, err)
	}
	s := &Store{db: db}
	if err := s.initTables(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init tables: %w", err)
	}
	return s, nil
}

// NewWithDB creates a MySQL KnowledgeStore over an existing connection. The
// caller retains ownership of the *sql.DB lifecycle (Close); the store only
// uses it. This is the recommended constructor for production wiring.
func NewWithDB(db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("mysql store: db is nil")
	}
	s := &Store{db: db}
	if err := s.initTables(context.Background()); err != nil {
		return nil, fmt.Errorf("init tables: %w", err)
	}
	return s, nil
}

// Close releases the underlying connection pool when the store owns it. Stores
// created via NewWithDB do NOT own their *sql.DB and should not call Close.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) initTables(ctx context.Context) error {
	// CREATE statements use IF NOT EXISTS, which is universally supported, so
	// they are executed strictly — a failure here is a real schema problem.
	creates := []string{
		`CREATE TABLE IF NOT EXISTS akf_objects (
			id VARCHAR(255) NOT NULL PRIMARY KEY,
			type VARCHAR(64) NOT NULL DEFAULT '',
			namespace VARCHAR(255) NOT NULL DEFAULT '',
			raw LONGBLOB NULL,
			normalized TEXT NOT NULL,
			summary TEXT NOT NULL,
			metadata JSON NULL,
			tags TEXT NULL,
			confidence FLOAT NOT NULL DEFAULT 1.0,
			version BIGINT NOT NULL DEFAULT 1,
			created_at DATETIME(3) NOT NULL,
			updated_at DATETIME(3) NOT NULL,
			status VARCHAR(32) NOT NULL DEFAULT '',
			quality JSON NULL,
			relations JSON NULL,
			embedding_model VARCHAR(255) NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS akf_representations (
			id VARCHAR(255) NOT NULL PRIMARY KEY,
			object_id VARCHAR(255) NOT NULL,
			model VARCHAR(255) NOT NULL,
			dimension INT NOT NULL DEFAULT 0,
			vector JSON NULL,
			metadata JSON NULL,
			created_at DATETIME(3) NOT NULL,
			CONSTRAINT fk_akf_repr_obj FOREIGN KEY (object_id)
				REFERENCES akf_objects(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_akf_objects_type ON akf_objects(type)`,
		`CREATE INDEX IF NOT EXISTS idx_akf_objects_namespace ON akf_objects(namespace)`,
		`CREATE INDEX IF NOT EXISTS idx_akf_objects_status ON akf_objects(status)`,
		`CREATE INDEX IF NOT EXISTS idx_akf_repr_obj_model ON akf_representations(object_id, model)`,
	}
	for _, q := range creates {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("init ddl: %w", err)
		}
	}

	// Migrate pre-0.2.9 databases by ensuring the new columns exist. This is
	// version-robust: ensureColumn probes information_schema first, so it works
	// on MySQL 5.7 (no ADD COLUMN IF NOT EXISTS) and 8.0+/MariaDB alike.
	migrations := []struct{ col, def string }{
		{"status", "VARCHAR(32) NOT NULL DEFAULT ''"},
		{"quality", "JSON NULL"},
		{"relations", "JSON NULL"},
		{"embedding_model", "VARCHAR(255) NOT NULL DEFAULT ''"},
	}
	for _, m := range migrations {
		if err := s.ensureColumn(ctx, "akf_objects", m.col, m.def); err != nil {
			return fmt.Errorf("migrate akf_objects.%s: %w", m.col, err)
		}
	}
	return nil
}

// ensureColumn adds column to table if it is absent. It probes
// information_schema.columns (works across schemas and server versions) before
// issuing a plain ADD COLUMN, avoiding the IF NOT EXISTS syntax that older
// MySQL 5.7 rejects. A duplicate-column race is tolerated as success.
func (s *Store) ensureColumn(ctx context.Context, table, column, def string) error {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM information_schema.columns WHERE table_name = ? AND column_name = ? LIMIT 1`,
		table, column).Scan(&n)
	if err == nil {
		return nil // column already present
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("probe %s.%s: %w", table, column, err)
	}

	if _, err := s.db.ExecContext(ctx,
		fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, def)); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "Duplicate column") || strings.Contains(msg, "duplicate column") {
			return nil // raced with a concurrent migration; column now exists
		}
		return err
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
		qualityJSON := marshalQuality(obj.Quality)
		relationsJSON := marshalRelations(obj.Relations)
		now := time.Now().UTC()

		// ON DUPLICATE KEY UPDATE is the MySQL upsert. VALUES(col) refers to
		// the value proposed by the INSERT; it is widely supported (5.7+,
		// 8.0, MariaDB) though deprecated in 8.0.20 in favor of row aliases.
		_, err := s.db.ExecContext(ctx, `
			INSERT INTO akf_objects
				(id, type, namespace, raw, normalized, summary, metadata, tags, confidence, version, created_at, updated_at, status, quality, relations, embedding_model)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				type = VALUES(type),
				namespace = VALUES(namespace),
				raw = VALUES(raw),
				normalized = VALUES(normalized),
				summary = VALUES(summary),
				metadata = VALUES(metadata),
				tags = VALUES(tags),
				confidence = VALUES(confidence),
				version = version + 1,
				updated_at = VALUES(updated_at),
				status = VALUES(status),
				quality = VALUES(quality),
				relations = VALUES(relations),
				embedding_model = VALUES(embedding_model)
		`, obj.ID, string(obj.Type), obj.Namespace, obj.Raw, obj.Normalized, obj.Summary,
			string(metaJSON), tags, obj.Confidence, obj.Version,
			obj.CreatedAt.UTC(), now,
			string(obj.Status), qualityJSON, relationsJSON, obj.EmbeddingModel)
		if err != nil {
			return fmt.Errorf("save %q: %w", obj.ID, err)
		}
	}
	return nil
}

// marshalQuality returns the JSON encoding of q, or NULL (empty string) when q
// is nil. Marshal errors are logged as warnings and return "" so data is not
// silently lost.
func marshalQuality(q *knowledge.Quality) string {
	if q == nil {
		return ""
	}
	b, err := json.Marshal(q)
	if err != nil {
		slog.Warn("marshal quality failed", "error", err)
		return ""
	}
	return string(b)
}

// marshalRelations returns the JSON encoding of rels, or "" when empty.
func marshalRelations(rels []knowledge.Relation) string {
	if len(rels) == 0 {
		return ""
	}
	b, err := json.Marshal(rels)
	if err != nil {
		slog.Warn("marshal relations failed", "error", err)
		return ""
	}
	return string(b)
}

func (s *Store) Get(ctx context.Context, id string) (*knowledge.KnowledgeObject, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, type, namespace, raw, normalized, summary, metadata, tags, confidence, version, created_at, updated_at, status, quality, relations, embedding_model
		FROM akf_objects WHERE id = ?`, id)

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
	conditions, args := buildConditions(q.Namespace, q.Types, q.Tags, 0)

	query := "SELECT id, type, namespace, raw, normalized, summary, metadata, tags, confidence, version, created_at, updated_at, status, quality, relations, embedding_model FROM akf_objects"
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ") //nolint:gosec // conditions use ? placeholders
	}
	query += " ORDER BY created_at DESC"

	if q.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, q.Limit)
	}
	if q.Offset > 0 {
		// MySQL requires LIMIT when OFFSET is present; supply a large limit if
		// only an offset was requested.
		if q.Limit <= 0 {
			query += " LIMIT 18446744073709551615"
		}
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
		SELECT id, type, namespace, raw, normalized, summary, metadata, tags, confidence, version, created_at, updated_at, status, quality, relations, embedding_model
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
	vecJSON, _ := json.Marshal(rep.Vector)
	now := time.Now().UTC()

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO akf_representations (id, object_id, model, dimension, vector, metadata, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			model = VALUES(model),
			dimension = VALUES(dimension),
			vector = VALUES(vector),
			metadata = VALUES(metadata)
	`, rep.ID, rep.ObjectID, rep.Model, rep.Dimension, string(vecJSON), string(metaJSON), now)
	return err
}

func (s *Store) GetRepresentation(ctx context.Context, objectID string, model string) (*knowledge.Representation, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, object_id, model, dimension, vector, metadata, created_at
		FROM akf_representations WHERE object_id = ? AND model = ?`, objectID, model)

	var rep knowledge.Representation
	var metaJSON, vecJSON []byte
	var createdAt flexTime

	err := row.Scan(&rep.ID, &rep.ObjectID, &rep.Model, &rep.Dimension, &vecJSON, &metaJSON, &createdAt)
	if err == sql.ErrNoRows {
		return nil, ErrObjectNotFound
	}
	if err != nil {
		return nil, err
	}

	_ = json.Unmarshal(vecJSON, &rep.Vector)
	_ = json.Unmarshal(metaJSON, &rep.Metadata)
	rep.CreatedAt = time.Time(createdAt)
	return &rep, nil
}

// scanner is the common Scan interface shared by *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// flexTime is a timestamp receiver that accepts whatever the driver returns —
// time.Time (go-sql-driver with parseTime=true) or a string/[]byte datetime —
// so the store is robust to DSN configuration.
type flexTime time.Time

func (f *flexTime) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		return nil
	case time.Time:
		*f = flexTime(v)
		return nil
	case []byte:
		return f.parse(string(v))
	case string:
		return f.parse(v)
	default:
		return fmt.Errorf("flexTime: unsupported source type %T", src)
	}
}

// parse tries common layouts emitted by MySQL drivers (RFC3339 with and
// without sub-second precision, and the native DATETIME layout).
func (f *flexTime) parse(s string) error {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			*f = flexTime(t)
			return nil
		}
	}
	return fmt.Errorf("flexTime: cannot parse %q", s)
}

func scanObject(row scanner) (*knowledge.KnowledgeObject, error) {
	var obj knowledge.KnowledgeObject
	var typeStr, ns, norm, summary string
	var raw []byte
	var metaJSON, tagsStr []byte
	var createdAt, updatedAt flexTime
	var statusStr, qualityJSON, relationsJSON, embeddingModel []byte

	if err := row.Scan(&obj.ID, &typeStr, &ns, &raw, &norm, &summary, &metaJSON, &tagsStr,
		&obj.Confidence, &obj.Version, &createdAt, &updatedAt,
		&statusStr, &qualityJSON, &relationsJSON, &embeddingModel); err != nil {
		return nil, err
	}

	obj.Type = knowledge.ObjectType(typeStr)
	obj.Namespace = ns
	obj.Normalized = norm
	obj.Summary = summary
	obj.Raw = raw
	if len(tagsStr) > 0 {
		obj.Tags = strings.Split(string(tagsStr), ",")
	}
	obj.CreatedAt = time.Time(createdAt)
	obj.UpdatedAt = time.Time(updatedAt)
	obj.Status = knowledge.ObjectStatus(statusStr)
	obj.EmbeddingModel = string(embeddingModel)
	// Best-effort unmarshal: malformed JSON is ignored so a single bad row
	// never poisons the whole result set.
	if len(qualityJSON) > 0 && string(qualityJSON) != "null" {
		var q knowledge.Quality
		if err := json.Unmarshal(qualityJSON, &q); err == nil {
			obj.Quality = &q
		}
	}
	if len(relationsJSON) > 0 && string(relationsJSON) != "null" {
		_ = json.Unmarshal(relationsJSON, &obj.Relations)
	}
	if len(metaJSON) > 0 {
		_ = json.Unmarshal(metaJSON, &obj.Metadata)
	}
	return &obj, nil
}

// HybridSearch performs vector (cosine) plus lexical (keyword) scoring over
// MySQL-stored objects. Vectors are stored as JSON, so recall is computed
// in-process after loading candidates — identical to the SQLite backend. This
// keeps the store storage-agnostic: swapping in pgvector/Milvus only changes
// the vector fetch, never the KnowledgeStore contract.
func (s *Store) HybridSearch(ctx context.Context, req knowledge.HybridSearchRequest) ([]knowledge.ScoredObject, error) {
	statuses := req.StatusFilter
	if len(statuses) == 0 {
		statuses = []knowledge.ObjectStatus{knowledge.StatusActive}
	}
	conditions, args := hybridConditions(req.Namespace, req.Types, statuses)

	//nolint:gosec // conditions are static WHERE fragments; values use ? placeholders.
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
		placeholders := make([]string, len(ids))
		repArgs := make([]any, 0, len(ids)+1)
		for i, id := range ids {
			placeholders[i] = "?"
			repArgs = append(repArgs, id)
		}
		repArgs = append(repArgs, req.Model)
		//nolint:gosec // placeholders are ?, ids are local object IDs
		repQuery := fmt.Sprintf(`SELECT id, object_id, model, dimension, vector, metadata, created_at FROM akf_representations WHERE object_id IN (%s) AND model = ?`, strings.Join(placeholders, ","))
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

	if req.MinScore > 0 {
		filtered := scored[:0]
		for _, r := range scored {
			if r.FinalScore >= req.MinScore {
				filtered = append(filtered, r)
			}
		}
		scored = filtered
	}

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

// hybridConditions builds the WHERE clause (with ? placeholders) and args for
// HybridSearch candidates based on namespace, types, and status filter. An
// empty status on a row matches the active filter for back-compat. When
// statuses is empty it defaults to [StatusActive], matching HybridSearch's
// contract, so the helper never emits a malformed empty status group.
func hybridConditions(namespace string, types []knowledge.ObjectType, statuses []knowledge.ObjectStatus) (string, []any) {
	var conditions []string
	var args []any
	if namespace != "" {
		conditions = append(conditions, "namespace = ?")
		args = append(args, namespace)
	}
	if len(types) > 0 {
		placeholders := make([]string, len(types))
		for i, t := range types {
			placeholders[i] = "?"
			args = append(args, string(t))
		}
		conditions = append(conditions, fmt.Sprintf("type IN (%s)", strings.Join(placeholders, ",")))
	}
	if len(statuses) == 0 {
		statuses = []knowledge.ObjectStatus{knowledge.StatusActive}
	}
	var statusConds []string
	for _, st := range statuses {
		if st == knowledge.StatusActive {
			// Backward compat: empty status is treated as active.
			statusConds = append(statusConds, "status = ''")
			statusConds = append(statusConds, "status = ?")
			args = append(args, string(st))
		} else {
			statusConds = append(statusConds, "status = ?")
			args = append(args, string(st))
		}
	}
	conditions = append(conditions, "("+strings.Join(statusConds, " OR ")+")")
	return " WHERE " + strings.Join(conditions, " AND "), args
}

// buildConditions assembles the WHERE fragments for Query. statusLimit is
// optional and unused here (kept for signature symmetry); Query callers add
// their own LIMIT/OFFSET after this.
func buildConditions(namespace string, types []knowledge.ObjectType, tags []string, _ int) ([]string, []any) {
	var conditions []string
	var args []any
	if namespace != "" {
		conditions = append(conditions, "namespace = ?")
		args = append(args, namespace)
	}
	if len(types) > 0 {
		placeholders := make([]string, len(types))
		for i, t := range types {
			placeholders[i] = "?"
			args = append(args, string(t))
		}
		conditions = append(conditions, fmt.Sprintf("type IN (%s)", strings.Join(placeholders, ",")))
	}
	if len(tags) > 0 {
		tagConditions := make([]string, len(tags))
		for i, tag := range tags {
			tagConditions[i] = "tags LIKE ?"
			args = append(args, "%"+tag+"%")
		}
		conditions = append(conditions, "("+strings.Join(tagConditions, " OR ")+")")
	}
	return conditions, args
}

func scanRepresentation(row scanner) (*knowledge.Representation, error) {
	var rep knowledge.Representation
	var metaJSON, vecJSON []byte
	var createdAt flexTime
	if err := row.Scan(&rep.ID, &rep.ObjectID, &rep.Model, &rep.Dimension, &vecJSON, &metaJSON, &createdAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(vecJSON, &rep.Vector)
	_ = json.Unmarshal(metaJSON, &rep.Metadata)
	rep.CreatedAt = time.Time(createdAt)
	return &rep, nil
}

// ListByStatus returns objects in ns matching the given status. Empty status
// matches objects with no status (backward compatibility).
func (s *Store) ListByStatus(ctx context.Context, ns string, status knowledge.ObjectStatus, limit int) ([]*knowledge.KnowledgeObject, error) {
	var conditions []string
	var args []any
	if ns != "" {
		conditions = append(conditions, "namespace = ?")
		args = append(args, ns)
	}
	if status == knowledge.StatusActive {
		conditions = append(conditions, "(status = '' OR status = ?)")
		args = append(args, string(status))
	} else {
		conditions = append(conditions, "status = ?")
		args = append(args, string(status))
	}
	//nolint:gosec // conditions are static WHERE fragments; values use ? placeholders.
	query := `SELECT id, type, namespace, raw, normalized, summary, metadata, tags, confidence, version, created_at, updated_at, status, quality, relations, embedding_model
		FROM akf_objects WHERE ` + strings.Join(conditions, " AND ") + " ORDER BY created_at DESC"
	if limit > 0 {
		query += " LIMIT ?"
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
	res, err := s.db.ExecContext(ctx, "UPDATE akf_objects SET status = ?, updated_at = ? WHERE id = ?",
		string(status), time.Now().UTC(), id)
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
	res, err := s.db.ExecContext(ctx, "UPDATE akf_objects SET status = ?, quality = ?, updated_at = ? WHERE id = ?",
		string(knowledge.StatusActive), qualityJSON, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("promote %q: %w", id, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrObjectNotFound
	}
	return nil
}
