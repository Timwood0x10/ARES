package ares_bootstrap

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	evolution "github.com/Timwood0x10/ares/internal/ares_evolution"
	aresexp "github.com/Timwood0x10/ares/internal/ares_experience"
	"github.com/Timwood0x10/ares/internal/llm"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
	"github.com/Timwood0x10/ares/internal/storage/postgres/embedding"
	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
)

// defaultDistillTenant aligns distillation writes (event trigger) and GA hint
// reads (GuidanceProvider) in the single-tenant default configuration. It is
// sourced from ares_events.DefaultTenantID — the same value the sub-agent
// emitter writes into EventTaskCompleted/EventTaskFailed payloads — so both
// sides agree. The experience repository scopes every read by tenant_id, so a
// mismatch would silently starve the GA of hints.
const defaultDistillTenant = ares_events.DefaultTenantID

// provideDistillation constructs the experience distillation service and a
// GuidanceProvider that feeds distilled experiences back into the GA's
// experience-guided mutation. It is intentionally non-fatal: any failure
// (e.g. Postgres unreachable, LLM client of unexpected type) is returned as an
// error and the caller logs + skips, leaving the system running without
// distillation.
func provideDistillation(
	ctx context.Context,
	cfg *ares_config.Config,
	llmClientArg interface{},
) (*postgres.Pool, *embedding.EmbeddingClient, repositories.ExperienceRepositoryInterface, *aresexp.DistillationService, evolution.GuidanceProvider, error) {
	llmClient, ok := llmClientArg.(*llm.Client)
	if !ok {
		return nil, nil, nil, nil, nil, fmt.Errorf("distillation requires *llm.Client, got %T", llmClientArg)
	}

	pgCfg := &postgres.Config{
		Host:     cfg.Storage.Host,
		Port:     cfg.Storage.Port,
		User:     cfg.Storage.Username,
		Password: cfg.Storage.Password,
		Database: cfg.Storage.Database,
		SSLMode:  cfg.Storage.SSLMode,
	}
	pool, err := postgres.NewPool(pgCfg)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("distillation: open postgres pool: %w", err)
	}

	timeout := time.Duration(cfg.Embedding.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	embClient := embedding.NewEmbeddingClient(cfg.Embedding.BaseURL, cfg.Embedding.Model, nil, timeout)

	expRepo := repositories.NewExperienceRepository(pool.GetDB())

	distSvc := aresexp.NewDistillationService(llmClient, embClient, expRepo)

	guidProv := &evolution.FuncGuidanceProvider{
		HintsFunc: func(ctx context.Context, taskType string, limit int) ([]evolution.EvolutionHint, error) {
			if limit <= 0 {
				limit = 5
			}
			exps := fetchExperiences(ctx, expRepo, embClient, taskType, limit)
			hints := make([]evolution.EvolutionHint, 0, len(exps))
			for _, exp := range exps {
				hints = append(hints, experienceToHint(exp))
			}
			return hints, nil
		},
		// RecordStrategyOutcome is currently never invoked by the GA core; the
		// FuncGuidanceProvider treats a nil RecordFunc as a successful no-op.
	}

	return pool, embClient, expRepo, distSvc, guidProv, nil
}

// fetchExperiences retrieves candidate experiences for the GA's hint lookup.
// It prefers semantic vector search (embedding the task type) and falls back to
// keyword search. Two tenant scopes are tried to tolerate single-tenant
// conventions (an explicit "default" tenant vs an empty tenant).
func fetchExperiences(
	ctx context.Context,
	repo repositories.ExperienceRepositoryInterface,
	emb *embedding.EmbeddingClient,
	taskType string,
	limit int,
) []*storage_models.Experience {
	for _, tenant := range []string{defaultDistillTenant, ""} {
		if emb != nil {
			if vec, e := emb.Embed(ctx, taskType); e == nil {
				if exps, e := repo.SearchByVector(ctx, vec, tenant, limit); e == nil && len(exps) > 0 {
					return exps
				}
			}
		}
		if exps, e := repo.SearchByKeyword(ctx, taskType, tenant, limit); e == nil && len(exps) > 0 {
			return exps
		}
	}
	return nil
}

// experienceToHint maps a stored experience into an evolution hint consumed by
// the GA's experience-guided mutator. Read fields are exp.Input (problem) and
// exp.Output (solution); constraints are lifted from metadata via GetConstraints.
func experienceToHint(exp *storage_models.Experience) evolution.EvolutionHint {
	confidence := exp.Score
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}

	var constraints []string
	if c := exp.GetConstraints(); c != "" {
		constraints = strings.Split(c, "\n")
	}

	return evolution.EvolutionHint{
		ID:                  exp.ID,
		TaskType:            exp.Type,
		Problem:             exp.Input,
		Solution:            exp.Output,
		Constraints:         constraints,
		Confidence:          confidence,
		SourceExperienceIDs: []string{exp.ID},
	}
}

// handleTaskCompletedForDistillation turns a task-completed/failed event into a
// distilled experience. The sub-agent emitter now enriches these events with
// task text, result text, tenant_id, and the consumed experience ID, so the
// loop is live. A guard still applies: Distill requires a non-empty tenant_id
// plus task/result text of sufficient length, which holds for normally
// completed tasks and any failure whose error text is long enough to be useful.
func handleTaskCompletedForDistillation(ctx context.Context, svc *aresexp.DistillationService, ev *ares_events.Event) {
	p := ev.Payload

	taskText := stringField(p, ares_events.EventKeyTask)
	resultText := stringField(p, ares_events.EventKeyResult)
	tenantID := stringField(p, ares_events.EventKeyTenantID)
	agentID := stringField(p, "agent_id")
	usedExpID := stringField(p, ares_events.EventKeyUsedExperienceID)

	if tenantID == "" || len(taskText) < 10 || len(resultText) < 20 {
		log.Debug("bootstrap: distillation skipped — event payload lacks task/result/tenant content",
			"event_id", ev.ID, "type", ev.Type)
		return
	}

	taskResult := &aresexp.TaskResult{
		Task:             taskText,
		Result:           resultText,
		TenantID:         tenantID,
		AgentID:          agentID,
		UsedExperienceID: usedExpID,
		Success:          ev.Type == ares_events.EventTaskCompleted,
	}
	if _, err := svc.Distill(ctx, taskResult); err != nil {
		log.Warn("bootstrap: distillation on task completion failed", "error", err)
	}
}

// stringField returns the first non-empty string value among the given keys.
func stringField(p map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := p[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}
