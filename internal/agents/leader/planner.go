package leader

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/Timwood0x10/ares/internal/core/models"
)

// Sentinel errors for taskPlanner validation.
var (
	ErrNilProfile = errors.New("profile cannot be nil")
)

// taskIDCounter is used to generate unique task IDs.
var taskIDCounter uint64

// getRandomSuffix returns a random suffix for extra uniqueness.
func getRandomSuffix() string {
	n, err := rand.Int(rand.Reader, big.NewInt(100000000))
	if err != nil {
		slog.Warn("Failed to generate random suffix, using timestamp fallback", "error", err)
		return fmt.Sprintf("%08d", time.Now().UnixNano()%100000000)
	}
	return fmt.Sprintf("%08d", n.Int64())
}

// generateTaskID generates a unique task ID.
func generateTaskID() string {
	id := atomic.AddUint64(&taskIDCounter, 1)
	randSuffix := getRandomSuffix()
	return fmt.Sprintf("task_%s_%d_%s", time.Now().Format("20060102150405"), id, randSuffix)
}

// SubAgentConfig represents sub agent configuration (mirrors config.SubAgentConfig).
// SubAgentConfig defines a sub-agent that can be dispatched by the planner.
type SubAgentConfig struct {
	ID       string
	Type     string
	Triggers []string
	Priority int // Optional. Defaults to 1 if unset or <= 0.
}

// ExperienceLocator returns the best matching experience ID for a given input.
// Returns empty string if no relevant experience is found (e.g., first run, no match).
// This enables the planner to record which experience aided its decision for bandit feedback.
type ExperienceLocator func(inputText string) string

// PlannerOption configures a taskPlanner instance.
type PlannerOption func(*taskPlanner)

// WithExperienceLocator sets the experience locator for UsedExperienceID tracking.
// When set, each created task will have its UsedExperienceID populated from
// the best-matching experience found by the locator.
func WithExperienceLocator(locator ExperienceLocator) PlannerOption {
	return func(p *taskPlanner) {
		p.expLocator = locator
	}
}

// taskPlanner creates tasks based on user profile and config.
type taskPlanner struct {
	maxTasks          int
	subAgents         []SubAgentConfig
	fallbackOnNoMatch bool // When true, include all subAgents if no triggers match. Default: true.
	expLocator        ExperienceLocator
}

