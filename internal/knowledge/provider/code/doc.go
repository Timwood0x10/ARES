// Package code implements a GraphProvider that analyses Go source directories
// and emits functions, types, and interfaces as KnowledgeObjects.
//
// It uses the standard go/parser and go/ast packages — no external dependency.
//
// Beta: this package is part of the AKG (Autonomous Knowledge Graph)
// subsystem and is currently BETA. The API is not yet stable and may
// change between minor releases. Do not depend on it in production
// without pinning a version. Feedback welcome.
package code
