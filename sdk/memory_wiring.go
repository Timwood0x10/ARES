// Package sdk wiring helpers for the production MemoryManager.
//
// This file closes the compression + RAG + distillation loop inside the SDK
// Runtime. It extracts the memory-construction logic out of New() so the
// constructor stays under the 100-line limit, and mirrors the reference
// wiring in internal/ares_bootstrap (retriever_wiring.go +
// provide_distillation.go) without taking a build-time dependency on that
// internal bootstrap package.
//
// Two adapters live here because the in-tree retriever/storage types do not
// line up directly with the production contracts:
//
//   - sdkExperienceSearcher adapts repositories.ExperienceRepositoryInterface
//     (returns *storage_models.Experience) to memctx.ExperienceSearcher
//     (returns distillation.Experience). The MemoryRetriever only reads, so
//     the narrow ExperienceSearcher interface is sufficient.
//   - sdkDistillationRepo adapts the same postgres repository to the full
//     distillation.ExperienceRepository contract required by
//     NewMemoryManagerWithDistiller. It carries the write-side methods
//     (Create/Update/Delete/...) the distiller invokes at store time.
//   - sdkKnowledgeRetrieverAdapter adapts adapter.KnowledgeRetriever (returns
//     adapter.ContextSnippet) to memctx.ContextRetriever (returns
//     memctx.ContextSnippet).
package sdk

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	apiembed "github.com/Timwood0x10/ares/api/embedding"
	"github.com/Timwood0x10/ares/api/experience"
	"github.com/Timwood0x10/ares/internal/ares_events"
	aresexp "github.com/Timwood0x10/ares/internal/ares_experience"
	memory "github.com/Timwood0x10/ares/internal/ares_memory"
	memctx "github.com/Timwood0x10/ares/internal/ares_memory/context"
	"github.com/Timwood0x10/ares/internal/ares_memory/distillation"
	memembed "github.com/Timwood0x10/ares/internal/ares_memory/embedding"
	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/adapter"
	khruntime "github.com/Timwood0x10/ares/internal/knowledge/runtime"
	"github.com/Timwood0x10/ares/internal/llm"
	"github.com/Timwood0x10/ares/internal/scoreutil"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
	pgembedding "github.com/Timwood0x10/ares/internal/storage/postgres/embedding"
	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
)

// ErrDistillDepsMissing signals that distillation dependencies (embedding
// service URL or database host) were not configured. It is wrapped by
// wireDistillationDeps so wireMemory can distinguish "missing config" from
// "construction failed" and fall back to a non-distilling MemoryManager
// without failing the whole Runtime.
var ErrDistillDepsMissing = errors.New("distillation dependencies unavailable")

// defaultDistillTenant is the tenant scope used for distillation writes and
// experience reads when no explicit tenant is carried by the Experience DTO.
// It mirrors internal/ares_bootstrap.defaultDistillTenant so SDK-produced
// experiences are visible to the same single-tenant consumers.
const defaultDistillTenant = ares_events.DefaultTenantID

// defaultEmbeddingTimeout is the HTTP timeout used when the SDK builds an
// embedding client from cfg.embedCfg (which carries no explicit timeout).
const defaultEmbeddingTimeout = 30 * time.Second

// defaultListLimit caps the best-effort ListByType call backing
// GetByMemoryType/CountByMemoryType on the distillation repo adapter. The
// distiller uses these for deduplication, so a generous cap keeps semantics
// correct without unbounded scans.
const defaultListLimit = 1000

// retrieverSetter is the minimal interface for injecting ContextRetrievers
// into a MemoryManager. Both *memory.memoryManager and
// *memory.ProductionMemoryManager satisfy it, but the public MemoryManager
// interface does not expose SetRetrievers (retrieval is an optional
// capability), so we type-assert at wiring time instead of widening the
// interface. Mirrors internal/ares_bootstrap.retrieverSetter.
type retrieverSetter interface {
	SetRetrievers(retrievers []memctx.ContextRetriever)
}

