// Package leader provides the Leader Agent implementation for multi-agent orchestration.
package leader

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/core/models"
)

func (a *leaderAgent) initMemoryContext(ctx context.Context, strInput string) (enrichedInput string, sessionID string, taskID string) {
	if a.memoryManager == nil {
		return strInput, "", ""
	}
	a.mu.RLock()
	sessionID = a.sessionID
	checkpoint := a.checkpoint
	leaderID := a.id
	a.mu.RUnlock()

	if sessionID == "" {
		// Acquire write lock to check and create session atomically.
		// Unlike sync.Once, this retries on failure — if CreateSession fails
		// (e.g. transient DB error), the next call will try again (P0-5).
		a.mu.Lock()
		sessionID = a.sessionID
		if sessionID == "" {
			recovered := false
			if checkpoint != nil {
				cp, cpErr := checkpoint.GetLatest(ctx, leaderID)
				if cpErr != nil {
					log.Warn("Checkpoint recovery failed, creating new session", "error", cpErr)
				} else if cp != nil && cp.SessionID != "" {
					sessionID = cp.SessionID
					recovered = true
					log.Info("Session recovered from checkpoint", "session_id", sessionID, "leader_id", leaderID)
				}
			}
			if !recovered {
				sid, createErr := a.memoryManager.CreateSession(ctx, a.getUserID())
				if createErr != nil {
					log.Warn("Failed to create session", "error", createErr)
					a.mu.Unlock()
					return strInput, "", ""
				}
				sessionID = sid
			}
			a.sessionID = sessionID
		}
		a.mu.Unlock()
	}
	if sessionID == "" {
		return strInput, "", ""
	}
	if err := a.memoryManager.AddMessage(ctx, sessionID, "user", strInput); err != nil {
		log.Warn("memory operation failed, proceeding without", "operation", "AddMessage", "error", err)
		return strInput, sessionID, ""
	}

	// Build context with conversation history and similar tasks.
	enrichedInput, err := a.memoryManager.BuildContext(ctx, strInput, sessionID)
	if err != nil {
		log.Warn("memory operation failed, proceeding without", "operation", "BuildContext", "error", err)
		enrichedInput = strInput
	}

	if sessionID != "" {
		a.emitEvent(ctx, ares_events.EventMessageAdded, map[string]any{
			"session_id": sessionID,
			"role":       "user",
		})
	}

	taskID, err = a.memoryManager.CreateTask(ctx, sessionID, a.getUserID(), strInput)
	if err != nil {
		log.Warn("memory operation failed, proceeding without", "operation", "CreateTask", "error", err)
		return enrichedInput, sessionID, ""
	}
	return enrichedInput, sessionID, taskID
}

func (a *leaderAgent) finalizeMemory(ctx context.Context, sessionID, taskID string, result *models.RecommendResult) {
	if a.memoryManager == nil || result == nil || sessionID == "" {
		return
	}
	resultStr := fmt.Sprintf("Generated %d items", len(result.Items))
	if taskID != "" {
		if err := a.memoryManager.UpdateTaskOutput(ctx, taskID, resultStr); err != nil {
			log.Warn("memory operation failed, proceeding without", "operation", "UpdateTaskOutput", "error", err)
		}
	}
	if err := a.memoryManager.AddMessage(ctx, sessionID, "assistant", resultStr); err != nil {
		log.Warn("memory operation failed, proceeding without", "operation", "AddMessage", "error", err)
	}
	if sessionID != "" {
		a.emitEvent(ctx, ares_events.EventMessageAdded, map[string]any{
			"session_id": sessionID,
			"role":       "assistant",
		})
	}
	if taskID != "" {
		a.emitEvent(ctx, ares_events.EventTaskCompleted, map[string]any{
			"task_id": taskID,
			"status":  "completed",
		})
	}
	if taskID == "" {
		return
	}
	// Submit distillation to errgroup for lifecycle-managed execution.
	// errgroup provides context propagation and error collection.
	a.distillEg.Go(func() error {
		distillCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		if _, err := a.memoryManager.DistillTask(distillCtx, taskID); err != nil {
			log.Warn("memory operation failed, proceeding without", "operation", "DistillTask", "error", err)
		}
		return nil
	})
}

func (a *leaderAgent) recordExperienceFeedback(ctx context.Context, tasks []*models.Task, results []*models.TaskResult) {
	if a.feedbackSvc == nil || len(tasks) == 0 {
		return
	}
	resultByTaskID := make(map[string]*models.TaskResult, len(results))
	for _, r := range results {
		if r != nil {
			resultByTaskID[r.TaskID] = r
		}
	}
	for _, task := range tasks {
		if task.UsedExperienceID == "" {
			continue
		}
		var success bool
		if result, ok := resultByTaskID[task.TaskID]; ok && result != nil {
			success = result.Success
		}
		if err := a.feedbackSvc.RecordFeedback(ctx, task.UsedExperienceID, success); err != nil {
			log.Warn("Failed to record experience feedback",
				"task_id", task.TaskID,
				"experience_id", task.UsedExperienceID,
				"success", success,
				"error", err,
			)
		}
	}
}
