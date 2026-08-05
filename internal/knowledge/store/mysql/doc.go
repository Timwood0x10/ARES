// Package mysqlstore implements KnowledgeStore for MySQL (and MySQL-protocol
// compatible servers such as MariaDB or TiDB).
//
// This package deliberately depends ONLY on the standard library plus the
// knowledge package. It does NOT import any MySQL driver: the consumer is
// responsible for blank-importing the driver, e.g.
//
//	import _ "github.com/go-sql-driver/mysql"
//
// and passing the registered driver name (conventionally "mysql") to New, or
// opening the *sql.DB themselves and handing it to NewWithDB. This keeps the
// store package dependency-free so that adding a MySQL backend imposes no new
// requirement on consumers that do not use MySQL — the architecture stays open.
//
// The implementation mirrors the SQLite store: vectors are JSON-serialized
// into JSON columns, tags are comma-joined, and timestamps are stored with
// millisecond precision. The only MySQL-specific SQL is the upsert
// (ON DUPLICATE KEY UPDATE) and the connection-idempotent DDL.
//
// Beta: this package is part of the AKG (Autonomous Knowledge Graph)
// subsystem and is currently BETA. The API is not yet stable and may
// change between minor releases. Do not depend on it in production
// without pinning a version. Feedback welcome.
package mysqlstore