// sdkExperienceSearcher adapts the PostgreSQL experience repository to the
// memctx.ExperienceSearcher interface expected by MemoryRetriever.
//
// The postgres repository returns *storage_models.Experience (the storage
// DTO with backward-compat Input/Output aliases and metadata), while the
// retriever consumes distillation.Experience (the canonical api/experience
// DTO). This adapter performs the field mapping on every SearchByVector
// call so the retriever stays storage-agnostic.
//
// TODO: unify with internal/ares_bootstrap pgExperienceSearcher into a shared package.
type sdkExperienceSearcher struct {
	repo repositories.ExperienceRepositoryInterface
}

// SearchByVector delegates to the PostgreSQL repository and converts each
// storage_models.Experience into a distillation.Experience. Entries with a
// blank ID are dropped defensively — they cannot be referenced later and
// would only add noise to the prompt.
func (s *sdkExperienceSearcher) SearchByVector(
	ctx context.Context,
	vector []float64,
	tenantID string,
	limit int,
) ([]distillation.Experience, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("sdk experience searcher: repository is nil")
	}
	storageExps, err := s.repo.SearchByVector(ctx, vector, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("sdk experience searcher: %w", err)
	}
	out := make([]distillation.Experience, 0, len(storageExps))
	for _, se := range storageExps {
		if se == nil || se.ID == "" {
			continue
		}
		out = append(out, toDistillationExperience(se))
	}
	return out, nil
}

// sdkDistillationRepo adapts repositories.ExperienceRepositoryInterface to
// the full distillation.ExperienceRepository contract required by
// NewMemoryManagerWithDistiller. It carries the write-side methods
// (Create/Update/Delete/DeleteBatch) and the memory-type queries
// (GetByMemoryType/CountByMemoryType) the distiller invokes at store and
// deduplication time.
//
// The underlying repository is responsible for its own concurrency safety;
// this adapter holds no mutable state and is safe for concurrent use.
//
// TODO: unify with internal/ares_bootstrap pgExperienceSearcher into a shared package.
type sdkDistillationRepo struct {
	repo          repositories.ExperienceRepositoryInterface
	defaultTenant string
}

// newSDKDistillationRepo constructs an adapter wrapping the given postgres
// repository. defaultTenant is used for Create/Update when the Experience
// DTO carries no tenant (the distillation.Experience struct has no TenantID
// field, so the distiller path relies on the adapter to supply one).
func newSDKDistillationRepo(repo repositories.ExperienceRepositoryInterface, defaultTenant string) *sdkDistillationRepo {
	if defaultTenant == "" {
		defaultTenant = defaultDistillTenant
	}
	return &sdkDistillationRepo{repo: repo, defaultTenant: defaultTenant}
}

// SearchByVector delegates to the postgres repository and converts each
// storage_models.Experience into a distillation.Experience.
func (r *sdkDistillationRepo) SearchByVector(
	ctx context.Context,
	vector []float64,
	tenantID string,
	limit int,
) ([]distillation.Experience, error) {
	if r == nil || r.repo == nil {
		return nil, fmt.Errorf("sdk distillation repo: repository is nil")
	}
	storageExps, err := r.repo.SearchByVector(ctx, vector, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("sdk distillation repo search: %w", err)
	}
	out := make([]distillation.Experience, 0, len(storageExps))
	for _, se := range storageExps {
		if se == nil || se.ID == "" {
			continue
		}
		out = append(out, toDistillationExperience(se))
	}
	return out, nil
}

// GetByMemoryType returns experiences whose storage Type matches the
// memory-type label. The mapping is best-effort: storage Type stores
// success/failure/etc. while MemoryType.String() returns
// fact/preference/solution/rule, so this surface is approximate. It is
// only used by the distiller's deduplication path.
func (r *sdkDistillationRepo) GetByMemoryType(
	ctx context.Context,
	tenantID string,
	memoryType experience.MemoryType,
) ([]experience.Experience, error) {
	if r == nil || r.repo == nil {
		return nil, fmt.Errorf("sdk distillation repo: repository is nil")
	}
	storageExps, err := r.repo.ListByType(ctx, memoryType.String(), tenantID, defaultListLimit)
	if err != nil {
		return nil, fmt.Errorf("sdk distillation repo get by memory type: %w", err)
	}
	out := make([]experience.Experience, 0, len(storageExps))
	for _, se := range storageExps {
		if se == nil || se.ID == "" {
			continue
		}
		out = append(out, toDistillationExperience(se))
	}
	return out, nil
}

