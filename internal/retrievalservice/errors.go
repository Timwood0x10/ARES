// Package retrieval provides error definitions for retrieval service.
package retrievalservice

import "errors"

var (
	// ErrInvalidTenantID is returned when tenant ID is empty.
	ErrInvalidTenantID = errors.New("invalid tenant ID")

	// ErrInvalidQuery is returned when query is empty.
	ErrInvalidQuery = errors.New("invalid query")

	// ErrInvalidConfig is returned when configuration is invalid.
	ErrInvalidConfig = errors.New("invalid configuration")

	// ErrInvalidContent is returned when content is empty.
	ErrInvalidContent = errors.New("invalid content")

	// ErrInvalidItemID is returned when item ID is empty.
	ErrInvalidItemID = errors.New("invalid item ID")

	// ErrKnowledgeNotFound is returned when knowledge item does not exist.
	ErrKnowledgeNotFound = errors.New("knowledge not found")

	// ErrAccessDenied is returned when access to a resource is denied.
	ErrAccessDenied = errors.New("access denied")

	// ErrSearchFailed is returned when search operation fails.
	ErrSearchFailed = errors.New("search failed")
)
