// Package service adapts the internal KnowledgeRuntime to the public
// api/knowledge.KnowledgeService interface.
//
// Architecture:
//
//	api/knowledge         (public DTOs + KnowledgeService interface)
//	     ↑
//	internal/knowledge/service  (this package: adapter)
//	     ↓
//	internal/knowledge/runtime  (real implementation)
//
// The adapter lives in a sub-package to avoid an import cycle:
// api/knowledge already imports internal/knowledge for DTO aliases,
// so the adapter cannot live in internal/knowledge itself (it would
// need to import api/knowledge).
//
// Beta: this package is part of the AKG (Autonomous Knowledge Graph)
// subsystem and is currently BETA. The API is not yet stable and may
// change between minor releases. Do not depend on it in production
// without pinning a version. Feedback welcome.
package service