// CountByMemoryType returns the number of experiences for the given tenant
// and memory type. The postgres repository exposes no direct count API, so
// this delegates to GetByMemoryType and returns the slice length. This is
// inefficient for large tables but correct; the distiller only calls it
// during deduplication, which is itself bounded by MaxDistilledTasks.
func (r *sdkDistillationRepo) CountByMemoryType(
	ctx context.Context,
	tenantID string,
	memoryType experience.MemoryType,
) (int, error) {
	exps, err := r.GetByMemoryType(ctx, tenantID, memoryType)
	if err != nil {
		return 0, fmt.Errorf("sdk distillation repo count: %w", err)
	}
	return len(exps), nil
}

// Create inserts a new experience. The Experience DTO carries no tenant,
// so the adapter's defaultTenant is applied. ExtractionMethod is preserved
// in Metadata so the round-trip through SearchByVector restores it.
func (r *sdkDistillationRepo) Create(ctx context.Context, exp *experience.Experience) error {
	if r == nil || r.repo == nil {
		return fmt.Errorf("sdk distillation repo: repository is nil")
	}
	if exp == nil {
		return fmt.Errorf("sdk distillation repo: experience is nil")
	}
	storage := toStorageExperience(exp, r.defaultTenant)
	if err := r.repo.Create(ctx, storage); err != nil {
		return fmt.Errorf("sdk distillation repo create: %w", err)
	}
	return nil
}

// Update updates an existing experience. Same tenant/ExtractionMethod
// handling as Create.
func (r *sdkDistillationRepo) Update(ctx context.Context, exp *experience.Experience) error {
	if r == nil || r.repo == nil {
		return fmt.Errorf("sdk distillation repo: repository is nil")
	}
	if exp == nil {
		return fmt.Errorf("sdk distillation repo: experience is nil")
	}
	storage := toStorageExperience(exp, r.defaultTenant)
	if err := r.repo.Update(ctx, storage); err != nil {
		return fmt.Errorf("sdk distillation repo update: %w", err)
	}
	return nil
}

// Delete removes an experience by ID.
func (r *sdkDistillationRepo) Delete(ctx context.Context, id string) error {
	if r == nil || r.repo == nil {
		return fmt.Errorf("sdk distillation repo: repository is nil")
	}
	if err := r.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("sdk distillation repo delete: %w", err)
	}
	return nil
}

// DeleteBatch deletes multiple experiences by ID. The postgres repository
// exposes no batch API, so this loops single deletes. A failure short-circuits
// and the remaining IDs are left in place; the caller (distiller) already
// falls back to per-id deletes on batch failure.
func (r *sdkDistillationRepo) DeleteBatch(ctx context.Context, ids []string) error {
	if r == nil || r.repo == nil {
		return fmt.Errorf("sdk distillation repo: repository is nil")
	}
	for _, id := range ids {
		if err := r.repo.Delete(ctx, id); err != nil {
			return fmt.Errorf("sdk distillation repo delete batch %s: %w", id, err)
		}
	}
	return nil
}

// toDistillationExperience maps a storage_models.Experience into the
// canonical distillation.Experience DTO. Problem/Solution fall back to the
// legacy Input/Output fields when the high-level fields are empty (the
// storage layer stores them in the 'input'/'output' columns for backward
// compat). Confidence is clamped to [0, 1] so downstream filtering operates
// on a well-defined domain. ExtractionMethod is recovered from Metadata
// when present, defaulting to ExtractionDirect.
func toDistillationExperience(e *storage_models.Experience) distillation.Experience {
	problem := e.Problem
	if problem == "" {
		problem = e.Input
	}
	solution := e.Solution
	if solution == "" {
		solution = e.Output
	}
	method := distillation.ExtractionDirect
	if e.Metadata != nil {
		if m, ok := e.Metadata["extraction_method"].(string); ok && m != "" {
			method = distillation.ExtractionMethod(m)
		}
	}
	return distillation.Experience{
		ID:               e.ID,
		Problem:          problem,
		Solution:         solution,
		Confidence:       scoreutil.ClampUnit(e.Score),
		ExtractionMethod: method,
		Vector:           e.Embedding,
	}
}

