// Package experience provides experience distillation service.
package experience

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/llm"
	"github.com/Timwood0x10/ares/internal/storage/postgres/embedding"
	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
)

// DistillationService provides experience distillation from task results.
// This service converts task execution logs into reusable experiences.
type DistillationService struct {
	llmClient       *llm.Client
	embeddingClient *embedding.EmbeddingClient
	experienceRepo  repositories.ExperienceRepositoryInterface
	logger          *slog.Logger
}

// NewDistillationService creates a new DistillationService instance.
func NewDistillationService(
	llmClient *llm.Client,
	embeddingClient *embedding.EmbeddingClient,
	experienceRepo repositories.ExperienceRepositoryInterface,
) *DistillationService {
	return &DistillationService{
		llmClient:       llmClient,
		embeddingClient: embeddingClient,
		experienceRepo:  experienceRepo,
		logger:          slog.Default(),
	}
}

// ShouldDistill checks if a task result should be distilled.
func (s *DistillationService) ShouldDistill(ctx context.Context, task *TaskResult) bool {
	if task == nil {
		return false
	}
	if !task.Success {
		return false
	}
	if len(task.Task) < 10 {
		return false
	}
	if len(task.Result) < 20 {
		return false
	}
	return true
}

// Distill extracts a reusable experience from a task result.
func (s *DistillationService) Distill(ctx context.Context, task *TaskResult) (*Experience, error) {
	if task == nil {
		return nil, fmt.Errorf("task result is nil")
	}
	if task.TenantID == "" {
		return nil, fmt.Errorf("tenant ID is required")
	}

	if !s.ShouldDistill(ctx, task) {
		return nil, fmt.Errorf("distillation skipped: task does not meet distillation criteria")
	}

	extracted, err := s.extractExperience(ctx, task)
	if err != nil {
		return nil, errors.Wrap(err, "extract experience")
	}

	if extracted.Problem == "" || extracted.Solution == "" {
		return nil, fmt.Errorf("invalid extracted experience")
	}

	embedding, err := s.embeddingClient.Embed(ctx, extracted.Problem)
	if err != nil {
		return nil, errors.Wrap(err, "generate embedding")
	}

	expType := ExperienceTypeFailure
	if task.Success {
		expType = ExperienceTypeSuccess
	}

	exp := &storage_models.Experience{
		TenantID:         task.TenantID,
		Type:             expType,
		Problem:          extracted.Problem,
		Solution:         extracted.Solution,
		Constraints:      extracted.Constraints,
		Input:            extracted.Problem,
		Output:           extracted.Solution,
		Embedding:        embedding,
		EmbeddingModel:   s.embeddingClient.GetModel(),
		EmbeddingVersion: 1,
		Score:            0.0,
		Success:          task.Success,
		AgentID:          task.AgentID,
		UsageCount:       0,
		Metadata:         nil,
		CreatedAt:        time.Now(),
	}

	err = s.experienceRepo.Create(ctx, exp)
	if err != nil {
		return nil, errors.Wrap(err, "store experience")
	}

	return &Experience{
		ID:               exp.ID,
		TenantID:         exp.TenantID,
		Type:             exp.Type,
		Problem:          exp.Problem,
		Solution:         exp.Solution,
		Constraints:      exp.Constraints,
		Embedding:        exp.Embedding,
		EmbeddingModel:   exp.EmbeddingModel,
		EmbeddingVersion: exp.EmbeddingVersion,
		Score:            exp.Score,
		Success:          exp.Success,
		AgentID:          exp.AgentID,
		UsageCount:       exp.UsageCount,
		DecayAt:          exp.DecayAt,
		CreatedAt:        exp.CreatedAt,
	}, nil
}

// DistillBatch distills multiple task results.
func (s *DistillationService) DistillBatch(ctx context.Context, tasks []*TaskResult) ([]*Experience, error) {
	if len(tasks) == 0 {
		return []*Experience{}, nil
	}

	experiences := make([]*Experience, 0, len(tasks))
	for _, task := range tasks {
		exp, err := s.Distill(ctx, task)
		if err != nil {
			s.logger.Error("Failed to distill task", "error", err)
			continue
		}
		if exp != nil {
			experiences = append(experiences, exp)
		}
	}

	return experiences, nil
}

// extractExperience extracts experience components using LLM.
func (s *DistillationService) extractExperience(ctx context.Context, task *TaskResult) (*ExtractedExperience, error) {
	if s.llmClient == nil || !s.llmClient.IsEnabled() {
		return nil, fmt.Errorf("LLM client is not available")
	}

	prompt := s.buildExtractionPrompt(task)

	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	response, err := s.llmClient.Generate(timeoutCtx, prompt)
	if err != nil {
		return nil, errors.Wrap(err, "LLM generation failed")
	}

	return s.parseExtractionResponse(response)
}

// buildExtractionPrompt builds the prompt for experience extraction.
func (s *DistillationService) buildExtractionPrompt(task *TaskResult) string {
	return fmt.Sprintf(`Extract a reusable experience from the task.

Task:
%s

Context:
%s

Result:
%s

Return:

Problem:
The core problem being solved.

Solution:
The concise solution approach.

Constraints:
Important constraints or context.

Keep each section short and concise.`,
		task.Task,
		task.Context,
		task.Result,
	)
}

// parseExtractionResponse parses the LLM response.
func (s *DistillationService) parseExtractionResponse(response string) (*ExtractedExperience, error) {
	lines := strings.Split(response, "\n")

	var problem, solution, constraints strings.Builder
	var currentSection string

	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)

		lowerLine := strings.ToLower(trimmedLine)
		switch {
		case strings.HasPrefix(lowerLine, "problem:"):
			currentSection = "problem"
			content := strings.TrimSpace(trimmedLine[8:])
			if content != "" {
				problem.WriteString(content)
			}
		case strings.HasPrefix(lowerLine, "solution:"):
			currentSection = "solution"
			content := strings.TrimSpace(trimmedLine[9:])
			if content != "" {
				solution.WriteString(content)
			}
		case strings.HasPrefix(lowerLine, "constraints:"):
			currentSection = "constraints"
			content := strings.TrimSpace(trimmedLine[12:])
			if content != "" {
				constraints.WriteString(content)
			}
		case trimmedLine != "":
			switch currentSection {
			case "problem":
				if problem.Len() > 0 {
					problem.WriteString(" ")
				}
				problem.WriteString(trimmedLine)
			case "solution":
				if solution.Len() > 0 {
					solution.WriteString(" ")
				}
				solution.WriteString(trimmedLine)
			case "constraints":
				if constraints.Len() > 0 {
					constraints.WriteString(" ")
				}
				constraints.WriteString(trimmedLine)
			}
		}
	}

	return &ExtractedExperience{
		Problem:     strings.TrimSpace(problem.String()),
		Solution:    strings.TrimSpace(solution.String()),
		Constraints: strings.TrimSpace(constraints.String()),
	}, nil
}

// ExtractedExperience represents extracted components.
type ExtractedExperience struct {
	Problem     string
	Solution    string
	Constraints string
}
