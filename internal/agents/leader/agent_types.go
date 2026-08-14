// Package leader provides the Leader Agent implementation for multi-agent orchestration.
package leader

import (
	"context"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/agents/leader/state"
	"github.com/Timwood0x10/ares/internal/agents/peer"
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

// TaskDispatcher dispatches tasks to sub-agents via event-driven dispatch.
type TaskDispatcher interface {
	Dispatch(ctx context.Context, tasks []*models.Task) ([]*models.TaskResult, error)
}

// ResultAggregator aggregates results from sub-agents.
type ResultAggregator interface {
	Aggregate(ctx context.Context, results []*models.TaskResult, tasks []*models.Task) (*models.RecommendResult, error)
}

// SetPeerRegistry attaches the peer registry for direct agent-to-agent
// notifications (primitive 2). The leader keeps dispatching tasks via events
// (primary path); the peer channel is used only for supplementary
// notifications so the two mechanisms never race on task execution.
func (a *leaderAgent) SetPeerRegistry(reg *peer.Registry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.peerRegistry = reg
}

// NotifyPeer sends a peer-to-peer notification to the target agent via the
// registry. It is best-effort: a nil registry or unknown target is silently
// skipped (notifications are supplementary, never fatal to the leader loop).
func (a *leaderAgent) NotifyPeer(ctx context.Context, targetID, message string) {
	a.mu.RLock()
	reg := a.peerRegistry
	a.mu.RUnlock()
	if reg == nil || targetID == "" {
		return
	}
	msg := ahp.NewMessage(ahp.AHPMethodProgress, a.id, targetID, "", "")
	msg.Payload = map[string]any{"note": message}
	if err := reg.Send(ctx, targetID, msg); err != nil {
		log.Debug("leader peer notify skipped", "target", targetID, "error", err)
	}
}

// LeaderOption configures a leaderAgent instance.
type LeaderOption func(*leaderAgent)

// WithCheckpoint injects a checkpoint repository for session recovery.
func WithCheckpoint(cp *state.CheckpointRepository) LeaderOption {
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

// WithStrategySource injects a live evolution StrategySource into the leader's
// profile parser so the active strategy can steer prompt + LLM params at runtime.
func WithStrategySource(src agents.StrategySource) LeaderOption {
	return func(a *leaderAgent) {
		if pp, ok := a.parser.(*profileParser); ok {
			pp.WithStrategySource(src)
		}
	}
}

// WithProfileRegistry injects the agent role registry used for multi-stage
// role switching during task dispatch (P0-3). When unset, New() registers the
// built-in DefaultProfiles().
func WithProfileRegistry(registry *agents.ProfileRegistry) LeaderOption {
	return func(a *leaderAgent) {
		if registry != nil {
			a.profileRegistry = registry
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
	mu              sync.RWMutex
	id              string
	agentType       models.AgentType
	status          models.AgentStatus
	config          *LeaderAgentConfig
	parser          ProfileParser
	planner         TaskPlanner
	dispatcher      TaskDispatcher
	aggregator      ResultAggregator
	messageQueue    *ahp.MessageQueue
	heartbeatMon    *ahp.HeartbeatMonitor
	memoryManager   memory.MemoryManager
	feedbackSvc     *experience.FeedbackService
	profileRegistry *agents.ProfileRegistry
	sessionID       string
	checkpoint      *state.CheckpointRepository
	eventStore      ares_events.EventStore
	ares_callbacks  ares_callbacks.Emitter
	// peerRegistry, when non-nil, enables direct peer-to-peer messaging to sub
	// agents (primitive 2). The leader dispatches tasks via events (primary
	// path) and uses the peer channel only for supplementary notifications,
	// so the two mechanisms never race on task execution. Set via
	// SetPeerRegistry.
	peerRegistry *peer.Registry

	lastTaskID          string
	lastCompletedTaskID string
	conversationSummary string
	lastInteractionTime time.Time

	// stopCh is allocated by Start/ensureInitialized and closed by Stop, both
	// under mu. Stop performs an idempotent close via select rather than a
	// sync.Once, so that a restarted agent (which gets a fresh stopCh) can be
	// stopped again. See Stop in agent.go.
	stopCh       chan struct{}
	streamEg     *errgroup.Group
	processingMu sync.Mutex

	// memoryConsumer is the dedicated, event-driven worker for post-result
	// memory finalization. Started in Start and stopped in Stop. Nil when the
	// agent has no event store or memory manager (leader/sub decoupling, C phase).
	memoryConsumer *memoryConsumer
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
		MaxParallelTasks: 10,
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