// toStorageExperience maps a distillation.Experience into a
// storage_models.Experience DTO ready for postgres persistence. Problem and
// Solution are mirrored into the legacy Input/Output columns so existing
// keyword-search and backward-compat reads keep working. ExtractionMethod
// is stashed in Metadata for round-trip fidelity. tenantID is supplied by
// the adapter (the Experience DTO carries no tenant).
func toStorageExperience(exp *distillation.Experience, tenantID string) *storage_models.Experience {
	meta := map[string]any{}
	if exp.ExtractionMethod != "" {
		meta["extraction_method"] = string(exp.ExtractionMethod)
	}
	return &storage_models.Experience{
		ID:        exp.ID,
		TenantID:  tenantID,
		Problem:   exp.Problem,
		Solution:  exp.Solution,
		Input:     exp.Problem,
		Output:    exp.Solution,
		Embedding: exp.Vector,
		Score:     exp.Confidence,
		Metadata:  meta,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

// sdkKnowledgeRetrieverAdapter wraps adapter.KnowledgeRetriever and converts
// its local adapter.ContextSnippet results into the canonical
// memctx.ContextSnippet so the MemoryManager's context builder can consume
// them uniformly alongside MemoryRetriever output.
//
// TODO: unify with internal/ares_bootstrap knowledgeRetrieverAdapter into a shared package.
type sdkKnowledgeRetrieverAdapter struct {
	inner *adapter.KnowledgeRetriever
}

// Retrieve delegates to the underlying KnowledgeRetriever and converts each
// adapter.ContextSnippet into a memctx.ContextSnippet. A nil inner
// retriever yields an empty slice — this keeps BuildContext resilient when
// the AKG runtime was not constructed.
func (a *sdkKnowledgeRetrieverAdapter) Retrieve(
	ctx context.Context,
	input string,
	topK int,
) ([]memctx.ContextSnippet, error) {
	if a == nil || a.inner == nil {
		return []memctx.ContextSnippet{}, nil
	}
	snippets, err := a.inner.Retrieve(ctx, input, topK)
	if err != nil {
		return nil, fmt.Errorf("sdk knowledge retriever adapter: %w", err)
	}
	out := make([]memctx.ContextSnippet, 0, len(snippets))
	for _, s := range snippets {
		out = append(out, memctx.ContextSnippet{
			Source:   s.Source,
			Content:  s.Content,
			Score:    s.Score,
			Metadata: s.Metadata,
		})
	}
	return out, nil
}

// memoryWiring bundles the outputs of wireMemory so New() can unpack a
// single struct instead of juggling five return values. embClient and
// expRepo are nil when distillation is disabled or its deps are missing;
// wireSDKRetrievers handles that gracefully. distillSvc is the standalone
// DistillationService consumed by the event-driven distillation subscriber;
// it is nil when distillation is disabled, deps are missing, or the service
// could not be constructed (non-fatal: the memory manager still works).
// akgDistiller is a separate distiller built for the AKG DistillBridge
// (conversation → KnowledgeObject pipeline). It is nil when embClient or
// expRepo is unavailable, since the distiller requires both.
type memoryWiring struct {
	mgr          memory.MemoryManager
	embClient    apiembed.EmbeddingService
	expRepo      repositories.ExperienceRepositoryInterface
	cleanup      func()
	distillSvc   *aresexp.DistillationService
	akgDistiller adapter.ConversationDistiller
}

// wireMemory constructs the production MemoryManager (compression + RAG +
// distillation) from the SDK config. When distillation is enabled and its
// dependencies are available, it returns a manager backed by
// NewMemoryManagerWithDistiller; otherwise it falls back to the
// compression-only NewMemoryManager. The returned cleanup closes the
// postgres pool when distillation deps were constructed (nil otherwise).
//
// Args:
//
//	ctx  - construction context, used for postgres pool init.
//	cfg  - fully applied SDK config; memCfg/distillCfg/embedCfg/dbCfg are read.
//
// Returns:
//
//	*memoryWiring - mgr is always non-nil on success; embClient/expRepo may be nil.
//	error         - wrapped error if the memory manager itself cannot be constructed.
func wireMemory(ctx context.Context, cfg *config) (*memoryWiring, error) {
	memCfg := buildMemoryConfig(cfg.memCfg)

	if !cfg.distillCfg.Enabled {
		mgr, err := memory.NewMemoryManager(memCfg)
		if err != nil {
			return nil, fmt.Errorf("wire memory: %w", err)
		}
		return &memoryWiring{mgr: mgr}, nil
	}

	embClient, expRepo, cleanup, err := wireDistillationDeps(ctx, cfg)
	if err != nil {
		if errors.Is(err, ErrDistillDepsMissing) {
			slog.Warn("sdk: distillation deps missing, falling back to compression-only memory",
				"error", err)
		} else {
			slog.Warn("sdk: distillation deps construction failed, falling back to compression-only memory",
				"error", err)
		}
		mgr, fallbackErr := memory.NewMemoryManager(memCfg)
		if fallbackErr != nil {
			return nil, fmt.Errorf("wire memory fallback: %w", fallbackErr)
		}
		return &memoryWiring{mgr: mgr}, nil
	}

	mgr, err := memory.NewMemoryManagerWithDistiller(memCfg, embClient, newSDKDistillationRepo(expRepo, defaultDistillTenant))
	if err != nil {
		if cleanup != nil {
			cleanup()
		}
		return nil, fmt.Errorf("wire memory distiller: %w", err)
	}

	// Build the standalone DistillationService consumed by the event-driven
	// distillation subscriber. Non-fatal: when construction fails the memory
	// manager still works; only event-driven distillation is disabled.
	distillSvc, derr := buildDistillationService(cfg, embClient, expRepo)
	if derr != nil {
		slog.Warn("sdk: distillation service construction failed; event-driven distillation disabled",
			"error", derr)
	}

	// Build a separate distiller for the AKG DistillBridge. The memory
	// manager's internal distiller is not exposed, so a dedicated instance is
	// constructed from the same embedding client and experience repo. Non-fatal:
	// when construction fails the AKG bridge is simply not wired. The deps-nil
	// guard lives here (not in buildAKGDistiller) so that function never returns
	// the ambiguous (nil, nil) — see the nilnil linter rule.
	var akgDistiller adapter.ConversationDistiller
	if embClient != nil && expRepo != nil {
		d, aerr := buildAKGDistiller(embClient, expRepo)
		if aerr != nil {
			slog.Warn("sdk: AKG distiller construction failed; AKG distillation disabled",
				"error", aerr)
		} else {
			akgDistiller = d
		}
	}

	return &memoryWiring{
		mgr:          mgr,
		embClient:    embClient,
		expRepo:      expRepo,
		cleanup:      cleanup,
		distillSvc:   distillSvc,
		akgDistiller: akgDistiller,
	}, nil
}

// buildMemoryConfig translates the SDK memoryCfg into a production
// memory.MemoryConfig. It starts from DefaultMemoryConfig so all
// storage/TTL/vector defaults are preserved, then overrides the user-facing
// knobs. Zero values in memoryCfg mean "use default" — they do NOT clobber
// the defaults.
func buildMemoryConfig(cfg memoryCfg) *memory.MemoryConfig {
	mc := memory.DefaultMemoryConfig()
	mc.Enabled = true
	if cfg.MaxHistory > 0 {
		mc.MaxHistory = cfg.MaxHistory
	}
	if cfg.MaxSessions > 0 {
		mc.MaxSessions = cfg.MaxSessions
	}
	mc.EnableRAG = cfg.EnableRAG
	if cfg.RAGTopK > 0 {
		mc.RAGTopK = cfg.RAGTopK
	}
	if cfg.RAGMinScore > 0 {
		mc.RAGMinScore = cfg.RAGMinScore
	}
	return mc
}

// wireDistillationDeps constructs the embedding client and postgres-backed
// experience repository required by NewMemoryManagerWithDistiller. Both are
// optional SDK features (gated by WithEmbeddingService + WithPostgres +
// WithDistillation), so a missing config yields ErrDistillDepsMissing
// rather than a hard failure.
//
// The embedding client is returned as the concrete *pgembedding.EmbeddingClient
// (which satisfies apiembed.EmbeddingService) so it can be reused by
// buildDistillationService, whose NewDistillationService target requires the
// concrete type.
//
// Args:
//
//	ctx  - construction context, used for postgres pool init (ping).
//	cfg  - fully applied SDK config; embedCfg and dbCfg are read.
//
// Returns:
//
//	embClient - concrete embedding client; satisfies apiembed.EmbeddingService; nil only on error.
//	expRepo   - postgres experience repository; nil only on error.
//	cleanup   - closes the postgres pool; safe to call when non-nil. Nil on error.
//	err       - wrapped ErrDistillDepsMissing when config is incomplete, or a
//	            construction error otherwise.
func wireDistillationDeps(ctx context.Context, cfg *config) (*pgembedding.EmbeddingClient, repositories.ExperienceRepositoryInterface, func(), error) {
	if cfg.embedCfg.ServiceURL == "" || cfg.dbCfg.Host == "" {
		return nil, nil, nil, fmt.Errorf("distillation deps: %w", ErrDistillDepsMissing)
	}

	embClient := buildEmbeddingClient(cfg.embedCfg)
	pool, err := buildPostgresPool(ctx, cfg.dbCfg)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("distillation deps postgres pool: %w", err)
	}

	expRepo := repositories.NewExperienceRepository(pool.GetDB())
	cleanup := func() {
		if cerr := pool.Close(); cerr != nil {
			slog.Warn("sdk: distillation postgres pool close failed", "error", cerr)
		}
	}
	return embClient, expRepo, cleanup, nil
}

// buildEmbeddingClient constructs the postgres embedding client from the
// SDK embeddingCfg. The client satisfies apiembed.EmbeddingService. Redis
// caching is not wired (nil), matching the bootstrap default.
func buildEmbeddingClient(cfg embeddingCfg) *pgembedding.EmbeddingClient {
	return pgembedding.NewEmbeddingClient(cfg.ServiceURL, cfg.Model, nil, defaultEmbeddingTimeout)
}

// buildLLMClient constructs a standalone internal *llm.Client from the SDK
// config. The DistillationService requires a *llm.Client (not the public
// *llm.Service used by the agent loop), so this mirrors the internal-config
// conversion done by llmservice.NewService: the public core.LLMConfig is
// mapped field-by-field into the internal llm.Config. Fallbacks are not
// applied here because distillation is a best-effort background path that
// does not warrant a failover client.
//
// Args:
//
//	cfg - fully applied SDK config; llmCfg is read. A nil llmCfg yields an error.
//
// Returns:
//
//	*llm.Client - configured LLM client ready for DistillationService.Distill.
//	error       - wrapped error if llmCfg is nil or llm.NewClient fails.
func buildLLMClient(cfg *config) (*llm.Client, error) {
	if cfg == nil || cfg.llmCfg == nil {
		return nil, fmt.Errorf("build llm client: %w", ErrDistillDepsMissing)
	}
	internalCfg := &llm.Config{
		Provider:        string(cfg.llmCfg.Provider),
		APIKey:          cfg.llmCfg.APIKey,
		BaseURL:         cfg.llmCfg.BaseURL,
		Model:           cfg.llmCfg.Model,
		Timeout:         cfg.llmCfg.Timeout,
		MaxTokens:       cfg.llmCfg.MaxTokens,
		MaxPromptLength: cfg.llmCfg.MaxPromptLength,
	}
	client, err := llm.NewClient(internalCfg)
	if err != nil {
		return nil, fmt.Errorf("build llm client: %w", err)
	}
	return client, nil
}

// buildDistillationService constructs the standalone DistillationService
// consumed by the event-driven distillation subscriber. It builds a
// dedicated *llm.Client (independent of the agent loop's *llm.Service) and
// reuses the same embedding client and experience repo already wired for
// the memory manager's distiller, so distilled experiences land in the same
// store the RAG retriever reads from.
//
// Args:
//
//	cfg       - fully applied SDK config; llmCfg is read by buildLLMClient.
//	embClient - embedding client shared with the memory distiller; must be non-nil.
//	expRepo   - postgres experience repo shared with the memory distiller; must be non-nil.
//
// Returns:
//
//	*aresexp.DistillationService - ready to consume TaskCompleted/TaskFailed events.
//	error                       - wrapped ErrDistillDepsMissing when inputs are nil,
//	                             or the llm.NewClient error.
func buildDistillationService(
	cfg *config,
	embClient *pgembedding.EmbeddingClient,
	expRepo repositories.ExperienceRepositoryInterface,
) (*aresexp.DistillationService, error) {
	if embClient == nil || expRepo == nil {
		return nil, fmt.Errorf("build distillation service: %w", ErrDistillDepsMissing)
	}
	llmClient, err := buildLLMClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("build distillation service: %w", err)
	}
	return aresexp.NewDistillationService(llmClient, embClient, expRepo), nil
}

