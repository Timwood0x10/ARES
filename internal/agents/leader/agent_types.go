// Package leader provides the Leader Agent implementation for multi-agent orchestration.
package leader

import (
	"context"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/ares_callbacks"
	"github.com/Timwood0x10/ares/internal/ares_events"
	experience "github.com/Timwood0x10/ares/internal/ares_experience"
	memory "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/Timwood0x10/ares/internal/ares_protocol/ahp"
	"github.com/Timwood0x10/ares/internal/core/models"

	"golang.org/x/sync/errgroup"
)

// Agent represents the Leader Agent interface.
type Agent interface {
	base.Agent
}

// Compile-time check: leaderAgent must satisfy base.StatefulAgent.
var _ base.StatefulAgent = (*leaderAgent)(nil)

// ProfileParser parses user profile from input.
type ProfileParser interface {
	Parse(ctx context.Context, input string) (*models.UserProfile, error)
}

// TaskPlanner plans tasks based on user profile and input text.
type TaskPlanner interface {
	Plan(ctx context.Context, profile *models.UserProfile, inputText string) ([]*models.Task, error)
	Replan(ctx context.Context, profile *models.UserProfile, inputText string, previousResult *models.RecommendResult, feedback string) ([]*models.Task, error)
}

// TaskDispatcher dispatches tasks to sub-agents.
type TaskDispatcher interface {
	Dispatch(ctx context.Context, tasks []*models.Task) ([]*models.TaskResult, error)
	RegisterExecutor(agentType models.AgentType, fn func(ctx context.Context, task *models.Task) (*models.TaskResult, error))
}

// ResultAggregator aggregates results from sub-agents.
type ResultAggregator interface {
	Aggregate(ctx context.Context, results []*models.TaskResult, tasks []*models.Task) (*models.RecommendResult, error)
}

// LeaderOption configures a leaderAgent instance.
type LeaderOption func(*leaderAgent)

func WithCheckpoint(cp *CheckpointRepository) LeaderOption {
	return func(a *leaderAgent) { a.checkpoint = cp }
}

func WithEventStore(store ares_events.EventStore) LeaderOption {
	return func(a *leaderAgent) {
		a.eventStore = store
		if pp, ok := a.parser.(*profileParser); ok {
			pp.WithEventStore(store)
		}
	}
}

func WithCallbacks(emitter ares_callbacks.Emitter) LeaderOption {
	return func(a *leaderAgent) { a.ares_callbacks = emitter }
}

func WithFeedbackService(svc *experience.FeedbackService) LeaderOption {
	return func(a *leaderAgent) { a.feedbackSvc = svc }
}

// leaderAgent implements the Leader Agent.
type leaderAgent struct {
	mu             sync.RWMutex
	id             string
	agentType      models.AgentType
	status         models.AgentStatus
	config         *LeaderAgentConfig
	parser         ProfileParser
	planner        TaskPlanner
	dispatcher     TaskDispatcher
	aggregator     ResultAggregator
	messageQueue   *ahp.MessageQueue
	heartbeatMon   *ahp.HeartbeatMonitor
	memoryManager  memory.MemoryManager
	feedbackSvc    *experience.FeedbackService
	sessionID      string
	checkpoint     *CheckpointRepository
	eventStore     ares_events.EventStore
	ares_callbacks ares_callbacks.Emitter

	lastTaskID          string
	lastCompletedTaskID string
	conversationSummary string
	lastInteractionTime time.Time

	stopCh          chan struct{}
	distillMu       sync.Mutex
	distillWg       sync.WaitGroup
	distillEg       *errgroup.Group
	streamEg        *errgroup.Group
	processingMu    sync.Mutex
	cleanupOnce     sync.Once
	sessionInitOnce sync.Once
}

// LeaderAgentConfig holds configuration for LeaderAgent.
type LeaderAgentConfig struct {
	base.Config
	MaxParallelTasks int
	MaxSteps         int
	EnableCache      bool
	UserID           string
	Loop             LoopConfig
}

// LoopConfig holds configuration for agent loop behavior.
type LoopConfig struct {
	MaxIterations    int
	QualityThreshold float64
	EnableReflection bool
	MaxTotalLLMCalls int
	MaxLoopDuration  time.Duration
}

func DefaultLeaderAgentConfig() *LeaderAgentConfig {
	return &LeaderAgentConfig{
		Config:           *base.DefaultConfig(models.AgentTypeLeader),
		MaxParallelTasks: 5,
		MaxSteps:         20,
		EnableCache:      true,
		UserID:           "default_user",
		Loop: LoopConfig{
			MaxIterations:    3,
			QualityThreshold: 0.7,
			MaxTotalLLMCalls: 50,
			MaxLoopDuration:  10 * time.Minute,
		},
	}
}
