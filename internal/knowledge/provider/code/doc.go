// Package code implements a GraphProvider that analyses Go source directories
// and emits functions, types, and interfaces as KnowledgeObjects.
//
// It uses the standard go/parser and go/ast packages — no external dependency.
package code