// buildAKGDistiller constructs a standalone distiller for the AKG DistillBridge.
// The memory manager builds its own internal distiller via
// NewMemoryManagerWithDistiller, but that distiller is not exposed on the
// MemoryManager interface, so a separate instance is constructed from the
// same embedding client and experience repo. The distillation config uses
// conservative defaults (DefaultDistillationConfig) and the embedding pipeline
// is wired so conflict detection uses the canonical spec builders.
//
// Precondition: both embClient and expRepo are non-nil. The caller (wireMemory)
// guards the deps-nil case so this function never has to return the ambiguous
// (nil, nil) — it always returns either a usable distiller or a real error.
//
// Args:
//
//	embClient - embedding service for vector generation; must be non-nil.
//	expRepo   - postgres experience repo; must be non-nil.
//
// Returns:
//
//	adapter.ConversationDistiller - ready to feed to NewDistillBridgeWithGate.
//	error                        - wrapped error if the embedding pipeline cannot be constructed.
func buildAKGDistiller(
	embClient apiembed.EmbeddingService,
	expRepo repositories.ExperienceRepositoryInterface,
) (adapter.ConversationDistiller, error) {
	distillRepo := newSDKDistillationRepo(expRepo, defaultDistillTenant)
	distiller := distillation.NewDistiller(distillation.DefaultDistillationConfig(), embClient, distillRepo)
	pipeline, err := memembed.NewEmbeddingPipeline(embClient)
	if err != nil {
		return nil, fmt.Errorf("akg distiller embedding pipeline: %w", err)
	}
	distiller.SetEmbeddingPipeline(pipeline)
	return distiller, nil
}

