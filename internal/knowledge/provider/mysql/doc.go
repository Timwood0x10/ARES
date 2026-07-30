// Package mysql implements a GraphProvider for external MySQL databases.
// It reads table rows via SQL queries and converts them to KnowledgeObjects.
//
// Usage:
//
//	db, _ := sql.Open("mysql", "user:pass@tcp(host:3306)/dbname")
//	provider, _ := mysql.NewMySQLProvider(db, cfg, mapping)
//
// Beta: this package is part of the AKG (Autonomous Knowledge Graph)
// subsystem and is currently BETA. The API is not yet stable and may
// change between minor releases. Do not depend on it in production
// without pinning a version. Feedback welcome.
package mysql
