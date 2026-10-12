// Package service adapts the internal KnowledgeRuntime to the canonical
// knowledgeapi.KnowledgeService interface.
//
// Architecture:
//
//	internal/knowledgeapi   (canonical DTOs + KnowledgeService interface)
//	     ↑
//	internal/knowledge/service  (this package: adapter)
//	     ↓
//	internal/knowledge/runtime  (real implementation)
//
// The adapter lives in a sub-package to avoid an import cycle:
// knowledgeapi imports internal/knowledge for DTO aliases,
// so the adapter cannot live in internal/knowledge itself (it would
// need to import knowledgeapi).
package service