// buildPostgresPool opens and pings a postgres connection pool from the SDK
// databaseCfg. The pool is returned ready for use; the caller owns Close.
func buildPostgresPool(ctx context.Context, cfg databaseCfg) (*postgres.Pool, error) {
	sslMode := cfg.SSLMode
	if sslMode == "" {
		sslMode = "disable"
	}
	pgCfg := &postgres.Config{
		Host:     cfg.Host,
		Port:     cfg.Port,
		User:     cfg.User,
		Password: cfg.Password,
		Database: cfg.Database,
		SSLMode:  sslMode,
	}
	pool, err := postgres.NewPool(pgCfg)
	if err != nil {
		return nil, fmt.Errorf("open postgres pool: %w", err)
	}
	_ = ctx // postgres.NewPool pings internally with context.Background();
	// ctx reserved for future use (e.g. ping with caller deadline).
	return pool, nil
}

// wireSDKRetrievers constructs the MemoryRetriever and KnowledgeRetriever
// from the wired production dependencies and injects them into the
// MemoryManager via SetRetrievers. Best-effort and non-fatal: if a
// dependency is missing (nil embedding client, nil experience repo, nil
// knowledge runtime) the corresponding retriever is skipped and a warning
// is logged. Retrieval only fires at runtime when the MemoryManager's
// config.EnableRAG is true, so callers still control the feature via
// config regardless of whether retrievers are wired.
//
// When knowStore is non-nil the KnowledgeRetriever takes the AKG read loop:
// HybridSearch against the store's AKG-distilled facts instead of re-running
// provider streaming via runtime.Execute. This closes the 0.2.9 read loop
// (facts written by the DistillBridge are recalled here).
//
// Args:
//
//	ctx        - construction context, used for KnowledgeRetriever construction.
//	cfg        - fully applied SDK config; memCfg.RAGMinScore tunes memory retriever.
//	memMgr     - the MemoryManager; type-asserted to retrieverSetter.
//	embClient  - embedding client for query embedding. Nil skips memory retriever.
//	expRepo    - postgres experience repo. Nil skips memory retriever.
//	knowRt     - AKG KnowledgeRuntime. Nil skips knowledge retriever.
//	knowStore  - optional KnowledgeStore; when non-nil the retriever reads AKG facts via HybridSearch.
//	embModel   - embedding model name for HybridSearch; empty = lexical-only.
func wireSDKRetrievers(
	ctx context.Context,
	cfg *config,
	memMgr memory.MemoryManager,
	embClient apiembed.EmbeddingService,
	expRepo repositories.ExperienceRepositoryInterface,
	knowRt *khruntime.KnowledgeRuntime,
	knowStore knowledge.KnowledgeStore,
	embModel string,
) {
	setter, ok := memMgr.(retrieverSetter)
	if !ok {
		slog.Warn("sdk: memory manager does not expose SetRetrievers; RAG wiring skipped",
			"type", fmt.Sprintf("%T", memMgr))
		return
	}

	var retrievers []memctx.ContextRetriever

	if embClient != nil && expRepo != nil {
		mr, err := buildMemoryRetriever(embClient, expRepo, cfg.memCfg.RAGMinScore)
		if err != nil {
			slog.Warn("sdk: memory retriever construction failed; skipping", "error", err)
		} else {
			retrievers = append(retrievers, mr)
			slog.Info("sdk: memory retriever wired (distilled experiences -> RAG)")
		}
	}

	if knowRt != nil {
		minScore := cfg.knowledgeRT.MinScore
		var kr *adapter.KnowledgeRetriever
		var err error
		if knowStore != nil {
			kr, err = adapter.NewKnowledgeRetrieverWithStore(ctx, knowRt, knowStore, embModel, minScore)
		} else {
			kr, err = adapter.NewKnowledgeRetriever(ctx, knowRt, minScore)
		}
		if err != nil {
			slog.Warn("sdk: knowledge retriever construction failed; skipping", "error", err)
		} else {
			retrievers = append(retrievers, &sdkKnowledgeRetrieverAdapter{inner: kr})
			slog.Info("sdk: knowledge retriever wired (AKG -> RAG)", "min_score", minScore, "store_backed", knowStore != nil)
		}
	}

	if len(retrievers) == 0 {
		slog.Info("sdk: no RAG retrievers wired (memory/knowledge deps unavailable)")
		return
	}

	setter.SetRetrievers(retrievers)
	slog.Info("sdk: RAG retrievers injected into memory manager", "count", len(retrievers))
}

// buildMemoryRetriever constructs a MemoryRetriever from the embedding
// client and postgres experience repository. The embedding pipeline is
// built from the embedding client so query vectors match the prefix scheme
// used at write time. minScore falls back to memctx.DefaultMinScore when
// non-positive. Extracted to keep wireSDKRetrievers under 100 lines.
func buildMemoryRetriever(
	embClient apiembed.EmbeddingService,
	expRepo repositories.ExperienceRepositoryInterface,
	minScore float64,
) (memctx.ContextRetriever, error) {
	if minScore <= 0 {
		minScore = memctx.DefaultMinScore
	}
	pipeline, err := memembed.NewEmbeddingPipeline(embClient)
	if err != nil {
		return nil, fmt.Errorf("build memory retriever pipeline: %w", err)
	}
	searcher := &sdkExperienceSearcher{repo: expRepo}
	mr, err := memctx.NewMemoryRetriever(embClient, pipeline, searcher, defaultDistillTenant, minScore)
	if err != nil {
		return nil, fmt.Errorf("build memory retriever: %w", err)
	}
	return mr, nil
}