// NewTaskPlanner creates a new TaskPlanner.
//
// Args:
//
//	maxTasks - maximum number of tasks to create; uses DefaultMaxTasks if <= 0.
//	opts - optional planner configuration options.
//
// Returns:
//
//	planner - a new TaskPlanner instance.
func NewTaskPlanner(maxTasks int, opts ...PlannerOption) TaskPlanner {
	if maxTasks <= 0 {
		slog.Warn("NewTaskPlanner: maxTasks <= 0, falling back to default",
			"provided", maxTasks, "default", DefaultMaxTasks)
		maxTasks = DefaultMaxTasks
	}
	p := &taskPlanner{
		maxTasks:          maxTasks,
		subAgents:         nil,
		fallbackOnNoMatch: true,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// NewTaskPlannerWithConfig creates a TaskPlanner with sub-agent configuration.
//
// Args:
//
//	maxTasks - maximum number of tasks to create; uses DefaultMaxTasks if <= 0.
//	subAgents - list of sub-agent configurations for task dispatch.
//	opts - optional planner configuration options.
//
// Returns:
//
//	planner - a new TaskPlanner instance.
func NewTaskPlannerWithConfig(maxTasks int, subAgents []SubAgentConfig, opts ...PlannerOption) TaskPlanner {
	if maxTasks <= 0 {
		slog.Warn("NewTaskPlannerWithConfig: maxTasks <= 0, falling back to default",
			"provided", maxTasks, "default", DefaultMaxTasks)
		maxTasks = DefaultMaxTasks
	}
	p := &taskPlanner{
		maxTasks:          maxTasks,
		subAgents:         subAgents,
		fallbackOnNoMatch: true,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Plan creates tasks based on user profile and input text.
// When subAgents are configured, it creates one task per subAgent filtered by triggers.
// When no subAgents are configured, it falls back to creating a single default task.
func (p *taskPlanner) Plan(ctx context.Context, profile *models.UserProfile, inputText string) ([]*models.Task, error) {
	if profile == nil {
		return nil, fmt.Errorf("profile cannot be nil")
	}

	tasks := make([]*models.Task, 0)

	// When subAgents are configured, create tasks per subAgent with trigger filtering.
	if len(p.subAgents) > 0 {
		lowerInput := strings.ToLower(inputText)

		// Empty input: only include subAgents without triggers (no keyword matching possible).
		if lowerInput == "" {
			for _, sa := range p.subAgents {
				if len(sa.Triggers) == 0 {
					tasks = append(tasks, p.createTask(sa, profile, inputText))
				}
			}
			// If no subAgent without triggers exists, fallback to all.
			if len(tasks) == 0 && p.fallbackOnNoMatch {
				for _, sa := range p.subAgents {
					tasks = append(tasks, p.createTask(sa, profile, inputText))
				}
			}
		} else {
			// Filter subAgents by triggers.
			matched := make([]SubAgentConfig, 0, len(p.subAgents))
			allHaveTriggers := true
			for _, sa := range p.subAgents {
				if len(sa.Triggers) == 0 {
					// No triggers means always selected.
					matched = append(matched, sa)
					allHaveTriggers = false
					continue
				}
				// Check if any trigger keyword matches the input using word boundaries.
				triggered := false
				for _, trigger := range sa.Triggers {
					if matchWordBoundary(lowerInput, strings.ToLower(trigger)) {
						triggered = true
						break
					}
				}
				if triggered {
					matched = append(matched, sa)
				}
			}

			// Fallback: if every subAgent has triggers but none matched, include all subAgents
			// to avoid returning zero tasks. Controlled by fallbackOnNoMatch.
			if len(matched) == 0 && allHaveTriggers && p.fallbackOnNoMatch {
				matched = p.subAgents
			}

			// Create one task per matched subAgent.
			for _, sa := range matched {
				tasks = append(tasks, p.createTask(sa, profile, inputText))
			}
		}
	} else {
		// No subAgents configured: create a single default task (backward compatible).
		task := &models.Task{
			TaskID:      generateTaskID(),
			TaskType:    models.AgentTypeTop,
			AgentType:   models.AgentTypeTop,
			UserProfile: profile,
			Payload:     map[string]any{"action": "analyze_profile"},
			Priority:    1,
			CreatedAt:   time.Now(),
		}
		// Populate UsedExperienceID if an experience locator is configured.
		if p.expLocator != nil {
			task.UsedExperienceID = p.expLocator(inputText)
		}
		tasks = append(tasks, task)
	}

	// Limit total tasks to maxTasks.
	if len(tasks) > p.maxTasks {
		tasks = tasks[:p.maxTasks]
	}

	return tasks, nil
}

// createTask builds a Task from a SubAgentConfig.
// sa.Type must be a non-empty string representing a valid agent type;
// validation is the caller's responsibility (config loading or YAML schema).
func (p *taskPlanner) createTask(sa SubAgentConfig, profile *models.UserProfile, inputText string) *models.Task {
	priority := sa.Priority
	if priority <= 0 {
		priority = 1
	}
	task := &models.Task{
		TaskID:      generateTaskID(),
		TaskType:    models.AgentType(sa.Type),
		AgentType:   models.AgentType(sa.Type),
		UserProfile: profile,
		Payload:     map[string]any{"subAgentID": sa.ID},
		Priority:    priority,
		CreatedAt:   time.Now(),
	}

	// Populate UsedExperienceID if an experience locator is configured.
	// This enables bandit feedback: successful tasks increment usage count,
	// failed tasks decrement rank score.
	if p.expLocator != nil {
		task.UsedExperienceID = p.expLocator(inputText)
	}

	return task
}

// matchWordBoundary checks if keyword appears in text as a whole word
// (preceded and followed by a non-alphanumeric character or string boundary).
func matchWordBoundary(text, keyword string) bool {
	if keyword == "" {
		return false
	}
	runes := []rune(text)
	kwRunes := []rune(keyword)
	kwLen := len(kwRunes)
	for i := 0; i <= len(runes)-kwLen; i++ {
		// Compare rune slices.
		match := true
		for j := 0; j < kwLen; j++ {
			if runes[i+j] != kwRunes[j] {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		before := i == 0 || !isAlphaNum(runes[i-1])
		after := i+kwLen >= len(runes) || !isAlphaNum(runes[i+kwLen])
		if before && after {
			return true
		}
	}
	return false
}

// isAlphaNum reports whether c is a letter or digit (Unicode-aware).
func isAlphaNum(c rune) bool {
	return unicode.IsLetter(c) || unicode.IsDigit(c)
}

// Replan creates new tasks based on previous result and feedback.
// It appends the feedback to the input text for re-planning.
func (p *taskPlanner) Replan(ctx context.Context, profile *models.UserProfile, inputText string, previousResult *models.RecommendResult, feedback string) ([]*models.Task, error) {
	if profile == nil {
		return nil, fmt.Errorf("replan tasks: %w", ErrNilProfile)
	}

	// Append feedback to input for re-planning
	enhancedInput := inputText
	if feedback != "" {
		enhancedInput = fmt.Sprintf("%s\n\nFeedback for improvement: %s", inputText, feedback)
	}

	// Use the same planning logic
	return p.Plan(ctx, profile, enhancedInput)
}
