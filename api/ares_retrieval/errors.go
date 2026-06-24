// Package retrieval provides error definitions for retrieval operations.
package retrieval

import "errors"

var (
	// ErrInvalidTenantID is returned when tenant ID is empty.
	ErrInvalidTenantID = errors.New("invalid tenant ID")

	// ErrInvalidQuery is returned when query is empty.
	ErrInvalidQuery = errors.New("invalid query")

	// ErrNoRetrievalService is returned when no retrieval service is configured.
	ErrNoRetrievalService = errors.New("no retrieval service configured")

	// ErrSearchFailed is returned when search operation fails.
	ErrSearchFailed = errors.New("search failed")
)
